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

export interface ApplyResult {
  ref: string;
  sheet: string;
  field: string;
  old: unknown;
  new: unknown;
  status: "ok" | "rejected";
  note?: string;
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
  plan: (instruction: string, file?: string) =>
    post<{ id: string; proposal: Proposal }>("/api/v1/plan", { instruction, file }),
  apply: (id: string) =>
    post<{ ok: boolean; applied: number; rejected: number; results: ApplyResult[] }>("/api/v1/apply", { id }),
  // 会话
  chat: (message: string, conversationId?: string) =>
    post<{ conversationId: string; conversation: ConvoDto }>("/api/v1/chat", { message, conversationId }),
  conversations: () => get<{ items: ConvoSummary[] }>("/api/v1/conversations"),
  conversation: (id: string) => get<ConvoDto>(`/api/v1/conversation?id=${encodeURIComponent(id)}`),
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
