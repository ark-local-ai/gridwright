import { useEffect, useCallback, useState } from "react";
import "./dashboard.css";
import { agentApi, nodeId } from "../api-agent";
import type { ScanReport, GraphData, GraphNode, LedgerEntry, WorkspaceFiles, SheetPreview, WorkspaceListItem, Proposal, SafetyReport, SelfCheckReport } from "../api-agent";
import { IconRefresh, IconCheck, IconXls, IconNote, IconChevD, IconGear, IconFolder, IconLink, IconX, IconSpark, IconClock } from "../components/icons";
import AttentionList from "../components/AttentionList";
import DropZone from "../components/DropZone";
import LinkMap from "../components/LinkMap";
import SheetView from "./SheetView";
import { SkPanel } from "../components/Skeleton";
import Settings from "./Settings2";
import LedgerPanel from "./LedgerPanel";
import TasksPanel from "./TasksPanel";
import RulesPanel from "./RulesPanel";
import GeneratePanel from "./GeneratePanel";
import { PendingList, ChatPane } from "./Pending";
import { hotspots } from "../lib/issues";
import { linkHealth } from "../lib/linkage";

/* 数据管家 · 主看板（见 docs/agent-architecture/14-第一屏设计.md、17-工作区与跨文件联动.md）
   一屏答一个问题：「我的表，有没有事？它要动什么？」
   工作区 = 一块地盘，可以含多个 xlsx；跨文件关联是真的（实测有 [n] 外部引用）。 */

type Load = "idle" | "loading" | "ready" | "error";

export default function Dashboard({ pickFolder, watchDrop }: {
  pickFolder?: () => Promise<string | null>;
  /** 桌面壳注入的原生拖拽监听（浏览器里没有，退回 HTML5 drag-drop） */
  watchDrop?: (h: { enter: () => void; leave: () => void; drop: (paths: string[]) => void }) => Promise<() => void>;
} = {}) {
  const [load, setLoad] = useState<Load>("loading");
  const [err, setErr] = useState("");
  const [wsName, setWsName] = useState("");
  const [wsPath, setWsPath] = useState("");
  const [chosen, setChosen] = useState(true); // 是否显式选过工作区
  const [files, setFiles] = useState<WorkspaceFiles | null>(null);
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [scan, setScan] = useState<ScanReport | null>(null);
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [active, setActive] = useState<GraphNode | null>(null);
  const [chatOpen, setChatOpen] = useState(false);
  // 对话抽屉宽度：起始读上次拖到的值（不写死），拖动时实时更新。
  // 记在本地而不是工作区里——"我喜欢多宽"是使用这台机器的人的习惯，
  // 跟哪份台账无关。
  const [chatW, setChatW] = useState<number>(() => {
    const v = Number(localStorage.getItem("gw:chat-width"));
    return Number.isFinite(v) && v >= 360 ? v : 460;
  });
  const [activeResize, setActiveResize] = useState(false);
  const [ledgerOpen, setLedgerOpen] = useState(false);
  const [tasksOpen, setTasksOpen] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [generateOpen, setGenerateOpen] = useState(false);
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
  const [partErr, setPartErr] = useState<Record<string, string>>({});
  const [lastSelfCheck, setLastSelfCheck] = useState<SelfCheckReport | null>(null);

  const refresh = useCallback(async () => {
    setLoad("loading");
    setErr("");
    setPartErr({});
    try {
      await agentApi.workspace().then((ws) => {
        // 工作区名取目录名（工作区是容器，可以含多个表）
        const seg = ws.root.replace(/\\/g, "/").split("/").filter(Boolean);
        setWsName(seg[seg.length - 1] ?? "工作区");
        setWsPath(ws.root);
        setBrainReady(ws.brainReady);
        setChosen(ws.workspaceChosen);
      });
      // 每项各自降级：一项挂了不影响其余照常显示，但挂过这件事必须留下来。
      const failed = (part: string) => (e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        setPartErr((p) => ({ ...p, [part]: msg }));
        return null;
      };
      const [f, g, s, l] = await Promise.all([
        agentApi.files().catch(failed("文件清单")),
        agentApi.graph().catch(failed("表间关联")),
        agentApi.scanRun().catch(failed("体检")),
        agentApi.ledger(5).catch(failed("账目")),
      ]);
      setFiles(f);
      setGraph(g);
      setScan(s?.report ?? null);
      setLedger(l?.entries ?? []);
      // 安全报告：告诉用户"这张表能不能写"（含宏=硬拒绝）。确认改动前必须知道。
      // 读不到时不能静静留空——那会显示成“没有风险”，而实际是“没查成”。
      agentApi.safety()
        .then((r) => setSafetyRep(r.report))
        .catch(failed("安全检查"));
      // 默认选中"被引用最多"的那个节点（最能说明这张表的影响力）
      setActive(g?.nodes?.length ? pickHub(g) : null);
      setLoad("ready");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setLoad("error");
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  // ---------- 对话抽屉：拖左缘改宽 ----------
  //
  // 用 pointer 事件 + setPointerCapture：鼠标拖出窗口（拖得快时很正常）
  // 也不会丢事件。pointermove/up 挂在 window 上：pointerup 的目标是**被捕获的
  // 元素**（把手本身），若只挂在遮罩上，松手那一刻根本轮不到它，
  // 宽度就永远存不下来（实测踩到过）。
  // 宽度上限取窗口的 88%：既留出看板的余量，也不至于拖到全屏失去"抽屉"的意思。
  const startResize = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault();
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    setActiveResize(true);
  };
  useEffect(() => {
    if (!activeResize) return;
    const move = (e: PointerEvent) => {
      const max = Math.round(window.innerWidth * 0.88);
      // 抽屉贴右边：宽度 = 窗口右缘 - 指针位置
      const w = Math.round(window.innerWidth - e.clientX);
      setChatW(Math.max(360, Math.min(max, w)));
    };
    const up = () => {
      setActiveResize(false);
      // 松手才落盘：拖动过程中每帧都写 localStorage 是没必要的 IO
      setChatW((w) => {
        try { localStorage.setItem("gw:chat-width", String(w)); } catch { /* 存不下只影响下次打开 */ }
        return w;
      });
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
  }, [activeResize]);

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
  // 主角数字只数**账目对不上**（mismatch）。以前数的是全部问题（含 #REF! 一类
  // 表里本来就坏掉的格子），于是没有对不上也会报"2 处待核对"，而且下面那句
  // "N 个铺位对不上"会把无铺位的错误值算成"（未标注）"——**数字与说法都对不上**。
  // 用户要看的正是"改完之后数据对不对得上"，所以这里只认 mismatch。
  const mismatches = issues.filter((i) => i.kind === "mismatch");
  const totalIssues = mismatches.length;
  const spots = hotspots(mismatches);
  const lh = graph ? linkHealth(graph) : null;
  // 读取失败的板块名（有内容才渲染那条警示带）
  const partNames = Object.keys(partErr);

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
          {!brainReady && <span className="dash-offline" title="未配置模型：查看与体检可用；修改表格前需在设置中配置模型">未配模型</span>}
          {switcherOpen && (
            <WorkspaceSwitcher
              onPick={() => { setSwitcherOpen(false); void refresh(); }}
              onOpenSettings={() => { setSwitcherOpen(false); setSettingsOpen(true); }}
              onClose={() => setSwitcherOpen(false)}
            />
          )}
        </div>

        {/* 顶栏不再放读数：三个并列数字（表/待办/问题）各说各的，
            读者不知道该看哪个——这正是"乱"的一部分。
            "现在怎么样"由下面那个**唯一的主角**回答。 */}
        <div className="dash-actions">
          {appliedNote && <span className="dash-applied"><IconCheck size={12} />{appliedNote}</span>}
          {lastSelfCheck && lastSelfCheck.level !== "ok" && (
            <span className={`dash-self ${lastSelfCheck.level}`} title={lastSelfCheck.summary}>
              自检 {lastSelfCheck.findings.length} 项待看
            </span>
          )}
          {/* 也不放"联动图 / 体检"：联动图已收进下方折叠块，
              体检就在主角那一行（有事没事该在那儿说），放这里只会重复。 */}
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
            {!isFirstRun && (
              <button className="mini-btn" onClick={() => setGenerateOpen(true)} title="生成清单 / 文件（不动原表）" aria-label="生成">
                <IconXls size={15} />
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

      {/* 读取失败必须显形。失败的请求和“真的没有”在界面上一模一样，
          所以这里只说自己知道的事：哪一项没读到、为什么、
          以及下面那些空白可信不可信。 */}
      {partNames.length > 0 && (
        <div className="dash-degraded" role="alert">
          <IconX size={13} />
          <span className="dd-text">
            <b>{partNames.length} 项没读到</b>
            {partNames.map((k) => (
              <span key={k} className="dd-item">{k}<em>{partErr[k]}</em></span>
            ))}
            <span className="dd-warn">下面的空白可能是读取失败，不一定是真的没有。</span>
          </span>
          <button className="btn ghost sm" onClick={() => void refresh()}>
            <IconRefresh size={12} />重试
          </button>
        </div>
      )}

      {/* 对话：配置手段（说一句 → 出提案），不是常驻主界面。
          宽度可拖（左缘那道把手），并记住用户拖到的宽度——每个人读长句子
          舒服的宽度不一样，固定 460px 对写长指令的人是折磨。 */}
      {chatOpen && (
        <>
          {activeResize && <div className="chat-resize-veil" />}
          <div className="chat-drawer" style={{ width: chatW ? `${chatW}px` : undefined }}>
            <div className="cd-resize" role="separator" aria-orientation="vertical"
              aria-label="拖动调整对话宽度"
              onPointerDown={startResize} />
            <header className="cd-head">
              <IconSpark size={14} />
              <b>对话</b>
              <span className="cd-hint">输入指令即可安排工作；系统仅提供建议，修改表格需经确认</span>
              <button className="cd-x" onClick={() => setChatOpen(false)} aria-label="关闭对话"><IconX size={14} /></button>
            </header>
            <div className="cd-body">
              <ChatPane wsKey={wsPath} onPlanReady={(p) => { setProposal(p); setChatOpen(false); }} />
            </div>
          </div>
        </>
      )}

      {settingsOpen && (
        <Settings
          // 关闭时也刷新：配置模型后后端已 ready，但前端内存里还是旧的 false，
          // 于是顶栏一直挂着"未配模型"——用户会以为配了没生效。
          // 不是"只在工作区变了才刷"，因为模型配置不改变工作区。
          onClose={() => { setSettingsOpen(false); void refresh(); }}
        />
      )}

      {/* 空地盘：只有这一块，但顶栏在，能换工作区。
          "还没选过工作区"和"选了一个空的"都该能**拖**——后者以前只给一句
          "把 xlsx 拖进下面这个文件夹"，却没有任何地方接得住拖拽，等于把人
          打发去用资源管理器。两种空态共用同一个 DropZone：
          拖文件夹=把它当工作区，拖表=复制进当前工作区。 */}
      {isFirstRun ? (
        <div className="dash-body dash-body-empty">
          <div className="dash-col">
            <DropZone pickFolder={pickFolder} watchDrop={watchDrop} onDone={() => void refresh()} />
          </div>
        </div>
      ) : isEmpty ? (
        <div className="dash-body dash-body-empty">
          <div className="dash-col">
            <EmptyHeader root={wsPath} />
            <DropZone pickFolder={pickFolder} watchDrop={watchDrop} onDone={() => void refresh()} />
          </div>
        </div>
      ) : (
        <>

      <div className="dash-body">
        <div className="dash-col">
          {/* ① 主角：一句话说清"现在怎么样"。
              这是整页唯一可以大的东西。以前顶栏三个并列读数（表/待办/问题）
              + 左栏"该查什么" + 右栏"体检"各说一遍，读者不知道看哪。 */}
          <section className="dh">
            <span className={`dh-num${totalIssues === 0 ? " calm" : ""}`}>{totalIssues}</span>
            <div className="dh-copy">
              <span className="dh-lead">
                {totalIssues === 0 ? "账目都对得上" : "处待核对"}
              </span>
              {/* 一行之内"表"只指一种东西：工作簿说"个"，工作表说"张"，
                  两边不再打架（曾出现"1 张表 · 15 张表已断开"的自相矛盾）。 */}
              <span className="dh-meta">
                {tableCount > 0 && `${tableCount} 个工作簿${graph && graph.nodes.length > 0 ? ` · ${graph.nodes.length} 张工作表` : ""}`}
                {spots.length > 0 && ` · ${spots.length} 个铺位对不上`}
              </span>
            </div>
            <div className="dh-act">
              {!isEmpty && (
                <button className="btn ghost sm" onClick={() => void refresh()}>
                  <IconRefresh size={13} />重新体检
                </button>
              )}
            </div>
          </section>

          {/* ② 明细：这就是页面主体。按铺位聚合——同一批 71 处账不平，
              以前左栏聚一遍、右栏"体检"又原样列一遍，那才是"乱"的真因。 */}
          {issues.length > 0 && (
            <section className="dsec">
              <AttentionList issues={issues} onOpen={(it) => setOpened({ sheet: it.sheet, file: it.file, ref: it.ref })} />
            </section>
          )}

          {/* ③ 需要你处理：待确认的改动（要动手的只有这一处） */}
          {proposal && proposal.items.length > 0 && (
            <section className="dsec act-block has-work">
              <div className="blk-head">
                <h4>需要你确认</h4>
                <span className="blk-n act">{proposal.items.length} 处待确认</span>
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
          )}

          {/* ④ 收起来的：这些都是"偶尔去一趟"的，不该和主角抢屏。
              表的连接压成一行摘要（原来是 7 层文字堆在左栏）。 */}
          <section className="dsec dsec-quiet">
            {graph && (
              <Collapsed
                label="表的连接"
                summary={lh ? `${lh.connected} 连着 · ${lh.isolated} 断开` : "—"}
                tone={lh && lh.isolated > 0 ? "warn" : "ok"}
              >
                <LinkMap
                  graph={graph}
                  issues={issues}
                  onOpenSheet={(n) => void pickNode(n)}
                  onOpenIssue={(it) => setOpened({ sheet: it.sheet, file: it.file, ref: it.ref })}
                />
              </Collapsed>
            )}
            {active && graph && (
              <Collapsed label="当前这张表" summary={active.sheet}>
                <div className="node-detail">
                  <div className="nd-plate">
                    <div className="nd-plate-h">
                      <h2 className="nd-name">{active.sheet}</h2>
                      <button className="btn primary sm nd-open"
                        onClick={() => setOpened({ sheet: active.sheet, file: active.file })}>
                        打开表格
                      </button>
                    </div>
                    <p className="nd-file"><IconXls size={12} />{active.file || "外部文件"}</p>
                  </div>
                  <NodeStats node={active} />
                </div>
              </Collapsed>
            )}
            <Collapsed
              label="账目"
              summary={ledger.length === 0 ? "还没有改动记录" : `最近 ${ledger.length} 条`}
            >
              <div className="ledger-inline">
                {ledger.length === 0 ? <p className="dash-muted">还没有改动记录</p> : ledger.map((e, i) => (
                  <span key={`${e.ts}-${e.cell}-${e.old}-${e.new}`} className={`dl-row${i === 0 ? " fresh" : ""}`}>
                    <em>{e.ts}</em> {e.table} {e.cell} {e.old}→{e.new}
                    <span className={e.status === "ok" ? "ok" : "rj"}>{e.status}</span>
                  </span>
                ))}
              </div>
              <button className="btn ghost sm" onClick={() => setLedgerOpen(true)}>查看 / 回滚</button>
            </Collapsed>
          </section>
        </div>
      </div>

      {ledgerOpen && (
        <LedgerPanel onClose={() => setLedgerOpen(false)} onChanged={() => void refresh()} />
      )}
      {tasksOpen && <TasksPanel onClose={() => setTasksOpen(false)} />}
      {rulesOpen && <RulesPanel onClose={() => setRulesOpen(false)} />}
      {generateOpen && <GeneratePanel onClose={() => setGenerateOpen(false)} />}
        </>
      )}

      {zoomed && graph && (
        <GraphOverlay graph={graph} active={active} hot={hot} onPick={(n) => void pickNode(n)} onClose={() => setZoomed(false)} />
      )}
    </div>
  );
}

/**
 * Collapsed —— 一行摘要 + 点开才展开。
 *
 * 为什么要有它：这些内容（表连接、当前表、账目）都**不该和主角抢屏**。
 * 它们原来各自带标题、说明、统计，平铺在一屏里，读者找不到重点。
 * 收成一行后：想知道细节的人点开，不关心的人一眼扫过。
 *
 * 摘要必须在**收起状态**就说清结论（"5 连着 · 9 断开"），否则收起等于藏起来。
 */
function Collapsed({ label, summary, tone, children }: {
  label: string; summary: string; tone?: "ok" | "warn"; children: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className={`col${open ? " open" : ""}`}>
      <button className="col-h" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <span className={`cr-caret${open ? " open" : ""}`}>›</span>
        <span className="col-label">{label}</span>
        <span className={`col-sum${tone ? " " + tone : ""}`}>{summary}</span>
      </button>
      {open && <div className="col-body">{children}</div>}
    </div>
  );
}

/* ---------- 体检结果（按种类分组，别让 196 条同类淹掉 45 条真问题） ---------- */



/* ---------- 联动链（按文件分组 → 同系列折叠） ---------- */


/* ---------- 节点汇总（用户要的"点模块展示大概汇总数据"） ---------- */

function NodeStats({ node }: { node: GraphNode }) {
  const [pv, setPv] = useState<SheetPreview | null>(null);
  const [pvErr, setPvErr] = useState("");
  useEffect(() => {
    let alive = true;
    setPvErr("");
    agentApi.preview(node.sheet, node.file, 1)
      .then((d) => { if (alive) setPv(d); })
      .catch((e: unknown) => {
        if (!alive) return;
        setPv(null);
        setPvErr(e instanceof Error ? e.message : String(e));
      });
    return () => { alive = false; };
  }, [node.sheet, node.file]);

  // 读不到就明说。此前失败与“还在加载”共用同一个骨架屏，于是骨架会永远转下去：
  // 用户既不知道出了错，也不知道在等什么——比报错还难查。
  if (pvErr) return <div className="nd-note">预览读不到：{pvErr}</div>;
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

function WorkspaceSwitcher({ onPick, onOpenSettings, onClose }: { onPick: () => void; onOpenSettings: () => void; onClose: () => void }) {
  const [items, setItems] = useState<WorkspaceListItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    agentApi.workspaces()
      .then((r) => setItems(r.items))
      .catch((e: unknown) => {
        setItems([]);
        setErr(e instanceof Error ? e.message : String(e));
      });
  }, []);

  const pick = async (dir: string) => {
    setBusy(true);
    setErr("");
    try {
      await agentApi.openWorkspace(dir);
      onPick();
    } catch (e) {
      // 切换失败必须显形并让菜单留着：以前异常被吞掉，菜单不关、也没提示，
      // 用户只看到"点了没反应"。
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {/* 点菜单外面 = 关掉菜单。以前这里接的是 onOpenSettings：
          菜单定位错（见 dashboard.css 的 .dash-ident）时它盖住全屏，
          于是用户一点工作区名就弹出设置页——这是那次 bug 的另一半。 */}
      <div className="ws-backdrop" onClick={onClose} />
      <div className="ws-menu" onClick={(e) => e.stopPropagation()}>
        {err && <div className="ws-empty">读不到工作区列表：{err}</div>}
        {!err && items.length === 0 && <div className="ws-empty">还没有其他工作区</div>}
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


/** 服务端目录浏览：浏览器里也能"选文件夹"（列表来自本机服务）。 */
export function FolderPicker({ onPick, onCancel }: {
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

/* 空工作区（已选过、里面没表）以前是个纯文字页，只叫人"拖进这个文件夹"，
   却没有任何地方接得住拖拽——说的和做的对不上。现在它和首屏共用同一个
   DropZone（拖表=复制进来，拖文件夹=换成那个工作区），只在上面加一行说明。 */
function EmptyHeader({ root }: { root: string }) {
  return (
    <div className="empty-lead">
      <h2>这个工作区还是空的</h2>
      <p>工作区就是一个文件夹。把表拖进来，或者拖一个装着表的文件夹（会直接换过去）。</p>
      <p className="dash-hint">当前目录：<code>{root || "（未设置）"}</code></p>
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

type Group = { file: string; items: GraphNode[]; refs: Record<string, number> };

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

/** 体检条目按"所在表的注意力权重"排序：**高权重区的问题先看**（见 22-权重设计.md）。
    这实现用户要的"跑定时的时候根据权重着重校验"——界面层先做，定时层后续接同一套排序。 */
