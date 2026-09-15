import { useEffect, useCallback, useState } from "react";
import "./dashboard.css";
import { agentApi, nodeId } from "../api-agent";
import type { ScanReport, GraphData, GraphNode, LedgerEntry, WorkspaceFiles, SheetPreview, WorkspaceListItem, Proposal, WeightScore, ScanIssue, SafetyReport, SelfCheckReport } from "../api-agent";
import { IconRefresh, IconCheck, IconXls, IconNote, IconChevD, IconGear, IconFolder, IconLink, IconX, IconSpark, IconClock } from "../components/icons";
import AttentionList from "../components/AttentionList";
import LinkMap from "../components/LinkMap";
import SheetView from "./SheetView";
import { SkPanel } from "../components/Skeleton";
import { useChanged } from "../lib/useChanged";
import Settings from "./Settings2";
import LedgerPanel from "./LedgerPanel";
import TasksPanel from "./TasksPanel";
import RulesPanel from "./RulesPanel";
import { PendingList, ChatPane } from "./Pending";

/* 数据管家 · 主看板（见 docs/agent-architecture/14-第一屏设计.md、17-工作区与跨文件联动.md）
   一屏答一个问题：「我的表，有没有事？它要动什么？」
   工作区 = 一块地盘，可以含多个 xlsx；跨文件关联是真的（实测有 [n] 外部引用）。 */

type Load = "idle" | "loading" | "ready" | "error";

export default function Dashboard({ pickFolder }: { pickFolder?: () => Promise<string | null> } = {}) {
  const [load, setLoad] = useState<Load>("loading");
  const [err, setErr] = useState("");
  const [wsName, setWsName] = useState("");
  const [wsPath, setWsPath] = useState("");
  const [chosen, setChosen] = useState(true); // 是否显式选过工作区
  const [files, setFiles] = useState<WorkspaceFiles | null>(null);
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [weights, setWeights] = useState<Record<string, WeightScore>>({});
  const [weightsNote, setWeightsNote] = useState("");
  const [scan, setScan] = useState<ScanReport | null>(null);
  const [counts, setCounts] = useState({ error: 0, warn: 0 });
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [active, setActive] = useState<GraphNode | null>(null);
  const [chatOpen, setChatOpen] = useState(false);
  const [ledgerOpen, setLedgerOpen] = useState(false);
  const [tasksOpen, setTasksOpen] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [zoomed, setZoomed] = useState(false);
  // 打开表格：{sheet, file, highlight} —— 表预览整屏覆盖（表数据要看全）
  const [opened, setOpened] = useState<{ sheet: string; file?: string; ref?: string } | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [switcherOpen, setSwitcherOpen] = useState(false);
  const [brainReady, setBrainReady] = useState(true);
  // 待确认清单（A）与应用结果提示
  const [proposal, setProposal] = useState<Proposal | null>(null);
  const [safetyRep, setSafetyRep] = useState<SafetyReport | null>(null);
  const [appliedNote, setAppliedNote] = useState("");
  const [lastSelfCheck, setLastSelfCheck] = useState<SelfCheckReport | null>(null);

  const refresh = useCallback(async () => {
    setLoad("loading");
    setErr("");
    try {
      await agentApi.workspace().then((ws) => {
        // 工作区名取目录名（工作区是容器，可以含多个表）
        const seg = ws.root.replace(/\\/g, "/").split("/").filter(Boolean);
        setWsName(seg[seg.length - 1] ?? "工作区");
        setWsPath(ws.root);
        setBrainReady(ws.brainReady);
        setChosen(ws.workspaceChosen);
      });
      const [f, g, s, l, w] = await Promise.all([
        agentApi.files().catch(() => null),
        agentApi.graph().catch(() => null),
        agentApi.scanRun().catch(() => null),
        agentApi.ledger(5).catch(() => ({ entries: [], limit: 5 })),
        agentApi.weights().catch(() => null),
      ]);
      setFiles(f);
      setGraph(g);
      setScan(s?.report ?? null);
      setCounts({ error: s?.errors ?? 0, warn: s?.warns ?? 0 });
      setLedger(l.entries ?? []);
      agentApi.safety().then((r) => setSafetyRep(r.report)).catch(() => setSafetyRep(null));
      if (w) {
        const m: Record<string, WeightScore> = {};
        for (const sc of w.scores) m[nodeId(sc.node)] = sc;
        setWeights(m);
        setWeightsNote(w.note);
      }
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
    // 首屏加载：给骨架而不是"正在读取工作区…"——骨架一眼就懂，且不跳版
    return (
      <div className="dash">
        <div className="dash-boot">
          <div className="dash-boot-side"><SkPanel rows={7} /></div>
          <div className="dash-boot-main">
            <SkPanel rows={2} />
            <div className="sk-block sk-stat" />
            <SkPanel rows={5} />
          </div>
        </div>
      </div>
    );
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

  // 空地盘：工作区里还没有表。**仍要保留顶栏**——否则用户没法切换工作区/进设置，
  // 新建用户会卡在空白页没有出路。
  const tableCount = files?.tables?.length ?? 0;
  // 首次运行（还没选过工作区）：引导去选，**不是**告诉他"工作区是空的"
  const isFirstRun = load === "ready" && !chosen;
  const isEmpty = load === "ready" && chosen && tableCount === 0;

  const propagate = graph?.propagate?.to ?? [];
  const hot = new Set(propagate);
  const issues = scan?.issues ?? [];

  return (
    <div className="dash">
      {/* 仪表头：一眼看完这张表的"读数" —— 工作区、表数、待办、问题。
          高级感来自"读数排布"，不是装饰。 */}
      <header className="dash-top">
        <div className="dash-ident">
          <button className="ws-switch" onClick={() => setSwitcherOpen((v) => !v)} title="切换工作区">
            <span className="ws-name">{wsName || "工作区"}</span>
            <IconChevD size={14} />
          </button>
          {!brainReady && <span className="dash-offline" title="未配置模型：看表/体检可用，改表需先在设置里配">未配模型</span>}
          {switcherOpen && (
            <WorkspaceSwitcher
              onPick={() => { setSwitcherOpen(false); void refresh(); }}
              onOpenSettings={() => { setSwitcherOpen(false); setSettingsOpen(true); }}
            />
          )}
        </div>

        <div className="dash-readout">
          <Readout label="表" value={isFirstRun ? "—" : String(tableCount)} />
          <Readout label="待办" value={String(proposal?.items.length ?? 0)} tone={proposal && proposal.items.length > 0 ? "act" : undefined} />
          <Readout label="问题" value={String(counts.error + counts.warn)} tone={counts.error + counts.warn > 0 ? "warn" : undefined} />
        </div>

        <div className="dash-actions">
          {appliedNote && <span className="dash-applied"><IconCheck size={12} />{appliedNote}</span>}
          {lastSelfCheck && lastSelfCheck.level !== "ok" && (
            <span className={`dash-self ${lastSelfCheck.level}`} title={lastSelfCheck.summary}>
              自检 {lastSelfCheck.findings.length} 项待看
            </span>
          )}
          {/* 主行动（与"看这张表"同一档）：联动图 + 体检。留文字标签。 */}
          {!isEmpty && !isFirstRun && (
            <button className="btn ghost sm" onClick={() => setZoomed(true)}><IconLink size={13} />联动图</button>
          )}
          {!isEmpty && !isFirstRun && (
            <button className="btn ghost sm" onClick={() => void refresh()}><IconRefresh size={13} />体检</button>
          )}
          {/* 次要入口：图标 + 提示（tooltip），不占文字宽度。
              它们不是日常动作——每天要看的是上面的数字与下面的体检，
              任务/规则/对话/设置是"偶尔去一趟"的地方，做成小方块即可。 */}
          <div className="dash-mini" role="group" aria-label="更多">
            {!isFirstRun && (
              <button className="mini-btn" onClick={() => setTasksOpen(true)} title="定时任务" aria-label="定时任务">
                <IconClock size={15} />
              </button>
            )}
            {!isFirstRun && (
              <button className="mini-btn" onClick={() => setRulesOpen(true)} title="规则（rules.yaml）" aria-label="规则">
                <IconNote size={15} />
              </button>
            )}
            <button className="mini-btn" onClick={() => setChatOpen((v) => !v)} title="对话" aria-label="对话">
              <IconSpark size={15} />
            </button>
            <button className="mini-btn" onClick={() => setSettingsOpen(true)} title="设置" aria-label="设置">
              <IconGear size={15} />
            </button>
          </div>
        </div>
      </header>

      {/* 对话：配置手段（说一句 → 出提案），不是常驻主界面 */}
      {chatOpen && (
        <div className="chat-drawer">
          <header className="cd-head">
            <IconSpark size={14} />
            <b>对话</b>
            <span className="cd-hint">说一句让它安排工作；它只建议，改表要你确认</span>
            <button className="cd-x" onClick={() => setChatOpen(false)} aria-label="关闭对话"><IconX size={14} /></button>
          </header>
          <div className="cd-body">
            <ChatPane onPlanReady={(p) => { setProposal(p); setChatOpen(false); }} />
          </div>
        </div>
      )}

      {settingsOpen && (
        <Settings onClose={() => setSettingsOpen(false)} onWorkspaceChanged={() => void refresh()} pickFolder={pickFolder} />
      )}

      {/* 空地盘：只有这一块，但顶栏在，能换工作区 */}
      {isFirstRun ? (
        <div className="dash-body dash-body-empty">
          <FirstRun root={wsPath} pickFolder={pickFolder} onDone={() => void refresh()} />
        </div>
      ) : isEmpty ? (
        <div className="dash-body dash-body-empty">
          <EmptyWorkspace root={wsPath} onRefresh={() => void refresh()} onNew={() => setSettingsOpen(true)} />
        </div>
      ) : (
        <>

      <div className="dash-body">
        {/* 左：联动摘要（按文件分组） */}
        <section className="dash-left rise">
          {/* 待查清单：先答"该查什么"。收租率那种单表汇总 Excel 自己做得了，
              这里只放**跨表才能得出**的结论——同一个铺位在 13 张月表里对不上。 */}
          <h3 className="dash-h">该查什么</h3>
          <p className="dash-sub">跨月才对得出来的问题，点开可跳到那一格</p>
          <AttentionList issues={issues} onOpen={(it) => setOpened({ sheet: it.sheet, file: it.file, ref: it.ref })} />

          {/* 连接用**图**表达：一行一族，实心=连着、空心=断开。
              原来那种平铺列表既看不出结构，也说不出"哪断了"。 */}
          <h3 className="dash-h dash-h-links">表的连接</h3>
          <p className="dash-sub">哪些表该连着，现在断开了</p>
          {graph ? (
            <LinkMap
              graph={graph}
              issues={issues}
              onOpenSheet={(n) => void pickNode(n)}
              onOpenIssue={(it) => setOpened({ sheet: it.sheet, file: it.file, ref: it.ref })}
            />
          ) : <p className="dash-muted">未发现表间依赖</p>}

          {weightsNote && <p className="dash-weight-note">{weightsNote}</p>}
          <p className="dash-muted dash-foot">
            共 {graph?.nodes.length ?? 0} 张表 · {graph?.edges.length ?? 0} 条关联
            {(graph?.edges.filter((e) => e.crossFile).length ?? 0) > 0 &&
              ` · ${graph?.edges.filter((e) => e.crossFile).length} 条跨文件`}
          </p>
        </section>

        {/* 中/右：详情 + 待确认/体检 */}
        <section className="dash-right rise rise-1">
          {active && graph && (
            <div className="node-detail">
              {/* 选中的表 = 这一屏的主角，做成"仪器铭牌"：
                  大标题给名字，右侧一个主行动，下面一行等宽读数。
                  以前是"一段平文本 + 一个小按钮"，主角感全无。 */}
              <div className="nd-plate">
                <div className="nd-plate-h">
                  <h2 className="nd-name">{active.sheet}</h2>
                  <button
                    className="btn primary sm nd-open"
                    onClick={() => setOpened({ sheet: active.sheet, file: active.file })}
                  >
                    打开表格
                  </button>
                </div>
                <p className="nd-file">
                  <IconXls size={12} />
                  {active.file || "外部文件"}
                </p>
              </div>
              <NodeStats node={active} />
              {/* 关联只在其一时才显示对应那行；两边都空就整块不要——
                  避免两行"无"白占两百像素。 */}
              {(() => {
                const dependsOn = incoming(graph, active);
                const affects = [...hot];
                if (dependsOn.length === 0 && affects.length === 0) return null;
                return (
                  <div className="nd-links">
                    {affects.length > 0 && (
                      <>
                        <span className="nd-lab">牵动</span>
                        {affects.map((id) => <span key={id} className="nd-chip">{shortId(id)}</span>)}
                      </>
                    )}
                    {dependsOn.length > 0 && (
                      <>
                        <span className="nd-lab">依赖</span>
                        {dependsOn.map((id) => <span key={id} className="nd-chip dep">{shortId(id)}</span>)}
                      </>
                    )}
                  </div>
                );
              })()}
            </div>
          )}

          {/* 两块堆叠，不是一个平级 tab 组：
              上=「需要你处理」唯一要你动手的地方，给最强视觉权重；
              下=「体检」信息，安静呈现。
              （以前三个 tab 等权，用户得自己点进去才知道有没有事。） */}
          <div className="dash-pane">
            <section className={`act-block${proposal && proposal.items.length > 0 ? " has-work" : ""}`}>
              <div className="blk-head">
                <h4>需要你处理</h4>
                {proposal && proposal.items.length > 0 && <span className="blk-n act">{proposal.items.length} 处待确认</span>}
              </div>
              <PendingList
                proposal={proposal}
                safety={safetyRep}
                impactNode={proposal?.items?.[0] ? `${proposal.items[0].file}!${proposal.items[0].sheet}` : undefined}
                onApplied={(r) => {
                  setProposal(null);
                  setAppliedNote(`${r.applied} 处已应用${r.rejected ? `，${r.rejected} 处被拒` : ""}`);
                  setLastSelfCheck(r.selfCheck ?? null);
                  void refresh();
                }}
                onDiscarded={() => setProposal(null)}
              />
            </section>

            {!isEmpty && (
              <section className="scan-block">
                <div className="blk-head">
                  <h4>体检</h4>
                  <span className="blk-n">
                    {counts.error + counts.warn > 0
                      ? `${counts.error + counts.warn} 处`
                      : "无问题"}
                  </span>
                  <span className="blk-meta">{scan?.cells.toLocaleString() ?? 0} 格 · {scan?.elapsed ?? "—"}</span>
                </div>
                <ScanList
                  issues={sortedIssues(issues, weights)}
                  onOpen={(it) => setOpened({ sheet: it.sheet, file: it.file, ref: it.ref })}
                />
              </section>
            )}
          </div>
        </section>
      </div>

      <footer className="dash-ledger">
        <button className="dl-h" onClick={() => setLedgerOpen(true)}>
          <IconNote size={13} />账目
          <span className="dl-open">查看 / 回滚</span>
        </button>
        {ledger.length === 0 ? (
          <span className="dash-muted">还没有改动记录</span>
        ) : (
          ledger.map((e, i) => (
            <span key={`${e.ts}-${e.cell}-${e.old}-${e.new}`} className={`dl-row${i === 0 ? " fresh" : ""}`}>
              <em>{e.ts}</em> {e.table} {e.cell} {e.old}→{e.new}
              <span className={e.status === "ok" ? "ok" : "rj"}>{e.status}</span>
            </span>
          ))
        )}
      </footer>

      {ledgerOpen && (
        <LedgerPanel onClose={() => setLedgerOpen(false)} onChanged={() => void refresh()} />
      )}
      {tasksOpen && <TasksPanel onClose={() => setTasksOpen(false)} />}
      {rulesOpen && <RulesPanel onClose={() => setRulesOpen(false)} />}
        </>
      )}

      {zoomed && graph && (
        <GraphOverlay graph={graph} active={active} hot={hot} onPick={(n) => void pickNode(n)} onClose={() => setZoomed(false)} />
      )}
    </div>
  );
}

/* ---------- 仪表读数 ---------- */
function Readout({ label, value, tone }: { label: string; value: string; tone?: "act" | "warn" }) {
  // 读数变了就闪一下：这是用户最常盯的三个数，变化必须"演"出来而不是悄悄变
  const changed = useChanged(value);
  return (
    <span className={`readout${tone ? " " + tone : ""}`}>
      <span className="ro-label">{label}</span>
      <span className={`ro-value${changed ? " value-changed" : ""}`}>{value}</span>
    </span>
  );
}

/* ---------- 体检结果（按种类分组，别让 196 条同类淹掉 45 条真问题） ---------- */

/* 后端 kind 是下划线风格（bad_value / mismatch …），标签要与之对应 */
const KIND_LABEL: Record<string, string> = {
  mismatch: "账对不上（上月欠款 ≠ 本月上期）",
  bad_ref: "公式引用已失效（#REF!）",
  bad_value: "单元格是错误值",
  sheet_missing: "月表缺失（跨月核对已跳过）",
  sheet_unfit: "表缺关键列",
  scan_error: "该表扫描出错",
};

function ScanList({ issues, onOpen }: {
  issues: ScanIssue[];
  onOpen: (it: ScanIssue) => void;
}) {
  // 按 kind 归类，**账对不上（mismatch）排最前**——它才是需要人判断的，
  // 196 条同质的 #REF! 收成一行，不让它淹没有价值的信息。
  // 账对不上放最前：它需要人判断；196 条同质的 #REF! 收成一行，不淹没它
  const order = ["mismatch", "sheet_missing", "sheet_unfit", "bad_ref", "bad_value", "scan_error"];
  const groups = new Map<string, ScanIssue[]>();
  for (const it of issues) {
    if (!groups.has(it.kind)) groups.set(it.kind, []);
    groups.get(it.kind)!.push(it);
  }
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const sorted = [...groups.entries()].sort(
    (a, b) => (order.indexOf(a[0]) + 1 || 99) - (order.indexOf(b[0]) + 1 || 99),
  );

  if (issues.length === 0) return <p className="dash-muted">本次未发现问题</p>;

  return (
    <ul className="scan-groups">
      {sorted.map(([kind, items]) => {
        // 账对不上默认摊开，但**限量**——71 行全开会把页面拉成一条长卷
        // 账对不上默认摊开，但**限量**——71 行全开会把页面拉成一条长卷。
        // 显式展开过（open[kind]===true）才给全部。
        const explicit = open[kind] === true;
        const isOpen = open[kind] ?? kind === "mismatch";
        const cap = explicit ? items.length : kind === "mismatch" ? 8 : 40;
        const sev = items[0].severity;
        return (
          <li key={kind} className={`scan-grp ${sev}`}>
            <button className="sg-head" onClick={() => setOpen((o) => ({ ...o, [kind]: !isOpen }))}>
              <span className="sg-arrow">{isOpen ? "▾" : "▸"}</span>
              <span className="sg-label">{KIND_LABEL[kind] ?? kind}</span>
              <span className="sg-n">{items.length} 处</span>
            </button>
            {isOpen && (
              <ul className="scan-list">
                {items.slice(0, cap).map((it, i) => (
                  <li key={i} className={`scan-item ${it.severity}`}>
                    <div className="si-main">
                      <button
                        className="si-ref si-ref-btn"
                        onClick={() => onOpen(it)}
                        title="打开表格并定位到该格"
                      >
                        {shortName(it.sheet)}!{it.ref}
                      </button>
                      <span className="si-msg">{it.message}</span>
                    </div>
                  </li>
                ))}
                {items.length > cap && (
                  <li className="scan-more-row">
                    <button className="chain-more" onClick={() => setOpen((o) => ({ ...o, [kind]: true }))}>
                      还有 {items.length - cap} 处，展开全部
                    </button>
                  </li>
                )}
              </ul>
            )}
          </li>
        );
      })}
    </ul>
  );
}

/* ---------- 联动链（按文件分组 → 同系列折叠） ---------- */


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

  if (!pv) return <div className="sk-group nd-stats-sk"><SkPanel rows={3} /></div>;

  // 空表（无数据行）时 summaries/sample/header 可能是 null，必须兜底
  const sums = pv.summaries ?? [];
  const header = pv.header ?? [];
  const overview = sums.find((s) => s.label && s.values.length > 2) ?? sums[0];
  const ovValues = (overview?.values ?? []).filter((v) => v !== overview?.label);
  return (
    <div className="nd-stats">
      {/* 规模：三个等宽读数横排——仪器铭牌的语汇（小标注 + 大数字） */}
      <div className="nd-metrics">
        <Metric label="行" value={pv.rows.toLocaleString()} />
        <Metric label="列" value={String(pv.cols)} />
        <Metric label="公式" value={pv.formulas.toLocaleString()} />
      </div>
      {header.filter(Boolean).length > 0 && (
        <p className="nd-cols">
          <span className="nd-k">列</span>
          {header.filter(Boolean).slice(0, 6).join(" / ")}
          {header.filter(Boolean).length > 6 ? " …" : ""}
        </p>
      )}
      {ovValues.length > 0 && (
        <div className="nd-sum">
          <span className="nd-k">{overview?.label || "概览"}</span>
          <div className="ns-sum">
            {ovValues.slice(0, 8).map((v, i) => (
              <span key={i} className="ns-chip">{v}</span>
            ))}
          </div>
        </div>
      )}
      {pv.note && <div className="nd-note">{pv.note}</div>}
    </div>
  );
}

/** 一个仪器读数：小标注在上，等宽大数字在下。 */
function Metric({ label, value }: { label: string; value: string }) {
  return (
    <span className="nd-metric">
      <span className="nd-m-label">{label}</span>
      <span className="nd-m-value">{value}</span>
    </span>
  );
}

/* ---------- 工作区切换器 ---------- */

function WorkspaceSwitcher({ onPick, onOpenSettings }: { onPick: () => void; onOpenSettings: () => void }) {
  const [items, setItems] = useState<WorkspaceListItem[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    agentApi.workspaces()
      .then((r) => setItems(r.items))
      .catch(() => setItems([]));
  }, []);

  const pick = async (dir: string) => {
    setBusy(true);
    try { await agentApi.openWorkspace(dir); onPick(); } finally { setBusy(false); }
  };

  return (
    <>
      <div className="ws-backdrop" onClick={onOpenSettings} />
      <div className="ws-menu" onClick={(e) => e.stopPropagation()}>
        {items.length === 0 && <div className="ws-empty">还没有其他工作区</div>}
        {items.map((w) => (
          <button key={w.path} className={`ws-item${w.current ? " on" : ""}`}
            disabled={busy || w.current} onClick={() => void pick(w.path)}>
            <IconFolder size={13} />
            <span className="ws-n">{w.name}</span>
            <span className="ws-m">{w.tables} 表</span>
            {w.current && <IconCheck size={13} />}
          </button>
        ))}
        <button className="ws-item ws-manage" onClick={onOpenSettings}>
          <IconGear size={13} />管理工作区…
        </button>
      </div>
    </>
  );
}

/* ---------- 空地盘 ---------- */

/** 首次运行：还没选过工作区。三件事说清 + 两个入口。 */
function FirstRun({ root, pickFolder, onDone }: {
  root: string;
  pickFolder?: () => Promise<string | null>;
  onDone: () => void;
}) {
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [browsing, setBrowsing] = useState(false);

  const openDir = async (dir: string) => {
    setBusy(true);
    setErr("");
    try {
      await agentApi.openWorkspace(dir);
      onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  // 选文件夹：桌面壳用系统对话框；单文件版（浏览器里）用服务端列目录浏览。
  const choose = async () => {
    if (pickFolder) {
      const dir = await pickFolder();
      if (dir) await openDir(dir);
      return;
    }
    setBrowsing(true);
  };

  const create = async () => {
    if (!name.trim()) return;
    setBusy(true);
    setErr("");
    try {
      await agentApi.createWorkspace(name.trim());
      onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="first-run">
      <h2>先选一个工作区</h2>
      <p className="fr-sub">
        工作区就是一个放表的文件夹。<b>你的表一直在你自己的机器上</b>，
        选好之后它会盯着这个文件夹干活。
      </p>

      <div className="fr-ways">
        <div className="fr-way">
          <b>打开已有的文件夹</b>
          <span>表已经在某个文件夹里了，直接指过去。</span>
          <button className="btn primary" onClick={() => void choose()} disabled={busy}>
            选择文件夹…
          </button>
          <div className="fr-manual">
            <input value={path} placeholder="或直接填路径，如 D:\台账"
              onChange={(e) => setPath(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && path.trim()) void openDir(path.trim()); }} />
            <button className="btn ghost sm" onClick={() => void openDir(path.trim())}
              disabled={busy || !path.trim()}>打开</button>
          </div>
        </div>

        <div className="fr-way">
          <b>新建一个工作区</b>
          <span>还没有文件夹？建一个新的，再把表放进去。</span>
          <div className="fr-manual">
            <input value={name} placeholder="工作区名称，如 御龙湾台账"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") void create(); }} />
            <button className="btn ghost sm" onClick={() => void create()}
              disabled={busy || !name.trim()}>新建</button>
          </div>
        </div>
      </div>

      {err && <p className="fr-err">{err}</p>}
      <p className="fr-hint">
        当前临时目录（还没选）：<code>{root}</code>
      </p>

      {browsing && (
        <FolderPicker
          onCancel={() => setBrowsing(false)}
          onPick={(dir) => { setBrowsing(false); void openDir(dir); }}
        />
      )}
    </div>
  );
}

/** 服务端目录浏览：浏览器里也能"选文件夹"（列表来自本机服务）。 */
function FolderPicker({ onPick, onCancel }: {
  onPick: (dir: string) => void;
  onCancel: () => void;
}) {
  const [dir, setDir] = useState("");
  const [parent, setParent] = useState("");
  const [entries, setEntries] = useState<{ name: string; path: string }[]>([]);
  const [drives, setDrives] = useState<string[]>([]);
  const [tables, setTables] = useState(0);
  const [err, setErr] = useState("");

  const load = async (d?: string) => {
    setErr("");
    try {
      const r = await agentApi.fsList(d);
      setDir(r.dir);
      setParent(r.parent);
      setEntries(r.entries ?? []);
      setDrives(r.drives ?? []);
      setTables(r.tables);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  useEffect(() => { void load(); }, []);

  return (
    <div className="fp-overlay" onClick={onCancel}>
      <div className="fp-panel" onClick={(e) => e.stopPropagation()}>
        <header className="fp-head">
          <b>选择工作区文件夹</b>
          <button className="btn ghost sm" onClick={onCancel}>取消</button>
        </header>
        <div className="fp-path">
          <button className="btn ghost sm" onClick={() => parent && void load(parent)}
            disabled={!parent}>↑ 上级</button>
          <code>{dir}</code>
          {tables > 0 && <span className="fp-tables">此目录含 {tables} 个 xlsx</span>}
        </div>
        {drives.length > 0 && (
          <div className="fp-drives">
            {drives.map((d) => (
              <button key={d} className="pill ghost" onClick={() => void load(d)}>{d}</button>
            ))}
          </div>
        )}
        <ul className="fp-list">
          {entries.map((e) => (
            <li key={e.path}>
              <button className="fp-row" onDoubleClick={() => void load(e.path)}
                onClick={() => void load(e.path)}>
                <IconFolder size={13} />{e.name}
              </button>
            </li>
          ))}
          {entries.length === 0 && <li className="fp-empty">这个目录下没有子文件夹</li>}
        </ul>
        {err && <p className="fr-err">{err}</p>}
        <footer className="fp-foot">
          <span className="fp-hint">进到装表的目录，然后点"就用这个文件夹"</span>
          <button className="btn primary" onClick={() => onPick(dir)} disabled={!dir}>
            就用这个文件夹
          </button>
        </footer>
      </div>
    </div>
  );
}

function EmptyWorkspace({ root, onRefresh, onNew }: {
  root: string; onRefresh: () => void; onNew: () => void;
}) {
  return (
    <div className="dash-empty">
      <h2>这个工作区还是空的</h2>
      <p>工作区就是一个文件夹，表放在里面。两种做法：</p>
      <div className="empty-ways">
        <div className="empty-way">
          <b>放表格进来</b>
          <span>把 xlsx 拖进下面这个文件夹，然后点「重新读取」。</span>
        </div>
        <div className="empty-way">
          <b>换个工作区</b>
          <span>左上角点工作区名可切换；「设置」里能新建或打开别的文件夹。</span>
        </div>
      </div>
      <p className="dash-hint">当前目录：<code>{root || "（未设置）"}</code></p>
      <div className="empty-actions">
        <button className="btn primary" onClick={onRefresh}><IconRefresh size={14} />重新读取</button>
        <button className="btn ghost" onClick={onNew}><IconGear size={14} />打开设置</button>
      </div>
    </div>
  );
}

/* ---------- 大图 ---------- */

function GraphOverlay({ graph, active, hot, onPick, onClose }: {
  graph: GraphData; active: GraphNode | null; hot: Set<string>;
  onPick: (n: GraphNode) => void; onClose: () => void;
}) {
  // 连线模式（见 docs/agent-architecture/24 第五节）：
  // 用户在图上来回拨动 → 产生一条关系 → 存进关系记忆 → 权重随之修正。
  // **这比打字纠正更直觉**：所见即所得。
  const [linking, setLinking] = useState(false);
  const [linkKind, setLinkKind] = useState("");
  const [linkFrom, setLinkFrom] = useState<string>("");
  const [linkMsg, setLinkMsg] = useState("");

  const handleNodeClick = async (n: GraphNode) => {
    if (!linking) { onPick(n); return; }
    if (!linkFrom) { setLinkFrom(nodeId(n)); setLinkMsg("再点一张表，表示它们有关联"); return; }
    const target = nodeId(n);
    if (target === linkFrom) { setLinkMsg("不能连自己"); return; }
    if (!linkKind.trim()) { setLinkMsg("先填类别（如 收租）——它决定下次什么时候用上"); return; }
    try {
      // 存成"这类改动还涉及该表"的关系（源表视为该类改动的入口）
      const fromSheet = linkFrom.indexOf("!") >= 0 ? linkFrom.slice(linkFrom.indexOf("!") + 1) : linkFrom;
      const toSheet = n.sheet;
      await agentApi.learnRelation(linkKind.trim(), [fromSheet, toSheet], "用户在图上手动画的关联");
      setLinkMsg(`已记住：「${linkKind.trim()}」涉及 ${fromSheet}、${toSheet}`);
      setLinkFrom("");
    } catch (e) {
      setLinkMsg(e instanceof Error ? e.message : String(e));
    }
  };
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
            <FileGroup key={grp.file} grp={grp} active={active} hot={hot} onPick={onPick}
              linkFrom={linkFrom} onNodeClick={(n) => void handleNodeClick(n)} />
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

          {/* 手动画关联（把用户的判断直接变成记忆） */}
          <div className="gv-link">
            <button className={`btn ${linking ? "primary" : "ghost"} sm`}
              onClick={() => { setLinking((v) => !v); setLinkFrom(""); setLinkMsg(""); }}>
              <IconLink size={13} />{linking ? "退出连线" : "手动连关联"}
            </button>
            {linking && (
              <>
                <input className="gv-link-kind" value={linkKind} placeholder="类别（收租/售房…）"
                  onChange={(e) => setLinkKind(e.target.value)} />
                <span className="gv-link-msg">
                  {linkMsg || (linkFrom ? "再点一张表" : "点第一张表开始")}
                </span>
              </>
            )}
          </div>

          {isolated.length > 0 && (
            <div className="gv-isolated">
              <div className="gv-edges-h">
                暂无关联 <span className="dash-muted">（先留白 · 等权重出来再看是否连上）</span>
              </div>
              {isolatedGroups.map((grp) => (
                <FileGroup key={grp.file} grp={grp} active={active} hot={hot} onPick={onPick} muted
                  linkFrom={linkFrom} onNodeClick={(n) => void handleNodeClick(n)} />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function FileGroup({ grp, active, hot, onPick, muted, linkFrom, onNodeClick }: {
  grp: Group; active: GraphNode | null; hot: Set<string>;
  onPick: (n: GraphNode) => void; muted?: boolean;
  /** 连线模式下：已选中的起点（高亮用） */
  linkFrom?: string;
  /** 连线模式下的点击处理（非连线模式由调用方传 onPick） */
  onNodeClick?: (n: GraphNode) => void;
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
              className={`gv-node${isHot ? " hot" : ""}${isActive ? " on" : ""}${linkFrom === id ? " linking" : ""}`}
              onClick={() => { if (onNodeClick) onNodeClick(n); else onPick(n); }}
              title={`${n.file}!${n.sheet}`}>
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

/** 把给定节点按文件分组（用于"有关联 / 留白"分区渲染）。
    传了 weights 时，**组内按注意力权重降序**——重要的表排前面，否则权重看不见。 */
function groupNodes(g: GraphData, nodes: GraphNode[], limit = 0, weights?: Record<string, WeightScore>): Group[] {
  const refs: Record<string, number> = {};
  for (const e of g.edges) refs[nodeId(e.to)] = (refs[nodeId(e.to)] ?? 0) + e.count;
  const byFile = new Map<string, GraphNode[]>();
  for (const n of nodes) {
    const key = n.file || "（外部文件）";
    if (!byFile.has(key)) byFile.set(key, []);
    byFile.get(key)!.push(n);
  }
  const attn = (n: GraphNode) => (weights ? (weights[nodeId(n)]?.attention ?? 0) : 0);
  return [...byFile.entries()]
    .sort((a, b) => b[1].length - a[1].length)
    .map(([file, items]) => {
      const sorted = weights ? [...items].sort((x, y) => attn(y) - attn(x)) : items;
      return { file, items: limit > 0 ? sorted.slice(0, limit) : sorted, refs };
    });
}

function shortName(name: string): string {
  return name.replace(/\s*（[日月末]）\s*/g, "").replace(/\s+/g, "").trim();
}

function shortId(id: string): string {
  const i = id.indexOf("!");
  return i >= 0 ? shortName(id.slice(i + 1)) : shortName(id);
}
/** 体检条目按"所在表的注意力权重"排序：**高权重区的问题先看**（见 22-权重设计.md）。
    这实现用户要的"跑定时的时候根据权重着重校验"——界面层先做，定时层后续接同一套排序。 */
function sortedIssues(issues: ScanIssue[], weights: Record<string, WeightScore>): ScanIssue[] {
  const wOf = (sheet: string): number => {
    // 体检条目只带 sheet 名（不含文件），按 sheet 匹配即可
    for (const [id, w] of Object.entries(weights)) {
      const i = id.indexOf("!");
      const s = i >= 0 ? id.slice(i + 1) : id;
      if (s === sheet) return w.attention;
    }
    return 0;
  };
  return [...issues].sort((a, b) => {
    const d = wOf(b.sheet) - wOf(a.sheet);
    if (d !== 0) return d;
    // 同权重时：错误优先于警告
    if (a.severity !== b.severity) return a.severity === "error" ? -1 : 1;
    return 0;
  });
}
