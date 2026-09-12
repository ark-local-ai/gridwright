import { useEffect, useCallback, useState } from "react";
import "./dashboard.css";
import { agentApi } from "../api-agent";
import type { GraphData, ScanReport, LedgerEntry } from "../api-agent";
import { IconRefresh, IconCheck, IconXls, IconNote } from "../components/icons";

/* 数据管家 · 主看板（见 docs/agent-architecture/14-第一屏设计.md）
   一屏答一个问题：「我的表，有没有事？它要动什么？」
   左=联动图（这张表的脸）  右=需要你确认 + 体检发现  底=账目 */

type Load = "idle" | "loading" | "ready" | "error";

export default function Dashboard() {
  const [load, setLoad] = useState<Load>("loading");
  const [err, setErr] = useState("");
  const [wsName, setWsName] = useState("");
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [scan, setScan] = useState<ScanReport | null>(null);
  const [counts, setCounts] = useState({ error: 0, warn: 0 });
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [activeSheet, setActiveSheet] = useState<string | null>(null);
  const [tab, setTab] = useState<"confirm" | "scan">("confirm");

  const refresh = useCallback(async () => {
    setLoad("loading");
    setErr("");
    try {
      await agentApi.workspace(); // 确认工作区可用（失败会抛错，进 error 态）
      // 工作区根目录只是容器；标题应显示真正被看管的表名。
      const files = await agentApi.files().catch(() => null);
      setWsName(files?.tables?.[0]?.name ?? "");
      // 体检 + 联动图都是秒级只读，进工作区就一起跑（首屏立刻有内容）
      const [g, s, l] = await Promise.all([
        agentApi.graph().catch(() => null),
        agentApi.scanRun().catch(() => null),
        agentApi.ledger(5).catch(() => ({ entries: [], limit: 5 })),
      ]);
      setGraph(g);
      setScan(s?.report ?? null);
      setCounts({ error: s?.errors ?? 0, warn: s?.warns ?? 0 });
      setLedger(l.entries ?? []);
      if (g?.sheets?.length) setActiveSheet(g.sheets[0]);
      setLoad("ready");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setLoad("error");
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  const pickSheet = useCallback(async (sheet: string) => {
    setActiveSheet(sheet);
    try {
      const g = await agentApi.graph(sheet);
      setGraph(g);
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

  const propagate = graph?.propagate?.to ?? [];
  const chain = buildChain(graph);
  const issues = scan?.issues ?? [];

  return (
    <div className="dash">
      <header className="dash-top">
        <div className="dash-title">
          <IconXls size={17} />
          <span>{wsName || "工作区"}</span>
        </div>
        <div className="dash-actions">
          <button className="btn ghost sm" onClick={() => void refresh()}><IconRefresh size={13} />体检</button>
        </div>
      </header>

      <div className="dash-body">
        {/* 左：联动图 */}
        <section className="dash-left">
          <h3 className="dash-h">表的联动</h3>
          <p className="dash-sub">点了哪张表，就高亮它牵动到谁</p>
          <div className="chain">
            {chain.map((node, i) => {
              const hot = propagate.includes(node.name);
              const isActive = activeSheet === node.name;
              return (
                <div key={node.name} className="chain-row">
                  <button
                    className={`chain-node${hot ? " hot" : ""}${isActive ? " on" : ""}`}
                    onClick={() => void pickSheet(node.name)}
                    title={node.name}
                  >
                    <span className="cn-name">{shortName(node.name)}</span>
                    {node.edges > 0 && <span className="cn-count">{node.edges}</span>}
                  </button>
                  {i < chain.length - 1 && <span className={`chain-link${hot ? " hot" : ""}`} />}
                </div>
              );
            })}
            {chain.length === 0 && <p className="dash-muted">未发现表间依赖</p>}
          </div>
          <p className="dash-muted dash-foot">共 {graph?.sheets.length ?? 0} 张表 · {graph?.edges.length ?? 0} 条关联</p>
        </section>

        {/* 右：需要你确认 / 体检发现 */}
        <section className="dash-right">
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
                <span className="scan-cells">{scan?.cells.toLocaleString() ?? 0} 格 · {scan?.elapsed ?? "—"}</span>
              </div>
              <ul className="scan-list">
                {issues.slice(0, 60).map((it, i) => (
                  <li key={i} className={`scan-item ${it.severity}`}>
                    <span className="si-dot" />
                    <div className="si-main">
                      <span className="si-ref">{shortName(it.sheet)}!{it.ref}</span>
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

      {/* 底：账目 */}
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
    </div>
  );
}

/* ---- 辅助 ---- */

type ChainNode = { name: string; edges: number };

/** 把联动图压成一条主链：按入度/被引用次数排序，取有边的表构成可读的纵向链。 */
function buildChain(g: GraphData | null): ChainNode[] {
  if (!g || g.edges.length === 0) {
    // 没有边时，把前若干张表平铺（仍让用户能点）
    return (g?.sheets ?? []).slice(0, 6).map((name) => ({ name, edges: 0 }));
  }
  const score = new Map<string, number>();
  for (const e of g.edges) {
    score.set(e.to, (score.get(e.to) ?? 0) + e.count);
    if (!score.has(e.from)) score.set(e.from, score.get(e.from) ?? 0);
  }
  return [...score.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, 6)
    .map(([name, edges]) => ({ name, edges }));
}

/** 长 sheet 名压短：去掉"租金情况表"这类尾巴，保留关键信息。 */
function shortName(name: string): string {
  return name
    .replace(/\s*（[日月末]）\s*/g, "")
    .replace(/\s+/g, "")
    .trim();
}
