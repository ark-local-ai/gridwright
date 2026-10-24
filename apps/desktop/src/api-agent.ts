// 数据管家 API 客户端（对接 Go 引擎的 /api/v1，见 docs/agent-architecture/13-接口契约.md）
//
// 与旧 api.ts 的区别：旧文件对接的是 legacy TS 后端（tasks/chat/skills…，将被砍）；
// 本文件对接"手脑分离"的 Go 引擎。两条 API 在过渡期并存，互不干扰。
//
// 开发时走 Vite 代理 /agent-api → 127.0.0.1:7700；桌面构建时由 VITE_AGENT_API_BASE 注入。
const BASE = import.meta.env.VITE_AGENT_API_BASE ?? "/agent-api";

// 本机访问令牌（见 apps/agent/internal/api/auth.go）。
//
// 引擎启动时生成一个令牌，除 health/session 外的接口都要求
// `Authorization: Bearer <token>`。界面在第一次请求前向 /session 领一次，
// 之后缓存在内存里；成功后不再重复请求。
//
// 旧版引擎没有 /session（404）：视为鉴权未启用，照常无头请求。
let token: string | null = null;
let tokenLoaded = false;
let tokenInflight: Promise<void> | null = null;

async function loadToken(force = false): Promise<void> {
  if (tokenLoaded && !force) return;
  if (tokenInflight) return tokenInflight;
  tokenInflight = (async () => {
    try {
      const res = await fetch(`${BASE}/api/v1/session`);
      if (res.ok) {
        const body = (await res.json()) as { token?: unknown };
        if (typeof body.token === "string" && body.token) token = body.token;
      }
      tokenLoaded = true;
    } catch {
      // 引擎尚未起来：不要把"领不到令牌"缓存成永久状态，
      // 否则引擎稍后启动，界面仍然一直不带令牌。
      tokenLoaded = token !== null;
    } finally {
      tokenInflight = null;
    }
  })();
  return tokenInflight;
}

/** 401 是一个可恢复错误：令牌轮换/引擎重启后自动重领并重试一次。 */
class Unauthorized extends Error {}

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  await loadToken();
  const send = async (): Promise<T> => {
    const headers = new Headers(init.headers);
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const res = await fetch(`${BASE}${path}`, { ...init, headers });
    if (res.status === 401) throw new Unauthorized("需要重新领取本机访问令牌");
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error((body as { error?: string }).error ?? `${res.status}`);
    }
    return res.json() as Promise<T>;
  };
  try {
    return await send();
  } catch (e) {
    if (e instanceof Unauthorized) {
      token = null;
      await loadToken(true);
      return await send();
    }
    throw e;
  }
}

async function get<T>(path: string): Promise<T> {
  return call<T>(path);
}

/** 取二进制/文本文件（带鉴权，401 自动重领一次）。CSV 导出用。 */
async function rawGet(path: string): Promise<Blob> {
  await loadToken();
  const send = async (): Promise<Blob> => {
    const headers = new Headers();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const res = await fetch(`${BASE}${path}`, { headers });
    if (res.status === 401) throw new Unauthorized("需要重新领取本机访问令牌");
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error((body as { error?: string }).error ?? `${res.status}`);
    }
    return res.blob();
  };
  try {
    return await send();
  } catch (e) {
    if (e instanceof Unauthorized) {
      token = null;
      await loadToken(true);
      return await send();
    }
    throw e;
  }
}

async function post<T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return call<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
}

async function del<T>(path: string): Promise<T> {
  return call<T>(path, { method: "DELETE" });
}

async function put<T>(path: string, body?: unknown): Promise<T> {
  return call<T>(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export interface WorkspaceInfo {
  root: string;
  tables: number;
  inboxCount: number;
  ledgerPath: string;
  brainReady: boolean;
  offline: boolean;
  /** 用户是否显式选过工作区（false=首次运行，界面要引导去选） */
  workspaceChosen: boolean;
}

export interface FileItem {
  name: string;
  size: number;
  time: string;
}

export interface WorkspaceFiles {
  tables: FileItem[];
  inbox: FileItem[];
  inboxDone: FileItem[];
}

export interface GraphNode {
  file: string; // 文件名（空=未知外部文件）
  sheet: string;
}

export interface GraphEdge {
  from: GraphNode;
  to: GraphNode;
  kind: "formula" | "semantic" | "declared";
  count: number;
  confidence: "high" | "medium";
  crossFile?: boolean;
  externalIdx?: number; // >0 表示来自 [n] 外部工作簿引用
}

export interface GraphData {
  root: string;
  files: string[];
  nodes: GraphNode[];
  edges: GraphEdge[];
  propagate?: { from: string; to: string[] };
}

/** 节点稳定 ID（与 Go 侧 Node.ID() 一致）。 */
export function nodeId(n: GraphNode): string {
  return n.file ? `${n.file}!${n.sheet}` : n.sheet;
}

export interface ScanIssue {
  kind: string;
  severity: "error" | "warn" | "info";
  file: string;
  sheet: string;
  ref: string;
  row: number;
  col: number;
  message: string;
  detail?: string;
}

export interface ScanReport {
  file: string;
  sheets: number;
  cells: number;
  issues: ScanIssue[];
  elapsed: string;
}

export interface ScanResult {
  report: ScanReport;
  counts: { error: number; warn: number; info: number };
}

export interface LedgerEntry {
  ts: string;
  table: string;
  cell: string;
  op: string;
  old: string;
  new: string;
  reason: string;
  source: string;
  /** 会话标题：source 为 "对话:<id>" 时由后端译回；看板据此显示「哪次对话改的」。 */
  sourceTitle?: string;
  rule: string;
  model: string;
  status: string;
}

/* ---------- 作业区与任务 ---------- */

export interface ZoneInput {
  type: "folder" | "system" | "manual";
  path?: string;
  connector?: string;
  watch?: string;
  pattern?: string;
}

export interface ZoneTarget {
  file?: string;
  sheet?: string;
  newTable?: string;
}

export interface Zone {
  id: string;
  name: string;
  icon?: string;
  input: ZoneInput;
  target: ZoneTarget;
  rules?: string[];
  mode: "dryRun" | "apply";
  status?: string;
  lastRun?: string;
  lastError?: string;
  outputs?: string[];
  createdAt?: string;
}

export interface ZoneRun {
  zoneId: string;
  started: string;
  ok: boolean;
  applied: number;
  rejected: number;
  summary?: string;
  err?: string;
}

export interface TaskStep {
  key: string;
  label: string;
  status: "pending" | "running" | "ok" | "skipped" | "failed";
  detail?: string;
}

export interface Task {
  id: string;
  zoneId: string;
  title: string;
  /** chat = 对话采纳创建；manual = 接口/界面直跑。看板只展示 chat。 */
  source?: "chat" | "manual" | string;
  conversationId?: string;
  status: "queued" | "running" | "ok" | "failed" | "canceled";
  dryRun: boolean;
  /** 这次运行产出的清单 id（有待确认改动时非空）：卡片据此回看清单。 */
  proposalIds?: string[];
  /** 处理失败的文件（可重试）。 */
  failedFiles?: string[];
  steps: TaskStep[];
  summary?: string;
  error?: string;
  counts?: Record<string, number>;
  createdAt: string;
  startedAt?: string;
  endedAt?: string;
}

export interface TaskSnapshot {
  runningId: string;
  /** 同时最多 4 个；不同目标表可并行，同一本表串行。 */
  runningIds?: string[];
  items: Task[];
  queued: number;
}

export interface SheetSummary {
  label: string;
  values: string[];
  ref?: string;
}

export interface SheetPreview {
  file: string;
  sheet: string;
  rows: number;
  cols: number;
  formulas: number;
  headerRow: number;
  header: string[];
  sample: string[][];
  summaries: SheetSummary[];
  note?: string;
}

/** 工作表详情卡：大概描述（体检时模型生成）+ 上次改动/改动日期（来自账目）。 */
export interface SheetDetail {
  file: string;
  sheet: string;
  /** 没有描述时为空串（不是 null） */
  description: string;
  descriptionAt: string;
  descriptionModel: string;
  /** 人类可读的一行，如 "E12 100 → 23540"；没有改动时为空串 */
  lastChange: string;
  lastChangeAt: string;
}

/** 一张表的"形状"（行/列/公式数）——画微缩缩略图用。 */
export interface SheetShape {
  sheet: string;
  file: string;
  rows: number;
  cols: number;
  formulas: number;
  /** 表自带的概览读数（如"本月收租率 8.34%"）。是表里写的，不是另算的。 */
  kpi?: SheetSummary[];
}

export interface WorkspaceListItem {
  path: string;
  name: string;
  tables: number;
  opened: string;
  current: boolean;
}

export interface SettingsDto {
  baseUrl: string;
  model: string;
  hasApiKey: boolean;
  brainReady: boolean;
  workspace: string;
  pollSeconds: number;
  configPath: string;
}

export interface ProposalItem {
  file: string;
  sheet: string;
  ref: string;
  row: number;
  col: number;
  key?: Record<string, string>;
  month?: string;
  field: string;
  op: string;
  old: unknown;
  new: unknown;
  /**
   * 新增行时要写入的「列名 → 值」。op="append" 时用它——
   * 新增一行是多列，单个 new 表达不了。
   */
  values?: Record<string, unknown>;
  reason?: string;
  affects?: string[];
}

export interface ProposalBlocked {
  sheet?: string;
  ref?: string;
  field?: string;
  reason: string;
}

export interface Proposal {
  id: string;
  root: string;
  target: string;
  summary: string;
  items: ProposalItem[];
  blocked?: ProposalBlocked[];
  /** 产生这份清单的会话 id（apply 时写进账目）。 */
  conversation?: string;
}

/* ---------- 清单的整行预览 ---------- */

/** 某一行里被改动的一格。 */
export interface ProposalRowChange {
  col: number;
  /** A1 坐标，点击跳到那一格用。 */
  ref?: string;
  field: string;
  op: string;
  old?: unknown;
  new?: unknown;
  reason?: string;
  affects?: string[];
}

/** 预览表里的一行：整行内容 + 这一行要改的格子。 */
export interface ProposalRow {
  row: number;
  cells: string[];
  kind: "edit" | "new";
  changes: ProposalRowChange[];
}

export interface ProposalSheetRows {
  sheet: string;
  headerRow: number;
  header: string[];
  rows: ProposalRow[];
}

export interface ProposalRows {
  id: string;
  target: string;
  sheets: ProposalSheetRows[];
}

/** 把引擎回传的清单收敛成界面可直接用的形状：null → []，并给每条补上 affects。 */
export function normalizeProposal(p: Proposal): Proposal {
  const items = (p.items ?? []).map((it) => ({ ...it, affects: it.affects ?? [] }));
  return { ...p, items, blocked: p.blocked ?? [] };
}

export interface ApplyResult {
  ref: string;
  sheet: string;
  field: string;
  old: unknown;
  new: unknown;
  status: "ok" | "rejected";
  note?: string;
}

export interface SelfCheckFinding {
  node: GraphNode;
  level: "ok" | "notice" | "issue";
  kind: string;
  where?: string;
  message: string;
  needClarify: boolean;
  score: number;
  reasons: string[];
}
export interface SelfCheckReport {
  level: "ok" | "notice" | "issue";
  checked: number;
  findings: SelfCheckFinding[];
  summary: string;
  elapsed: string;
  correctable: boolean;
}
export interface ImpactCandidate {
  node: GraphNode;
  score: number;
  reasons: string[];
  byStructure: boolean;
  byMemory: boolean;
  touched: boolean;
}
export interface ImpactResult {
  kind: string;
  source: string;
  candidates: ImpactCandidate[];
  note?: string;
}
export interface RelationDto {
  kind: string;
  tables: string[];
  source: string;
  approved: string;
  note?: string;
  hits?: number;
}
export interface SafetyRisk {
  level: string;
  kind: string;
  where?: string;
  detail: string;
}
export interface SafetyReport {
  file: string;
  level: string;
  risks: SafetyRisk[];
  advice: string;
  canWrite: boolean;
}
export interface TermDto {
  word: string;
  resolves_to: { sheet?: string; field?: string; kind?: string; key?: Record<string, string> };
  note?: string;
  source: string;
  approved?: string;
  hits?: number;
}

export interface JobDto {
  id: string;
  name: string;
  schedule: string;
  /** 人话描述（后端给的，如"每天 17:30"） */
  when: string;
  kind: string;
  enabled: boolean;
  source?: string;
  created: string;
  lastRun?: string;
  lastOk: boolean;
  lastNote?: string;
  nextRun?: string;
}

export interface JobRunDto {
  jobId: string;
  name: string;
  started: string;
  ok: boolean;
  summary: string;
  errors: number;
  warns: number;
}

export interface RollbackItem {
  id: number;
  ts: string;
  table: string;
  sheet: string;
  cell: string;
  op: string;
  old: string;
  new: string;
  reason: string;
  status: string;
  /** 来源（"对话:<id>" / 规则 / inbox 文件）与译回后的会话标题。 */
  source?: string;
  sourceTitle?: string;
  canRollback: boolean;
  whyNot?: string;
  group: string;
}

export interface TraceStep { name: string; detail?: string; ms: number; }
/** 一次运行的步骤账（后端 trace 包），供「运行详情」面板直接渲染。 */
export interface TraceData { runId: string; steps: TraceStep[]; prompt?: string; raw?: string; }

export interface GenerateResult {
  ok: boolean;
  kind: string;
  title: string;
  path: string;
  file: string;
  rows: number;
  columns: string[];
  preview?: string[][];
  sum?: Record<string, number>;
  content?: string;
  notes?: string[];
}

export interface WeightScore {
  node: GraphNode;
  structural: number;
  activity: number;
  risk: number;
  attention: number;
  inDegree: number;
  formulas: number;
  master: boolean;
  reasons: string[];
  hasUsage: boolean;
}

export interface InboxItem {
  name: string;
  size: number;
  modified: string;
  status: "waiting" | "processing" | "failed";
  attempts?: number;
  error?: string;
  errorAt?: string;
  claimedAt?: string;
  claimStale?: boolean;
}

export interface InboxData {
  items: InboxItem[];
  count: number;
}

export const agentApi = {
  health: () => get<{ ok: boolean; workspace: string }>("/api/v1/health"),
  workspace: () => get<WorkspaceInfo>("/api/v1/workspace"),
  files: () => get<WorkspaceFiles>("/api/v1/workspace/files"),
  // 收件箱队列：状态 / 手动处理 / 跳过
  inbox: () => get<InboxData>("/api/v1/inbox"),
  inboxRun: (file?: string) =>
    post<{ ok: boolean; items?: InboxItem[]; count?: number; file?: string; claimed?: boolean; error?: string }>(
      "/api/v1/inbox/run",
      file ? { file } : {},
    ),
  inboxSkip: (file: string, reason?: string) =>
    post<{ ok: boolean; file: string }>("/api/v1/inbox/skip", { file, reason }),
  graph: (node?: string) =>
    get<GraphData>(`/api/v1/graph${node ? `?node=${encodeURIComponent(node)}` : ""}`),
  scan: () => get<ScanResult>("/api/v1/scan"),
  scanRun: () => post<{ ok: boolean; errors: number; warns: number; report: ScanReport }>("/api/v1/scan/run"),
  ledger: (limit = 50) => get<{ entries: LedgerEntry[]; limit: number }>(`/api/v1/ledger?limit=${limit}`),
  // 作业区：卡片列表、创建/更新、运行（入任务队列）
  zones: () => get<{ items: Zone[] }>("/api/v1/zones"),
  saveZone: (z: Partial<Zone>) => post<{ ok: boolean; zone: Zone }>("/api/v1/zones", z),
  deleteZone: (id: string) => post<{ ok: boolean }>("/api/v1/zones/delete", { id }),
  zoneRuns: (id?: string, limit = 20) =>
    get<{ items: ZoneRun[] }>(`/api/v1/zones/runs?limit=${limit}` + (id ? `&id=${encodeURIComponent(id)}` : "")),
  // 任务队列：看板最多 6 条；历史另取
  tasks: () => get<TaskSnapshot>("/api/v1/tasks"),
  tasksHistory: (limit = 50) => get<{ items: Task[] }>(`/api/v1/tasks/history?limit=${limit}`),
  runZone: (zoneId: string, dryRun: boolean) =>
    post<{ ok: boolean; task: Task }>("/api/v1/tasks/run", { zoneId, dryRun }),
  cancelTask: (id: string) => post<{ ok: boolean }>("/api/v1/tasks/cancel", { id }),
  /** 重试失败的文件：把这条任务重新排队（POST /api/v1/tasks/retry）。 */
  retryTask: (id: string) => post<{ ok: boolean; task?: Task }>("/api/v1/tasks/retry", { id }),
  // 路径授权：作业区的输入目录必须先在这里登记（用户显式输入路径 = 一次授权）。
  grants: () => get<{ enabled: boolean; items: { root: string; source: string; mode: string; resolved: string; added: string }[] }>("/api/v1/grants"),
  saveGrant: (path: string, mode: "rw" | "read" = "read") =>
    post<{ ok: boolean }>("/api/v1/grants", { path, mode }),
  /** 账目 CSV 导出（带鉴权）。筛选参数与后端 /ledger/export 一致。 */
  ledgerExport: (params: { table?: string; status?: string; op?: string; since?: string; until?: string; source?: string; zone?: string } = {}) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v) q.set(k, String(v));
    }
    const qs = q.toString();
    return rawGet(`/api/v1/ledger/export${qs ? `?${qs}` : ""}`);
  },
  sheets: (file?: string) =>
    get<{ file: string; sheets: string[] }>(`/api/v1/sheets${file ? `?file=${encodeURIComponent(file)}` : ""}`),
  // 批量取所有表的"形状"（行/列/公式数）——给左栏微缩缩略图用。
  // 一次开文件拿全部，避免 24 张表打 24 次请求。
  sheetShapes: (file?: string) =>
    get<{ file: string; shapes: SheetShape[] }>(
      `/api/v1/sheets/shapes${file ? `?file=${encodeURIComponent(file)}` : ""}`),
  preview: (sheet: string, file?: string, rows = 50) => {
    const q = new URLSearchParams({ sheet, rows: String(rows) });
    if (file) q.set("file", file);
    return get<SheetPreview>(`/api/v1/sheets/preview?${q.toString()}`);
  },
  // 工作表详情卡：大概描述（体检时模型生成并缓存）+ 上次改动/改动日期（账目）
  sheetDetail: (file: string | undefined, sheet: string) => {
    const q = new URLSearchParams({ sheet });
    if (file) q.set("file", file);
    return get<SheetDetail>(`/api/v1/sheets/detail?${q.toString()}`);
  },
  // 立即为该表生成/刷新描述（没配模型会 400，人话）
  describeSheet: (file: string | undefined, sheet: string) =>
    post<{ ok: boolean; description: string; at: string; model: string }>(
      "/api/v1/sheets/describe", { file, sheet }),
  // 用本机 Excel / WPS 打开某张表（入口在表格预览页）。
  // 引擎与工作区在同一台机器上，由它调系统打开：桌面壳与单文件版行为一致。
  openTargets: (file?: string) =>
    get<{ file: string; path: string; targets: { id: string; label: string }[] }>(
      `/api/v1/open/targets${file ? `?file=${encodeURIComponent(file)}` : ""}`),
  openFile: (file?: string, target?: string) =>
    post<{ ok: boolean; path: string; target: string }>("/api/v1/open", { file, target }),
  // 回滚：列出可回滚的账目（带"能不能倒"的判断）+ 执行
  rollbackList: (limit = 200) =>
    get<{ items: RollbackItem[] }>(`/api/v1/rollback?limit=${limit}`),
  rollback: (ids: number[], table?: string) =>
    post<{ ok: boolean; rolled: number; skipped: number; details: string[]; note?: string }>(
      "/api/v1/rollback", { ids, table }),
  // 自动化任务
  jobs: () => get<{ jobs: JobDto[] }>("/api/v1/jobs"),
  createJob: (p: { name: string; schedule: string; kind?: string; enabled?: boolean; source?: string }) =>
    post<{ ok: boolean; job: JobDto }>("/api/v1/jobs", p),
  updateJob: (id: string, body: { name?: string; schedule?: string; enabled?: boolean }) =>
    put<{ ok: boolean; job: JobDto }>("/api/v1/jobs", { id, ...body }),
  deleteJob: (id: string) => del<{ ok: boolean }>(`/api/v1/jobs?id=${encodeURIComponent(id)}`),
  runJobNow: (id: string) => post<{ ok: boolean; run: JobRunDto }>("/api/v1/jobs/run", { id }),
  jobRuns: () => get<{ runs: JobRunDto[] }>("/api/v1/jobs/runs"),
  // 规则引擎（见 docs/agent-architecture/30-规则引擎.md）
  rules: () => get<RulesResp>("/api/v1/rules"),
  saveRules: (p: { rules?: RuleDto[]; raw?: string }) =>
    post<{ saved: boolean; total: number; executable: number; problems?: RuleProblem[]; path: string }>(
      "/api/v1/rules", p),
  validateRules: (p: { rules?: RuleDto[]; raw?: string }) =>
    post<{ ok: boolean; error?: string; total?: number; executable?: number; problems?: RuleProblem[]; dup?: string }>(
      "/api/v1/rules/validate", p),
  dryRunRules: (file?: string) =>
    post<RulesDryRun>(`/api/v1/rules/dry-run${file ? `?file=${encodeURIComponent(file)}` : ""}`, {}),
  // 生成：给规格或一句需求
  generate: (instruction?: string, spec?: unknown, dryRun?: boolean) =>
    post<GenerateResult>("/api/v1/generate", { instruction, spec, dryRun }),
  // 服务端目录浏览（浏览器拿不到绝对路径，故选文件夹由服务端做）
  fsList: (dir?: string) =>
    get<{ dir: string; parent: string; entries: { name: string; path: string }[]; drives?: string[]; tables: number }>(
      `/api/v1/fs/list${dir ? `?dir=${encodeURIComponent(dir)}` : ""}`),
  // 工作区
  workspaces: () => get<{ current: string; items: WorkspaceListItem[] }>("/api/v1/workspaces"),
  openWorkspace: (dir: string) =>
    post<{ ok: boolean; root: string }>("/api/v1/workspace/open", { dir }),
  createWorkspace: (name: string, base?: string) =>
    post<{ ok: boolean; root: string }>("/api/v1/workspace/create", { name, base }),
  forgetWorkspace: (dir: string) =>
    post<{ ok: boolean }>("/api/v1/workspace/forget", { dir }),
  /** 把本机文件导入工作区（桌面版拖拽/选择后调用）。
   *  走路径而非上传：文件就在同一台机器上，读成字节流再传一圈是绕路。 */
  importFiles: (paths: string[]) =>
    post<{ ok: boolean; results: { name: string; ok: boolean; err?: string }[] }>(
      "/api/v1/workspace/import", { paths }),
  // 设置（脑）
  settings: () => get<SettingsDto>("/api/v1/settings"),
  saveSettings: (p: { baseUrl?: string; apiKey?: string; model?: string }) =>
    put<{ ok: boolean; brainReady: boolean; saved: boolean; configPath?: string; warning?: string }>(
      "/api/v1/settings", p),
  // 待确认闭环：计划 → 确认 → 执行
  // 归一化：Go 的空切片会 marshal 成 null，界面若直接 .length/.map 会整页崩，
  // 所以在 API 边界把 items/blocked/affects 一律收敛成数组。
  // images：随指令附的图。数据常常就在截图里（"按 D 列名字填 E、F 列"），
  // 不带图的话模型只能反问数据来源，把能做的事停成澄清。
  plan: async (instruction: string, file?: string, images?: string[], signal?: AbortSignal, conversationId?: string) => {
    const r = await post<{ id: string; proposal: Proposal; trace?: TraceData }>("/api/v1/plan", { instruction, file, images, conversationId }, signal);
    if (r?.proposal) r.proposal = normalizeProposal(r.proposal);
    return r;
  },
  /** 回看一份已算好的清单（卡片「查看待确认」用；GET /api/v1/plan?id=）。 */
  planGet: (id: string) =>
    get<Proposal>(`/api/v1/plan?id=${encodeURIComponent(id)}`).then((p) => normalizeProposal(p)),
  apply: (id: string) =>
    post<{
      ok: boolean; applied: number; rejected: number;
      results: ApplyResult[]; selfCheck?: SelfCheckReport | null;
      /** 模型对“这次改了什么”的一句话总结（每张表一条，≤20 字） */
      summaries?: { sheet: string; text: string }[];
    }>("/api/v1/apply", { id }),
  /** 把清单里的改动还原成整行预览（只读，供待确认面板画表格） */
  proposalRows: (id: string) =>
    get<ProposalRows>(`/api/v1/proposal/rows?id=${encodeURIComponent(id)}`),
  selfCheck: (node: string, kind?: string, files?: string[]) =>
    post<SelfCheckReport>("/api/v1/selfcheck", { node, kind, files }),
  // 会话
  chat: (message: string, conversationId?: string, images?: string[]) =>
    post<{ conversationId: string; conversation: ConvoDto; trace?: TraceData }>("/api/v1/chat", { message, conversationId, images }),
  conversations: () => get<{ items: ConvoSummary[] }>("/api/v1/conversations"),
  conversation: (id: string) => get<ConvoDto>(`/api/v1/conversation?id=${encodeURIComponent(id)}`),
  /** 追加一条消息（user 用于把界面里的反馈写回对话而不触发模型；agent/system 用于确定性回执）。 */
  appendConversation: (conversationId: string, text: string, role?: "user" | "agent" | "system") =>
    post<{ ok: boolean; conversation: ConvoDto }>("/api/v1/conversation/append", { conversationId, text, role }),
  acceptProposal: (conversationId: string, index: number) =>
    post<{ ok: boolean; kind: string; note?: string; task?: Task }>("/api/v1/conversation/accept", { conversationId, index }),
  renameConversation: (id: string, title: string) =>
    put<{ ok: boolean; title: string }>(`/api/v1/conversation?id=${encodeURIComponent(id)}`, { title }),
  deleteConversation: (id: string) =>
    del<{ ok: boolean }>(`/api/v1/conversation?id=${encodeURIComponent(id)}`),
  /** 从一段记录里提炼**记忆候选**（只提议，不落盘；落盘走 saveMemory） */
  distill: (id: string) =>
    post<{ conversationId: string; title: string; candidates: DistillCandidate[] }>(
      `/api/v1/conversation/distill?id=${encodeURIComponent(id)}`, {}),
  // 记忆（人批准后才写入）
  memory: () => get<{ memory: MemoryFile; counts: { facts: number; decisions: number; stale: number }; path: string }>("/api/v1/memory"),
  saveMemory: (m: MemoryInput) => post<{ ok: boolean }>("/api/v1/memory", { ...m, approve: true }),
  /** 标记/清除"可能过时"（stale 机制：让记忆能自我纠错） */
  memoryStale: (id: string, action: "mark" | "clear", note?: string) =>
    post<{ ok: boolean }>("/api/v1/memory/stale", { id, action, note }),
  /**
   * 自动查一遍"哪些记忆点名的东西在表里已经没了"。
   * 只做这一件可证明的事（**不比金额、不判语义**）：误报会让整套提示失去信任。
   * 有表读不到时会拒绝下结论（返回 409），而不是照报。
   */
  checkMemoryStale: () =>
    post<{ ok: boolean; count: number; marked: StaleMark[] }>("/api/v1/memory/stale/check", {}),
  /** 删掉一条写错的记忆（错的记忆会污染以后所有判断，所以必须能删） */
  deleteMemory: (kind: "fact" | "decision", id: string) =>
    post<{ ok: boolean }>("/api/v1/memory/delete", { kind, id }),
  // 影响面 / 语义映射 / 安全
  impact: (node: string, kind?: string) =>
    post<ImpactResult>("/api/v1/impact", { node, kind }),
  learnRelation: (kind: string, tables: string[], note?: string) =>
    post<{ ok: boolean }>("/api/v1/impact/learn", { kind, tables, note }),
  relations: () => get<{ relations: RelationDto[] }>("/api/v1/relations"),
  safety: (file?: string) =>
    get<{ report: SafetyReport; advice: string; canWrite: boolean }>(
      `/api/v1/safety${file ? `?file=${encodeURIComponent(file)}` : ""}`),
  terms: () => get<{ terms: TermDto[]; prompts: Record<string, string>; path: string }>("/api/v1/terms"),
  saveTerm: (p: { word: string; sheet?: string; field?: string; kind?: string; key?: Record<string,string>; note?: string; manual?: boolean }) =>
    post<{ ok: boolean }>("/api/v1/terms", p),
  termsDelete: (word: string) => post<{ ok: boolean }>("/api/v1/terms/delete", { word }),
  // 快捷任务模板：从对话沉淀、人手可编辑；触发仍走 /plan，不短路
  prompts: () => get<{ prompts: PromptTemplate[]; path: string }>("/api/v1/prompts"),
  savePrompt: (p: Partial<PromptTemplate>) =>
    post<{ ok: boolean; prompt: PromptTemplate }>("/api/v1/prompts", p),
  deletePrompt: (id: string) => post<{ ok: boolean }>("/api/v1/prompts/delete", { id }),
  usePrompt: (id: string) => post<{ ok: boolean }>("/api/v1/prompts/use", { id }),
  /** 从一段对话总结出快捷任务候选（只读，不落盘） */
  distillPrompt: (conversationId: string) =>
    post<{ conversationId: string; title: string; candidates: PromptCandidate[] }>(
      `/api/v1/prompts/distill?id=${encodeURIComponent(conversationId)}`, {}),
  /** 只总结**勾选的那几条消息**（只读，不落盘） */
  distillPromptMessages: (conversationId: string, messages: { role: string; text: string }[]) =>
    post<{ conversationId: string; title: string; candidates: PromptCandidate[] }>(
      "/api/v1/prompts/distill", { conversationId, messages }),
  /** 随包内置的通用模板骨架（离线，可作起点） */
  builtinPrompts: () => get<{ templates: PromptTemplate[] }>("/api/v1/prompts/builtin"),
  // 权重（结构重要性 + 活跃度 + 风险 → 注意力）
  weights: () => get<{ scores: WeightScore[]; hasUsage: boolean; note: string }>("/api/v1/weights"),
};

export interface ConvoSummary {
  id: string;
  title: string;
  created: string;
  updated: string;
}

/**
 * 一条**记忆候选**：从对话记录里提炼出来的、还没落盘的东西。
 *
 * 为什么它是独立类型而不是直接复用记忆：候选是"提议"，记忆是"结论"。
 * 前者模型说了不算，人要逐条过目；后者是已批准的长期资产。
 * 类型分开，就不会有人不小心把候选直接当记忆用。
 */
export interface DistillCandidate {
  kind: "fact" | "decision";
  title: string;
  text: string;
  key?: Record<string, string>;
  source?: string;
  why?: string;
}

/** 写入一条记忆（走 /api/v1/memory，服务端强制要 approve=true）。 */
export interface MemoryInput {
  kind: "fact" | "decision";
  id: string;
  value?: string;
  text?: string;
  unit?: string;
  key?: Record<string, string>;
  source?: string;
}

export interface MemoryFact {
  id: string;
  kind?: string;
  key?: Record<string, string>;
  value: string;
  unit?: string;
  source?: string;
  observed?: string;
  confidence?: string;
  updated?: string;
}

export interface MemoryDecision {
  id: string;
  key?: Record<string, string>;
  text: string;
  source?: string;
  by?: string;
  approved?: string;
}

/** 一条"可能过时"的标记（让记忆能自我纠错）。 */
export interface MemoryStale {
  id: string;
  note: string;
  found?: string;
}

/** 自动检查标出来的一条（给"查完告诉用户这次发现了什么"用）。 */
export interface StaleMark {
  id: string;
  kind: "fact" | "decision";
  value: string;
  note: string;
  where?: string;
  source?: string;
}

export interface MemoryFile {
  facts?: MemoryFact[];
  decisions?: MemoryDecision[];
  relations?: { kind?: string; tables?: string[]; source?: string; note?: string }[];
  stale?: MemoryStale[];
}

export interface ConvoMsgDto {
  role: "user" | "agent" | "system";
  text: string;
  time: string;
  /** 这条消息附的图（data URL）。落库了才能回看"当时给的是哪张图"。 */
  images?: string[];
  proposal?: {
    kind: string; title: string; detail: string; schedule?: string;
    action?: string; tools?: string[]; options?: string[];
    /** kind=task：跑哪区区；kind=zone：修改哪区区（空=新建）。 */
    zoneId?: string;
    mode?: string;
    zoneName?: string;
    zoneInputType?: string;
    zoneInputPath?: string;
    zoneInputPattern?: string;
    zoneTargetFile?: string;
    zoneTargetSheet?: string;
    zoneTargetNewTable?: string;
    accepted?: boolean;
  };
}

export interface ConvoDto {
  id: string;
  title: string;
  messages: ConvoMsgDto[];
  created: string;
  updated: string;
}

/* ---------- 快捷任务模板 ---------- */

/** 一条快捷任务模板：{槽位} 占位，触发后仍走 /chat → /plan → 确认。 */
export interface PromptTemplate {
  id: string;
  name: string;
  when?: string;
  template: string;
  slots: string[];
  note?: string;
  source?: string;
  created?: string;
  hits?: number;
}

/** 从对话总结出的候选（还没落盘，落盘前可改）。 */
export interface PromptCandidate {
  name: string;
  when?: string;
  template: string;
  note?: string;
  slots?: string[];
}

/* ---------- 规则引擎 ---------- */

/** 一条规则的可执行条件（when）——与导入同义，都会命中才算。 */
export interface RuleWhenDto {
  file?: string;
  format?: string;
  has_columns?: string[];
}

/** 一条规则的动作（then）。取值只从 inbox 按列名取，不做算术。 */
export interface RuleThenDto {
  sheet?: string;
  target_file?: string;
  key?: Record<string, string>;
  field?: Record<string, string>;
  month_from?: string;
  op?: string;
}

/** 人工声明的表间关联（补公式够不到的关系）。 */
export interface RuleLinkDto {
  from: string;
  to: string;
  note?: string;
}

export interface RuleDto {
  name: string;
  trigger?: string;
  action?: string;
  forbid?: string[];
  when?: RuleWhenDto;
  then?: RuleThenDto;
  link?: RuleLinkDto;
}

/** 规则 + 引擎对它"能不能跑"的判定（runnable=false 时 reason 说明原因）。 */
export interface RuleViewDto extends RuleDto {
  /** 类别：edge=改表 / link=声明关联 / forbid=护栏 / hint=纯提示。 */
  kind?: "edge" | "link" | "forbid" | "hint";
  runnable: boolean;
  reason?: string;
  target?: string;
  summary?: string;
}

export interface RuleProblem {
  name: string;
  reason: string;
}

export interface RulesResp {
  rules: RuleViewDto[];
  path: string;
  raw: string;
  executable: number;
  total: number;
}

export interface RuleDryItem {
  rule?: string;
  sheet?: string;
  ref?: string;
  field?: string;
  old?: string;
  new?: string;
  line?: number;
  why?: string;
}

export interface RulesDryRun {
  file?: string;
  target?: string;
  hits?: string[];
  items?: RuleDryItem[];
  skips?: RuleDryItem[];
  note?: string;
  wrote: boolean;
}
