// 数据管家 API 客户端（对接 Go 引擎的 /api/v1，见 docs/agent-architecture/13-接口契约.md）
//
// 与旧 api.ts 的区别：旧文件对接的是 legacy TS 后端（tasks/chat/skills…，将被砍）；
// 本文件对接"手脑分离"的 Go 引擎。两条 API 在过渡期并存，互不干扰。
//
// 开发时走 Vite 代理 /agent-api → 127.0.0.1:7700；桌面构建时由 VITE_AGENT_API_BASE 注入。
const BASE = import.meta.env.VITE_AGENT_API_BASE ?? "/agent-api";

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `${res.status}`);
  }
  return res.json() as Promise<T>;
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const b = await res.json().catch(() => ({}));
    throw new Error((b as { error?: string }).error ?? `${res.status}`);
  }
  return res.json() as Promise<T>;
}

async function del<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`, { method: "DELETE" });
  if (!res.ok) {
    const b = await res.json().catch(() => ({}));
    throw new Error((b as { error?: string }).error ?? `${res.status}`);
  }
  return res.json() as Promise<T>;
}

async function put<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const b = await res.json().catch(() => ({}));
    throw new Error((b as { error?: string }).error ?? `${res.status}`);
  }
  return res.json() as Promise<T>;
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
  rule: string;
  model: string;
  status: string;
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
  canRollback: boolean;
  whyNot?: string;
  group: string;
}

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

export const agentApi = {
  health: () => get<{ ok: boolean; workspace: string }>("/api/v1/health"),
  workspace: () => get<WorkspaceInfo>("/api/v1/workspace"),
  files: () => get<WorkspaceFiles>("/api/v1/workspace/files"),
  graph: (node?: string) =>
    get<GraphData>(`/api/v1/graph${node ? `?node=${encodeURIComponent(node)}` : ""}`),
  scan: () => get<ScanResult>("/api/v1/scan"),
  scanRun: () => post<{ ok: boolean; errors: number; warns: number; report: ScanReport }>("/api/v1/scan/run"),
  ledger: (limit = 50) => get<{ entries: LedgerEntry[]; limit: number }>(`/api/v1/ledger?limit=${limit}`),
  sheets: (file?: string) =>
    get<{ file: string; sheets: string[] }>(`/api/v1/sheets${file ? `?file=${encodeURIComponent(file)}` : ""}`),
  preview: (sheet: string, file?: string, rows = 50) => {
    const q = new URLSearchParams({ sheet, rows: String(rows) });
    if (file) q.set("file", file);
    return get<SheetPreview>(`/api/v1/sheets/preview?${q.toString()}`);
  },
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
  // 设置（脑）
  settings: () => get<SettingsDto>("/api/v1/settings"),
  saveSettings: (p: { baseUrl?: string; apiKey?: string; model?: string }) =>
    put<{ ok: boolean; brainReady: boolean; saved: boolean; configPath?: string; warning?: string }>(
      "/api/v1/settings", p),
  // 待确认闭环：计划 → 确认 → 执行
  // 归一化：Go 的空切片会 marshal 成 null，界面若直接 .length/.map 会整页崩，
  // 所以在 API 边界把 items/blocked/affects 一律收敛成数组。
  plan: async (instruction: string, file?: string) => {
    const r = await post<{ id: string; proposal: Proposal }>("/api/v1/plan", { instruction, file });
    if (r?.proposal) r.proposal = normalizeProposal(r.proposal);
    return r;
  },
  apply: (id: string) =>
    post<{
      ok: boolean; applied: number; rejected: number;
      results: ApplyResult[]; selfCheck?: SelfCheckReport | null;
    }>("/api/v1/apply", { id }),
  selfCheck: (node: string, kind?: string, files?: string[]) =>
    post<SelfCheckReport>("/api/v1/selfcheck", { node, kind, files }),
  // 会话
  chat: (message: string, conversationId?: string) =>
    post<{ conversationId: string; conversation: ConvoDto }>("/api/v1/chat", { message, conversationId }),
  conversations: () => get<{ items: ConvoSummary[] }>("/api/v1/conversations"),
  conversation: (id: string) => get<ConvoDto>(`/api/v1/conversation?id=${encodeURIComponent(id)}`),
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
  // 权重（结构重要性 + 活跃度 + 风险 → 注意力）
  weights: () => get<{ scores: WeightScore[]; hasUsage: boolean; note: string }>("/api/v1/weights"),
};

export interface ConvoSummary {
  id: string;
  title: string;
  created: string;
  updated: string;
}

export interface ConvoMsgDto {
  role: "user" | "agent" | "system";
  text: string;
  time: string;
  proposal?: {
    kind: string; title: string; detail: string; schedule?: string;
    action?: string; tools?: string[]; options?: string[];
  };
}

export interface ConvoDto {
  id: string;
  title: string;
  messages: ConvoMsgDto[];
  created: string;
  updated: string;
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
