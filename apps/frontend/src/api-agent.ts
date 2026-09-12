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

export interface WorkspaceInfo {
  root: string;
  tables: number;
  inboxCount: number;
  ledgerPath: string;
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

export interface GraphEdge {
  from: string;
  to: string;
  kind: "formula" | "semantic" | "declared";
  count: number;
  confidence: "high" | "medium";
}

export interface GraphData {
  file: string;
  sheets: string[];
  edges: GraphEdge[];
  propagate?: { from: string; to: string[] };
}

export interface ScanIssue {
  kind: string;
  severity: "error" | "warn" | "info";
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

export const agentApi = {
  health: () => get<{ ok: boolean; workspace: string }>("/api/v1/health"),
  workspace: () => get<WorkspaceInfo>("/api/v1/workspace"),
  files: () => get<WorkspaceFiles>("/api/v1/workspace/files"),
  graph: (sheet?: string) =>
    get<GraphData>(`/api/v1/graph${sheet ? `?sheet=${encodeURIComponent(sheet)}` : ""}`),
  scan: () => get<ScanResult>("/api/v1/scan"),
  scanRun: () => post<{ ok: boolean; errors: number; warns: number; report: ScanReport }>("/api/v1/scan/run"),
  ledger: (limit = 50) => get<{ entries: LedgerEntry[]; limit: number }>(`/api/v1/ledger?limit=${limit}`),
};
