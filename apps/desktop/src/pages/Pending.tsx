import { useEffect, useRef, useState } from "react";
import "./confirm.css";
import { agentApi } from "../api-agent";
import type { Proposal, ApplyResult, ImpactResult, SafetyReport, SelfCheckReport, ConvoSummary, DistillCandidate, TraceData, ProposalRows } from "../api-agent";
import RunTrace from "../components/RunTrace";
import PromptDistillPanel from "./PromptDistillPanel";
import { IconCheck, IconSpark, IconSend, IconNote, IconLink, IconShield, IconX, IconClock, IconPlus, IconTrash, IconRename, IconAssistant } from "../components/icons";

/* 待确认（A）+ 会话（B）（见 docs/agent-architecture/19-界面设计.md 阶段 3-4）
   用户的规则：看清单 → 你确认 → 才改。会话是配置入口，产出结构化建议。 */

/* ---------- 待确认卡片 ---------- */

export function PendingList({ proposal, onApplied, onDiscarded, impactNode, safety, trace, onFeedback, feedbackBusy, feedbackErr, onOpenCell, onBusyChange }: {
  proposal: Proposal | null;
  onApplied: (r: { applied: number; rejected: number; results: ApplyResult[]; selfCheck?: SelfCheckReport | null; summaries?: { sheet: string; text: string }[] }) => void;
  onDiscarded: () => void;
  /** 改动点（用于推断"这笔还牵连谁"） */
  impactNode?: string;
  /** 写入安全评估 */
  safety?: SafetyReport | null;
  /** 这份清单是怎么算出来的（后端 trace），展开可看每步与模型收发 */
  trace?: TraceData | null;
  /** 复核反馈：用户指出哪里不对 → 回到产生这份清单的对话里继续矫正并重算。
      返回 true=已重新出清单；false=失败（输入不自动清空，便于改后重试）。 */
  onFeedback?: (text: string) => Promise<boolean> | boolean;
  /** 反馈重算进行中 */
  feedbackBusy?: boolean;
  /** 反馈重算的错误 */
  feedbackErr?: string;
  /** 点击预览表里的改动格 → 打开那张表并定位（可选）。 */
  onOpenCell?: (cell: { sheet: string; file?: string; ref?: string }) => void;
  /** 写入进行中（应用）回调：上层据此在对话里显示"正在执行"并锁住输入。 */
  onBusyChange?: (busy: boolean) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [err, setErr] = useState("");
  const [impact, setImpact] = useState<ImpactResult | null>(null);
  const [kind, setKind] = useState("");
  const [correcting, setCorrecting] = useState(false);
  const [extra, setExtra] = useState("");
  const [learned, setLearned] = useState("");
  // 整行预览（只读拉取）：把清单里的改动摆回原表的行与表头里
  const [rows, setRows] = useState<ProposalRows | null>(null);

  // 推断"这一笔还牵连谁"（见 23-影响面推断）。
  // kind 是输入框：每敲一个字都请求一次 impact 是纯浪费（"收租"要打两次），
  // 停手 400ms 再算；impactNode 变化（点另一行）仍然立即生效。
  useEffect(() => {
    if (!impactNode) { setImpact(null); return; }
    let alive = true;
    const t = setTimeout(() => {
      agentApi.impact(impactNode, kind || undefined)
        .then((r) => { if (alive) setImpact(r); })
        .catch(() => { if (alive) setImpact(null); });
    }, 400);
    return () => { alive = false; clearTimeout(t); };
  }, [impactNode, kind]);

  // 整行预览：清单变了就重拉一次，把改动还原成原表的行。
  // 拉不到就退回下面的文字清单（预览是增强，不是唯一路径）。
  useEffect(() => {
    if (!proposal?.id) { setRows(null); return; }
    let alive = true;
    agentApi.proposalRows(proposal.id)
      .then((r) => { if (alive) setRows(r); })
      .catch(() => { if (alive) setRows(null); });
    return () => { alive = false; };
  }, [proposal?.id]);

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

  // 复核反馈：把用户这句话交回上游（Dashboard）→ 写进原对话 + 重新出清单。
  // 只有成功才清空，失败保留原文供改后重试。
  const submitFeedback = async () => {
    const t = feedback.trim();
    if (!t || feedbackBusy || !onFeedback) return;
    const ok = await onFeedback(t);
    if (ok !== false) setFeedback("");
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
    onBusyChange?.(true);
    setErr("");
    try {
      const r = await agentApi.apply(proposal.id);
      onApplied(r);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
      onBusyChange?.(false);
    }
  };

  return (
    <div className="pend">
      <div className="pend-head">
        <b>{proposal.summary}</b>
        <span className="pend-target">{proposal.target.split(/[\\/]/).pop()}</span>
      </div>
      <RunTrace trace={trace} label="清单是怎么算出来的" />

      {rows && rows.sheets.length > 0 ? (
        <ProposalPreview data={rows} onOpenCell={onOpenCell} />
      ) : (
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
      )}

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

      {/* 写入安全（见 24-语义映射与安全边界） */}
      {safety && safety.level !== "ok" && (
        <div className={`pend-safe ${safety.level}`}>
          <IconShield size={13} />
          <span>{safety.advice}</span>
        </div>
      )}

      {/* 复核反馈：用户检查一遍，哪里不对就在这里说一句——
          会回到产生这份清单的对话里继续矫正，并重新出清单。
          与上面「漏了？」不同：那个只记关系记忆，这个真的重算。 */}
      {onFeedback && (
        <div className="pend-feedback">
          <div className="pf-h">
            <IconAssistant size={13} />
            <b>检查下来哪里不对？</b>
            <span className="pf-sub">说一句，回到这次对话里改，重新出清单再给你看</span>
          </div>
          <textarea
            value={feedback}
            rows={2}
            placeholder="例如：b30 那行不该累加；漏了 2025年12月那张表；金额应该减掉已收部分…"
            onChange={(e) => setFeedback(e.target.value)}
            onKeyDown={(e) => {
              // Ctrl/⌘+Enter 发送，Enter 留给换行——反馈常常不止一句
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); void submitFeedback(); }
            }}
            disabled={feedbackBusy}
          />
          {feedbackErr && <p className="pf-err">{feedbackErr}</p>}
          <div className="pf-foot">
            <span className="pf-hint">Ctrl/⌘ + Enter 发送</span>
            <button className="btn sm" onClick={() => void submitFeedback()}
              disabled={feedbackBusy || !feedback.trim()}>
              {feedbackBusy ? "正在重新算…" : "反馈并重新算"}
            </button>
          </div>
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

/** 清单的整行预览：把改动还原到原表的行与表头里，改动的格子标色。
 *  改动行与新增行用不同颜色的左边距区分；改动格显示旧值→新值。 */
function ProposalPreview({ data, onOpenCell }: { data: ProposalRows; onOpenCell?: (cell: { sheet: string; file?: string; ref?: string }) => void }) {
  return (
    <div className="ppv">
      {data.sheets.map((sh, si) => {
        // 列数取表头与各行内容的较大者，避免行宽于表头时丢列
        const cols = Math.max(sh.header.length, ...sh.rows.map((r) => r.cells.length), 0);
        return (
          <div key={si} className="ppv-sheet">
            <div className="ppv-sheet-h">
              <span className="ppv-name">{sh.sheet}</span>
              <span className="ppv-meta">表头第 {sh.headerRow} 行 · {sh.rows.length} 处</span>
            </div>
            <div className="ppv-scroll">
              <table className="ppv-table">
                <thead>
                  <tr>
                    <th className="ppv-rownum">行</th>
                    {Array.from({ length: cols }).map((_, ci) => (
                      <th key={ci}>{sh.header[ci] ?? `第${ci + 1}列`}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {sh.rows.map((r, ri) => {
                    const byCol = new Map(r.changes.map((c) => [c.col, c]));
                    return (
                      <tr key={ri} className={`ppv-row ${r.kind}`}>
                        <td className="ppv-rownum">{r.kind === "new" ? "新增" : r.row}</td>
                        {Array.from({ length: cols }).map((_, ci) => {
                          const ch = byCol.get(ci + 1);
                          if (!ch) return <td key={ci} className="ppv-cell">{r.cells[ci] ?? ""}</td>;
                          const isNew = r.kind === "new" || ch.op === "append";
                          const tip = [
                            ch.old !== undefined && ch.old !== "" ? `旧值：${fmt(ch.old)}` : "",
                            ch.reason ?? "",
                            (ch.affects?.length ?? 0) > 0 ? `牵动 ${ch.affects!.length} 张表` : "",
                          ].filter(Boolean).join(" · ");
                          const body = isNew ? (
                            <span className="ppv-new">{fmt(ch.new)}</span>
                          ) : (
                            <>
                              <span className="ppv-old">{fmt(ch.old)}</span>
                              <span className="ppv-arrow">→</span>
                              <span className="ppv-new">{fmt(ch.new)}</span>
                              {ch.op === "add" && <span className="ppv-op">累加</span>}
                            </>
                          );
                          // 有 ref 且给了跳转回调：这一格可点，直接打开原表定位
                          const jump = onOpenCell && ch.ref;
                          return (
                            <td key={ci} className={`ppv-cell ${isNew ? "new" : "edit"}${jump ? " jump" : ""}`}
                              title={[tip, jump ? "点击跳到这一格" : ""].filter(Boolean).join(" · ") || undefined}>
                              {jump ? (
                                <button className="ppv-jump"
                                  onClick={() => onOpenCell!({ sheet: sh.sheet, file: data.target, ref: ch.ref })}>
                                  {body}
                                </button>
                              ) : body}
                            </td>
                          );
                        })}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {sh.rows.some((r) => r.changes.some((c) => c.reason)) && (
              <ul className="ppv-reasons">
                {sh.rows.flatMap((r) =>
                  Array.from(new Map(r.changes.filter((c) => c.reason).map((c) => [c.field + c.reason, c])).values())
                    .map((c, ci) => (
                      <li key={`${r.row}-${ci}`}>
                        <em>{r.kind === "new" ? "新增" : `第 ${r.row} 行`}</em>
                        {c.field}：{c.reason}
                      </li>
                    ))
                )}
              </ul>
            )}
          </div>
        );
      })}
      <div className="ppv-legend">
        <span className="ppv-lg edit" />改动
        <span className="ppv-lg new" />新增行
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

export function ChatPane({ onPlanReady, wsKey, syncToken, syncConvoId, externalRunning, externalLabel, runText, onRunConsumed }: {
  onPlanReady: (p: Proposal, trace?: TraceData) => void;
  /** 当前工作区标识（用于把没发出去的草稿按工作区隔离存下来） */
  wsKey?: string;
  /** 外部（待确认面板的复核反馈）把一句话写进原对话后，递增值让这里重载。 */
  syncToken?: number;
  /** 要重载的会话 id；空则重载当前会话。 */
  syncConvoId?: string;
  /** 外部正在跑（面板重算 / 写入）——对话也要锁住输入并显示运行中。 */
  externalRunning?: boolean;
  /** 外部运行时的说明文字（如“正在写入表格…”）。 */
  externalLabel?: string;
  /** 从「自动化任务」面板点运行的快捷任务：渲染好的指令在这里发出去。 */
  runText?: { text: string; token: number };
  /** 这条快捷任务已被消费（上层清掉，避免重开抽屉时又跑一遍）。 */
  onRunConsumed?: () => void;
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
  const [distillOpen, setDistillOpen] = useState(false);
  const convoTitle = history.find((h) => h.id === convoId)?.title ?? "当前对话";
  // 「停在上次那条」：打开对话时不再总是新开，而是回到上次用的那条。
  // 会话多数是"接着上次说"，每次从零开始等于每次把上下文丢掉。
  // 存本地、按工作区分开——"我在跟哪条记录说话"是本机习惯，不是服务端状态。
  const lastConvoKey = `gw:chat-last:${wsKey || "default"}`;
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

  // 打开时恢复上次的会话。只跑一次：这是"进入时的一次动作"，
  // 不该在每次渲染后再去覆盖用户的手动切换。
  const restoredRef = useRef(false);
  useEffect(() => {
    if (restoredRef.current) return;
    restoredRef.current = true;
    void (async () => {
      try {
        const items = (await agentApi.conversations()).items ?? [];
        setHistory(items);
        const last = localStorage.getItem(lastConvoKey);
        if (last && items.some((c) => c.id === last)) {
          const c = await agentApi.conversation(last);
          setConvoId(c.id);
          setMsgs(c.messages);
        }
      } catch { /* 读不到就当新会话，不拦人 */ }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastConvoKey]);

  // 外部反馈回来时刷新对话：待确认面板里说了句反馈 → 后端已把这句写进原对话，
  // 这里从服务端重载一次，用户就能在对话里看见自己刚说的话与它的回应。
  // 只在 token 真的变化时重载（用 ref 跳过挂载时的首次）。
  const lastSyncRef = useRef(syncToken);
  useEffect(() => {
    if (syncToken === undefined || syncToken === lastSyncRef.current) return;
    lastSyncRef.current = syncToken;
    void (async () => {
      const id = syncConvoId || convoId;
      if (!id) return;
      try {
        const c = await agentApi.conversation(id);
        setConvoId(c.id);
        setMsgs(c.messages as ConvoMsg[]);
        setErr("");
      } catch { /* 刷新失败不该打断反馈主流程 */ }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [syncToken]);

  // 记住"当前在跟哪条记录说话"，供下次打开恢复，也用于列表里的标识。
  useEffect(() => {
    try {
      if (convoId) localStorage.setItem(lastConvoKey, convoId);
    } catch { /* 存不下只影响下次恢复 */ }
  }, [convoId, lastConvoKey]);

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
    if ((!text && images.length === 0) || busy || planning || externalRunning) return;
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
        void makePlan(text, pics.length ? pics : undefined, at, r.conversationId);
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

  const makePlan = async (instruction: string, images: string[] | undefined, at: number, convoIdArg?: string) => {
    setPlanning(true);
    setErr("");
    setPlanFailed((p) => ({ ...p, [at]: false }));
    const ac = new AbortController();
    planAbort.current = ac;
    try {
      const r = await agentApi.plan(instruction, undefined, images, ac.signal, convoIdArg || convoId || undefined);
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

  // 快捷任务（从「自动化任务」面板点运行）→ 在这里发出去，走 send 的同一条路
  // （/chat → /plan → 出清单）。用 token 去重；消费后由上层清掉。
  const lastRunToken = useRef(0);
  useEffect(() => {
    if (!runText || runText.token === lastRunToken.current) return;
    lastRunToken.current = runText.token;
    void send(runText.text);
    onRunConsumed?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runText?.token]);

  // agent 是否在跑：本面板在发/在算，或外部（待确认面板）在重算。
  // 跑的时候**必须锁住输入框**——否则用户在半途又发一句，两轮规划会互相覆盖。
  const agentRunning = busy || planning || !!externalRunning;

  // 最近一次“算清单失败”的 agent 消息下标（用于重试）；-1 = 没有。
  const planFailIdx = (() => {
    for (let k = msgs.length - 1; k >= 0; k--) {
      if (planFailed[k]) return k;
    }
    return -1;
  })();

  // 重试出清单：取那条回答在回的那句用户话（含图），这也是它当初该办的。
  const retryPlan = () => {
    if (planFailIdx < 0) return;
    const src = precedingUser(msgs, planFailIdx);
    void makePlan(src.text, src.images, planFailIdx);
  };

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
        {/* 沉淀为快捷任务：勾选对话消息 → 总结/选模板 → 存成可复用的快捷任务 */}
        <button className="cb-btn" onClick={() => setDistillOpen(true)} title="把勾选的对话消息沉淀成快捷任务">
          <IconSpark size={13} />沉淀为快捷
        </button>
        {savedNote && <span className="cb-saved"><IconCheck size={12} />{savedNote}</span>}
        {/* 判断标识：现在在跟哪条会话说话（打开时会自动续上上次那条）。 */}
        <span className="cb-now" title="当前会话">{convoId ? convoTitle : "新对话"}</span>
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
                    {c.id === convoId && <span className="ch-tag">当前</span>}
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
            {/* 改动清单不再在对话里重复展示：它由待确认面板专门呈现（整行预览）。
                对话只留 agent 的文字回应；其余提案（任务/规则/工具/澄清）照旧。 */}
            {m.proposal && m.proposal.kind !== "plan" && (
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
                {/* 改动清单改由待确认面板（整行预览）呈现，对话里不再重复。 */}
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

      {/* agent 运行中：给一条明确的进度，并把输入框锁住（见 agentRunning）。
          待确认面板里的重算也算——焦点在那里时，对话同样显示"在跑"。 */}
      {(agentRunning || planFailIdx >= 0) && (
        <div className="chat-run">
          {agentRunning ? (
            <>
              <span className="cr-dot" />
              <span className="cr-text">
                {planning ? `正在算清单… ${planElapsed}s（模型在算，通常 10–40 秒）`
                  : externalLabel ?? (externalRunning ? "待确认面板正在重新算，请稍候…" : "正在想…")}
              </span>
              {planning && <button className="btn ghost sm" onClick={cancelPlan}>取消</button>}
            </>
          ) : (
            <>
              <span className="cr-text">算清单失败</span>
              <button className="btn primary sm" onClick={() => void retryPlan()}>重试出清单</button>
            </>
          )}
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
          disabled={agentRunning}
        />
        <button className="btn primary" onClick={() => void send()}
          disabled={agentRunning || (!input.trim() && images.length === 0)}
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

      {distillOpen && (
        <PromptDistillPanel msgs={msgs} convoId={convoId}
          onClose={() => setDistillOpen(false)}
          onSaved={(name) => {
            setDistillOpen(false);
            setSavedNote("已存入快捷任务：" + name);
            setTimeout(() => setSavedNote(""), 4000);
          }} />
      )}
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
