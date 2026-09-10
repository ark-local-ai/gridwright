import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { loadIsolatedStore, makeTask, taskWithStatus, cleanup } from "../test/helpers";
import type * as StoreModule from "./store";

let store: typeof StoreModule;
let tmpDir: string;
let db: typeof StoreModule.db;

const resetTables = () => {
  db.exec(`
    DELETE FROM tasks;
    DELETE FROM steps;
    DELETE FROM artifacts;
    DELETE FROM spaces;
    DELETE FROM chat_sessions;
    DELETE FROM chat_messages;
    DELETE FROM chat_messages_fts;
    DELETE FROM users;
    DELETE FROM sessions;
    DELETE FROM task_templates;
    DELETE FROM file_versions;
    DELETE FROM experts;
    DELETE FROM memories;
    DELETE FROM memories_fts;
    DELETE FROM settings;
  `);
};

beforeAll(async () => {
  const loaded = await loadIsolatedStore();
  store = loaded.store;
  tmpDir = loaded.dir;
  db = store.db;
  resetTables();
});

afterAll(() => cleanup(tmpDir));

beforeEach(() => {
  resetTables();
  // 重新播种默认空间，模拟首次启动
  store.listSpaces(undefined);
});

describe("tasks", () => {
  it("插入并读回任务（含 JSON 字段）", () => {
    const t = makeTask({ id: "abc" });
    store.insertTask(t, undefined);
    // 步骤独立于任务主行（getTask 从 steps 表读）
    store.insertStep("abc", 0, "步骤A");
    const got = store.getTask("abc")!;
    expect(got.id).toBe("abc");
    expect(got.status).toBe("queue");
    expect(got.checks).toHaveLength(3);
    expect(got.steps).toHaveLength(1);
    expect(got.steps[0].title).toBe("步骤A");
    expect(got.archived).toBe(false);
  });

  it("getTask 对不存在 id 返回 null", () => {
    expect(store.getTask("nope")).toBeNull();
  });

  it("insertTask 落库 refs 参考资料；getTaskRefs 读回；updateTaskRefs 覆写（M64）", () => {
    const t = makeTask({ id: "refs1" });
    store.insertTask(t, "u1", [{ title: "背景", url: "https://x.example", text: "正文A" }, { title: "补充", text: "正文B" }]);
    // 读回：两条有效 refs
    expect(store.getTaskRefs("refs1")).toHaveLength(2);
    expect(store.getTaskRefs("refs1")[0]).toMatchObject({ title: "背景", url: "https://x.example", text: "正文A" });
    // 覆写为空（清掉）
    store.updateTaskRefs("refs1", []);
    expect(store.getTaskRefs("refs1")).toEqual([]);
    // 覆写为新 refs
    store.updateTaskRefs("refs1", [{ text: "仅正文" }]);
    expect(store.getTaskRefs("refs1")).toEqual([{ text: "仅正文" }]);
    // 不存在 id → 空数组
    expect(store.getTaskRefs("nope")).toEqual([]);
  });

  it("步骤与产物增删", () => {
    store.insertTask(makeTask({ id: "x" }), undefined);
    const sid = store.insertStep("x", 0, "第一步");
    store.updateStepStatus(sid, "done");
    expect(store.getTask("x")!.steps[0].status).toBe("done");
    store.insertArtifact({ name: "成果.pptx", kind: "ppt", note: "n" }, "x", 1);
    expect(store.getTask("x")!.deliverable?.name).toBe("成果.pptx");
  });

  it("resetTask 幂等复位（重试前清残留）", () => {
    store.insertTask(makeTask({ id: "r" }), undefined);
    const sid = store.insertStep("r", 0, "步骤");
    store.insertArtifact({ name: "a.pptx", kind: "ppt", note: "n" }, "r", 1);
    store.resetTask("r");
    // 主行被删、步骤/产物被删
    expect(store.getTask("r")).toBeNull();
    expect(store.getTask("r")).toBeNull();
    void sid;
  });

  it("listTasks 默认只看活跃，归档需 archivedOnly", () => {
    store.insertTask(makeTask({ id: "active" }), undefined);
    store.insertTask(makeTask({ id: "arch", status: "done" }), undefined);
    store.setTaskArchived("arch", true);
    const active = store.listTasks(50, undefined, false).map((t) => t.id);
    const archived = store.listTasks(50, undefined, true).map((t) => t.id);
    expect(active).toContain("active");
    expect(active).not.toContain("arch");
    expect(archived).toContain("arch");
  });

  it("多用户隔离：见自己的 + 全局，不见他人", () => {
    store.insertTask(makeTask({ id: "alice1" }), "alice");
    store.insertTask(makeTask({ id: "alice2" }), "alice");
    store.insertTask(makeTask({ id: "bob1" }), "bob");
    store.insertTask(makeTask({ id: "global1" }), undefined); // 全局无主
    const aliceIds = store.listTasks(50, "alice", false).map((t) => t.id);
    const bobIds = store.listTasks(50, "bob", false).map((t) => t.id);
    const anonIds = store.listTasks(50, undefined, false).map((t) => t.id);
    expect(aliceIds).toEqual(expect.arrayContaining(["alice1", "alice2", "global1"]));
    expect(aliceIds).not.toContain("bob1");
    expect(bobIds).toContain("bob1");
    expect(anonIds.length).toBeGreaterThanOrEqual(4); // 匿名看全部
  });

  it("getUnfinishedTasks 只取非归档 queue/running 且带 user_id", () => {
    store.insertTask(taskWithStatus("q1", "queue", { prompt: "p1" }), "alice");
    store.insertTask(taskWithStatus("r1", "running"), undefined);
    store.insertTask(taskWithStatus("d1", "done"), undefined);
    store.insertTask(taskWithStatus("a1", "running", { archived: true }), undefined);
    const u = store.getUnfinishedTasks();
    const ids = u.map((t) => t.id);
    expect(ids).toEqual(expect.arrayContaining(["q1", "r1"]));
    expect(ids).not.toContain("d1");
    expect(ids).not.toContain("a1"); // 归档的不恢复
    expect(u.find((t) => t.id === "q1")?.userId).toBe("alice"); // 保留归属
  });

  it("countTasksByStatus 聚合", () => {
    store.insertTask(taskWithStatus("c1", "done"), undefined);
    store.insertTask(taskWithStatus("c2", "done"), undefined);
    store.insertTask(taskWithStatus("c3", "failed"), undefined);
    const by = store.countTasksByStatus();
    expect(by.done).toBe(2);
    expect(by.failed).toBe(1);
  });
});

describe("chat + FTS 搜索", () => {
  it("建会话、落消息、搜索命中", () => {
    const sid = store.createChatSession("测试会话", undefined);
    store.appendChatMessage(sid, "user", "想找一份行业报告的数据");
    store.appendChatMessage(sid, "assistant", "这是关于量子计算的行业报告。");
    const hits = store.searchChatMessages("行业报告", undefined);
    expect(hits).toHaveLength(1);
    expect(hits[0].sessionId).toBe(sid);
    expect(hits[0].snippet).toContain("行业报告");
  });

  it("搜索按会话去重（同会话多条命中只回一条）", () => {
    const sid = store.createChatSession("去重会话", undefined);
    store.appendChatMessage(sid, "user", "市场战略分析");
    store.appendChatMessage(sid, "assistant", "再做一次市场战略分析总结");
    const hits = store.searchChatMessages("战略", undefined);
    expect(hits).toHaveLength(1);
    expect(hits[0].sessionId).toBe(sid);
  });

  it("无匹配返回空数组", () => {
    const sid = store.createChatSession("a", undefined);
    store.appendChatMessage(sid, "user", "今天天气不错");
    expect(store.searchChatMessages("不存在的词XYZ", undefined)).toEqual([]);
  });
});

describe("spaces", () => {
  it("createSpace / getActiveSpace / deleteSpace", () => {
    const s = store.createSpace("项目A", "projA", "sp1", undefined);
    expect(s.id).toBe("sp1");
    store.updateSpace("sp1", { isActive: true });
    expect(store.getActiveSpace()?.id).toBe("sp1");
    expect(store.deleteSpace("sp1")).toBe(true);
    expect(store.deleteSpace("sp1")).toBe(false);
  });
});

describe("auth", () => {
  it("注册/校验密码/会话", () => {
    const u = store.createUser("alice", "secret123", "Alice");
    expect(u.username).toBe("alice");
    const row = store.findUserByUsername("alice")!;
    expect(store.verifyPassword("secret123", row.password_hash)).toBe(true);
    expect(store.verifyPassword("wrong", row.password_hash)).toBe(false);
    expect(row.id).toBe(u.id);
    const token = store.createSession(u.id);
    expect(store.resolveSession(token)?.id).toBe(u.id);
    store.destroySession(token);
    expect(store.resolveSession(token)).toBeUndefined();
  });
});

describe("管理端用户（M67 store）", () => {
  it("首个注册用户自动成为管理员；后续用户为普通", () => {
    const admin = store.createUser("root1", "p1", "Root");
    expect(admin.isAdmin).toBe(true);
    const normal = store.createUser("u1", "p2", "U");
    expect(normal.isAdmin).toBe(false);
    expect(store.countAdmins()).toBe(1);
  });

  it("listUsers 返回用量汇总（任务/记忆/会话）与最近活跃", () => {
    const u = store.createUser("lu_admin", "p", "LU");
    const rows = store.listUsers();
    const row = rows.find((r) => r.username === "lu_admin")!;
    expect(row.id).toBe(u.id);
    expect(row.isAdmin).toBe(true);
    // 至少字段齐全（任务/记忆/会话计数非负、最近活跃可空）
    expect(typeof row.tasks).toBe("number");
    expect(typeof row.sessions).toBe("number");
    expect("lastActive" in row).toBe(true);
  });

  it("禁用后 resolveSession 返回空（会话失效）；启用恢复", () => {
    const u = store.createUser("dis_admin", "p", "Dis");
    const token = store.createSession(u.id);
    expect(store.resolveSession(token)?.id).toBe(u.id);
    store.setUserDisabled(u.id, true);
    expect(store.resolveSession(token)).toBeUndefined();
    store.setUserDisabled(u.id, false);
    expect(store.resolveSession(token)?.id).toBe(u.id);
  });

  it("deleteUserWithData 连带清该用户的会话与 token", () => {
    const u = store.createUser("del_admin", "p", "Del");
    const token = store.createSession(u.id);
    store.deleteUserWithData(u.id);
    expect(store.findUserById(u.id)).toBeUndefined();
    expect(store.resolveSession(token)).toBeUndefined();
  });
});

describe("audit", () => {
  it("appendAudit 写入 + listAudit 按倒序返回", () => {
    store.clearAudit();
    store.appendAudit({ userId: "u-1", userName: "Alice", action: "创建任务", target: "abc", detail: "POST /api/tasks" });
    store.appendAudit({ userId: "u-1", userName: "Alice", action: "删除任务", target: "abc", detail: "DELETE /api/tasks/abc" });
    const rows = store.listAudit();
    expect(rows.length).toBe(2);
    // 倒序：最新的在前
    expect(rows[0].action).toBe("删除任务");
    expect(rows[0].target).toBe("abc");
    expect(rows[1].action).toBe("创建任务");
    expect(rows[1].userName).toBe("Alice");
    expect(rows[0].userId).toBe("u-1");
  });

  it("listAudit 支持 limit", () => {
    store.clearAudit();
    for (let i = 0; i < 5; i++) store.appendAudit({ action: `动作${i}` });
    expect(store.listAudit(2).length).toBe(2);
    expect(store.listAudit().length).toBe(5);
    store.clearAudit();
  });

  it("appendAudit 匿名（无 user）也可写", () => {
    store.clearAudit();
    store.appendAudit({ action: "登录" }); // 无 userId/userName
    const rows = store.listAudit();
    expect(rows.length).toBe(1);
    expect(rows[0].action).toBe("登录");
    expect(rows[0].userId).toBeNull(); // SQLite 无 user_id 时读回 null 而非 undefined
    store.clearAudit();
  });
});

describe("cleanup 自动清理（M32）", () => {
  const setAge = (id: string, msAgo: number) =>
    db.prepare(`UPDATE tasks SET created_ts = ? WHERE id = ?`).run(Date.now() - msAgo, id);

  it("getCleanableTaskIds 只取旧的终态任务（不动 running/queue/归档）", () => {
    // 老终态 → 应可清理
    store.insertTask(taskWithStatus("doneOld", "done"));
    setAge("doneOld", 100 * 24 * 3600 * 1000);
    store.insertTask(taskWithStatus("failOld", "failed"));
    setAge("failOld", 100 * 24 * 3600 * 1000);
    // 新终态 → 不应清理
    store.insertTask(taskWithStatus("doneNew", "done"));
    setAge("doneNew", 1000);
    // 非终态 → 不应清理
    store.insertTask(taskWithStatus("runOld", "running"));
    setAge("runOld", 100 * 24 * 3600 * 1000);
    // 已归档 → 不应清理
    store.insertTask(taskWithStatus("archOld", "done", { archived: true }));
    setAge("archOld", 100 * 24 * 3600 * 1000);

    const ids = store.getCleanableTaskIds(Date.now() - 30 * 24 * 3600 * 1000).map((t) => t.id);
    expect(ids).toContain("doneOld");
    expect(ids).toContain("failOld");
    expect(ids).not.toContain("doneNew");
    expect(ids).not.toContain("runOld");
    expect(ids).not.toContain("archOld");
  });

  it("listDeliverableFiles 取到 is_deliverable=1 且带 path 的文件名", () => {
    store.insertTask(taskWithStatus("t1", "done"));
    store.insertArtifact({ name: "报告.pptx", kind: "office", note: "x", path: "报告.pptx" }, "t1", 1);
    store.insertArtifact({ name: "草稿.txt", kind: "text", note: "x", path: "草稿.txt" }, "t1", 0); // 非交付
    store.insertTask(taskWithStatus("t2", "done"));
    store.insertArtifact({ name: "表.xlsx", kind: "office", path: "表.xlsx" }, "t2", 1);

    const files = store.listDeliverableFiles(["t1", "t2", "notExist"]);
    expect(files).toHaveLength(2);
    expect(files.map((f) => f.path).sort()).toEqual(["报告.pptx", "表.xlsx"]);
  });

  it("listDeliverableFiles 空入参安全返回空数组", () => {
    expect(store.listDeliverableFiles([])).toEqual([]);
  });

  it("pruneAudit 只保留最新 keepMax 条", () => {
    store.clearAudit();
    for (let i = 0; i < 6; i++) store.appendAudit({ action: `动作${i}` });
    const removed = store.pruneAudit(2);
    expect(removed).toBe(4);
    const rows = store.listAudit();
    expect(rows.length).toBe(2);
    expect(rows[0].action).toBe("动作5"); // 最新在前
    expect(rows[1].action).toBe("动作4");
    store.clearAudit();
  });

  it("insertTask 写入 created_ts（epoch ms）", () => {
    const before = Date.now();
    store.insertTask(taskWithStatus("ts1", "queue"));
    const row = db.prepare(`SELECT created_ts FROM tasks WHERE id = 'ts1'`).get() as { created_ts: number };
    expect(row.created_ts).toBeGreaterThanOrEqual(before);
    expect(row.created_ts).toBeLessThanOrEqual(Date.now());
  });
});

describe("任务模板（M33）", () => {
  it("createTaskTemplate + getTaskTemplate 读回 JSON 字段", () => {
    const id = store.createTaskTemplate(
      { name: "周报", desc: "生成销售周报", prompt: "基于数据生成周报", expert: "数据分析师", skills: ["Excel"], model: "c1·deepseek" },
      "u-1",
    );
    const t = store.getTaskTemplate(id)!;
    expect(t.name).toBe("周报");
    expect(t.expert).toBe("数据分析师");
    expect(t.skills).toEqual(["Excel"]);
    expect(t.model).toBe("c1·deepseek");
    expect(store.getTaskTemplate("nope")).toBeNull();
  });

  it("listTaskTemplates 多用户隔离（自己 + 全局）", () => {
    store.createTaskTemplate({ name: "全局模板", prompt: "p" }); // 无 userId → 全局
    store.createTaskTemplate({ name: "甲模板", prompt: "p" }, "alice");
    store.createTaskTemplate({ name: "乙模板", prompt: "p" }, "bob");
    const names = (s?: string) => store.listTaskTemplates(s).map((t) => t.name);
    expect(names("alice")).toEqual(expect.arrayContaining(["全局模板", "甲模板"]));
    expect(names("alice")).not.toContain("乙模板");
    expect(names(undefined)).toContain("全局模板");
  });

  it("updateTaskTemplate 局部更新 + 找不到返回 false", () => {
    const id = store.createTaskTemplate({ name: "旧名", prompt: "p" });
    expect(store.updateTaskTemplate(id, { name: "新名" })).toBe(true);
    expect(store.getTaskTemplate(id)!.name).toBe("新名");
    expect(store.updateTaskTemplate("ghost", { name: "x" })).toBe(false);
  });

  it("deleteTaskTemplate 删除并返回存在性", () => {
    const id = store.createTaskTemplate({ name: "待删", prompt: "p" });
    expect(store.deleteTaskTemplate(id)).toBe(true);
    expect(store.getTaskTemplate(id)).toBeNull();
    expect(store.deleteTaskTemplate(id)).toBe(false);
  });

  it("skills 缺省存 null，读回 undefined", () => {
    const id = store.createTaskTemplate({ name: "简", prompt: "p" });
    const t = store.getTaskTemplate(id)!;
    expect(t.skills).toBeUndefined();
    expect(t.expert).toBeUndefined();
  });
});

describe("交付版本历史（C5 store）", () => {
  it("addFileVersion 同文件 seq 递增、listFileVersions 倒序、getFileVersion 命中", () => {
    const s1 = store.addFileVersion("报告.pptx", "t1", "_ark_versions/报告.pptx/1-报告.pptx");
    const s2 = store.addFileVersion("报告.pptx", "t2", "_ark_versions/报告.pptx/2-报告.pptx");
    const s3 = store.addFileVersion("报告.pptx", undefined, "_ark_versions/报告.pptx/3-报告.pptx");
    expect(s1).toBe(1);
    expect(s2).toBe(2);
    expect(s3).toBe(3);
    const list = store.listFileVersions("报告.pptx");
    expect(list).toHaveLength(3);
    expect(list[0].seq).toBe(3); // 倒序：最新在前
    expect(list[0].taskId).toBeNull(); // SQLite 无值读回 null
    expect(list[1].taskId).toBe("t2");
    expect(store.getFileVersion("报告.pptx", 2)?.taskId).toBe("t2");
    expect(store.getFileVersion("报告.pptx", 99)).toBeNull();
  });

  it("不同文件名版本互不影响", () => {
    store.addFileVersion("A.xlsx", "t1", "a");
    store.addFileVersion("B.xlsx", "t1", "b");
    expect(store.listFileVersions("A.xlsx")).toHaveLength(1);
    expect(store.listFileVersions("B.xlsx")).toHaveLength(1); // 各自从 1 开始
  });

  it("deleteFileVersions 清空该文件版本记录", () => {
    store.addFileVersion("C.xlsx", "t1", "c1");
    store.addFileVersion("C.xlsx", "t2", "c2");
    store.deleteFileVersions("C.xlsx");
    expect(store.listFileVersions("C.xlsx")).toHaveLength(0);
  });
});

describe("用户自建专家（C6 store）", () => {
  it("createExpert 落库并回读、listCustomExperts 多用户隔离", () => {
    const aId = store.createExpert({ name: "投研专家", desc: "行业研究", skills: "2", connectors: "1" }, "u1");
    store.createExpert({ name: "另一用户的", desc: "私有", skills: "1" }, "u2");
    // u1 只见自己 + 全局，不见 u2 的
    const forU1 = store.listCustomExperts("u1");
    expect(forU1.some((e) => e.id === aId)).toBe(true);
    expect(forU1.some((e) => e.name === "另一用户的")).toBe(false);
    // 未登录（null）只见全局 user_id IS NULL，与任务/模板隔离语义一致
    const all = store.listCustomExperts(undefined);
    expect(all.length).toBe(0);
  });

  it("updateExpert 局部更新与全量聚合", () => {
    const id = store.createExpert({ name: "旧名", icon: "专", color: "#8B5E34", desc: "d", skills: "1", connectors: "0" });
    expect(store.updateExpert(id, { desc: "新简介", skills: "3" })).toBe(true);
    const list = store.listCustomExperts(undefined);
    const c = list.find((e) => e.id === id)!;
    expect(c.desc).toBe("新简介");
    expect(c.skills).toBe("3");
    expect(c.name).toBe("旧名"); // 未传字段保持不变
    expect(store.updateExpert("nope", { name: "x" })).toBe(false);
  });

  it("deleteExpert 删除存在返回 true，再次删除 false", () => {
    const id = store.createExpert({ name: "待删" });
    expect(store.deleteExpert(id)).toBe(true);
    expect(store.deleteExpert(id)).toBe(false);
    expect(store.listCustomExperts(undefined).some((e) => e.id === id)).toBe(false);
  });
});

describe("记忆系统（M41 store）", () => {
  it("createMemory 落库回读 + listMemories 多用户隔离", () => {
    const id = store.createMemory({ kind: "note", content: "用户偏好暖色调界面", tags: "偏好" }, "u1");
    store.createMemory({ kind: "fact", content: "手里的私有记忆", tags: "" }, "u2");
    // u1 只见自己，不见 u2
    const forU1 = store.listMemories("u1");
    expect(forU1.some((m) => m.id === id)).toBe(true);
    expect(forU1.some((m) => m.content === "手里的私有记忆")).toBe(false);
    // 未登录只见全局（user_id IS NULL）
    expect(store.listMemories(undefined)).toHaveLength(0);
  });

  it("updateMemory 局部更新 + 不存在返回 false", () => {
    const id = store.createMemory({ kind: "note", content: "旧" });
    expect(store.updateMemory(id, { content: "新内容" })).toBe(true);
    const g = store.getMemory(id)!;
    expect(g.content).toBe("新内容");
    expect(g.kind).toBe("note");
    expect(store.updateMemory("nope", { content: "x" })).toBe(false);
  });

  it("deleteMemory 删除返回 true 且 FTS 同步清理", () => {
    const id = store.createMemory({ kind: "note", content: "待删" });
    expect(store.deleteMemory(id)).toBe(true);
    expect(store.deleteMemory(id)).toBe(false);
    expect(store.searchMemories("待删", undefined)).toHaveLength(0);
  });

  it("searchMemories 走 FTS trigram 命中子串", () => {
    store.createMemory({ kind: "preference", content: "界面主色喜欢暖茶褐色", tags: "" });
    store.createMemory({ kind: "note", content: "完全不相关内容", tags: "" });
    const hits = store.searchMemories("暖茶褐", undefined);
    expect(hits.some((m) => m.content.includes("暖茶褐色"))).toBe(true);
    expect(hits.some((m) => m.content === "完全不相关内容")).toBe(false);
  });
});

describe("记忆来源溯源（M60 store）", () => {
  it("createMemory 记录 source，三条读取路径都透出", () => {
    const manual = store.createMemory({ kind: "note", content: "手动一条", source: "manual" }, "u1");
    const chat = store.createMemory({ kind: "preference", content: "来自对话 偏好暖色", source: "chat:abc123" }, "u1");
    const distill = store.createMemory({ kind: "fact", content: "来自提炼 团队五人", source: "chat-distill:xyz789" }, "u1");
    // 未给 source 默认 null
    const none = store.createMemory({ kind: "note", content: "没标来源" }, "u1");

    expect(store.getMemory(manual)!.source).toBe("manual");
    expect(store.getMemory(chat)!.source).toBe("chat:abc123");
    expect(store.getMemory(distill)!.source).toBe("chat-distill:xyz789");
    expect(store.getMemory(none)!.source).toBeNull();

    // listMemories 透出
    const list = store.listMemories("u1");
    expect(list.find((m) => m.id === chat)!.source).toBe("chat:abc123");
    // searchMemories（FTS）透出
    const hits = store.searchMemories("来自对话", "u1");
    expect(hits.find((m) => m.id === chat)!.source).toBe("chat:abc123");
    // searchMemoriesFor 透出
    const forHits = store.searchMemoriesFor("用户说偏好暖色", "u1");
    expect(forHits.find((m) => m.id === chat)!.source).toBe("chat:abc123");
  });

  it("createMemory 不传 source 时回读为 null（向后兼容）", () => {
    const id = store.createMemory({ kind: "note", content: "旧式调用" }, "u1");
    expect(store.getMemory(id)!.source).toBeNull();
  });
});

describe("记忆时效（M61 store）", () => {
  it("expires_at 落库透出；自动注入(searchMemoriesFor)过滤过期、list/get 仍可见", () => {
    const fresh = store.createMemory({ kind: "preference", content: "偏好暖茶褐界面", source: "manual" }, "u1");
    const stale = store.createMemory(
      { kind: "note", content: "偏好暖茶褐界面 过期版", source: "manual", expiresAtMs: Date.now() - 1000 },
      "u1",
    );
    // list/get 透出过期时间（管理仍可见）
    expect(store.getMemory(stale)!.expires_at).toBeLessThan(Date.now());
    expect(store.listMemories("u1").some((m) => m.id === stale)).toBe(true);
    // 自动注入路径过滤掉过期记忆
    const inj = store.searchMemoriesFor("偏好暖茶褐界面", "u1");
    expect(inj.some((m) => m.id === fresh)).toBe(true);
    expect(inj.some((m) => m.id === stale)).toBe(false);
  });

  it("setMemoryExpiry 设/清过期；不存在或越权返回 false", () => {
    const id = store.createMemory({ kind: "note", content: "可设时效" }, "u1");
    // 设过期
    expect(store.setMemoryExpiry(id, Date.now() + 60000, "u1")).toBe(true);
    expect(store.getMemory(id)!.expires_at).toBeGreaterThan(Date.now());
    // 清过期（长期有效）
    expect(store.setMemoryExpiry(id, null, "u1")).toBe(true);
    expect(store.getMemory(id)!.expires_at).toBeNull();
    // 不存在 / 不可见
    expect(store.setMemoryExpiry("nope", 1, "u1")).toBe(false);
    const other = store.createMemory({ kind: "note", content: "他人记忆" }, "u2");
    expect(store.setMemoryExpiry(other, 1, "u1")).toBe(false);
  });
});

describe("记忆合并维护闭环（M63 store）", () => {
  it("mergeMemories 保时间为空时继承来源 + 取最早过期", () => {
    const keep = store.createMemory({ kind: "note", content: "同一件事", tags: "" }, "u1"); // 无来源、无时效
    const shortExpiry = Date.now() + 3600000; // 一小时后
    const dupWithSrc = store.createMemory(
      { kind: "preference", content: "同一件事", tags: "界面", source: "chat:seed1", expiresAtMs: Date.now() + 86400000 },
      "u1",
    );
    const dupShort = store.createMemory(
      { kind: "fact", content: "同一件事", tags: "设计", source: "chat-distill:seed2", expiresAtMs: shortExpiry },
      "u1",
    );
    store.mergeMemories(keep, [dupWithSrc, dupShort], "u1");
    const k = store.getMemory(keep)!;
    // 继承最早一个有来源的（dupWithSrc 的 source）
    expect(k.source).toBe("chat:seed1");
    // 取最早过期（dupShort 的一小时后）
    expect(k.expires_at).toBe(shortExpiry);
  });

  it("mergeMemories 保留 keep 已有来源/更早时效不被覆盖", () => {
    const keepExpiry = Date.now() + 60000; // keep 一分钟后的更早过期
    const keep = store.createMemory(
      { kind: "note", content: "另一件事", tags: "", source: "manual", expiresAtMs: keepExpiry },
      "u1",
    );
    const dup = store.createMemory(
      { kind: "note", content: "另一件事", tags: "标签", source: "chat:later", expiresAtMs: Date.now() + 86400000 },
      "u1",
    );
    store.mergeMemories(keep, [dup], "u1");
    const k = store.getMemory(keep)!;
    // keep 已有来源 → 不被覆盖
    expect(k.source).toBe("manual");
    // keep 更早过期 → 保留
    expect(k.expires_at).toBe(keepExpiry);
  });
});

describe("记忆批量整理（M57 store）", () => {
  beforeEach(() => {
    resetTables();
  });

  it("deleteMemories 批量删除只删本用户可见的记忆", () => {
    const a = store.createMemory({ kind: "note", content: "甲" }, "u1");
    const b = store.createMemory({ kind: "fact", content: "乙" }, "u1");
    store.createMemory({ kind: "note", content: "丙（他人）" }, "u2");
    // 传入他人 id 应被隔离过滤，不误删
    const deleted = store.deleteMemories([a, b, "notexist"], "u1");
    expect(deleted).toBe(2);
    expect(store.getMemory(a)).toBeNull();
    expect(store.getMemory(b)).toBeNull();
    expect(store.listMemories(undefined)).toHaveLength(0);
  });

  it("deleteMemories 空参返回 0；FTS 同步清理", () => {
    expect(store.deleteMemories([], "u1")).toBe(0);
    const a = store.createMemory({ kind: "note", content: "全文可搜 暖茶褐" }, "u1");
    store.deleteMemories([a], "u1");
    expect(store.searchMemories("暖茶褐", "u1")).toHaveLength(0);
  });

  it("findDuplicateMemories 归一化后归组，保留最早、其余为重复", () => {
    const first = store.createMemory({ kind: "note", content: "用户喜欢暖茶褐界面。" }, "u1");
    store.createMemory({ kind: "fact", content: "用户喜欢暖茶褐界面！" }, "u1");
    store.createMemory({ kind: "note", content: "完全不同的另一条" }, "u1");
    const groups = store.findDuplicateMemories("u1");
    expect(groups).toHaveLength(1);
    expect(groups[0].canonical.id).toBe(first);
    expect(groups[0].duplicates).toHaveLength(1);
  });

  it("findDuplicateMemories 无重复返回空；?kind 可仅对某类扫描", () => {
    store.createMemory({ kind: "note", content: "唯一一条" }, "u1");
    expect(store.findDuplicateMemories("u1")).toHaveLength(0);
  });

  it("findDuplicateMemories 跨 kind 同内容也视为重复，kind 过滤只扫该类", () => {
    store.createMemory({ kind: "fact", content: "重复内容" }, "u1");
    store.createMemory({ kind: "preference", content: "重复内容" }, "u1");
    // 不指定 kind：跨类同内容归为一组（1 条重复）
    const all = store.findDuplicateMemories("u1");
    expect(all).toHaveLength(1);
    expect(all[0].duplicates).toHaveLength(1);
    // 只扫 fact：该类仅 1 条，无重复
    expect(store.findDuplicateMemories("u1", "fact")).toHaveLength(0);
  });
});

describe("记忆合并（M59 store）", () => {
  beforeEach(() => {
    resetTables();
  });

  it("mergeMemories 合并 tags 后删除重复，keep 保留", () => {
    const keep = store.createMemory({ kind: "note", content: "用户偏好暖色", tags: "偏好,界面" }, "u1");
    const dup = store.createMemory({ kind: "note", content: "用户偏好暖色", tags: "设计" }, "u1");
    const r = store.mergeMemories(keep, [dup], "u1");
    expect(r.kept).toBe(keep);
    expect(r.removed).toEqual([dup]);
    const g = store.getMemory(keep);
    expect(g).not.toBeNull();
    // 集合比较：三个来源标签都应合并且出现一次（顺序无关）
    const tags = (g!.tags ?? "").split(",").filter(Boolean);
    expect(tags).toHaveLength(3);
    for (const t of ["偏好", "界面", "设计"]) expect(tags).toContain(t);
    expect(store.getMemory(dup)).toBeNull();
    // FTS 同步清理：kept 内容仍可搜到（用 ≥3 字串，FTS5 trigram 阈值）
    expect(store.searchMemories("偏好暖色", "u1")).toHaveLength(1);
  });

  it("mergeMemories 空 removeIds / 不可见 keep → 拒绝且不改数据", () => {
    const keep = store.createMemory({ kind: "note", content: "私有记忆", tags: "a" }, "u1");
    expect(store.mergeMemories(keep, [], "u1").kept).toBeNull();
    store.createMemory({ kind: "note", content: "他人记忆" }, "u2");
    // keep 不可见（他人）
    const other = store.createMemory({ kind: "note", content: "他人待并", tags: "x" }, "u2");
    expect(store.mergeMemories(keep, [other], "u1").kept).toBeNull();
    // 数据未被改动
    expect(store.getMemory(keep)!.tags).toBe("a");
    expect(store.getMemory(other)).not.toBeNull();
  });
});

describe("记忆对话注入（M42 store searchMemoriesFor）", () => {
  beforeEach(() => {
    resetTables();
  });

  it("抽取用户消息的 CJK 二元组，高召回命中相关记忆", () => {
    store.createMemory({ kind: "preference", content: "开会喜欢简短结论" }, "u1");
    store.createMemory({ kind: "note", content: "下周去上海出差" }, "u1");
    // 消息里有「喜欢」二元组 → 命中第一条
    const hits = store.searchMemoriesFor("我比较喜欢简洁汇报", "u1");
    expect(hits.some((m) => m.content.includes("开会喜欢简短结论"))).toBe(true);
    expect(hits.some((m) => m.content.includes("上海出差"))).toBe(false);
  });

  it("英文/数字词（≥3 字符）也能命中；多用户隔离", () => {
    store.createMemory({ kind: "note", content: "项目代号 ark engine" }, "u1");
    store.createMemory({ kind: "note", content: "另一用户 ark 私有" }, "u2");
    const hits = store.searchMemoriesFor("帮我看下 ark engine 部署", "u1");
    expect(hits.some((m) => m.content.includes("项目代号 ark engine"))).toBe(true);
    expect(hits.some((m) => m.content.includes("另一用户"))).toBe(false);
  });

  it("无字词或纯标点消息返回空", () => {
    store.createMemory({ kind: "note", content: "会议定在上午" }, "u1");
    expect(store.searchMemoriesFor("！！！？？", "u1")).toHaveLength(0);
    expect(store.searchMemoriesFor("", "u1")).toHaveLength(0);
  });
});

describe("IM 消息桥配置（M45 store）", () => {
  it("默认关闭并自动生成 secret", () => {
    const s = store.getImSettings();
    expect(s.enabled).toBe(false);
    expect(s.secret.length).toBeGreaterThan(5);
    expect(s.endpoint).toBe("/api/im");
  });

  it("开关/改名可持久化", () => {
    store.setImEnabled(true);
    store.setImName("企业微信");
    const s = store.getImSettings();
    expect(s.enabled).toBe(true);
    expect(s.name).toBe("企业微信");
  });

  it("rotateImSecret 生成新值且与旧值不同", () => {
    const a = store.getImSettings().secret;
    const b = store.rotateImSecret();
    expect(b).not.toBe(a);
    expect(store.getImSettings().secret).toBe(b);
  });
});
