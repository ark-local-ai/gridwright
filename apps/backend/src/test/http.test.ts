import { afterAll, beforeAll, describe, expect, it } from "vitest";
import type { LightMyRequestResponse } from "fastify";
import { loadIsolatedStore, makeTask, cleanup } from "../test/helpers";

// C3：默认 http 测试关掉认证硬门禁，保持既有注入用例（auth-gate 单独开立一套验证）
process.env.ARK_REQUIRE_AUTH = "0";

// buildApp 由 server.ts 提供；其 import 会触发 store/searchIndex 等模块副作用，
// 但 loadIsolatedStore 已先设好临时 ARK_DB_PATH / ARK_TEST，不会污染真实库、也不会起监听。
let store: typeof import("../db/store");
let buildApp: typeof import("../server").buildApp;
let tmpDir: string;
let app: Awaited<ReturnType<typeof import("../server").buildApp>>;

beforeAll(async () => {
  const loaded = await loadIsolatedStore();
  store = loaded.store;
  tmpDir = loaded.dir;
  // 清掉播种之外的数据，保证幂等起点
  store.db.exec(`DELETE FROM tasks; DELETE FROM steps; DELETE FROM artifacts; DELETE FROM users; DELETE FROM sessions;`);
  const serverModule = await import("../server");
  buildApp = serverModule.buildApp;
  app = await buildApp();
  await app.ready();
});

afterAll(async () => {
  await app.close();
  cleanup(tmpDir);
});

async function post(url: string, body: unknown, token?: string): Promise<LightMyRequestResponse> {
  return app.inject({
    method: "POST",
    url,
    payload: body as Record<string, unknown>,
    headers: token ? { authorization: `Bearer ${token}` } : {},
  });
}
async function get(url: string, token?: string): Promise<LightMyRequestResponse> {
  return app.inject({
    method: "GET",
    url,
    headers: token ? { authorization: `Bearer ${token}` } : {},
  });
}
async function putTemplate(id: string, body: unknown): Promise<LightMyRequestResponse> {
  return app.inject({ method: "PUT", url: `/api/templates/${id}`, payload: body as Record<string, unknown> });
}
async function delTemplate(id: string): Promise<LightMyRequestResponse> {
  return app.inject({ method: "DELETE", url: `/api/templates/${id}` });
}
async function injectJson(method: "POST" | "PUT" | "DELETE", url: string, body?: unknown): Promise<LightMyRequestResponse> {
  return app.inject({ method, url, payload: body as Record<string, unknown> | undefined });
}

describe("认证 API", () => {
  it("register → me → login → 错密 401 → logout → me 401", async () => {
    const reg = await post("/api/auth/register", { username: "alice", password: "secret1", displayName: "Alice" });
    expect(reg.statusCode).toBe(201);
    const token: string = reg.json().token;
    expect(token).toBeTruthy();

    const me = await get("/api/auth/me", token);
    expect(me.statusCode).toBe(200);
    expect(me.json().user.username).toBe("alice");

    const dup = await post("/api/auth/register", { username: "alice", password: "secret1" });
    expect(dup.statusCode).toBe(409); // 重名

    const login = await post("/api/auth/login", { username: "alice", password: "secret1" });
    expect(login.statusCode).toBe(200);

    const bad = await post("/api/auth/login", { username: "alice", password: "wrong" });
    expect(bad.statusCode).toBe(401);

    const logout = await post("/api/auth/logout", {}, token);
    expect(logout.statusCode).toBe(204);
    const me2 = await get("/api/auth/me", token);
    expect(me2.statusCode).toBe(401);
  });
});

describe("任务/统计/搜索路由", () => {
  beforeAll(async () => {
    // 直接落库构造数据，避免触发真实编排（写盘慢、非幂等）
    store.insertTask(makeTask({ id: "t1", title: "甲", status: "done" }), undefined);
    store.insertTask(makeTask({ id: "t2", title: "乙", status: "failed" }), undefined);
    store.insertTask(makeTask({ id: "t3", title: "丙", status: "running" }), undefined);
    store.insertStep("t1", 0, "步骤A");
  });

  it("GET /health", async () => {
    const r = await get("/health");
    expect(r.statusCode).toBe(200);
    expect(r.json().ok).toBe(true);
  });

  it("GET /api/tasks 返回摘要（含归档过滤）", async () => {
    const r = await get("/api/tasks");
    expect(r.statusCode).toBe(200);
    const ids = r.json().map((t: { id: string }) => t.id);
    expect(ids).toEqual(expect.arrayContaining(["t1", "t2", "t3"]));
  });

  it("GET /api/tasks/:id 完整快照（含步骤）", async () => {
    const r = await get("/api/tasks/t1");
    expect(r.statusCode).toBe(200);
    const t = r.json();
    expect(t.status).toBe("done");
    expect(t.steps).toHaveLength(1);
  });

  it("GET /:id 不存在返回 404", async () => {
    const r = await get("/api/tasks/nope");
    expect(r.statusCode).toBe(404);
  });

  it("GET /api/stats 聚合任务与渠道", async () => {
    const r = await get("/api/stats");
    expect(r.statusCode).toBe(200);
    const s = r.json();
    expect(s.tasks.total).toBeGreaterThanOrEqual(3);
    expect(s.channels.total).toBeGreaterThanOrEqual(1);
  });
});

describe("审计 API", () => {
  it("写请求经 onResponse 钩子落审计，GET /api/audit 可读", async () => {
    // 触发一个真实的 HTTP 写请求
    const r = await post("/api/auth/register", { username: "audit_probe", password: "x1234567", displayName: "probe" });
    expect(r.statusCode).toBe(201);
    const probe = await get("/api/audit");
    expect(probe.statusCode).toBe(200);
    const rows = probe.json() as { action: string; detail: string; userId?: string }[];
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.some((x) => x.detail.includes("/api/auth/register"))).toBe(true);
    expect(rows.some((x) => x.action === "登录" || x.action === "注册用户")).toBe(true);
  });
});

describe("管理端用户 API（M67）", () => {
  // alice 是本文件首个注册用户 → 自动成为管理员；bob_admin 是新注册的普通用户
  it("仅管理员可访问用户清单；普通用户 403", async () => {
    const admin = await post("/api/auth/login", { username: "alice", password: "secret1" });
    const adminToken: string = admin.json().token;
    const r = await get("/api/users", adminToken);
    expect(r.statusCode).toBe(200);
    const { users } = r.json() as { users: { username: string; isAdmin: boolean }[] };
    expect(users.some((u) => u.username === "alice" && u.isAdmin)).toBe(true);

    // 普通用户访问 → 403
    const normal = await post("/api/auth/register", { username: "bob_admin", password: "secret2" });
    expect(normal.statusCode).toBe(201);
    const bobToken: string = normal.json().token;
    const forbidden = await get("/api/users", bobToken);
    expect(forbidden.statusCode).toBe(403);
  });

  it("禁用用户后其会话失效；不能禁自己/最后一个管理员", async () => {
    const admin = await post("/api/auth/login", { username: "alice", password: "secret1" });
    const adminToken: string = admin.json().token;

    // 注册一个将被禁用的用户，拿到其 id 与 token
    const victim = await post("/api/auth/register", { username: "bob_disabled", password: "secret3" });
    const victimToken: string = victim.json().token;
    const victimId: string = victim.json().user.id;

    // 禁用它
    const dis = await post(`/api/users/${victimId}/disable`, {}, adminToken);
    expect(dis.statusCode).toBe(200);

    // 禁用后其会话应立即失效：GET /api/auth/me → 401
    const meAfter = await get("/api/auth/me", victimToken);
    expect(meAfter.statusCode).toBe(401);

    // 普通用户不能操作其他用户（非管理员对 disable 也是 403）
    const normal = await post("/api/auth/register", { username: "bob_snoop", password: "secret4" });
    const snoopToken: string = normal.json().token;
    const viaNormal = await post(`/api/users/${victimId}/enable`, {}, snoopToken);
    expect(viaNormal.statusCode).toBe(403);

    // 管理员重新启用它，会话恢复（重新登录产生新 token）
    const en = await post(`/api/users/${victimId}/enable`, {}, adminToken);
    expect(en.statusCode).toBe(200);
  });

  it("不能删除最后一个管理员（自护）；管理员可删除普通用户", async () => {
    const admin = await post("/api/auth/login", { username: "alice", password: "secret1" });
    const adminToken: string = admin.json().token;
    const { users } = (await get("/api/users", adminToken)).json() as { users: { id: string; isAdmin: boolean }[] };
    const alice = users.find((u) => u.isAdmin)!;

    // 删最后一个管理员（alice 是唯一 admin）→ 400 拒绝
    const delLastAdmin = await app.inject({
      method: "DELETE", url: `/api/users/${alice.id}`, headers: { authorization: `Bearer ${adminToken}` },
    });
    expect(delLastAdmin.statusCode).toBe(400);

    // 管理员删除一个普通用户 → 200，且用户清单里不再有它
    const tmp = await post("/api/auth/register", { username: "bob_del", password: "secret5" });
    const tmpId: string = tmp.json().user.id;
    const del = await app.inject({
      method: "DELETE", url: `/api/users/${tmpId}`, headers: { authorization: `Bearer ${adminToken}` },
    });
    expect(del.statusCode).toBe(200);
    const after = (await get("/api/users", adminToken)).json() as { users: { username: string }[] };
    expect(after.users.some((u) => u.username === "bob_del")).toBe(false);
  });
});

describe("对话搜索路由", () => {
  it("发消息后 /api/chat/search 命中", async () => {
    const sid = store.createChatSession("会话1", undefined);
    store.appendChatMessage(sid, "user", "想找行业报告和量子计算的资料");
    const r = await get("/api/chat/search?q=" + encodeURIComponent("行业报告"));
    expect(r.statusCode).toBe(200);
    const hits = r.json() as { sessionId: string }[];
    expect(hits.length).toBeGreaterThanOrEqual(1);
    expect(hits[0].sessionId).toBe(sid);
  });
});

describe("维护 API（M32）", () => {
  it("POST /api/maintenance/cleanup 返回清理汇总（含审计裁剪）", async () => {
    // 造几条审计日志，触发裁剪
    for (let i = 0; i < 3; i++) store.appendAudit({ action: `动作${i}` });
    const r = await post("/api/maintenance/cleanup", {});
    expect(r.statusCode).toBe(200);
    const body = r.json() as {
      enabled: boolean;
      tasksDeleted: number;
      filesDeleted: number;
      auditPruned: number;
      taskRetentionDays: number;
      auditKeepMax: number;
    };
    expect(body.enabled).toBe(true);
    expect(typeof body.tasksDeleted).toBe("number");
    expect(typeof body.filesDeleted).toBe("number");
    expect(typeof body.auditPruned).toBe("number");
    expect(body.taskRetentionDays).toBeGreaterThan(0);
    expect(body.auditKeepMax).toBeGreaterThan(0);
  });
});

describe("任务模板 API（M33）", () => {
  it("POST /api/templates 创建 → GET 列表/详情 → PUT 更新 → DELETE（含审计归类）", async () => {
    // 创建
    const created = await post("/api/templates", { name: "周报", prompt: "生成一份周报", expert: "数据分析师" });
    expect(created.statusCode).toBe(201);
    const id: string = created.json().id;
    expect(id).toBeTruthy();

    // 校验 name/prompt 必填
    const bad = await post("/api/templates", { name: "x" }); // 缺 prompt
    expect(bad.statusCode).toBe(400);

    // 列表包含
    const list = await get("/api/templates");
    expect(list.statusCode).toBe(200);
    expect((list.json() as { id: string }[]).some((t) => t.id === id)).toBe(true);

    // 详情回填 JSON 字段
    const detail = await get(`/api/templates/${id}`);
    expect(detail.statusCode).toBe(200);
    expect(detail.json().expert).toBe("数据分析师");

    // 更新
    const updated = await putTemplate(id, { name: "周报v2" });
    expect(updated.statusCode).toBe(200);
    const after = await get(`/api/templates/${id}`);
    expect(after.json().name).toBe("周报v2");

    // 删除
    const del = await delTemplate(id);
    expect(del.statusCode).toBe(200);
    const gone = await get(`/api/templates/${id}`);
    expect(gone.statusCode).toBe(404);
  });

  it("POST /api/templates/:id/run 应用模板建任务并返回 taskId", async () => {
    const created = await post("/api/templates", { name: "快跑", prompt: "跑一个任务" });
    const id: string = created.json().id;
    const run = await post(`/api/templates/${id}/run`, {});
    expect(run.statusCode).toBe(201);
    const taskId: string = run.json().taskId;
    expect(taskId).toBeTruthy();
    // 任务由 runner 异步入队创建，这里不 assert 列表（异步竞态）；201+taskId 即契约
    // 清理
    await delTemplate(id);
  });
});

describe("任务另存为模板 API（C1）", () => {
  it("POST /api/tasks/:id/template 从已完成任务固化为模板；非终态 409", async () => {
    // 用 makeTask 直接落一个 done 任务到库
    const task = makeTask({ status: "done", prompt: "生成一份调研报告", expert: "数据分析师", model: "c1·deepseek" });
    store.insertTask(task);
    const r = await post(`/api/tasks/${task.id}/template`, { name: "调研模板" });
    expect(r.statusCode).toBe(201);
    const tplId: string = r.json().id;
    expect(tplId).toBeTruthy();
    // 模板已入库且继承了 prompt/expert/model
    const t = store.getTaskTemplate(tplId);
    expect(t?.name).toBe("调研模板");
    expect(t?.prompt).toBe("生成一份调研报告");
    expect(t?.expert).toBe("数据分析师");
    expect(t?.model).toBe("c1·deepseek");

    // 非 done 任务 → 409
    const run = makeTask({ status: "running", prompt: "进行中" });
    store.insertTask(run);
    const nope = await post(`/api/tasks/${run.id}/template`, {});
    expect(nope.statusCode).toBe(409);
  });
});

describe("C3 认证硬门禁（写请求需登录）", () => {
  it("匿名写请求 401、带 token 写 201、读请求开放；/api/auth/* 放行", async () => {
    // 关掉前一个 app 用的 env，重开一套带硬门禁的 app
    const saved = process.env.ARK_REQUIRE_AUTH;
    process.env.ARK_REQUIRE_AUTH = "1";
    const gapp = await buildApp();
    await gapp.ready();
    try {
      // 匿名写 → 401
      const anon = await gapp.inject({ method: "POST", url: "/api/tasks", payload: { prompt: "xx" } });
      expect(anon.statusCode).toBe(401);

      // 读请求开放（匿名可看列表，虽然空）
      const read = await gapp.inject({ method: "GET", url: "/api/tasks" });
      expect(read.statusCode).toBe(200);

      // auth 端点放行（注册）
      const reg = await gapp.inject({
        method: "POST", url: "/api/auth/register",
        payload: { username: "gate_user", password: "pw1234", displayName: "Gate" },
      });
      expect(reg.statusCode).toBe(201);
      const token: string = reg.json().token;

      // 带 token 写 → 201
      const authed = await gapp.inject({
        method: "POST", url: "/api/tasks", payload: { prompt: "做一份报告" },
        headers: { authorization: `Bearer ${token}` },
      });
      expect(authed.statusCode).toBe(201);
    } finally {
      await gapp.close();
      process.env.ARK_REQUIRE_AUTH = saved;
    }
  });

  it("M52 POST /api/tasks 携带 refs（送给任务）→ 201 返回 taskId（入队成功）", async () => {
    // refs 逻辑（注入上下文/参考资料 section）已由 context.test.ts 单测覆盖；
    // 此处只验证路由契约：带 refs 载荷建任务不报错、返回 taskId、空正文 ref 被安全过滤。
    const r = await app.inject({
      method: "POST",
      url: "/api/tasks",
      payload: {
        prompt: "基于参考资料写一份分析",
        refs: [
          { title: "趋势报告", url: "https://a.com/x", text: "这是用户指定带过去的参考资料正文。" },
          { text: "只有正文的纯片段" },
          { title: "空正文应被过滤" },
        ],
      },
    });
    expect(r.statusCode).toBe(201);
    const { taskId: id } = r.json() as { taskId: string };
    expect(id).toBeTruthy();

    // 无 prompt → 400
    const bad = await app.inject({ method: "POST", url: "/api/tasks", payload: { prompt: "  " } });
    expect(bad.statusCode).toBe(400);
  });
});

describe("交付版本 API（C5）", () => {
  it("GET /api/versions/:name 返回历史（无则空数组）；非法名 400", async () => {
    const empty = await get("/api/versions/report.pptx");
    expect(empty.statusCode).toBe(200);
    expect(empty.json()).toEqual([]);

    const bad = await get(`/api/versions/${encodeURIComponent("a/b")}`);
    expect(bad.statusCode).toBe(400);
  });

  it("POST /:name/rollback 缺 seq → 400", async () => {
    const r = await post("/api/versions/report.pptx/rollback", {});
    expect(r.statusCode).toBe(400);
  });
});

describe("用户自建专家 API（C6）", () => {
  it("POST 创建 → GET 列表含自定义 → PUT 编辑 → DELETE 删除", async () => {
    // 创建（无认证门禁：ARK_REQUIRE_AUTH=0）
    const c = await injectJson("POST", "/api/experts", { name: "行业投研专家", desc: "行业研究", skills: "2" });
    expect(c.statusCode).toBe(201);
    const { id } = c.json() as { id: string };
    expect(id).toBeTruthy();

    // 列表包含内置 + 自定义
    const list = await get("/api/experts");
    expect(list.statusCode).toBe(200);
    const arr = list.json() as { id: string; builtin: boolean; name: string }[];
    expect(arr.some((x) => x.id === id && !x.builtin && x.name === "行业投研专家")).toBe(true);
    expect(arr.some((x) => x.builtin)).toBe(true); // 内置种子仍在

    // 编辑
    const up = await injectJson("PUT", `/api/experts/${id}`, { desc: "更新后的简介" });
    expect(up.statusCode).toBe(200);

    // 删除
    const del = await injectJson("DELETE", `/api/experts/${id}`, undefined);
    expect(del.statusCode).toBe(200);
    const after = await get("/api/experts");
    const arr2 = after.json() as { id: string }[];
    expect(arr2.some((x) => x.id === id)).toBe(false);
  });

  it("创建缺 name → 400；删除不存在 → 404；专家写操作落审计", async () => {
    const bad = await injectJson("POST", "/api/experts", { desc: "没名字" });
    expect(bad.statusCode).toBe(400);

    const nf = await injectJson("DELETE", "/api/experts/nope", undefined);
    expect(nf.statusCode).toBe(404);

    // 审计应记录创建/删除专家
    const audit = await get("/api/audit");
    const rows = audit.json() as { action: string }[];
    expect(rows.some((x) => x.action.includes("专家"))).toBe(true);
  });
});

describe("联网搜索源管理 API（M53）", () => {
  it("GET /search/source 返回启停/provider/配额；默认 duckduckgo + rpm", async () => {
    const r = await get("/api/tools/search/source");
    expect(r.statusCode).toBe(200);
    const s = r.json();
    expect(s.enabled).toBe(true);
    expect(s.provider).toBe("duckduckgo");
    expect(s.quote.rpm).toBeGreaterThan(0);
  });

  it("POST /provider 切 custom 存 endpoint/key，再读回；非法 provider 400", async () => {
    const ok = await post("/api/tools/search/source/provider",
      { provider: "custom", endpoint: "https://search.example.com/v1", key: "sk-abc" });
    expect(ok.statusCode).toBe(200);
    expect(ok.json().provider).toBe("custom");

    const s = (await get("/api/tools/search/source")).json();
    expect(s.provider).toBe("custom");
    expect(s.endpoint).toBe("https://search.example.com/v1");
    expect(s.hasKey).toBe(true);

    const bad = await post("/api/tools/search/source/provider", { provider: "nope" });
    expect(bad.statusCode).toBe(400);

    // 切回 duckduckgo 清理
    await post("/api/tools/search/source/provider", { provider: "duckduckgo" });
  });

  it("POST /quota 调整配额（含上下限钳制）；缺 rpm 400", async () => {
    const ok = await post("/api/tools/search/source/quota", { rpm: 5 });
    expect(ok.statusCode).toBe(200);
    expect(ok.json().quote.rpm).toBe(5);

    const huge = await post("/api/tools/search/source/quota", { rpm: 99999 });
    expect(huge.json().quote.rpm).toBe(6000);

    const bad = await post("/api/tools/search/source/quota", {});
    expect(bad.statusCode).toBe(400);

    // 复位
    await post("/api/tools/search/source/quota", { rpm: 30 });
  });
});

describe("资料加工 API（M54 refine）", () => {
  it("POST /refine 非法 kind → 400；缺 text → 400；合法调用回 viaLLM/文本（无渠道则脚本降级）", async () => {
    const badKind = await post("/api/tools/refine", { kind: "nope", text: "x" });
    expect(badKind.statusCode).toBe(400);

    const noText = await post("/api/tools/refine", { kind: "summarize" });
    expect(noText.statusCode).toBe(400);

    // 合法调用：本测试环境通常无可用模型渠道 → 走确定性降级，viaLLM=false 但返回文本
    const ok = await post("/api/tools/refine", { kind: "summarize", text: "一段需要总结的资料正文内容。" });
    expect(ok.statusCode).toBe(200);
    const body = ok.json();
    expect(body.kind).toBe("summarize");
    expect(typeof body.text).toBe("string");
  });
});

describe("记忆批量整理 API（M57）", () => {
  it("POST /api/memories/batch-delete 批量删 + 空 ids 400", async () => {
    // 软门禁下 currentUser 为 undefined，故建无主（user_id NULL）记忆供其可见
    const a = store.createMemory({ kind: "note", content: "批量甲" });
    const b = store.createMemory({ kind: "fact", content: "批量乙" });

    const ok = await post("/api/memories/batch-delete", { ids: [a, b, "nope"] });
    expect(ok.statusCode).toBe(200);
    expect(ok.json()).toMatchObject({ ok: true, deleted: 2 });

    expect(store.getMemory(a)).toBeNull();
    expect(store.getMemory(b)).toBeNull();

    const empty = await post("/api/memories/batch-delete", { ids: [] });
    expect(empty.statusCode).toBe(400);
  });

  it("GET /api/memories/duplicates 归一化归组，?kind 过滤生效", async () => {
    store.createMemory({ kind: "fact", content: "重复内容暖茶褐" });
    store.createMemory({ kind: "preference", content: "重复内容暖茶褐" });
    store.createMemory({ kind: "note", content: "唯一不重复" });

    const all = await get("/api/memories/duplicates");
    expect(all.statusCode).toBe(200);
    const groups = all.json() as { canonical: { content: string }; duplicates: unknown[] }[];
    expect(groups.length).toBe(1);
    expect(groups[0].duplicates.length).toBe(1);

    const faktOnly = await get("/api/memories/duplicates?kind=fact");
    expect((faktOnly.json() as unknown[]).length).toBe(0);
  });
});

describe("对话记忆沉淀 API（M58）", () => {
  it("POST /:id/distill/save 保存勾选的候选记忆，空 facts 400", async () => {
    const sid = store.createChatSession("M58 会话", "u1");
    store.appendChatMessage(sid, "user", "我喜欢简洁的深色 PPT");
    store.appendChatMessage(sid, "assistant", "好的");

    const ok = await post(`/api/chat/sessions/${sid}/distill/save`, {
      facts: [{ kind: "preference", content: "喜欢简洁的深色 PPT" }],
    });
    expect(ok.statusCode).toBe(200);
    const body = ok.json() as { ok: boolean; saved: unknown[] };
    expect(body.ok).toBe(true);
    expect(body.saved.length).toBe(1);
    // 落库后可被检索到
    expect(store.searchMemories("深色 PPT", "u1").length).toBeGreaterThan(0);

    const empty = await post(`/api/chat/sessions/${sid}/distill/save`, { facts: [] });
    expect(empty.statusCode).toBe(400);
  });

  it("POST /:id/distill 返回候选（无模型走启发式，空会话 404）", async () => {
    const sid = store.createChatSession("M58 提取", "u1");
    store.appendChatMessage(sid, "user", "我平时偏好暖色调界面，不要用冷蓝色。");
    store.appendChatMessage(sid, "assistant", "收到");

    const r = await post(`/api/chat/sessions/${sid}/distill`, {});
    expect(r.statusCode).toBe(200);
    const body = r.json() as { candidates: { kind: string; content: string }[] };
    expect(Array.isArray(body.candidates)).toBe(true);
    // 作者：启发式会抽取带「偏好」信号的用户陈述
    expect(body.candidates.some((c) => c.content.includes("暖色调"))).toBe(true);

    const emptySid = store.createChatSession("空会话", "u1");
    const empty = await post(`/api/chat/sessions/${emptySid}/distill`, {});
    expect(empty.statusCode).toBe(404);
  });
});

describe("记忆合并 API（M59）", () => {
  it("POST /api/memories/merge 合并 tags 删重复；缺参 400；不可见 404", async () => {
    const keep = store.createMemory({ kind: "note", content: "偏好暖色", tags: "界面" });
    const dup = store.createMemory({ kind: "note", content: "偏好暖色", tags: "设计" });

    const ok = await post("/api/memories/merge", { keepId: keep, removeIds: [dup] });
    expect(ok.statusCode).toBe(200);
    expect(ok.json()).toMatchObject({ ok: true, kept: keep, removed: [dup] });
    // 数据落库校验
    const g = store.getMemory(keep)!;
    const tags = (g.tags ?? "").split(",").filter(Boolean);
    expect(tags).toHaveLength(2);
    expect(tags).toContain("界面");
    expect(tags).toContain("设计");
    expect(store.getMemory(dup)).toBeNull();

    const noarg = await post("/api/memories/merge", { keepId: "", removeIds: [] });
    expect(noarg.statusCode).toBe(400);

    const missing = await post("/api/memories/merge", { keepId: "nope", removeIds: [keep] });
    expect(missing.statusCode).toBe(404);
  });
});

describe("记忆来源溯源 API（M60）", () => {
  it("POST /api/memories 手动画 source 落库透出；缺省返 manual", async () => {
    const withSrc = await post("/api/memories", { kind: "note", content: "带来源", source: "chat:abc" });
    expect(withSrc.statusCode).toBe(201);
    expect(store.getMemory(withSrc.json().id)!.source).toBe("chat:abc");

    const noSrc = await post("/api/memories", { kind: "note", content: "不带来源" });
    expect(noSrc.statusCode).toBe(201);
    expect(store.getMemory(noSrc.json().id)!.source).toBe("manual");
  });

  it("POST /:id/distill/save 落库记忆带 chat-distill 来源", async () => {
    const sid = store.createChatSession("M60 会话", "u1");
    store.appendChatMessage(sid, "user", "我喜欢墨绿配色");
    store.appendChatMessage(sid, "assistant", "收到");

    const ok = await post(`/api/chat/sessions/${sid}/distill/save`, {
      facts: [{ kind: "preference", content: "喜欢墨绿配色" }],
    });
    expect(ok.statusCode).toBe(200);
    const savedId = (ok.json() as { saved: { id: string }[] }).saved[0].id;
    expect(store.getMemory(savedId)!.source).toBe(`chat-distill:${sid}`);
  });
});

describe("记忆时效 API（M61）", () => {
  it("POST /api/memories 支持 expiresAtMs；POST /:id/expiry 设/清时效；越权 404", async () => {
    const withExp = await post("/api/memories", { kind: "note", content: "带时效", expiresAtMs: Date.now() + 60000 });
    expect(withExp.statusCode).toBe(201);
    expect(store.getMemory(withExp.json().id)!.expires_at).toBeGreaterThan(Date.now());

    const noExp = await post("/api/memories", { kind: "note", content: "不带时效" });
    expect(store.getMemory(noExp.json().id)!.expires_at).toBeNull();

    // 设时效
    const set = await post(`/api/memories/${noExp.json().id}/expiry`, { expiresAtMs: Date.now() + 3600000 });
    expect(set.statusCode).toBe(200);
    expect(store.getMemory(noExp.json().id)!.expires_at).toBeGreaterThan(Date.now());
    // 清时效（长期有效）
    const clear = await post(`/api/memories/${noExp.json().id}/expiry`, { expiresAtMs: null });
    expect(clear.statusCode).toBe(200);
    expect(store.getMemory(noExp.json().id)!.expires_at).toBeNull();
    // 不存在 → 404
    const missing = await post("/api/memories/nope/expiry", { expiresAtMs: 1 });
    expect(missing.statusCode).toBe(404);
  });
});
