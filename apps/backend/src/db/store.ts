import { DatabaseSync } from "node:sqlite";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { Artifact, Task, TaskStep, StepStatus, TaskStatus } from "../types";

const __dirname = dirname(fileURLToPath(import.meta.url));
const dataDir = join(__dirname, "..", "..", "data");
mkdirSync(dataDir, { recursive: true });
// 默认库文件 data/ark.db；测试可用 ARK_DB_PATH 指向临时库，避免污染真实数据（M25）
const dbPath = process.env.ARK_DB_PATH || join(dataDir, "ark.db");

// Node 22 内置 SQLite：零原生编译依赖，本地优先首选
export const db = new DatabaseSync(dbPath);
db.exec("PRAGMA journal_mode = WAL;");

// ---- 建表（幂等）----
db.exec(`
  CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queue',
    model TEXT,
    expert TEXT,
    skills TEXT,
    workspace TEXT,
    checks TEXT,
    timeline TEXT,
    created TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    note TEXT,
    seq INTEGER NOT NULL
  );

  CREATE TABLE IF NOT EXISTS artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    note TEXT,
    path TEXT,
    is_deliverable INTEGER NOT NULL DEFAULT 0
  );
`);

// ---- 迁移：旧库补 steps.note 列（CREATE IF NOT EXISTS 不会补列）----
const stepCols = db.prepare(`PRAGMA table_info(steps)`).all() as { name: string }[];
if (!stepCols.some((c) => c.name === "note")) {
  db.exec(`ALTER TABLE steps ADD COLUMN note TEXT`);
}

// ---- 迁移：多用户归属（tasks.user_id / spaces.user_id），NULL=全局/未登录兼容 ----
const taskCols = db.prepare(`PRAGMA table_info(tasks)`).all() as { name: string }[];
if (!taskCols.some((c) => c.name === "user_id")) {
  db.exec(`ALTER TABLE tasks ADD COLUMN user_id TEXT`);
}

// ---- 迁移：任务归档标记（tasks.archived），0=活跃 1=已归档 ----
if (!taskCols.some((c) => c.name === "archived")) {
  db.exec(`ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`);
}

// ---- 迁移：任务创建时间戳（tasks.created_ts，epoch ms，M32 清理按龄用）----
// 旧行没有该列：补列后把既有记录统一回填为当前时间——本地既有数据的 created 是 zh-CN locale
// 字符串，无法可靠解析为 epoch，按"现在"记既安全又避免一迁移就被按龄清除。
if (!taskCols.some((c) => c.name === "created_ts")) {
  db.exec(`ALTER TABLE tasks ADD COLUMN created_ts INTEGER`);
  db.exec(`UPDATE tasks SET created_ts = ${Date.now()}`);
}

// ---- 迁移：任务参考资料（tasks.refs，M64 方向二）----
// refs 存「送给任务」的参考资料（AttachmentRef[] 的 JSON 序列化）——断点续跑/进程重启后
// resumeTask 需要重建参考资料注入多 Agent 上下文，否则参考资料会随重启丢失。
if (!taskCols.some((c) => c.name === "refs")) {
  db.exec(`ALTER TABLE tasks ADD COLUMN refs TEXT`);
}

// ---- 任务写 / 读（node:sqlite 同步 API，prepare().run() / .get() / .all()）----
export function insertTask(task: Task, userId?: string, refs?: { title?: string; url?: string; text: string }[]): void {
  db.prepare(
    `INSERT INTO tasks (id, title, prompt, status, model, expert, skills, workspace, checks, timeline, created, created_ts, archived, user_id, refs)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
  ).run(
    task.id, task.title, task.prompt, task.status, task.model, task.expert,
    JSON.stringify(task.skills), task.workspace,
    JSON.stringify(task.checks), JSON.stringify(task.timeline), task.created,
    Date.now(), task.archived ? 1 : 0, userId ?? null,
    refs && refs.length ? JSON.stringify(refs) : null,
  );
}

/** M64：读取一条任务的参考资料（AttachmentRef[]），无则返回空数组。 */
export function getTaskRefs(id: string): { title?: string; url?: string; text: string }[] {
  const r = db.prepare(`SELECT refs FROM tasks WHERE id = ?`).get(id) as { refs: string | null } | undefined;
  if (!r?.refs) return [];
  try {
    const arr = JSON.parse(r.refs);
    return Array.isArray(arr) ? arr.filter((x: { text?: string }) => !!x?.text) : [];
  } catch {
    return [];
  }
}

/** M64：为已存在的任务写入参考资料（用于入队前异步补存/修复）。 */
export function updateTaskRefs(id: string, refs: { title?: string; url?: string; text: string }[]): void {
  db.prepare(`UPDATE tasks SET refs = ? WHERE id = ?`).run(refs && refs.length ? JSON.stringify(refs) : null, id);
}

export function insertStep(taskId: string, seq: number, title: string): number {
  const r = db
    .prepare(`INSERT INTO steps (task_id, title, status, seq) VALUES (?, ?, 'pending', ?)`)
    .run(taskId, title, seq);
  return Number(r.lastInsertRowid);
}

export function updateStepStatus(id: number, status: StepStatus, note?: string): void {
  db.prepare(`UPDATE steps SET status = ?, note = ? WHERE id = ?`).run(status, note ?? null, id);
}

export function updateTaskStatus(id: string, status: TaskStatus): void {
  db.prepare(`UPDATE tasks SET status = ? WHERE id = ?`).run(status, id);
}

export function updateTaskChecksAndTimeline(
  id: string,
  checks: { label: string; ok: boolean }[],
  timeline: { time: string; label: string }[],
): void {
  db.prepare(`UPDATE tasks SET checks = ?, timeline = ? WHERE id = ?`).run(
    JSON.stringify(checks), JSON.stringify(timeline), id,
  );
}

export function insertArtifact(a: Artifact, taskId: string, isDeliverable = 0): void {
  db.prepare(
    `INSERT INTO artifacts (task_id, name, kind, note, path, is_deliverable)
     VALUES (?, ?, ?, ?, ?, ?)`,
  ).run(taskId, a.name, a.kind, a.note ?? null, a.path ?? null, isDeliverable);
}

/**
 * 幂等清空某任务的全部旧行（steps / artifacts / 主行），用于重试同一 id 前复位，
 * 也是删除可复用的底层（M17）。
 */
export function resetTask(id: string): void {
  db.prepare(`DELETE FROM steps WHERE task_id = ?`).run(id);
  db.prepare(`DELETE FROM artifacts WHERE task_id = ?`).run(id);
  db.prepare(`DELETE FROM tasks WHERE id = ?`).run(id);
}

/** 删除某任务（含其步骤/产物）；返回该任务原本是否存在 */
export function deleteTask(id: string): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM tasks WHERE id = ?`).get(id);
  resetTask(id);
  return existed;
}

// ---- M32 自动清理：按龄取可清理的终态任务 / 取交付文件 / 裁剪审计 ----
/** 取创建时间早于 `olderThanMs` 的**终态**（done/failed）任务 id（running/queue/归档都不动） */
export function getCleanableTaskIds(olderThanMs: number): { id: string }[] {
  return db.prepare(
    `SELECT id FROM tasks
     WHERE status IN ('done','failed') AND archived = 0 AND created_ts IS NOT NULL AND created_ts < ?
     ORDER BY created_ts ASC`,
  ).all(olderThanMs) as { id: string }[];
}

/** 取这些任务的交付文件名（artifacts.path 只存文件名；绝对路径由调用方按工作目录拼装） */
export function listDeliverableFiles(taskIds: string[]): { taskId: string; path: string }[] {
  if (taskIds.length === 0) return [];
  const ph = taskIds.map(() => "?").join(",");
  return db.prepare(
    `SELECT task_id AS taskId, path FROM artifacts WHERE is_deliverable = 1 AND path IS NOT NULL AND task_id IN (${ph})`,
  ).all(...taskIds) as { taskId: string; path: string }[];
}

/** 裁剪审计日志：只保留最新 keepMax 条（按 id 倒序），返回删除条数 */
export function pruneAudit(keepMax: number): number {
  const r = db.prepare(
    `DELETE FROM audit_log WHERE id NOT IN (SELECT id FROM audit_log ORDER BY id DESC LIMIT ?)`,
  ).run(keepMax);
  return Number(r.changes);
}

/**
 * 进程重启后仍「未完成」的任务（queue/running 且未归档）——供 M24 启动恢复重入队。
 * 编排在 runTask 开头会 resetTask(id) 幂等复位，因此重跑一次即干净地续跑/重做。
 */
export function getUnfinishedTasks(): { id: string; prompt: string; userId?: string; status: TaskStatus }[] {
  const rows = db.prepare(
    `SELECT id, prompt, user_id AS userId, status FROM tasks WHERE status IN ('queue','running') AND archived = 0 ORDER BY created`,
  ).all() as { id: string; prompt: string; userId?: string; status: TaskStatus }[];
  return rows;
}

/** 归档/取消归档任务（M22）；返回该任务原本是否存在 */
export function setTaskArchived(id: string, archived: boolean): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM tasks WHERE id = ?`).get(id);
  if (existed) {
    db.prepare(`UPDATE tasks SET archived = ? WHERE id = ?`).run(archived ? 1 : 0, id);
  }
  return existed;
}

// ---- 任务读 ----
export function getTask(id: string): Task | null {
  const row = db.prepare(`SELECT * FROM tasks WHERE id = ?`).get(id) as
    | ({
        id: string; title: string; prompt: string; status: TaskStatus; model: string;
        expert: string; skills: string; workspace: string; checks: string; timeline: string; created: string;
        archived: number;
      })
    | undefined;
  if (!row) return null;

  const steps = db
    .prepare(`SELECT id, title, status, note FROM steps WHERE task_id = ? ORDER BY seq`)
    .all(id)
    .map((s) => s as unknown as TaskStep);
  const artifacts = db
    .prepare(`SELECT name, kind, note, path FROM artifacts WHERE task_id = ? AND is_deliverable = 0`)
    .all(id)
    .map((a) => a as unknown as Artifact);
  const deliverable = db
    .prepare(`SELECT name, kind, note, path FROM artifacts WHERE task_id = ? AND is_deliverable = 1 LIMIT 1`)
    .get(id) as Artifact | undefined;

  return {
    id: row.id,
    title: row.title,
    prompt: row.prompt,
    status: row.status,
    model: row.model,
    expert: row.expert,
    skills: JSON.parse(row.skills),
    workspace: row.workspace,
    steps,
    artifacts,
    deliverable: deliverable ?? null,
    checks: JSON.parse(row.checks),
    timeline: JSON.parse(row.timeline),
    created: row.created,
    archived: !!row.archived,
  };
}

/** 任务各状态计数（供统计仪表盘 M18） */
export function countTasksByStatus(): Record<string, number> {
  const rows = db.prepare(`SELECT status, COUNT(*) AS n FROM tasks GROUP BY status`).all() as
    { status: string; n: number }[];
  return rows.reduce<Record<string, number>>((acc, r) => { acc[r.status] = r.n; return acc; }, {});
}

/** 最近 N 个任务（含状态+标题，供仪表盘最近动态） */
export function recentTasks(limit = 8): { id: string; title: string; created: string; status: string; archived: boolean }[] {
  const rows = db
    .prepare(`SELECT id, title, created, status, archived FROM tasks ORDER BY created DESC LIMIT ?`)
    .all(limit) as { id: string; title: string; created: string; status: string; archived: number }[];
  return rows.map((r) => ({ id: r.id, title: r.title, created: r.created, status: r.status, archived: !!r.archived }));
}
export interface TaskSummary {
  id: string;
  title: string;
  created: string;
  status: TaskStatus;
  archived: boolean;
}

/**
 * 任务列表摘要（供侧栏「最近任务」），按创建时间倒序。
 * `archivedOnly`：仅列出归档任务（否则默认只列活跃任务——归档任务默认不在主列表出现）。
 */
export function listTasks(limit = 50, userId?: string, archivedOnly = false): TaskSummary[] {
  // 归档筛选列：archivedOnly ? 只看归档 : 只看活跃（归档默认不进主列表）
  const filter = archivedOnly ? `archived = 1` : `archived = 0`;
  let rows: { id: string; title: string; created: string; status: TaskStatus; archived: number }[];
  // 多用户隔离：登录用户看自己的 + 全局无主任务；未登录看全部（兼容）
  if (userId) {
    rows = db.prepare(
      `SELECT id, title, created, status, archived FROM tasks WHERE (user_id = ? OR user_id IS NULL) AND ${filter} ORDER BY created DESC LIMIT ?`,
    ).all(userId, limit) as typeof rows;
  } else {
    rows = db.prepare(
      `SELECT id, title, created, status, archived FROM tasks WHERE ${filter} ORDER BY created DESC LIMIT ?`,
    ).all(limit) as typeof rows;
  }
  return rows.map((r) => ({ id: r.id, title: r.title, created: r.created, status: r.status, archived: !!r.archived }));
}

// ============================== 多工作空间 spaces ==============================
// 每个空间 = SQLite 一行 + 磁盘一个工作目录（workspace/<dir>/）。
// 数据不出本机：表建在本地 ark.db，目录在本地 workspace。

export interface Space {
  id: string;
  name: string;
  dir: string;      // 工作子目录名（唯一）
  isActive: boolean; // 是否活动空间
  created: string;
  userId?: string;  // 归属用户；undefined=全局/默认
}

// 建表（幂等）
db.exec(`
  CREATE TABLE IF NOT EXISTS spaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    dir TEXT NOT NULL UNIQUE,
    is_active INTEGER NOT NULL DEFAULT 0,
    created TEXT NOT NULL
  );
`);

// 迁移：旧库补 spaces.user_id 列（须在建表之后执行，否则全新库会因表不存在报错）
const spaceCols = db.prepare(`PRAGMA table_info(spaces)`).all() as { name: string }[];
if (!spaceCols.some((c) => c.name === "user_id")) {
  db.exec(`ALTER TABLE spaces ADD COLUMN user_id TEXT`);
}

// ===== 对话（chat）持久化（M19）=====
// 助理对话存入 SQLite：会话 + 消息两表。user_id 与任务同规（NULL=全局/未登录）。
db.exec(`
  CREATE TABLE IF NOT EXISTS chat_sessions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    user_id TEXT,
    created TEXT NOT NULL,
    updated TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    seq INTEGER NOT NULL
  );

  CREATE VIRTUAL TABLE IF NOT EXISTS chat_messages_fts USING fts5(
    session_id UNINDEXED, role UNINDEXED, content, tokenize = 'trigram'
  );

  CREATE TABLE IF NOT EXISTS task_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    desc TEXT,
    prompt TEXT NOT NULL,
    expert TEXT,
    skills TEXT,
    model TEXT,
    user_id TEXT,
    created TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS file_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_name TEXT NOT NULL,
    task_id TEXT,
    seq INTEGER NOT NULL DEFAULT 1,
    ts TEXT NOT NULL,
    archived TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS experts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    icon TEXT,
    color TEXT,
    desc TEXT,
    skills TEXT,
    connectors TEXT,
    user_id TEXT,
    created TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL DEFAULT 'note',
    content TEXT NOT NULL,
    tags TEXT,
    user_id TEXT,
    created TEXT NOT NULL
  );

  CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content, id UNINDEXED, user_id UNINDEXED, tokenize = 'trigram'
  );
`);

// ---- 迁移：记忆溯源（memories.source，M60）----
// source 记录这条记忆从哪来：null=手动/未知；"chat:<sessionId>"（/记得 沉淀于某会话）、
// "chat-distill:<sessionId>"（M58 从某会话提炼）。前端据此渲染「来源」回链，点开原会话。
const memCols = db.prepare(`PRAGMA table_info(memories)`).all() as { name: string }[];
if (!memCols.some((c) => c.name === "source")) {
  db.exec(`ALTER TABLE memories ADD COLUMN source TEXT`);
}

// ---- 迁移：记忆时效（memories.expires_at，M61）----
// expires_at 存储过期时间（epoch ms，可空）：到达后该记忆视为失效，不再被自动注入进对话，
// 但保留在列表（前端标注「已过期」供延期或删除）。null=长期有效（永不自动过期）。
if (!memCols.some((c) => c.name === "expires_at")) {
  db.exec(`ALTER TABLE memories ADD COLUMN expires_at INTEGER`);
}

export interface ChatSessionRow {
  id: string; title: string; created: string; updated: string;
}
export interface ChatMessageRow {
  role: "user" | "assistant"; content: string;
}

export function createChatSession(title: string, userId?: string): string {
  const id = Math.random().toString(36).slice(2, 10);
  const ts = new Date().toLocaleString("zh-CN", { hour12: false });
  db.prepare(`INSERT INTO chat_sessions (id, title, user_id, created, updated) VALUES (?, ?, ?, ?, ?)`)
    .run(id, title, userId ?? null, ts, ts);
  return id;
}

export function appendChatMessage(sessionId: string, role: string, content: string): void {
  const { n } = db
    .prepare(`SELECT COALESCE(MAX(seq), 0) + 1 AS n FROM chat_messages WHERE session_id = ?`)
    .get(sessionId) as { n: number };
  db.prepare(`INSERT INTO chat_messages (session_id, role, content, seq) VALUES (?, ?, ?, ?)`)
    .run(sessionId, role, content, n);
  // 同步进 FTS 索引，保证新消息即时可搜（M23）
  db.prepare(`INSERT INTO chat_messages_fts (session_id, role, content) VALUES (?, ?, ?)`)
    .run(sessionId, role, content);
  db.prepare(`UPDATE chat_sessions SET updated = ? WHERE id = ?`)
    .run(new Date().toLocaleString("zh-CN", { hour12: false }), sessionId);
}

export function listChatSessions(userId?: string): ChatSessionRow[] {
  return (userId
    ? db.prepare(`SELECT id, title, created, updated FROM chat_sessions WHERE user_id = ? OR user_id IS NULL ORDER BY updated DESC`)
      .all(userId)
    : db.prepare(`SELECT id, title, created, updated FROM chat_sessions ORDER BY updated DESC`).all()
  ) as unknown as ChatSessionRow[];
}

export function getChatMessages(sessionId: string): ChatMessageRow[] {
  return db
    .prepare(`SELECT role, content FROM chat_messages WHERE session_id = ? ORDER BY seq`)
    .all(sessionId) as unknown as ChatMessageRow[];
}

export function deleteChatSession(id: string): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM chat_sessions WHERE id = ?`).get(id);
  db.prepare(`DELETE FROM chat_messages WHERE session_id = ?`).run(id);
  db.prepare(`DELETE FROM chat_sessions WHERE id = ?`).run(id);
  return existed;
}

// ===== 对话全文搜索（M23，FTS5 trigram）=====
// 与工作空间文件搜索（searchIndex.ts）同套路：对中文做真正的子串匹配。
// 消息在 appendChatMessage 时增量入索引；此处也提供启动时全量重建兜底。
export interface ChatSearchHit {
  sessionId: string;
  title: string;
  role: string;
  snippet: string;
  updated: string;
}

/** 重建整个聊天 FTS 索引（启动时调用）：清空 → 回灌全部消息 */
export function reindexChats(): number {
  db.exec(`DELETE FROM chat_messages_fts;`);
  const rows = db.prepare(`SELECT session_id, role, content FROM chat_messages`).all() as
    { session_id: string; role: string; content: string }[];
  const ins = db.prepare(`INSERT INTO chat_messages_fts (session_id, role, content) VALUES (?, ?, ?)`);
  for (const r of rows) ins.run(r.session_id, r.role, r.content);
  return rows.length;
}

function escapeFtsPhrase(q: string): string {
  return q.replace(/["']/g, " ");
}

function makeSnippet(content: string, q: string): string {
  const idx = content.indexOf(q);
  const start = Math.max(0, idx - 10);
  const piece = content.slice(start, start + 50);
  return (start > 0 ? "…" : "") + piece + (start + 50 < content.length ? "…" : "");
}

/**
 * 按消息内容搜索历史对话（≥3 字走 FTS trigram 子串；更短退回 LIKE）。
 * 按会话去重（一个会话只返回一条命中），多用户隔离与会话列表同规。
 */
export function searchChatMessages(rawQ: string, userId?: string): ChatSearchHit[] {
  const q = rawQ.trim();
  if (!q) return [];
  const base = `FROM chat_messages_fts ft JOIN chat_sessions s ON s.id = ft.session_id`;
  const sel = `SELECT s.id AS sessionId, s.title AS title, ft.role AS role, ft.content AS content, s.updated AS updated\n           ${base}`;
  const rows: { sessionId: string; title: string; role: string; content: string; updated: string }[] =
    q.length >= 3
      ? userId
        ? (db.prepare(`${sel} WHERE chat_messages_fts MATCH ? AND (s.user_id = ? OR s.user_id IS NULL) ORDER BY s.updated DESC LIMIT 50`).all(`"${escapeFtsPhrase(q)}"`, userId) as typeof rows)
        : (db.prepare(`${sel} WHERE chat_messages_fts MATCH ? ORDER BY s.updated DESC LIMIT 50`).all(`"${escapeFtsPhrase(q)}"`) as typeof rows)
      : userId
        ? (db.prepare(`${sel} WHERE ft.content LIKE ? AND (s.user_id = ? OR s.user_id IS NULL) ORDER BY s.updated DESC LIMIT 50`).all(`%${q}%`, userId) as typeof rows)
        : (db.prepare(`${sel} WHERE ft.content LIKE ? ORDER BY s.updated DESC LIMIT 50`).all(`%${q}%`) as typeof rows);
  // 按会话去重（保留最新一条命中）
  const seen = new Set<string>();
  const hits: ChatSearchHit[] = [];
  for (const r of rows) {
    if (seen.has(r.sessionId)) continue;
    seen.add(r.sessionId);
    hits.push({ sessionId: r.sessionId, title: r.title, role: r.role, snippet: makeSnippet(r.content, q), updated: r.updated });
  }
  return hits;
}

// 首次启动若没有空间，播种一个「默认工作空间」（dir 为空字符串表示根目录，兼容既有平铺文件）
function seedSpaces(): void {
  const n = db.prepare(`SELECT COUNT(*) AS n FROM spaces`).get() as { n: number };
  if (n.n === 0) {
    db.prepare(`INSERT INTO spaces (id, name, dir, is_active, created) VALUES (?, ?, ?, ?, ?)`)
      .run("default", "默认工作空间", "", 1, new Date().toLocaleString("zh-CN", { hour12: false }));
  }
}
seedSpaces();
reindexChats();

export function listSpaces(userId?: string): Space[] {
  // 多用户隔离：登录用户见自己的 + 全局；未登录见全部（兼容）
  const rows = userId
    ? db.prepare(`SELECT id, name, dir, is_active, created, user_id FROM spaces WHERE user_id = ? OR user_id IS NULL ORDER BY rowid`).all(userId) as {
        id: string; name: string; dir: string; is_active: number; created: string; user_id: string | null;
      }[]
    : db.prepare(`SELECT id, name, dir, is_active, created, user_id FROM spaces ORDER BY rowid`).all() as {
        id: string; name: string; dir: string; is_active: number; created: string; user_id: string | null;
      }[];
  return rows.map((r) => ({
    id: r.id, name: r.name, dir: r.dir, isActive: r.is_active === 1, created: r.created,
    userId: r.user_id ?? undefined,
  }));
}

export function getSpace(id: string): Space | undefined {
  const row = db.prepare(`SELECT id, name, dir, is_active, created FROM spaces WHERE id = ?`).get(id) as
    | { id: string; name: string; dir: string; is_active: number; created: string }
    | undefined;
  return row ? { id: row.id, name: row.name, dir: row.dir, isActive: row.is_active === 1, created: row.created } : undefined;
}

export function getSpaceByDir(dir: string): Space | undefined {
  const row = db.prepare(`SELECT id, name, dir, is_active, created FROM spaces WHERE dir = ?`).get(dir) as
    | { id: string; name: string; dir: string; is_active: number; created: string }
    | undefined;
  return row ? { id: row.id, name: row.name, dir: row.dir, isActive: row.is_active === 1, created: row.created } : undefined;
}

export function createSpace(name: string, dir: string, id?: string, userId?: string): Space {
  const sid = id ?? `sp-${Math.random().toString(36).slice(2, 8)}`;
  const created = new Date().toLocaleString("zh-CN", { hour12: false });
  db.prepare(`INSERT INTO spaces (id, name, dir, is_active, created, user_id) VALUES (?, ?, ?, ?, ?, ?)`)
    .run(sid, name, dir, 0, created, userId ?? null);
  return { id: sid, name, dir, isActive: false, created, userId };
}

export function updateSpace(
  id: string,
  patch: { name?: string; dir?: string; isActive?: boolean },
): Space | undefined {
  const cur = getSpace(id);
  if (!cur) return undefined;
  const name = patch.name ?? cur.name;
  const dir = patch.dir ?? cur.dir;
  const isActive = patch.isActive ?? cur.isActive;
  db.prepare(`UPDATE spaces SET name = ?, dir = ?, is_active = ? WHERE id = ?`).run(name, dir, isActive ? 1 : 0, id);
  return getSpace(id);
}

export function deleteSpace(id: string): boolean {
  const r = db.prepare(`DELETE FROM spaces WHERE id = ?`).run(id);
  return Number(r.changes) > 0;
}

/** 活动空间（is_active=1），无则取第一个，再无可取 null */
export function getActiveSpace(): Space | undefined {
  const first = listSpaces().find((s) => s.isActive);
  if (first) return first;
  return listSpaces()[0];
}

// ============================== 用户与会话（本地多用户）==============================
// 密码用 Node 内置 crypto.scrypt 哈希（零依赖、本地优先、免原生编译）。
// 会话 = 随机 token（存 sessions 表，带过期），前端持 token 走 Bearer 头。

export interface User {
  id: string;
  username: string;
  displayName: string;
  created: string;
  isAdmin: boolean;
  disabled: boolean;
}

export interface SessionRow {
  token: string;
  userId: string;
  expires: number;
}

// 建表（幂等）
db.exec(`
  CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    created TEXT NOT NULL
  );
  CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    expires INTEGER NOT NULL
  );
`);

export type UserRow = {
  id: string; username: string; password_hash: string; display_name: string; created: string;
  is_admin?: number; disabled?: number;
};

// 迁移：给 users 表补管理员/禁用标记（老库没有这两列，PRAGMA 检测后 ALTER 补上）
const userCols = db.prepare(`PRAGMA table_info(users)`).all() as { name: string }[];
if (!userCols.some((c) => c.name === "is_admin")) {
  db.exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`);
}
if (!userCols.some((c) => c.name === "disabled")) {
  db.exec(`ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`);
}

// ---- 密码哈希（scrypt：随机盐 + 时间成本）----
import { randomBytes, scryptSync, timingSafeEqual } from "node:crypto";

export function hashPassword(password: string): string {
  const salt = randomBytes(16);
  const hash = scryptSync(password, salt, 64) as Buffer;
  return `${salt.toString("hex")}:${hash.toString("hex")}`;
}

export function verifyPassword(password: string, stored: string): boolean {
  const [saltHex, hashHex] = stored.split(":");
  if (!saltHex || !hashHex) return false;
  const salt = Buffer.from(saltHex, "hex");
  const expected = Buffer.from(hashHex, "hex");
  const actual = scryptSync(password, salt, 64) as Buffer;
  return expected.length === actual.length && timingSafeEqual(expected, actual);
}

// ---- 用户读写 ----
export function findUserByUsername(username: string): UserRow | undefined {
  return db.prepare(`SELECT * FROM users WHERE username = ?`).get(username) as UserRow | undefined;
}

export function findUserById(id: string): User | undefined {
  const r = db.prepare(`SELECT id, username, display_name, created, is_admin, disabled FROM users WHERE id = ?`).get(id) as
    | { id: string; username: string; display_name: string; created: string; is_admin: number; disabled: number }
    | undefined;
  return r ? { id: r.id, username: r.username, displayName: r.display_name, created: r.created, isAdmin: r.is_admin === 1, disabled: r.disabled === 1 } : undefined;
}

export function createUser(username: string, password: string, displayName: string): User {
  const id = `u-${randomBytes(6).toString("hex")}`;
  const created = new Date().toLocaleString("zh-CN", { hour12: false });
  const hash = hashPassword(password);
  // 首个注册用户自动成为管理员（自托管多人服务端的合理默认）
  const isAdmin = countUsers() === 0 ? 1 : 0;
  db.prepare(`INSERT INTO users (id, username, password_hash, display_name, created, is_admin) VALUES (?, ?, ?, ?, ?, ?)`)
    .run(id, username, hash, displayName || username, created, isAdmin);
  return { id, username, displayName: displayName || username, created, isAdmin: isAdmin === 1, disabled: false };
}

export function countUsers(): number {
  const r = db.prepare(`SELECT COUNT(*) AS n FROM users`).get() as { n: number };
  return r.n;
}

// ---- 管理端（M67）：用户列表 + 用量 + 禁用 ----

export interface AdminUserRow {
  id: string;
  username: string;
  displayName: string;
  created: string;
  isAdmin: boolean;
  disabled: boolean;
  tasks: number;
  memories: number;
  sessions: number;
  lastActive: string | null;
}

export function listUsers(): AdminUserRow[] {
  const rows = db.prepare(`
    SELECT u.id, u.username, u.display_name AS displayName, u.created, u.is_admin AS isAdmin,
           u.disabled,
           (SELECT COUNT(*) FROM tasks t WHERE t.user_id = u.id) AS tasks,
           (SELECT COUNT(*) FROM memories m WHERE m.user_id = u.id) AS memories,
           (SELECT COUNT(*) FROM chat_sessions s WHERE s.user_id = u.id) AS sessions,
           (SELECT MAX(ts) FROM audit_log a WHERE a.user_id = u.id) AS lastActive
    FROM users u ORDER BY u.created
  `).all() as {
    id: string; username: string; displayName: string; created: string; isAdmin: number; disabled: number;
    tasks: number; memories: number; sessions: number; lastActive: string | null;
  }[];
  return rows.map((r) => ({
    id: r.id, username: r.username, displayName: r.displayName, created: r.created,
    isAdmin: r.isAdmin === 1, disabled: r.disabled === 1,
    tasks: r.tasks, memories: r.memories, sessions: r.sessions, lastActive: r.lastActive,
  }));
}

/** 设置用户的禁用/启用状态；返回是否成功（用户不存在返回 false） */
export function setUserDisabled(id: string, disabled: boolean): boolean {
  const r = db.prepare(`UPDATE users SET disabled = ? WHERE id = ?`).run(disabled ? 1 : 0, id);
  return r.changes > 0;
}

/** 统计当前有多少管理员——用于防止把最后一个管理员禁用/删掉 */
export function countAdmins(): number {
  const r = db.prepare(`SELECT COUNT(*) AS n FROM users WHERE is_admin = 1`).get() as { n: number };
  return r.n;
}

/** 删除用户及其全部归属数据（任务/步骤/记忆/会话/会话消息），并清其会话 token */
export function deleteUserWithData(userId: string): void {
  db.exec("BEGIN");
  try {
    const tids = (db.prepare(`SELECT id FROM tasks WHERE user_id = ?`).all(userId) as { id: string }[]).map((r) => r.id);
    for (const tid of tids) {
      db.prepare(`DELETE FROM steps WHERE task_id = ?`).run(tid);
      db.prepare(`DELETE FROM artifacts WHERE task_id = ?`).run(tid);
    }
    db.prepare(`DELETE FROM tasks WHERE user_id = ?`).run(userId);
    db.prepare(`DELETE FROM memories WHERE user_id = ?`).run(userId);
    const sids = (db.prepare(`SELECT id FROM chat_sessions WHERE user_id = ?`).all(userId) as { id: string }[]).map((r) => r.id);
    for (const sid of sids) {
      db.prepare(`DELETE FROM chat_messages WHERE session_id = ?`).run(sid);
      db.prepare(`DELETE FROM chat_messages_fts WHERE session_id = ?`).run(sid);
    }
    db.prepare(`DELETE FROM chat_sessions WHERE user_id = ?`).run(userId);
    db.prepare(`DELETE FROM sessions WHERE user_id = ?`).run(userId);
    db.prepare(`DELETE FROM users WHERE id = ?`).run(userId);
    db.exec("COMMIT");
  } catch (e) {
    db.exec("ROLLBACK");
    throw e;
  }
}

/** 用户维度任务计数：{userId: 正在进行的数量}，用于并发治理提示 */
export function countRunningTasksByUser(): Record<string, number> {
  const rows = db.prepare(`
    SELECT user_id AS userId, COUNT(*) AS n FROM tasks
    WHERE status IN ('queue', 'running') GROUP BY user_id
  `).all() as { userId: string | null; n: number }[];
  return Object.fromEntries(rows.map((r) => [r.userId ?? "", r.n]));
}

// ---- 会话 ----
const SESSION_TTL = 7 * 24 * 3600 * 1000; // 7 天

export function createSession(userId: string): string {
  const token = randomBytes(24).toString("hex");
  const expires = Date.now() + SESSION_TTL;
  db.prepare(`INSERT INTO sessions (token, user_id, expires) VALUES (?, ?, ?)`)
    .run(token, userId, expires);
  return token;
}

export function resolveSession(token: string): User | undefined {
  const row = db.prepare(`SELECT user_id, expires FROM sessions WHERE token = ?`).get(token) as
    | { user_id: string; expires: number }
    | undefined;
  if (!row) return undefined;
  if (Date.now() > row.expires) return undefined; // 过期即失效
  const user = findUserById(row.user_id);
  if (!user || user.disabled) return undefined; // 禁用用户拒绝其会话
  return user;
}

export function destroySession(token: string): void {
  db.prepare(`DELETE FROM sessions WHERE token = ?`).run(token);
}

/** 清理过期会话（可选，不阻塞） */
export function pruneSessions(): void {
  db.prepare(`DELETE FROM sessions WHERE expires < ?`).run(Date.now());
}

// ===== 操作审计日志（M29） =====
// 记录"谁在何时做了什么写操作"（登录/注册/建删任务/渠道 CRUD/空间 CRUD 等），
// 供合规审计与排查。写入是 fire-and-forget（不失败回滚业务），查询按时间倒序。
db.exec(`
  CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    user_id TEXT,
    user_name TEXT,
    action TEXT NOT NULL,
    target TEXT,
    detail TEXT
  );
`);

export interface AuditEntry {
  id: number;
  ts: string;
  userId?: string;
  userName?: string;
  action: string;
  target?: string;
  detail?: string;
}

/** 追加一条审计记录（本地写，不抛错——审计不应影响业务） */
export function appendAudit(entry: Omit<AuditEntry, "id" | "ts">): void {
  try {
    db.prepare(
      `INSERT INTO audit_log (ts, user_id, user_name, action, target, detail) VALUES (?, ?, ?, ?, ?, ?)`,
    ).run(
      new Date().toLocaleString("zh-CN", { hour12: false }),
      entry.userId ?? null,
      entry.userName ?? null,
      entry.action,
      entry.target ?? null,
      entry.detail ?? null,
    );
  } catch {
    /* 审计失败静默，不影响主流程 */
  }
}

/** 最近 N 条审计记录（供审计查看；默认 200） */
export function listAudit(limit = 200): AuditEntry[] {
  return db
    .prepare(`SELECT id, ts, user_id AS userId, user_name AS userName, action, target, detail FROM audit_log ORDER BY id DESC LIMIT ?`)
    .all(limit) as unknown as AuditEntry[];
}

/** 清空审计日志（可选项；保留架构接口） */
export function clearAudit(): void {
  db.exec(`DELETE FROM audit_log;`);
}

// ---- M33 任务模板：CRUD（本地存储，可含预设的专家/技能/模型） ----
export interface TaskTemplate {
  id: string;
  name: string;
  desc?: string;
  prompt: string;
  expert?: string;
  skills?: string[];
  model?: string;
  created: string;
}

export function createTaskTemplate(
  tpl: { name: string; desc?: string; prompt: string; expert?: string; skills?: string[]; model?: string },
  userId?: string,
): string {
  const id = Math.random().toString(36).slice(2, 10);
  db.prepare(
    `INSERT INTO task_templates (id, name, desc, prompt, expert, skills, model, user_id, created)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
  ).run(
    id, tpl.name, tpl.desc ?? null, tpl.prompt, tpl.expert ?? null,
    tpl.skills?.length ? JSON.stringify(tpl.skills) : null, tpl.model ?? null,
    userId ?? null, new Date().toLocaleString("zh-CN", { hour12: false }),
  );
  return id;
}

export function listTaskTemplates(userId?: string): TaskTemplate[] {
  const rows = db.prepare(
    `SELECT id, name, desc, prompt, expert, skills, model, created
     FROM task_templates
     WHERE user_id = ? OR user_id IS NULL
     ORDER BY created DESC`,
  ).all(userId ?? null) as {
    id: string; name: string; desc: string | null; prompt: string;
    expert: string | null; skills: string | null; model: string | null; created: string;
  }[];
  return rows.map((r) => ({
    id: r.id, name: r.name, desc: r.desc ?? undefined, prompt: r.prompt,
    expert: r.expert ?? undefined,
    skills: r.skills ? JSON.parse(r.skills) as string[] : undefined,
    model: r.model ?? undefined,
    created: r.created,
  }));
}

export function getTaskTemplate(id: string): TaskTemplate | null {
  const r = db.prepare(
    `SELECT id, name, desc, prompt, expert, skills, model, created FROM task_templates WHERE id = ?`,
  ).get(id) as {
    id: string; name: string; desc: string | null; prompt: string;
    expert: string | null; skills: string | null; model: string | null; created: string;
  } | undefined;
  if (!r) return null;
  return {
    id: r.id, name: r.name, desc: r.desc ?? undefined, prompt: r.prompt,
    expert: r.expert ?? undefined,
    skills: r.skills ? JSON.parse(r.skills) as string[] : undefined,
    model: r.model ?? undefined,
    created: r.created,
  };
}

export function updateTaskTemplate(id: string, patch: Partial<{ name: string; desc: string; prompt: string; expert: string; skills: string[]; model: string }>): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM task_templates WHERE id = ?`).get(id);
  if (!existed) return false;
  const cur = getTaskTemplate(id)!;
  const name = patch.name ?? cur.name;
  const desc = patch.desc !== undefined ? patch.desc : (cur.desc ?? "");
  const prompt = patch.prompt ?? cur.prompt;
  const expert = patch.expert !== undefined ? patch.expert : (cur.expert ?? "");
  const skills = patch.skills ?? (cur.skills ?? []);
  const model = patch.model !== undefined ? patch.model : (cur.model ?? "");
  db.prepare(
    `UPDATE task_templates SET name = ?, desc = ?, prompt = ?, expert = ?, skills = ?, model = ? WHERE id = ?`,
  ).run(name, desc || null, prompt, expert || null, skills.length ? JSON.stringify(skills) : null, model || null, id);
  return true;
}

export function deleteTaskTemplate(id: string): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM task_templates WHERE id = ?`).get(id);
  if (existed) db.prepare(`DELETE FROM task_templates WHERE id = ?`).run(id);
  return existed;
}

// ---- C5 交付版本历史：file_versions 表（每个文件名一条递增 seq 的版本记录） ----
export interface FileVersion {
  id: number;
  file_name: string;
  taskId?: string;
  seq: number;
  ts: string;
  archived: string; // 版本快照的存档相对路径（相对工作空间根）
}

/** 追加一个版本记录，返回新 seq（同文件名按 1 递增） */
export function addFileVersion(fileName: string, taskId: string | undefined, archived: string): number {
  const cur = db.prepare(`SELECT COALESCE(MAX(seq),0)+1 AS s FROM file_versions WHERE file_name = ?`).get(fileName) as { s: number };
  const seq = Number(cur.s);
  db.prepare(
    `INSERT INTO file_versions (file_name, task_id, seq, ts, archived) VALUES (?, ?, ?, ?, ?)`,
  ).run(fileName, taskId ?? null, seq, new Date().toLocaleString("zh-CN", { hour12: false }), archived);
  return seq;
}

export function listFileVersions(fileName: string): FileVersion[] {
  return db.prepare(
    `SELECT id, file_name, task_id AS taskId, seq, ts, archived FROM file_versions WHERE file_name = ? ORDER BY seq DESC`,
  ).all(fileName) as unknown as FileVersion[];
}

export function getFileVersion(fileName: string, seq: number): FileVersion | null {
  const r = db.prepare(
    `SELECT id, file_name, task_id AS taskId, seq, ts, archived FROM file_versions WHERE file_name = ? AND seq = ?`,
  ).get(fileName, seq) as FileVersion | undefined;
  return r ?? null;
}

/** 删除某文件名的全部版本记录（配合工作目录清理） */
export function deleteFileVersions(fileName: string): void {
  db.prepare(`DELETE FROM file_versions WHERE file_name = ?`).run(fileName);
}

// ---- C6 用户自建专家：experts 表（内置专家为静态种子，用户自建落库、多用户隔离） ----
export interface ExpertRow {
  id: string;
  name: string;
  icon?: string;
  color?: string;
  desc?: string;
  skills?: string;
  connectors?: string;
  created: string;
}

export function createExpert(
  e: { name: string; icon?: string; color?: string; desc?: string; skills?: string; connectors?: string },
  userId?: string,
): string {
  const id = Math.random().toString(36).slice(2, 10);
  db.prepare(
    `INSERT INTO experts (id, name, icon, color, desc, skills, connectors, user_id, created)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
  ).run(
    id, e.name, e.icon ?? null, e.color ?? null, e.desc ?? null,
    e.skills ?? null, e.connectors ?? null, userId ?? null,
    new Date().toLocaleString("zh-CN", { hour12: false }),
  );
  return id;
}

/** 只列出用户自建 + 全局专家（用户私有隔离；内置种子由路由层拼接） */
export function listCustomExperts(userId?: string): ExpertRow[] {
  return db.prepare(
    `SELECT id, name, icon, color, desc, skills, connectors, created
     FROM experts
     WHERE user_id = ? OR user_id IS NULL
     ORDER BY created DESC`,
  ).all(userId ?? null) as unknown as ExpertRow[];
}

export function updateExpert(id: string, patch: Partial<{ name: string; icon: string; color: string; desc: string; skills: string; connectors: string }>): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM experts WHERE id = ?`).get(id);
  if (!existed) return false;
  const cur = db.prepare(
    `SELECT id, name, icon, color, desc, skills, connectors FROM experts WHERE id = ?`,
  ).get(id) as unknown as ExpertRow;
  const name = patch.name ?? cur.name;
  const icon = patch.icon !== undefined ? patch.icon : (cur.icon ?? "");
  const color = patch.color !== undefined ? patch.color : (cur.color ?? "");
  const desc = patch.desc !== undefined ? patch.desc : (cur.desc ?? "");
  const skills = patch.skills !== undefined ? patch.skills : (cur.skills ?? "");
  const connectors = patch.connectors !== undefined ? patch.connectors : (cur.connectors ?? "");
  db.prepare(
    `UPDATE experts SET name = ?, icon = ?, color = ?, desc = ?, skills = ?, connectors = ? WHERE id = ?`,
  ).run(name, icon || null, color || null, desc || null, skills || null, connectors || null, id);
  return true;
}

export function deleteExpert(id: string): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM experts WHERE id = ?`).get(id);
  if (existed) db.prepare(`DELETE FROM experts WHERE id = ?`).run(id);
  return existed;
}

// ---- M41 记忆系统：memories 表 + 全文检索索引 ----
export interface MemoryRow {
  id: string;
  kind: string;
  content: string;
  tags?: string;
  created: string;
  /** M60 来源溯源：null=手动/未知；"chat:<sid>"（/记得）、"chat-distill:<sid>"（M58 提炼） */
  source?: string | null;
  /** M61 过期时间（epoch ms，可空）；到达后不再自动注入，保留列表供延期/删除 */
  expires_at?: number | null;
}

function syncMemoryFts(id: string, content: string, userId: string | null | undefined): void {
  db.prepare(`DELETE FROM memories_fts WHERE id = ?`).run(id);
  if (content) {
    db.prepare(
      `INSERT INTO memories_fts (id, user_id, content) VALUES (?, ?, ?)`,
    ).run(id, userId ?? null, content);
  }
}

/** 创建一条记忆；kind 可为 note/preference/fact 等，tags 逗号分隔，source 可选溯源，expiresAtMS 可选过期。返回 id */
export function createMemory(
  m: { kind?: string; content: string; tags?: string; source?: string; expiresAtMs?: number | null },
  userId?: string,
): string {
  const id = Math.random().toString(36).slice(2, 10);
  const kind = m.kind || "note";
  db.prepare(
    `INSERT INTO memories (id, kind, content, tags, user_id, created, source, expires_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
  ).run(
    id, kind, m.content, m.tags ? m.tags : null, userId ?? null,
    new Date().toLocaleString("zh-CN", { hour12: false }),
    m.source ? m.source : null,
    m.expiresAtMs ?? null,
  );
  syncMemoryFts(id, m.content, userId);
  return id;
}

/** 列出记忆（可按 kind 过滤；多用户隔离：自己的 + 全局） */
export function listMemories(userId?: string, kind?: string): MemoryRow[] {
  const where = ["(user_id = ? OR user_id IS NULL)"];
  const params: (string | null)[] = [userId ?? null];
  if (kind) {
    where.push("kind = ?");
    params.push(kind);
  }
  return db.prepare(
    `SELECT id, kind, content, tags, created, source, expires_at FROM memories WHERE ${where.join(" AND ")}
     ORDER BY created DESC`,
  ).all(...params) as unknown as MemoryRow[];
}

export function getMemory(id: string): MemoryRow | null {
  const r = db.prepare(
    `SELECT id, kind, content, tags, created, source, expires_at FROM memories WHERE id = ?`,
  ).get(id) as unknown as MemoryRow | undefined;
  return r ?? null;
}

export function updateMemory(
  id: string,
  patch: { kind?: string; content?: string; tags?: string },
  userId?: string,
): boolean {
  const cur = getMemory(id);
  if (!cur) return false;
  const kind = patch.kind ?? cur.kind;
  const content = patch.content ?? cur.content;
  const tags = patch.tags !== undefined ? patch.tags : (cur.tags ?? "");
  db.prepare(
    `UPDATE memories SET kind = ?, content = ?, tags = ? WHERE id = ?`,
  ).run(kind, content, tags || null, id);
  syncMemoryFts(id, content, userId ?? null);
  return true;
}

export function deleteMemory(id: string): boolean {
  const existed = !!db.prepare(`SELECT 1 FROM memories WHERE id = ?`).get(id);
  if (existed) {
    db.prepare(`DELETE FROM memories WHERE id = ?`).run(id);
    db.prepare(`DELETE FROM memories_fts WHERE id = ?`).run(id);
  }
  return existed;
}

/** M57 批量删除记忆（仅删除传入的 id，多用户隔离；返回实际删除条数） */
export function deleteMemories(ids: string[], userId?: string | null): number {
  if (!ids.length) return 0;
  const placeholders = ids.map(() => "?").join(",");
  const rows = db.prepare(
    `SELECT id FROM memories WHERE id IN (${placeholders}) AND (user_id = ? OR user_id IS NULL)`,
  ).all(...ids, userId ?? null) as { id: string }[];
  const own = rows.map((r) => r.id);
  if (!own.length) return 0;
  const p2 = own.map(() => "?").join(",");
  db.prepare(`DELETE FROM memories WHERE id IN (${p2})`).run(...own);
  db.prepare(`DELETE FROM memories_fts WHERE id IN (${p2})`).run(...own);
  return own.length;
}

/** 归一化记忆内容用于去重：小写 + 折叠连续空白，去掉首尾空格 */
function normalizeContent(s: string): string {
  return (s ?? "").trim().toLowerCase().replace(/\s+/g, " ").replace(/[，。！？、；：""''（）【】]/g, "");
}

/** M57 找出彼此重复的记忆：归一化后完全相同 → canonical 分组（保留最早创建的为准），返回重复组 */
export function findDuplicateMemories(userId?: string, kind?: string): {
  canonical: MemoryRow;
  duplicates: MemoryRow[];
}[] {
  const list = listMemories(userId, kind);
  const map = new Map<string, MemoryRow[]>();
  for (const m of list) {
    const key = normalizeContent(m.content);
    if (!key) continue;
    const arr = map.get(key) ?? [];
    arr.push(m);
    map.set(key, arr);
  }
  const groups: { canonical: MemoryRow; duplicates: MemoryRow[] }[] = [];
  for (const arr of map.values()) {
    if (arr.length < 2) continue;
    // 保留创建最早的一条作 canonical，其余当重复
    const sorted = [...arr].sort((a, b) => (a.created < b.created ? -1 : 1));
    groups.push({ canonical: sorted[0], duplicates: sorted.slice(1) });
  }
  return groups;
}

/**
 * M59 一键合并重复记忆：把 removeIds 的 tags 并入 keepId（去重、逗号分隔），然后删除被合并的记忆。
 * M63 维护闭环补全：合并时一并继承 **来源溯源** 与 **时效**——
 *    · source：若 keep 没有来源（未知/手动），从被合并记忆中继承最早带来源的一条，避免合并后丢失 provenance；
 *    · expires_at：取 keep 与被合并记忆里**最早过期**的一个（更保守），避免并出来的记忆悄悄超出任一成员的保鲜期。
 * 只允许操作当前用户可见的记忆（user_id = 本人 或全局），杜绝越权合并/删除他人记忆。
 * 返回实际保留与被删的 id；keep 或任一 remove 不可见则整组拒绝（保持幂等、不留半删状态）。
 */
export function mergeMemories(
  keepId: string,
  removeIds: string[],
  userId?: string | null,
): { kept: string | null; removed: string[] } {
  const visible = (id: string) =>
    !!db.prepare(`SELECT 1 FROM memories WHERE id = ? AND (user_id = ? OR user_id IS NULL)`).get(id, userId?.toString() ?? null);
  if (!visible(keepId) || !removeIds.length) return { kept: null, removed: [] };
  for (const id of removeIds) {
    if (!visible(id)) return { kept: null, removed: [] };
  }
  const keep = getMemory(keepId);
  if (!keep) return { kept: null, removed: [] };

  // 汇总被合并记忆的 tags 并入 keep（按逗号拆开去重）
  const tagSet = new Set(
    (keep.tags ?? "").split(",").map((t) => t.trim()).filter(Boolean),
  );
  // M63：source 继承候选 + 时效取最早过期
  let source = keep.source ?? null;
  let expiresAt: number | null = keep.expires_at ?? null;
  for (const id of removeIds) {
    const m = getMemory(id);
    if (!m) continue;
    if (m.tags) {
      for (const t of m.tags.split(",").map((x) => x.trim()).filter(Boolean)) tagSet.add(t);
    }
    // 继承最早一个有来源的被合并记忆的溯源（keep 无来源时）
    if (!source && m.source) source = m.source;
    // 过期取更早（更保守）：null/undefined 视为「无时效=长期」，不参与取早；有值的才写入（若 keep 无时效或更晚）
    const exp = m.expires_at ?? null;
    if (exp !== null && (expiresAt === null || exp < expiresAt)) {
      expiresAt = exp;
    }
  }
  const mergedTags = [...tagSet].join(",");

  db.prepare(`UPDATE memories SET tags = ?, source = ?, expires_at = ? WHERE id = ?`)
    .run(mergedTags || null, source, expiresAt, keepId);
  deleteMemories(removeIds, userId);
  return { kept: keepId, removed: removeIds };
}

/** 记忆全文检索（FTS5 trigram，≥3 字子串；多用户隔离） */
export function searchMemories(q: string, userId?: string): MemoryRow[] {
  const term = (q ?? "").trim();
  if (!term) return [];
  return db.prepare(
    `SELECT m.id, m.kind, m.content, m.tags, m.created, m.source, m.expires_at
     FROM memories_fts
     JOIN memories m ON m.id = memories_fts.id
     WHERE memories_fts MATCH ? AND (memories_fts.user_id = ? OR memories_fts.user_id IS NULL)
     ORDER BY memories_fts.rowid DESC LIMIT 20`,
  ).all(`"${term}"`, userId ?? null) as unknown as MemoryRow[];
}

/**
 * M42 对话注入检索：FTS 短语式的整句匹配对中文召回太差（用户消息通常不是记忆的连续子串），
 * 改从消息里抽 **CJK 二元组 + 英文词**，用 LIKE 高召回找相关记忆。短记忆量级下效率足够。
 */
export function searchMemoriesFor(message: string, userId?: string): MemoryRow[] {
  const text = (message ?? "").trim();
  if (!text) return [];
  const grams: string[] = [];
  // CJK 二元组
  const cjk = text.match(/[\u4e00-\u9fa5]+/g) ?? [];
  for (const seg of cjk) {
    for (let i = 0; i + 1 < seg.length; i++) grams.push(seg.slice(i, i + 2));
  }
  // 英文/数字词（≥3 字符才有区分度）
  const words = text.match(/[a-zA-Z0-9]{3,}/g) ?? [];
  for (const w of words) grams.push(w.toLowerCase());
  const unique = [...new Set(grams)];
  if (!unique.length) return [];
  const like = unique.map((g) => `content LIKE '%'||?||'%'`).join(" OR ");
  const params: (string | null | number)[] = [userId ?? null, ...unique];
  // M61：自动注入只取「未过期」的记忆（expires_at IS NULL 或尚未到期）；管理/召回路径仍可见过期项
  return db.prepare(
    `SELECT id, kind, content, tags, created, source, expires_at
     FROM memories
     WHERE (user_id = ? OR user_id IS NULL) AND (${like})
       AND (expires_at IS NULL OR expires_at > ?)
     ORDER BY created DESC LIMIT 10`,
  ).all(...params, Date.now()) as unknown as MemoryRow[];
}

/**
 * M61 设置/清除一条记忆的过期时间（epoch ms；传 null 表示设为长期有效——清除过期）。
 * 多用户隔离：只能操作当前用户可见的记忆；不存在或不可见返回 false。
 */
export function setMemoryExpiry(id: string, expiresAtMs: number | null, userId?: string | null): boolean {
  const vis = db.prepare(
    `SELECT 1 FROM memories WHERE id = ? AND (user_id = ? OR user_id IS NULL)`,
  ).get(id, userId?.toString() ?? null);
  if (!vis) return false;
  db.prepare(`UPDATE memories SET expires_at = ? WHERE id = ?`).run(expiresAtMs, id);
  return true;
}

// ---- 通用 key-value 设置（M45 IM 桥等用） ----
export function getSetting(key: string): string | null {
  const r = db.prepare(`SELECT value FROM settings WHERE key = ?`).get(key) as { value: string } | undefined;
  return r ? r.value : null;
}
export function setSetting(key: string, value: string): void {
  db.prepare(`INSERT INTO settings (key, value) VALUES (?, ?)
              ON CONFLICT(key) DO UPDATE SET value = excluded.value`).run(key, value);
}

// ---- M45 IM 消息桥配置（默认关闭，数据不出机器） ----
export interface ImSettings {
  enabled: boolean;
  secret: string;
  name: string;
  /** 本机 webhook 端点（仅提示用） */
  endpoint: string;
}

function randomSecret(): string {
  return Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 8);
}

/** 读取 IM 桥配置；从不存在则初始化默认（关闭 + 随机 secret） */
export function getImSettings(): ImSettings {
  const enabled = getSetting("im_enabled") === "1";
  let secret = getSetting("im_secret");
  if (!secret) {
    secret = randomSecret();
    setSetting("im_secret", secret);
  }
  const name = getSetting("im_name") || "IM 消息";
  return { enabled, secret, name, endpoint: `/api/im` };
}

export function setImEnabled(enabled: boolean): void {
  setSetting("im_enabled", enabled ? "1" : "0");
}
export function setImName(name: string): void {
  setSetting("im_name", name.trim() || "IM 消息");
}
export function rotateImSecret(): string {
  const s = randomSecret();
  setSetting("im_secret", s);
  return s;
}
