import { useEffect, useCallback, useState } from "react";
import "./dashboard.css";
import { agentApi, nodeId } from "../api-agent";
import type { ScanReport, GraphData, GraphNode, LedgerEntry, WorkspaceFiles, SheetPreview } from "../api-agent";
import { IconRefresh, IconCheck, IconXls, IconNote } from "../components/icons";
import SheetView from "./SheetView";

/* 数据管家 · 主看板（见 docs/agent-architecture/14-第一屏设计.md、17-工作区与跨文件联动.md）
   一屏答一个问题：「我的表，有没有事？它要动什么？」
   工作区 = 一块地盘，可以含多个 xlsx；跨文件关联是真的（实测有 [n] 外部引用）。 */

type Load = "idle" | "loading" | "ready" | "error";

export default function Dashboard() {
  const [load, setLoad] = useState<Load>("loading");
  const [err, setErr] = useState("");
  const [wsName, setWsName] = useState("");
  const [files, setFiles] = useState<WorkspaceFiles | null>(null);
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [scan, setScan] = useState<ScanReport | null>(null);
  const [counts, setCounts] = useState({ error: 0, warn: 0 });
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [active, setActive] = useState<GraphNode | null>(null);
  const [tab, setTab] = useState<"confirm" | "scan">("confirm");
  const [zoomed, setZoomed] = useState(false);
  // 打开表格：{sheet, file, highlight} —— 表预览整屏覆盖（表数据要看全）
  const [opened, setOpened] = useState<{ sheet: string; file?: string; ref?: string } | null>(null);

  const refresh = useCallback(async () => {
    setLoad("loading");
    setErr("");
    try {
      await agentApi.workspace().then((ws) => {
        // 工作区名取目录名（工作区是容器，可以含多个表）
        const seg = ws.root.replace(/\\/g, "/").split("/").filter(Boolean);
        setWsName(seg[seg.length - 1] ?? "工作区");
      });
      const [f, g, s, l] = await Promise.all([
        agentApi.files().catch(() => null),
        agentApi.graph().catch(() => null),
        agentApi.scanRun().catch(() => null),
        agentApi.ledger(5).catch(() => ({ entries: [], limit: 5 })),
      ]);
      setFiles(f);
      setGraph(g);
      setScan(s?.report ?? null);
      setCounts({ error: s?.errors ?? 0, warn: s?.warns ?? 0 });
      setLedger(l.entries ?? []);
      // 默认选中"被引用最多"的那个节点（最能说明这张表的影响力）
      setActive(g?.nodes?.length ? pickHub(g) : null);
      setLoad("ready");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setLoad("error");
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  const pickNode = useCallback(async (n: GraphNode) => {
    setActive(n);
    try {
      setGraph(await agentApi.graph(nodeId(n)));
    } catch { /* 保持原图 */ }
  }, []);

  if (load === "loading" && !graph) {
    return <div className="dash"><div className="dash-empty"><p>正在读取工作区…</p></div></div>;
  }
  if (load === "error") {
    return (
      <div className="dash">
        <div className="dash-empty">
          <h2>还没连上数据管家</h2>
          <p>{err}</p>
          <p className="dash-hint">请先启动 Go 引擎：<code>gridwright -config config.yaml</code>（默认 127.0.0.1:7700）</p>
          <button className="btn primary" onClick={() => void refresh()}><IconRefresh size={14} />重试</button>
        </div>
      </div>
    );
  }

  // 表预览整屏覆盖：表数据要看全，右栏太窄
  if (opened) {
    return (
      <SheetView
        sheet={opened.sheet}
        file={opened.file}
        highlight={opened.ref ? { ref: opened.ref } : null}
        onBack={() => setOpened(null)}
      />
    );
  }

  // 空地盘：工作区里还没有表
  const tableCount = files?.tables?.length ?? 0;
  if (load === "ready" && tableCount === 0) {
    return (
      <div className="dash">
        <EmptyWorkspace root={graph?.root ?? ""} onRefresh={() => void refresh()} />
      </div>
    );
  }

  const propagate = graph?.propagate?.to ?? [];
  const hot = new Set(propagate);
  const issues = scan?.issues ?? [];

  return (
    <div className="dash">
      <header className="dash-top">
        <div className="dash-title">
          <IconXls size={17} />
          <span>{wsName || "工作区"}</span>
          <em className="dash-count">{tableCount} 个表</em>
        </div>
        <div className="dash-actions">
          <button className="btn ghost sm" onClick={() => setZoomed(true)}>展开联动图</button>
          <button className="btn ghost sm" onClick={() => void refresh()}><IconRefresh size={13} />体检</button>
        </div>
      </header>

      <div className="dash-body">
        {/* 左：联动摘要（按文件分组） */}
        <section className="dash-left">
          <h3 className="dash-h">表的联动</h3>
          <p className="dash-sub">点表看它牵动到谁 · 跨文件关联用虚线</p>

          {graph ? (
            <div className="chain-groups">
              {groupByFile(graph, 8).map((grp) => (
                <div key={grp.file} className="chain-group">
                  <div className="cg-file">
                    <IconXls size={12} />
                    <span>{grp.file}</span>
                  </div>
                  {grp.items.map((n) => {
                    const id = nodeId(n);
                    const isActive = active && nodeId(active) === id;
                    const isHot = hot.has(id);
                    return (
                      <button
                        key={id}
                        className={`chain-node${isHot ? " hot" : ""}${isActive ? " on" : ""}`}
                        onClick={() => void pickNode(n)}
                        title={`${n.file}!${n.sheet}`}
                      >
                        <span className="cn-name">{shortName(n.sheet)}</span>
                        {grp.refs[id] > 0 && <span className="cn-count">{grp.refs[id]}</span>}
                      </button>
                    );
                  })}
                </div>
              ))}
            </div>
          ) : <p className="dash-muted">未发现表间依赖</p>}

          <p className="dash-muted dash-foot">
            共 {graph?.nodes.length ?? 0} 张表 · {graph?.edges.length ?? 0} 条关联
            {(graph?.edges.filter((e) => e.crossFile).length ?? 0) > 0 &&
              ` · ${graph?.edges.filter((e) => e.crossFile).length} 条跨文件`}
          </p>
        </section>

        {/* 中/右：详情 + 待确认/体检 */}
        <section className="dash-right">
          {active && graph && (
            <div className="node-detail">
              <div className="nd-h">
                <b>{active.sheet}</b>
                <span>{active.file || "外部文件"}</span>
                <button
                  className="btn primary sm nd-open"
                  onClick={() => setOpened({ sheet: active.sheet, file: active.file })}
                >
                  打开表格
                </button>
              </div>
              <NodeStats node={active} />
              <div className="nd-links">
                <span className="nd-lab">牵动它</span>
                {hot.size === 0
                  ? <span className="dash-muted">无（改它不影响其他表）</span>
                  : [...hot].map((id) => <span key={id} className="nd-chip">{shortId(id)}</span>)}
              </div>
              <div className="nd-links">
                <span className="nd-lab">它依赖</span>
                {incoming(graph, active).length === 0
                  ? <span className="dash-muted">无</span>
                  : incoming(graph, active).map((id) => <span key={id} className="nd-chip dep">{shortId(id)}</span>)}
              </div>
            </div>
          )}

          <div className="dash-tabs">
            <button className={tab === "confirm" ? "on" : ""} onClick={() => setTab("confirm")}>需要确认</button>
            <button className={tab === "scan" ? "on" : ""} onClick={() => setTab("scan")}>
              体检发现{(counts.error + counts.warn) > 0 && <em>{counts.error + counts.warn}</em>}
            </button>
          </div>

          {tab === "confirm" ? (
            <div className="dash-pane">
              <div className="dash-none">
                <IconCheck size={18} />
                <p>没有待确认的改动</p>
                <span>把新数据放进 inbox，或到对话里下达任务</span>
              </div>
            </div>
          ) : (
            <div className="dash-pane">
              <div className="scan-sum">
                <span className="scan-err">{counts.error} 处错误</span>
                <span className="scan-warn">{counts.warn} 处存疑</span>
                <span className="scan-cells">{scan?.cells.toLocaleString() ?? 0} 格 · {scan?.sheets ?? 0} 表 · {scan?.elapsed ?? "—"}</span>
              </div>
              <ul className="scan-list">
                {issues.slice(0, 80).map((it, i) => (
                  <li key={i} className={`scan-item ${it.severity}`}>
                    <span className="si-dot" />
                    <div className="si-main">
                      <button
                        className="si-ref si-ref-btn"
                        onClick={() => setOpened({ sheet: it.sheet, file: it.file, ref: it.ref })}
                        title="打开表格并定位到该格"
                      >
                        {shortName(it.sheet)}!{it.ref}
                      </button>
                      <span className="si-msg">{it.message}</span>
                    </div>
                  </li>
                ))}
                {issues.length === 0 && <li className="dash-muted">本次未发现问题</li>}
              </ul>
            </div>
          )}
        </section>
      </div>

      <footer className="dash-ledger">
        <span className="dl-h"><IconNote size={13} />账目</span>
        {ledger.length === 0 ? (
          <span className="dash-muted">还没有改动记录</span>
        ) : (
          ledger.map((e, i) => (
            <span key={i} className="dl-row">
              <em>{e.ts}</em> {e.table} {e.cell} {e.old}→{e.new}
              <span className={e.status === "ok" ? "ok" : "rj"}>{e.status}</span>
            </span>
          ))
        )}
      </footer>

      {zoomed && graph && (
        <GraphOverlay graph={graph} active={active} hot={hot} onPick={(n) => void pickNode(n)} onClose={() => setZoomed(false)} />
      )}
    </div>
  );
}

/* ---------- 节点汇总（用户要的"点模块展示大概汇总数据"） ---------- */

function NodeStats({ node }: { node: GraphNode }) {
  const [pv, setPv] = useState<SheetPreview | null>(null);
  useEffect(() => {
    let alive = true;
    agentApi.preview(node.sheet, node.file, 1)
      .then((d) => { if (alive) setPv(d); })
      .catch(() => { if (alive) setPv(null); });
    return () => { alive = false; };
  }, [node.sheet, node.file]);

  if (!pv) return <div className="nd-stats muted">汇总读取中…</div>;

  // 空表（无数据行）时 summaries/sample/header 可能是 null，必须兜底
  const sums = pv.summaries ?? [];
  const header = pv.header ?? [];
  const overview = sums.find((s) => s.label && s.values.length > 2) ?? sums[0];
  return (
    <div className="nd-stats">
      <div className="nd-stat-row">
        <span className="ns-k">规模</span>
        <span className="ns-v">{pv.rows} 行 · {pv.cols} 列 · {pv.formulas} 公式</span>
      </div>
      <div className="nd-stat-row">
        <span className="ns-k">列</span>
        <span className="ns-v ns-cols">{header.filter(Boolean).slice(0, 6).join(" / ")}{header.length > 6 ? " …" : ""}</span>
      </div>
      {overview && (overview.values ?? []).length > 0 && (
        <div className="nd-stat-row">
          <span className="ns-k">概览</span>
          <span className="ns-v ns-sum">
            {(overview.values ?? []).filter((v) => v !== overview.label).slice(0, 8).map((v, i) => (
              <span key={i} className="ns-chip">{v}</span>
            ))}
          </span>
        </div>
      )}
      {pv.note && <div className="nd-note">{pv.note}</div>}
    </div>
  );
}

/* ---------- 空地盘 ---------- */

function EmptyWorkspace({ root, onRefresh }: { root: string; onRefresh: () => void }) {
  return (
    <div className="dash-empty">
      <h2>还没有工作区内容</h2>
      <p>工作区是一块地盘，可以放多个表。把 xlsx 拖进这个文件夹，或选一个已有文件夹当工作区。</p>
      <p className="dash-hint">当前目录：<code>{root || "（未设置）"}</code></p>
      <p className="dash-hint">放入文件后点这里重新读取。inbox 里的新数据会自动被处理。</p>
      <button className="btn primary" onClick={onRefresh}><IconRefresh size={14} />重新读取</button>
    </div>
  );
}

/* ---------- 大图 ---------- */

function GraphOverlay({ graph, active, hot, onPick, onClose }: {
  graph: GraphData; active: GraphNode | null; hot: Set<string>;
  onPick: (n: GraphNode) => void; onClose: () => void;
}) {
  // "相关就连，不相关留白"：只把有边的节点画进图里，孤立的单独留白区。
  // 留白本身是信息（这些表目前互不相关），也是将来权重计算的观察起点。
  const related = new Set<string>();
  for (const e of graph.edges) {
    related.add(nodeId(e.from));
    related.add(nodeId(e.to));
  }
  const linked: GraphNode[] = [];
  const isolated: GraphNode[] = [];
  for (const n of graph.nodes) {
    (related.has(nodeId(n)) ? linked : isolated).push(n);
  }
  const linkedGroups = groupNodes(graph, linked);
  const isolatedGroups = groupNodes(graph, isolated);

  return (
    <div className="gv-overlay" onClick={onClose}>
      <div className="gv-panel" onClick={(e) => e.stopPropagation()}>
        <header className="gv-head">
          <b>联动图</b>
          <span className="dash-muted">
            {graph.files.length} 个文件 · {linked.length} 张有关联 · {isolated.length} 张暂无关联 · {graph.edges.length} 条边
          </span>
          <button className="btn ghost sm" onClick={onClose}>关闭</button>
        </header>
        <div className="gv-body">
          <div className="gv-section-h">有关联</div>
          {linkedGroups.map((grp) => (
            <FileGroup key={grp.file} grp={grp} active={active} hot={hot} onPick={onPick} />
          ))}
          {linked.length === 0 && <p className="dash-muted">未发现表间关联</p>}

          {graph.edges.length > 0 && (
            <div className="gv-edges">
              <div className="gv-edges-h">关联明细</div>
              {graph.edges.map((e, i) => (
                <div key={i} className={`gv-edge${e.crossFile ? " cross" : ""}`}>
                  <span className="gv-from">{e.to.sheet}</span>
                  <span className="gv-arrow">{e.crossFile ? "⇢" : "→"}</span>
                  <span className="gv-to">{e.from.sheet}</span>
                  <span className="gv-kind">
                    {e.kind === "formula" ? "公式" : e.kind === "declared" ? "声明" : "键列"} ×{e.count}
                    {e.crossFile && " · 跨文件"}
                    {e.externalIdx ? ` · [${e.externalIdx}]` : ""}
                  </span>
                </div>
              ))}
            </div>
          )}

          {isolated.length > 0 && (
            <div className="gv-isolated">
              <div className="gv-edges-h">
                暂无关联 <span className="dash-muted">（先留白 · 等权重出来再看是否连上）</span>
              </div>
              {isolatedGroups.map((grp) => (
                <FileGroup key={grp.file} grp={grp} active={active} hot={hot} onPick={onPick} muted />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function FileGroup({ grp, active, hot, onPick, muted }: {
  grp: Group; active: GraphNode | null; hot: Set<string>;
  onPick: (n: GraphNode) => void; muted?: boolean;
}) {
  return (
    <div className={`gv-file${muted ? " muted" : ""}`}>
      <div className="gv-file-head"><IconXls size={13} />{grp.file}</div>
      <div className="gv-nodes">
        {grp.items.map((n) => {
          const id = nodeId(n);
          const isActive = active && nodeId(active) === id;
          const isHot = hot.has(id);
          return (
            <button key={id}
              className={`gv-node${isHot ? " hot" : ""}${isActive ? " on" : ""}`}
              onClick={() => onPick(n)} title={`${n.file}!${n.sheet}`}>
              {n.sheet}
              {grp.refs[id] > 0 && <span className="gv-count">{grp.refs[id]}</span>}
            </button>
          );
        })}
      </div>
    </div>
  );
}

/* ---------- 辅助 ---------- */

/** 被引用最多的节点（最能代表这张工作区的"枢纽"表）。 */
function pickHub(g: GraphData): GraphNode | null {
  if (!g.nodes.length) return null;
  const score = new Map<string, number>();
  for (const e of g.edges) score.set(nodeId(e.to), (score.get(nodeId(e.to)) ?? 0) + e.count);
  let best = g.nodes[0], bestN = -1;
  for (const n of g.nodes) {
    const s = score.get(nodeId(n)) ?? 0;
    if (s > bestN) { best = n; bestN = s; }
  }
  return best;
}

/** 某节点依赖谁（它作为 from 的边，指向的 to）。 */
function incoming(g: GraphData, n: GraphNode): string[] {
  const id = nodeId(n);
  const out = new Set<string>();
  for (const e of g.edges) {
    if (nodeId(e.from) === id && e.confidence === "high") out.add(nodeId(e.to));
  }
  return [...out];
}

type Group = { file: string; items: GraphNode[]; refs: Record<string, number> };

/** 按文件分组节点，并统计每个节点被引用次数（用于徽标）。limit=0 表示不截断。 */
function groupByFile(g: GraphData, limit = 0): Group[] {
  return groupNodes(g, g.nodes, limit);
}

/** 把给定节点按文件分组（用于"有关联 / 留白"分区渲染）。 */
function groupNodes(g: GraphData, nodes: GraphNode[], limit = 0): Group[] {
  const refs: Record<string, number> = {};
  for (const e of g.edges) refs[nodeId(e.to)] = (refs[nodeId(e.to)] ?? 0) + e.count;
  const byFile = new Map<string, GraphNode[]>();
  for (const n of nodes) {
    const key = n.file || "（外部文件）";
    if (!byFile.has(key)) byFile.set(key, []);
    byFile.get(key)!.push(n);
  }
  return [...byFile.entries()]
    .sort((a, b) => b[1].length - a[1].length)
    .map(([file, items]) => ({ file, items: limit > 0 ? items.slice(0, limit) : items, refs }));
}

function shortName(name: string): string {
  return name.replace(/\s*（[日月末]）\s*/g, "").replace(/\s+/g, "").trim();
}

function shortId(id: string): string {
  const i = id.indexOf("!");
  return i >= 0 ? shortName(id.slice(i + 1)) : shortName(id);
}
