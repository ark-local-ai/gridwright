import { useEffect, useRef, useState } from "react";
import "./confirm.css";
import { agentApi } from "../api-agent";
import type { Proposal, ApplyResult, ImpactResult, SafetyReport, SelfCheckReport, ConvoSummary, DistillCandidate, TraceData } from "../api-agent";
import RunTrace from "../components/RunTrace";
import { IconCheck, IconSpark, IconSend, IconNote, IconLink, IconShield, IconRefresh, IconX, IconClock, IconPlus, IconTrash, IconRename, IconAssistant } from "../components/icons";

/* 待确认（A）+ 会话（B）（见 docs/agent-architecture/19-界面设计.md 阶段 3-4）
   用户的规则：看清单 → 你确认 → 才改。会话是配置入口，产出结构化建议。 */

/* ---------- 待确认卡片 ---------- */

export function PendingList({ proposal, onApplied, onDiscarded, impactNode, safety, trace }: {
  proposal: Proposal | null;
  onApplied: (r: { applied: number; rejected: number; results: ApplyResult[]; selfCheck?: SelfCheckReport | null }) => void;
  onDiscarded: () => void;
  /** 改动点（用于推断"这笔还牵连谁"） */
  impactNode?: string;
  /** 写入安全评估 */
  safety?: SafetyReport | null;
  /** 这份清单是怎么算出来的（后端 trace），展开可看每步与模型收发 */
  trace?: TraceData | null;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [impact, setImpact] = useState<ImpactResult | null>(null);
  const [kind, setKind] = useState("");
  const [correcting, setCorrecting] = useState(false);
  const [extra, setExtra] = useState("");
  const [learned, setLearned] = useState("");
  const [selfCheck, setSelfCheck] = useState<SelfCheckReport | null>(null);

  // 推断"这一笔还牵连谁"（见 23-影响面推断）
  useEffect(() => {
    if (!impactNode) { setImpact(null); return; }
    let alive = true;
    agentApi.impact(impactNode, kind || undefined)
      .then((r) => { if (alive) setImpact(r); })
      .catch(() => { if (alive) setImpact(null); });
    return () => { alive = false; };
  }, [impactNode, kind]);

  // 用户纠正："还要看 X 表" → 存成关系记忆，下次自动带上
  const learn = async () => {
    const tables = extra.replace(/[，、]/g, ",").split(",").map((x) => x.trim()).filter(Boolean);
    if (!tables.length || !kind.trim()) return;
    try {
      await agentApi.learnRelation(kind.trim(), tables, "用户指出漏了");
      setLearned("已记住：" + kind.trim() + " 类改动还要看 " + tables.join("、"));
      setExtra("");
      setCorrecting(false);
      // 立刻重算，让用户看到变化
      if (impactNode) setImpact(await agentApi.impact(impactNode, kind.trim()));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  if (!proposal) {
    return (
      <div className="act-empty">
        <IconCheck size={14} />
        <span>没有待确认的改动</span>
        <span className="act-empty-hint">在「对话」中输入指令，系统会生成清单供您确认</span>
      </div>
    );
  }

  const apply = async () => {
    setBusy(true);
    setErr("");
    try {
      const r = await agentApi.apply(proposal.id);
      setSelfCheck(r.selfCheck ?? null);
      onApplied(r);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="pend">
      <div className="pend-head">
        <b>{proposal.summary}</b>
        <span className="pend-target">{proposal.target.split(/[\\/]/).pop()}</span>
      </div>
      <RunTrace trace={trace} label="清单是怎么算出来的" />

      <ul className="pend-items">
        {(proposal.items ?? []).map((it, i) => (
          <li key={i} className="pend-item">
            <div className="pi-main">
              <span className="pi-where">{describeWhere(it)}</span>
              <span className="pi-field">{it.field}</span>
            </div>
            <div className="pi-change">
              {it.op === "append" ? (
                // 新增行：没有旧值可比。必须**明确标出来**——
                // “改了一格”和“多了一行”对账时的后果差很远，不能长得一样。
                <>
                  <span className="pi-op pi-newrow">新增行</span>
                  <span className="pi-new">{fmtValues(it.values)}</span>
                </>
              ) : (
                <>
                  <span className="pi-old">{fmt(it.old)}</span>
                  <span className="pi-arrow">→</span>
                  <span className="pi-new">{fmt(it.new)}</span>
                  {it.op === "add" && <span className="pi-op">累加</span>}
                </>
              )}
            </div>
            <div className="pi-meta">
              <span className="pi-ref">{it.sheet}!{it.ref}</span>
              {it.reason && <span className="pi-reason">{it.reason}</span>}
              {(it.affects?.length ?? 0) > 0 && (
                <span className="pi-affects" title={it.affects?.join("、")}>
                  牵动 {it.affects!.length} 张表
                </span>
              )}
            </div>
          </li>
        ))}
      </ul>

      {(proposal.blocked?.length ?? 0) > 0 && (
        <div className="pend-blocked">
          <div className="pb-h">需要你注意（{proposal.blocked!.length}）</div>
          <ul>
            {proposal.blocked!.map((b, i) => (
              <li key={i}>{b.sheet ? <em>{b.sheet}{b.ref ? `!${b.ref}` : ""}</em> : null}{b.reason}</li>
            ))}
          </ul>
        </div>
      )}

      {err && <p className="pend-err">{err}</p>}

      {/* 同步检查：这笔改动还牵连谁（见 docs/agent-architecture/23-影响面推断.md） */}
      {impactNode && (
        <div className="pend-impact">
          <div className="pi-h">
            <IconLink size={13} />
            <b>这笔还牵连谁</b>
            <input className="pi-kind" value={kind} placeholder="类别（收租/售房…）"
              onChange={(e) => setKind(e.target.value)} />
          </div>
          {impact && impact.candidates.length > 0 ? (
            <ul className="pi-cands">
              {impact.candidates.slice(0, 8).map((c, i) => (
                <li key={i} className={c.byMemory ? "mem" : ""}>
                  <span className="pic-name">{c.node.sheet}</span>
                  <span className="pic-score">{(c.score * 100).toFixed(0)}</span>
                  <span className="pic-why">{c.reasons.join(" · ")}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="dash-muted">
              {impact?.note || "没找到相关表——可以自己补上，我会记住"}
            </p>
          )}

          {correcting ? (
            <div className="pi-correct">
              <input value={extra} placeholder="还要看哪些表？（逗号分隔）"
                onChange={(e) => setExtra(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") void learn(); }} />
              <button className="btn primary sm" onClick={() => void learn()}
                disabled={!extra.trim() || !kind.trim()}>记住</button>
              <button className="btn ghost sm" onClick={() => setCorrecting(false)}>取消</button>
            </div>
          ) : (
            <button className="pi-fix" onClick={() => setCorrecting(true)}>
              {learned || "漏了？告诉我还要看哪些表（会记住）"}
            </button>
          )}
        </div>
      )}

      {/* 自检结果（见 docs/agent-architecture/26-自检流程.md） */}
      {selfCheck && (
        <div className={`pend-self ${selfCheck.level}`}>
          <div className="ps-h">
            <IconRefresh size={13} />
            <b>改完自检</b>
            <span className="ps-sum">{selfCheck.summary}</span>
            <span className="ps-time">{selfCheck.elapsed}</span>
          </div>
          {selfCheck.findings.length > 0 && (
            <ul className="ps-list">
              {selfCheck.findings.map((f, i) => (
                <li key={i} className={f.level}>
                  <span className="ps-sheet">{f.node.sheet}</span>
                  <span className="ps-msg">{f.message}</span>
                </li>
              ))}
            </ul>
          )}
          {selfCheck.findings.some((f) => f.needClarify) && (
            <p className="ps-clarify">
              以上需要你确认；漏了哪些表就说一声，我会记住。
            </p>
          )}
        </div>
      )}

      {/* 写入安全（见 24-语义映射与安全边界） */}
      {safety && safety.level !== "ok" && (
        <div className={`pend-safe ${safety.level}`}>
          <IconShield size={13} />
          <span>{safety.advice}</span>
        </div>
      )}

      <div className="pend-actions">
        <button className="btn primary" onClick={() => void apply()} disabled={busy || proposal.items.length === 0}>
          {busy ? "应用中…" : `应用这 ${proposal.items.length} 处`}
        </button>
        <button className="btn ghost" onClick={onDiscarded} disabled={busy}>忽略</button>
        <span className="pend-hint">改动前会自动备份，每格旧值都记账</span>
      </div>
    </div>
  );
}

function describeWhere(it: { key?: Record<string, string>; month?: string; op?: string; ref?: string }): string {
  const parts: string[] = [];
  if (it.key) for (const v of Object.values(it.key)) parts.push(v);
  if (it.month) parts.push(it.month);
  // 新增行没有“原有位置”可言，用预估行号指明“加在哪一行附近”
  if (it.op === "append") parts.push(`表尾（约第 ${rowOfRef(it.ref)} 行）`);
  return parts.length ? parts.join(" · ") : "—";
}

/** 从 A1 坐标里取出行号（“表尾（约第 N 行）”显示用）。取不出就给个占位。 */
function rowOfRef(ref?: string): string {
  const m = /[A-Za-z]+(\d+)$/.exec(ref ?? "");
  return m ? m[1] : "末";
}

function fmt(v: unknown): string {
  if (v === null || v === undefined || v === "") return "（空）";
  return String(v);
}

/**
 * 把“新增行”的「列名→值」拼成一行。
 *
 * 新增一行是多列，pi-new 只能显示一个值；直接 fmt(map) 会输出
 * `[object Object]`（或一团 JSON），用户根本看不出要写什么。
 */
function fmtValues(values?: Record<string, unknown>): string {
  if (!values) return "（无）";
  const parts = Object.entries(values).map(([k, v]) => `${k}=${fmt(v)}`);
  return parts.length ? parts.join("　") : "（无）";
}

/* ---------- 会话面板 ---------- */

type ConvoMsg = {
  role: "user" | "agent" | "system";
  text: string;
  /** 这条消息附的图（data URL）。回看会话时能看见当时给的是什么图。 */
  images?: string[];
  time: string;
  proposal?: {
    kind: string; title: string; detail: string; schedule?: string;
    action?: string; tools?: string[]; options?: string[];
    /** 人点过"采纳"（task/rule/tool_request）。落盘了，重启后仍在。 */
    accepted?: boolean;
  };
};

export function ChatPane({ onPlanReady, wsKey }: {
  onPlanReady: (p: Proposal, trace?: TraceData) => void;
  /** 当前工作区标识（用于把没发出去的草稿按工作区隔离存下来） */
  wsKey?: string;
}) {
  const [convoId, setConvoId] = useState<string>("");
  const [msgs, setMsgs] = useState<ConvoMsg[]>([]);
  const [input, setInput] = useState("");
  // 附着的图（data URL）。图片走多模态消息送给模型——财务常拿到的不是 csv，
  // 而是一张截图（微信里发来的收款记录、别人拍的表格）。
  const [images, setImages] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [planning, setPlanning] = useState(false);
  /** 算清单失败的那几条（只有失败才需要手动重试：正常情况 agent 会自动算）。 */
  const [planFailed, setPlanFailed] = useState<Record<number, boolean>>({});
  /** 最近一次运行的步骤账（贴在对应的 agent 消息下面）。 */
  const [runTrace, setRunTrace] = useState<{ at: number; trace: TraceData } | null>(null);
  const [err, setErr] = useState("");
  /** 放大查看的图（点消息里的图打开）。null = 没开。 */
  const [zoom, setZoom] = useState<string | null>(null);
  /** 有图被拖到聊天框上方（用于点亮投放区） */
  const [dragOver, setDragOver] = useState(false);
  /** 历史会话列表（把"说过的"存成可翻的记录，见下方注释） */
  const [history, setHistory] = useState<ConvoSummary[]>([]);
  const [histOpen, setHistOpen] = useState(false);
  /** 正在改名的记录 id（null = 没在改名） */
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameDraft, setRenameDraft] = useState("");
  /** 提炼出的记忆候选（null = 还没提炼 / 已关掉） */
  const [cands, setCands] = useState<DistillCandidate[] | null>(null);
  /** 勾选了哪几条候选（默认全不勾：让人主动挑，而不是被动删） */
  const [picked, setPicked] = useState<Record<number, boolean>>({});
  const [distilling, setDistilling] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedNote, setSavedNote] = useState("");
  const convoTitle = history.find((h) => h.id === convoId)?.title ?? "当前对话";
  const endRef = useRef<HTMLDivElement | null>(null);
  const taRef = useRef<HTMLTextAreaElement | null>(null);

  // ---------- 草稿暂存（未发出的字与图）----------
  //
  // 为什么要存：用户在这里写的东西常常要几段话（"B31 收 8 月租金 23540，
  // 另外把 9 月滞纳金一起算上"），中途去看一眼表格、切个面板，
  // 回来发现写了一半的话和贴好的图都没了——那是最让人恼火的一种丢失。
  //
  // 存**本地**（localStorage 而非服务端）：草稿是"还没决定要发的东西"，
  // 不该先进账（账目是"干过什么"的记录，草稿不是干过的事）。
  // 按工作区隔离：换个台账看，不该把上一个台账的半句话带过来。
  const draftKey = `gw:chat-draft:${wsKey || "default"}`;

  // 打开时读回草稿（只读一次，之后由下面的 effect 持续写入）
  const [draftReady, setDraftReady] = useState(false);
  useEffect(() => {
    setDraftReady(false);
    try {
      const raw = localStorage.getItem(draftKey);
      if (raw) {
        const d = JSON.parse(raw) as { input?: string; images?: string[] };
        setInput(d.input ?? "");
        setImages(Array.isArray(d.images) ? d.images.slice(0, 4) : []);
      } else {
        setInput("");
        setImages([]);
      }
    } catch { /* 坏数据当没有，别让它拦住输入 */ }
    setDraftReady(true);
  }, [draftKey]);

  // 持续写回。等草稿读完再写，否则会用初始空值把刚读出来的草稿冲掉。
  useEffect(() => {
    if (!draftReady) return;
    try {
      if (!input && images.length === 0) localStorage.removeItem(draftKey);
      else localStorage.setItem(draftKey, JSON.stringify({ input, images }));
    } catch { /* 配额满了就算了：草稿丢了也不该让界面崩 */ }
  }, [input, images, draftKey, draftReady]);

  // 输入框随内容长高（到 max-height 才滚动）。textarea 不会自己长，
  // 得把高度先归零再按 scrollHeight 设，否则删字时会缩不回去。
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${ta.scrollHeight}px`;
  }, [input]);

  // 读图并**先压再送**：手机截图动辄 3–5MB，直接发会拖慢请求、
  // 甚至超过模型对单图的上限。长边压到 1600 足够模型读数。
  // 收 FileList / File[]：粘贴与拖拽给的都是浏览器文件对象。
  const addPics = async (files: FileList | File[] | null) => {
    if (!files || files.length === 0) return;
    const imgs = [...files].filter((f) => f.type.startsWith("image/"));
    if (imgs.length === 0) {
      setErr("只收图片（截图或照片）");
      return;
    }
    setErr("");
    const out: string[] = [];
    for (const f of imgs.slice(0, 4)) {
      try {
        out.push(await shrinkImage(f));
      } catch {
        setErr(`读不了这张图：${f.name}`);
      }
    }
    if (out.length) setImages((a) => [...a, ...out].slice(0, 4));
  };

  // 粘贴图片：截图后 Ctrl+V 直接进输入框。
  // 为什么这条比"传图"按钮更要紧：财务拿到的图多半在聊天工具里，
  // 复制粘贴是最短路径——让他先另存成文件再点选，等于把这条路堵上。
  // 只在剪贴板里**真的有图**时拦截：否则会把正常的文字粘贴也吃掉。
  const onPaste = (e: React.ClipboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const files: File[] = [];
    for (const it of items) {
      if (it.kind !== "file" || !it.type.startsWith("image/")) continue;
      const f = it.getAsFile();
      if (f) files.push(f);
    }
    if (files.length === 0) return;
    e.preventDefault();
    void addPics(files);
  };

  // 拖图进聊天框：从桌面/浏览器里把图直接拖进来也能收。
  // 只在"拖的确实是文件"时才防止默认行为——否则会把拖文本之类的操作也吃掉。
  const onDrop = (e: React.DragEvent) => {
    const hasFiles = [...(e.dataTransfer?.types ?? [])].includes("Files");
    if (!hasFiles) return;
    e.preventDefault();
    setDragOver(false);
    void addPics(e.dataTransfer.files);
  };

  // 历史会话：说过的要能翻回来。
  //
  // 归属说明（重要）：这是**记录**，不是"记忆"。记录 = 逐字留痕的流水；
  // 记忆（rules.yaml / state.yaml / memory2）是从流水里提炼出、经人确认、
  // 会过期的结论。两者的关系是：记忆的叙事层由会话压缩而来，但压缩后的
  // 才叫记忆——原始对话只是素材。所以这里存的是记录，不进记忆库。
  const loadHistory = async () => {
    try {
      const r = await agentApi.conversations();
      setHistory(r.items ?? []);
    } catch { setHistory([]); }
  };
  const openConvo = async (id: string) => {
    try {
      const c = await agentApi.conversation(id);
      setConvoId(c.id);
      setMsgs(c.messages);
      setHistOpen(false);
      setErr("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  const newConvo = () => {
    setConvoId("");
    setMsgs([]);
    setHistOpen(false);
    setErr("");
  };
  const delConvo = async (id: string) => {
    try {
      await agentApi.deleteConversation(id);
      if (id === convoId) newConvo();
      await loadHistory();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  // 改名：记录默认标题取首句前 20 字，而首句常常是"帮我看下这个"——
  // 要能翻回来，就得能起个自己记得住的名字。
  const commitRename = async (id: string) => {
    const t = renameDraft.trim();
    setRenamingId(null);
    if (!t) return;
    try {
      await agentApi.renameConversation(id, t);
      await loadHistory();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  // ---------- 从记录提炼记忆 ----------
  //
  // 这是"记录 → 记忆"的闸门。模型只**提议**，人逐条勾选后落盘。
  // 为什么不自动沉淀：记忆是长期资产，一条错的记忆会污染以后所有判断，
  // 而且用户很难事后查出是哪条带偏的。所以与改表同一个模式：先看清单，再确认。
  const startDistill = async () => {
    if (!convoId) { setErr("先打开一条记录，再从它提炼"); return; }
    setDistilling(true);
    setCands(null);
    setErr("");
    try {
      const r = await agentApi.distill(convoId);
      setCands(r.candidates ?? []);
      // 默认全不勾：让人主动挑，而不是被动删。
      setPicked({});
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setDistilling(false);
    }
  };
  const savePicked = async () => {
    if (!cands) return;
    const chosen = cands.filter((_, i) => picked[i]);
    if (chosen.length === 0) { setCands(null); return; }
    setSaving(true);
    setErr("");
    try {
      for (const c of chosen) {
        await agentApi.saveMemory({
          kind: c.kind,
          id: memID(c),
          value: c.kind === "fact" ? c.text : undefined,
          text: c.kind === "decision" ? c.text : undefined,
          key: c.key,
          source: c.source || `对话：${convoTitle}`,
        });
      }
      setCands(null);
      setSavedNote(`已记住 ${chosen.length} 条`);
      setTimeout(() => setSavedNote(""), 4000);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  useEffect(() => { endRef.current?.scrollIntoView({ behavior: "smooth" }); }, [msgs]);

  // Esc 关掉大图。看不到全屏遮罩时，键盘是第一反应——别逼人去找那个叉。
  useEffect(() => {
    if (!zoom) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setZoom(null); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [zoom]);

  const send = async (textArg?: string) => {
    // textArg 用于「点澄清选项」：那条路径要把选项文字**当场**当用户话说出去。
    // 不能先 setInput(opt) 再调 send()——send 的闭包里读到的还是旧的 input，
    // 发出去的会是上一句（或空）。这是个真踩过的陈旧状态 bug。
    const text = (textArg ?? input).trim();
    // 只有图片、没文字也算一条消息（"帮我看看这张图"常常就是拍张照）
    if ((!text && images.length === 0) || busy) return;
    setBusy(true);
    setErr("");
    setInput("");
    const pics = images;
    setImages([]);
    // 乐观显示用户这句（含图）
    setMsgs((m) => [...m, { role: "user", text, images: pics, time: "" }]);
    try {
      const r = await agentApi.chat(text, convoId || undefined, pics.length ? pics : undefined);
      setConvoId(r.conversationId);
      const next = r.conversation.messages;
      setMsgs(next);
      const at = next.length - 1;
      if (r.trace) setRunTrace({ at, trace: r.trace });
      // agent 判定这是「改动表」的指令（kind=plan）→ **直接接着算清单**。
      // 不再让用户多点一次「算出要改哪些格」：算清单本来就是 agent 该干的活，
      // 它把结果摆出来、用户确认即可。需要澄清（kind=clarify）时不自动算。
      if (next[at]?.role === "agent" && next[at]?.proposal?.kind === "plan") {
        void makePlan(text, pics.length ? pics : undefined, at);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  // 「改动清单提案」→ 真的去算清单（模型只说要改，清单由 plan 用语义坐标算出来）。
  //
  // 为什么要带上这条消息里的图：数据常常**就在截图里**（"按 D 列名字把 E、F 列
  // 填进销售明细表"）。不把图传下去，出清单这一步只能反问"数据来自哪张表"，
  // 把一件能做的事停成澄清——实测发生过。
  // 出清单要等模型（实测 7–60 秒不等，最坏是通道超时）。等待期间必须让人看见
  // “还在跑、跑了多久、能不能停”——否则一个 30 秒没反应的按钮在用户眼里就是卡死。
  const planAbort = useRef<AbortController | null>(null);
  const [planElapsed, setPlanElapsed] = useState(0);

  useEffect(() => {
    if (!planning) { setPlanElapsed(0); return; }
    const t0 = Date.now();
    setPlanElapsed(0);
    const id = setInterval(() => setPlanElapsed(Math.round((Date.now() - t0) / 1000)), 500);
    return () => clearInterval(id);
  }, [planning]);

  const cancelPlan = () => { planAbort.current?.abort(); };

  const makePlan = async (instruction: string, images: string[] | undefined, at: number) => {
    setPlanning(true);
    setErr("");
    setPlanFailed((p) => ({ ...p, [at]: false }));
    const ac = new AbortController();
    planAbort.current = ac;
    try {
      const r = await agentApi.plan(instruction, undefined, images, ac.signal, convoId || undefined);
      onPlanReady(r.proposal, r.trace);
    } catch (e) {
      setPlanFailed((p) => ({ ...p, [at]: true }));
      if (ac.signal.aborted) {
        setErr("已取消这次出清单（没有任何改动被写入）。");
      } else {
        const m = e instanceof Error ? e.message : String(e);
        // 通道超时是最常见的失败，给一句能照着做的解释，
        // 而不是把 "context deadline exceeded" 原样甩给用户。
        setErr(/deadline exceeded|timeout|timed out|超时/i.test(m)
          ? `模型通道超时：引擎等满 60 秒也没收到响应（上游接口慢或挂了）。\n可以直接再点一次；若反复如此，去设置里换一个更稳的接口/模型。\n原始错误：${m}`
          : m);
      }
    } finally {
      planAbort.current = null;
      setPlanning(false);
    }
  };

  // 澄清问询：点选项 = 把它当作用户的回答发出去（直接传文字，不走 input state）
  const answer = (opt: string) => { void send(opt); };

  // 采纳一条提案（task/rule/tool_request）：落成真的东西（task → 一条暂停的定时任务）。
  // 以前这些提案只能看、点不了——模型说"每天下班前体检"，用户点头也没下文。
  const [accepting, setAccepting] = useState(false);
  const acceptProposal = async (at: number) => {
    if (!convoId || accepting) return;
    setAccepting(true);
    setErr("");
    try {
      const r = await agentApi.acceptProposal(convoId, at);
      const c = await agentApi.conversation(convoId);
      setMsgs(c.messages as ConvoMsg[]);
      setSavedNote(r.note || "已采纳");
      setTimeout(() => setSavedNote(""), 5000);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setAccepting(false);
    }
  };

  // 动作按钮只挂在**最后一条** plan 提议上：历史消息再挂按钮既是噪音，
  // 也已经过期（待确认清单一次只可能有一份）。生成类入口已移出对话，
  // 见看板顶栏的「生成」面板。
  const lastPlanIdx = (() => {
    for (let k = msgs.length - 1; k >= 0; k--) {
      if (msgs[k].proposal?.kind === "plan") return k;
    }
    return -1;
  })();

  return (
    <div className={`chat${dragOver ? " dropping" : ""}`}
      onDragOver={(e) => {
        // 只在拖的是文件时才接管（拖文字不该被抢）
        if (![...(e.dataTransfer?.types ?? [])].includes("Files")) return;
        e.preventDefault();
        setDragOver(true);
      }}
      onDragLeave={(e) => {
        // 移出整个聊天区才熄灭（子元素间移动会连续触发 leave）
        if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
        setDragOver(false);
      }}
      onDrop={onDrop}>

      {/* 会话头：一条记录要能翻回来，也要能开新的、能起名。
          「对话」是配置入口，记录多了就必须能回到某一次。 */}
      <div className="chat-bar">
        <button className={`cb-btn${histOpen ? " on" : ""}`}
          onClick={() => { setHistOpen((v) => !v); if (!histOpen) void loadHistory(); }}
          title="历史对话">
          <IconClock size={13} />历史
          {history.length > 0 && <span className="cb-n">{history.length}</span>}
        </button>
        <button className="cb-btn" onClick={newConvo} title="开始新对话" disabled={busy}>
          <IconPlus size={13} />新对话
        </button>
        {/* 提炼记忆：把这次聊出来的结论沉淀成长期记忆（要逐条确认） */}
        <button className="cb-btn" onClick={() => void startDistill()}
          disabled={distilling || !convoId} title="从这条记录里提炼值得长期记住的东西">
          <IconAssistant size={13} />{distilling ? "正在提炼…" : "提炼记忆"}
        </button>
        {savedNote && <span className="cb-saved"><IconCheck size={12} />{savedNote}</span>}
      </div>

      {/* 历史列表：点一条切回去；可改名、可删。 */}
      {histOpen && (
        <ul className="chat-hist">
          {history.length === 0 && <li className="ch-empty">还没有历史对话</li>}
          {history.map((c) => (
            <li key={c.id} className={c.id === convoId ? "cur" : ""}>
              {renamingId === c.id ? (
                <input
                  className="ch-rename"
                  autoFocus
                  value={renameDraft}
                  onChange={(e) => setRenameDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") void commitRename(c.id);
                    if (e.key === "Escape") setRenamingId(null);
                  }}
                  onBlur={() => void commitRename(c.id)}
                  aria-label="重命名这条记录"
                />
              ) : (
                <>
                  <button className="ch-row" onClick={() => void openConvo(c.id)}>
                    <span className="ch-t">{c.title || "新对话"}</span>
                    <span className="ch-d">{c.updated}</span>
                  </button>
                  <button className="ch-act" title="重命名"
                    onClick={() => { setRenamingId(c.id); setRenameDraft(c.title || ""); }}
                    aria-label={`重命名 ${c.title}`}>
                    <IconRename size={12} />
                  </button>
                  <button className="ch-act ch-del" title="删除这条记录"
                    onClick={() => void delConvo(c.id)} aria-label={`删除 ${c.title}`}>
                    <IconTrash size={12} />
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}

      {/* 记忆候选：**先看清单，再决定留哪条**。
          默认全不勾 —— 让人主动挑；模型提议的东西不该默认就进记忆库。
          每条都给出处与理由，人才判断得了"这条真该长期记住吗"。 */}
      {cands && (
        <div className="chat-distill">
          <div className="cd-h">
            <IconAssistant size={13} />
            <b>提炼出 {cands.length} 条值得记住的</b>
            <button className="cd-x" onClick={() => setCands(null)} aria-label="关闭">
              <IconX size={13} />
            </button>
          </div>
          {cands.length === 0 ? (
            <p className="cd-none">
              这条记录里没有可沉淀的结论——一次性的问答不算记忆。这很正常。
            </p>
          ) : (
            <>
              <ul className="cd-list">
                {cands.map((c, i) => (
                  <li key={i} className={picked[i] ? "on" : ""}>
                    <button className="cdi-row" onClick={() => setPicked((p) => ({ ...p, [i]: !p[i] }))}>
                      <span className={`cdi-box${picked[i] ? " on" : ""}`} aria-hidden>
                        {picked[i] && <IconCheck size={10} />}
                      </span>
                      <span className="cdi-main">
                        <span className="cdi-top">
                          <span className={`cdi-kind ${c.kind}`}>
                            {c.kind === "fact" ? "事实" : "决策"}
                          </span>
                          <span className="cdi-title">{c.title}</span>
                        </span>
                        <span className="cdi-text">{c.text}</span>
                        {c.key && Object.keys(c.key).length > 0 && (
                          <span className="cdi-key">
                            {Object.entries(c.key).map(([k, v]) => `${k} ${v}`).join(" · ")}
                          </span>
                        )}
                        {c.why && <span className="cdi-why">为什么记：{c.why}</span>}
                        {c.source && <span className="cdi-src">出处：{c.source}</span>}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
              <div className="cd-foot">
                <span className="cd-n">已选 {Object.values(picked).filter(Boolean).length} 条</span>
                <button className="btn ghost sm" onClick={() => setCands(null)} disabled={saving}>取消</button>
                <button className="btn primary sm" onClick={() => void savePicked()} disabled={saving}>
                  {saving ? "正在记住…" : "记住选中的"}
                </button>
              </div>
            </>
          )}
        </div>
      )}

      <div className="chat-scroll">
        {msgs.length === 0 && (
          <div className="chat-hello">
            <IconSpark size={18} />
            <p>输入指令，安排工作</p>
            <span className="chat-ex">“记一笔：B31 收到 8 月租金 23540”</span>
            <span className="chat-ex">“每天下班前体检一次”</span>
            <span className="chat-ex">“含运费的以后都算进去”</span>
            <span className="chat-ex hint">截图可以直接 Ctrl+V 粘贴，也可以把图片拖进来</span>
          </div>
        )}
        {msgs.map((m, i) => (
          <div key={i} className={`msg ${m.role}`}>
            {/* 图在文字之前：回看会话时"当时给的是哪张图"比这句话更要紧。
                点一下放大——表格截图里的数字在小图上根本认不出。 */}
            {m.images && m.images.length > 0 && (
              <div className="msg-pics">
                {m.images.map((src, k) => (
                  <button key={k} className="msg-pic" onClick={() => setZoom(src)}
                    title="点开看大图">
                    <img src={src} alt={`附图 ${k + 1}`} loading="lazy" />
                  </button>
                ))}
              </div>
            )}
            <div className="msg-text">{m.text}</div>
            {m.time && <div className="msg-time">{m.time}</div>}
            {m.proposal && (
              <div className="msg-prop">
                <div className="mp-kind">{kindLabel(m.proposal.kind)}</div>
                <div className="mp-title">{m.proposal.title}</div>
                {m.proposal.detail && <div className="mp-detail">{m.proposal.detail}</div>}
                {m.proposal.schedule && <div className="mp-meta">时间：{m.proposal.schedule}</div>}
                {m.proposal.options && m.proposal.options.length > 0 && (
                  <div className="mp-opts">
                    {m.proposal.options.map((o, j) => (
                      <button key={j} className="pill blue" onClick={() => answer(o)}>{o}</button>
                    ))}
                  </div>
                )}
                {/* 清单由 agent 自己算：正常情况这里没有任何按钮（已自动跑）。
                    只在「正在算」时给进度与取消，「算失败」时给一个重试。 */}
                {m.proposal.kind === "plan" && i === lastPlanIdx && (planning || planFailed[i]) && (() => {
                  // 这条回答要办的是**用户那句话**（以及他那张图），不是模型写的 detail。
                  // 用 detail 会丢掉“图里就是数据”这件事（detail 只是文字描述），
                  // 结果出清单时模型看不到数据 → 生成空清单。
                  const src = precedingUser(msgs, i);
                  return (
                    <div className="mp-opts">
                      {planning ? (
                        <>
                          <span className="mp-wait">正在算清单… {planElapsed}s（模型在算，通常 10–40 秒）</span>
                          <button className="btn ghost sm" onClick={cancelPlan}>取消</button>
                        </>
                      ) : (
                        <button className="btn primary sm" onClick={() => void makePlan(src.text, src.images, i)}>
                          重试出清单
                        </button>
                      )}
                    </div>
                  );
                })()}
                {/* 非 plan 类提案（定时任务 / 规则 / 工具申请）：给一个明确的「采纳」。 */}
                {(m.proposal.kind === "task" || m.proposal.kind === "rule" || m.proposal.kind === "tool_request") && (
                  <div className="mp-opts">
                    {m.proposal.accepted
                      ? <span className="mp-wait">已采纳</span>
                      : <button className="btn primary sm" disabled={accepting}
                          onClick={() => void acceptProposal(i)}>采纳</button>}
                  </div>
                )}
              </div>
            )}
            {runTrace && runTrace.at === i && <RunTrace trace={runTrace.trace} />}
          </div>
        ))}
        {busy && <div className="msg agent"><div className="msg-text typing">正在想…</div></div>}
        {err && <div className="chat-err" role="alert">{err}</div>}
        <div ref={endRef} />
      </div>

      {/* 附着的图：发送前可逐个删掉。缩略图而不是文件名——
          截图往往文件名无意义（微信图片_20260916.png），看小图才能确认放对了。 */}
      {images.length > 0 && (
        <div className="chat-pics">
          {images.map((src, i) => (
            <span key={i} className="chat-pic">
              <button className="cp-open" onClick={() => setZoom(src)} title="点开看大图">
                <img src={src} alt={`附图 ${i + 1}`} />
              </button>
              <button className="cp-del" onClick={() => setImages((a) => a.filter((_, k) => k !== i))}
                aria-label={`移除第 ${i + 1} 张图`} title="移除">
                <IconX size={11} />
              </button>
            </span>
          ))}
        </div>
      )}

      <div className="chat-input">
        {/* 传图的入口是「粘贴」与「拖入」：截图 Ctrl+V 直接进来，
            也可以把图片文件拖到这块区域。都不需要先存成文件再点选。 */}
        <textarea
          ref={taRef}
          rows={2}
          value={input}
          placeholder={images.length ? "补充说明这几张图…" : "输入指令…（可粘贴或拖入截图）"}
          onChange={(e) => setInput(e.target.value)}
          onPaste={onPaste}
          onKeyDown={(e) => {
            // Enter 发送，Shift+Enter 换行（多行指令常见，别逼人一行写完）
            if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); void send(); }
          }}
          disabled={busy}
        />
        <button className="btn primary" onClick={() => void send()}
          disabled={busy || (!input.trim() && images.length === 0)}
          title="发送（Enter）">
          <IconSend size={13} />
        </button>
      </div>

      {/* 拖入时的投放提示：整块浮起 + 一句话说清松手会发生什么 */}
      {dragOver && (
        <div className="chat-drop" aria-hidden="true">
          <span>松手即可把图片加进来</span>
        </div>
      )}

      {/* 看大图：点消息里的图或待发缩略图打开。点任意处/Esc 关闭。 */}
      {zoom && (
        <div className="chat-zoom" onClick={() => setZoom(null)} role="dialog" aria-label="查看大图">
          <img src={zoom} alt="大图" />
          <button className="cz-x" onClick={() => setZoom(null)} aria-label="关闭">
            <IconX size={16} />
          </button>
        </div>
      )}
      <p className="chat-foot">
        <IconNote size={12} /> 它只建议，不会自己改表；要改的会进「待确认」等你点头。
      </p>
    </div>
  );
}

/**
 * 给一条记忆候选生成稳定 id。
 *
 * 为什么不能每次随机：记忆是**可覆盖**的（memory2 按 id 更新已有条目）。
 * 同一件事再提炼一次应该更新那条，而不是堆出两条一模一样的记忆——
 * 记忆库里重复项比没有更坏，它会让检索出来的结论互相打架。
 * 所以 id 由"类型 + 业务键 + 内容"派生：内容一样就落到同一条上。
 */
function memID(c: DistillCandidate): string {
  const keyPart = c.key ? Object.entries(c.key).map(([k, v]) => `${k}=${v}`).join(",") : "";
  const raw = `${c.kind}:${keyPart}:${c.text}`;
  // FNV-1a：短字符串分布够匀，且不引依赖
  let h = 0x811c9dc5;
  for (let i = 0; i < raw.length; i++) {
    h ^= raw.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return `${c.kind}-${(h >>> 0).toString(36)}`;
}

/**
 * 找出这条回答是在回哪句用户话（以及数据在哪张图里）。
 *
 * 为什么要单独找、不直接用 m.images：模型回答的那条消息**从不带图**，
 * 图永远挂在更早的用户消息上。直接取 m.images 得到的是空——这正是
 * “点『算出要改哪些格』后图丢了、清单生成空的『未提供图片』”的根因。
 *
 * 取法：
 *   - 文字：往前取最近几条用户消息**合起来**（从远到近），不是只取最近一句。
 *     踩过的坑：用户先给任务+图，模型追问一句，用户只答了“表。”；
 *     只拿最近一句当指令，出清单时模型看到的就只剩“表。”——要么反问、
 *     要么给个空清单。把原始任务和后面的补充连着给，才是人交接时的做法。
 *   - 图：往前找**最近一条带图**的用户消息——可以跨过中间那条回答，
 *     因为“用这张图片的数据”之后用户往往还会补一句不带图的说明。
 */
function precedingUser(msgs: ConvoMsg[], at: number): { text: string; images: string[] } {
  const texts: string[] = [];
  let images: string[] = [];
  for (let i = at - 1; i >= 0; i--) {
    const m = msgs[i];
    if (m.role !== "user") continue;
    const t = (m.text ?? "").trim();
    if (t) texts.push(t);
    if (images.length === 0 && m.images?.length) images = m.images;
    // 最近 3 句足够覆盖“任务 + 补充 + 回答”；再往前多半是另一件事了
    if (texts.length >= 3 && images.length) break;
  }
  return { text: texts.reverse().join("\n"), images };
}

function kindLabel(k: string): string {
  switch (k) {
    case "task": return "自动化任务提案";
    case "rule": return "规则提案";
    case "tool_request": return "工具申请";
    case "clarify": return "需要你确认";
    case "plan": return "改动清单提案";
    default: return "建议";
  }
}

/**
 * shrinkImage 把选中的图压到合理尺寸再转成 data URL。
 *
 * 为什么必须先压：手机截图动辄 3–5MB，几张一起发会让请求变慢，
 * 而且多数模型对单图有大小上限，超了直接报错——用户只会看到"发不出去"。
 * 长边压到 1600、JPEG 0.82：足够模型看清表格里的字，体积降一个数量级。
 *
 * 用 canvas 而不是读原文件：浏览器里没有更轻的缩放手段，
 * 而这里只需要"看得清"，不需要保留原始画质。
 */
async function shrinkImage(file: File, maxEdge = 1600, quality = 0.82): Promise<string> {
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise<HTMLImageElement>((res, rej) => {
      const el = new Image();
      el.onload = () => res(el);
      el.onerror = () => rej(new Error("图片解码失败"));
      el.src = url;
    });
    const scale = Math.min(1, maxEdge / Math.max(img.naturalWidth, img.naturalHeight));
    const w = Math.max(1, Math.round(img.naturalWidth * scale));
    const h = Math.max(1, Math.round(img.naturalHeight * scale));
    const cv = document.createElement("canvas");
    cv.width = w;
    cv.height = h;
    const ctx = cv.getContext("2d");
    if (!ctx) throw new Error("无法创建画布");
    // 白底：截图多为浅色内容，透明底转 JPEG 会发黑
    ctx.fillStyle = "#fff";
    ctx.fillRect(0, 0, w, h);
    ctx.drawImage(img, 0, 0, w, h);
    return cv.toDataURL("image/jpeg", quality);
  } finally {
    URL.revokeObjectURL(url);
  }
}
