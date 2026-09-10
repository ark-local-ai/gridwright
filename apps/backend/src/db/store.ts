import { DatabaseSync } from "node:sqlite";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Artifact, Task, TaskStep, StepStatus, TaskStatus } from "../types";
import { dataDir } from "../config/paths";

mkdirSync(dataDir, { recursive: true });
const dbPath = join(dataDir, "ark.db");

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

// ---- 任务写 / 读（node:sqlite 同步 API，prepare().run() / .get() / .all()）----
export function insertTask(task: Task, userId?: string): void {
  db.prepare(
    `INSERT INTO tasks (id, title, prompt, status, model, expert, skills, workspace, checks, timeline, created, user_id)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
  ).run(
    task.id, task.title, task.prompt, task.status, task.model, task.expert,
    JSON.stringify(task.skills), task.workspace,
    JSON.stringify(task.checks), JSON.stringify(task.timeline), task.created, userId ?? null,
  );
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

// ---- 任务读 ----
export function getTask(id: string): Task | null {
  const row = db.prepare(`SELECT * FROM tasks WHERE id = ?`).get(id) as
    | ({
        id: string; title: string; prompt: string; status: TaskStatus; model: string;
        expert: string; skills: string; workspace: string; checks: string; timeline: string; created: string;
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
  };
}

export interface TaskSummary {
  id: string;
  title: string;
  created: string;
  status: TaskStatus;
}

/** 任务列表摘要（供侧栏「最近任务」），按创建时间倒序 */
export function listTasks(limit = 50, userId?: string): TaskSummary[] {
  // 多用户隔离：登录用户看自己的 + 全局无主任务；未登录看全部（兼容）
  const rows = userId
    ? db.prepare(`SELECT id, title, created, status FROM tasks WHERE user_id = ? OR user_id IS NULL ORDER BY created DESC LIMIT ?`)
      .all(userId, limit) as { id: string; title: string; created: string; status: TaskStatus }[]
    : db.prepare(`SELECT id, title, created, status FROM tasks ORDER BY created DESC LIMIT ?`)
      .all(limit) as { id: string; title: string; created: string; status: TaskStatus }[];
  return rows.map((r) => ({ id: r.id, title: r.title, created: r.created, status: r.status }));
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

// 首次启动若没有空间，播种一个「默认工作空间」（dir 为空字符串表示根目录，兼容既有平铺文件）
function seedSpaces(): void {
  const n = db.prepare(`SELECT COUNT(*) AS n FROM spaces`).get() as { n: number };
  if (n.n === 0) {
    db.prepare(`INSERT INTO spaces (id, name, dir, is_active, created) VALUES (?, ?, ?, ?, ?)`)
      .run("default", "默认工作空间", "", 1, new Date().toLocaleString("zh-CN", { hour12: false }));
  }
}
seedSpaces();

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
};

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
  const r = db.prepare(`SELECT id, username, display_name, created FROM users WHERE id = ?`).get(id) as
    | { id: string; username: string; display_name: string; created: string }
    | undefined;
  return r ? { id: r.id, username: r.username, displayName: r.display_name, created: r.created } : undefined;
}

export function createUser(username: string, password: string, displayName: string): User {
  const id = `u-${randomBytes(6).toString("hex")}`;
  const created = new Date().toLocaleString("zh-CN", { hour12: false });
  const hash = hashPassword(password);
  db.prepare(`INSERT INTO users (id, username, password_hash, display_name, created) VALUES (?, ?, ?, ?, ?)`)
    .run(id, username, hash, displayName || username, created);
  return { id, username, displayName: displayName || username, created };
}

export function countUsers(): number {
  const r = db.prepare(`SELECT COUNT(*) AS n FROM users`).get() as { n: number };
  return r.n;
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
  return findUserById(row.user_id);
}

export function destroySession(token: string): void {
  db.prepare(`DELETE FROM sessions WHERE token = ?`).run(token);
}

/** 清理过期会话（可选，不阻塞） */
export function pruneSessions(): void {
  db.prepare(`DELETE FROM sessions WHERE expires < ?`).run(Date.now());
}
