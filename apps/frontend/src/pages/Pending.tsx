import { useEffect, useRef, useState } from "react";
import "./confirm.css";
import { agentApi } from "../api-agent";
import type { Proposal, ApplyResult, ImpactResult, SafetyReport, SelfCheckReport, GenerateResult } from "../api-agent";
import { IconCheck, IconSpark, IconSend, IconNote, IconLink, IconShield, IconRefresh, IconImage, IconX } from "../components/icons";

/* 待确认（A）+ 会话（B）（见 docs/agent-architecture/19-界面设计.md 阶段 3-4）
   用户的规则：看清单 → 你确认 → 才改。会话是配置入口，产出结构化建议。 */

/* ---------- 待确认卡片 ---------- */

export function PendingList({ proposal, onApplied, onDiscarded, impactNode, safety }: {
  proposal: Proposal | null;
  onApplied: (r: { applied: number; rejected: number; results: ApplyResult[]; selfCheck?: SelfCheckReport | null }) => void;
  onDiscarded: () => void;
  /** 改动点（用于推断"这笔还牵连谁"） */
  impactNode?: string;
  /** 写入安全评估 */
  safety?: SafetyReport | null;
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

      <ul className="pend-items">
        {(proposal.items ?? []).map((it, i) => (
          <li key={i} className="pend-item">
            <div className="pi-main">
              <span className="pi-where">{describeWhere(it)}</span>
              <span className="pi-field">{it.field}</span>
            </div>
            <div className="pi-change">
              <span className="pi-old">{fmt(it.old)}</span>
              <span className="pi-arrow">→</span>
              <span className="pi-new">{fmt(it.new)}</span>
              {it.op === "add" && <span className="pi-op">累加</span>}
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

function describeWhere(it: { key?: Record<string, string>; month?: string }): string {
  const parts: string[] = [];
  if (it.key) for (const v of Object.values(it.key)) parts.push(v);
  if (it.month) parts.push(it.month);
  return parts.length ? parts.join(" · ") : "—";
}

function fmt(v: unknown): string {
  if (v === null || v === undefined || v === "") return "（空）";
  return String(v);
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
  };
};

export function ChatPane({ onPlanReady }: { onPlanReady: (p: Proposal) => void }) {
  const [convoId, setConvoId] = useState<string>("");
  const [msgs, setMsgs] = useState<ConvoMsg[]>([]);
  const [input, setInput] = useState("");
  // 附着的图（data URL）。图片走多模态消息送给模型——财务常拿到的不是 csv，
  // 而是一张截图（微信里发来的收款记录、别人拍的表格）。
  const [images, setImages] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [planning, setPlanning] = useState(false);
  const [err, setErr] = useState("");
  const endRef = useRef<HTMLDivElement | null>(null);
  const picRef = useRef<HTMLInputElement | null>(null);

  // 读图并**先压再送**：手机截图动辄 3–5MB，直接发会拖慢请求、
  // 甚至超过模型对单图的上限。长边压到 1600 足够模型读数。
  const addPics = async (files: FileList | null) => {
    if (!files || files.length === 0) return;
    setErr("");
    const out: string[] = [];
    for (const f of [...files].slice(0, 4)) {
      try {
        out.push(await shrinkImage(f));
      } catch {
        setErr(`读不了这张图：${f.name}`);
      }
    }
    if (out.length) setImages((a) => [...a, ...out].slice(0, 4));
  };

  useEffect(() => { endRef.current?.scrollIntoView({ behavior: "smooth" }); }, [msgs]);

  const send = async () => {
    const text = input.trim();
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
      setMsgs(r.conversation.messages);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  // 「改动清单提案」→ 真的去算清单（模型只说要改，清单由 plan 用语义坐标算出来）
  const makePlan = async (instruction: string) => {
    setPlanning(true);
    setErr("");
    try {
      const r = await agentApi.plan(instruction);
      onPlanReady(r.proposal);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setPlanning(false);
    }
  };

  // 生成：把这句话交给后端产出草稿（落在工作区 生成/，绝不改原表）
  const [gen, setGen] = useState<GenerateResult | null>(null);
  const [genBusy, setGenBusy] = useState(false);
  const makeDraft = async (instruction: string) => {
    setGenBusy(true);
    setErr("");
    try {
      setGen(await agentApi.generate(instruction));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setGenBusy(false);
    }
  };

  // 澄清问询：点选项 = 直接把它当用户回答发出去
  const answer = (opt: string) => { setInput(opt); void send(); };

  return (
    <div className="chat">
      <div className="chat-scroll">
        {msgs.length === 0 && (
          <div className="chat-hello">
            <IconSpark size={18} />
            <p>输入指令，安排工作</p>
            <span className="chat-ex">“记一笔：B31 收到 8 月租金 23540”</span>
            <span className="chat-ex">“每天下班前体检一次”</span>
            <span className="chat-ex">“含运费的以后都算进去”</span>
          </div>
        )}
        {msgs.map((m, i) => (
          <div key={i} className={`msg ${m.role}`}>
            {/* 图在文字之前：回看会话时"当时给的是哪张图"比这句话更要紧 */}
            {m.images && m.images.length > 0 && (
              <div className="msg-pics">
                {m.images.map((src, k) => (
                  <img key={k} src={src} alt={`附图 ${k + 1}`} loading="lazy" />
                ))}
              </div>
            )}
            <div className="msg-text">{m.text}</div>
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
                {m.proposal.kind === "plan" && (
                  <div className="mp-opts">
                    <button className="btn primary sm" disabled={planning}
                      onClick={() => void makePlan(m.proposal!.detail || m.proposal!.title)}>
                      {planning ? "正在算清单…" : "算出要改哪些格"}
                    </button>
                  </div>
                )}
                {/* 生成类：产出新文件到 生成/，不碰原表 */}
                <div className="mp-opts">
                  <button className="btn ghost sm" disabled={genBusy}
                    onClick={() => void makeDraft(m.proposal!.detail || m.proposal!.title)}>
                    {genBusy ? "正在生成…" : "出一份草稿"}
                  </button>
                </div>
              </div>
            )}
          </div>
        ))}
        {busy && <div className="msg agent"><div className="msg-text typing">正在想…</div></div>}
        {err && <div className="chat-err">{err}</div>}
        <div ref={endRef} />
      </div>

      {/* 附着的图：发送前可逐个删掉。缩略图而不是文件名——
          截图往往文件名无意义（微信图片_20260916.png），看小图才能确认放对了。 */}
      {images.length > 0 && (
        <div className="chat-pics">
          {images.map((src, i) => (
            <span key={i} className="chat-pic">
              <img src={src} alt={`附图 ${i + 1}`} />
              <button onClick={() => setImages((a) => a.filter((_, k) => k !== i))}
                aria-label={`移除第 ${i + 1} 张图`} title="移除">
                <IconX size={11} />
              </button>
            </span>
          ))}
        </div>
      )}

      <div className="chat-input">
        {/* 传图：截图/照片里的数据也要能进得来。财务手上常常不是 csv 而是一张图 */}
        <button className="chat-attach" onClick={() => picRef.current?.click()}
          disabled={busy} title="传图（截图或照片）" aria-label="传图">
          <IconImage size={15} />
        </button>
        <input ref={picRef} type="file" accept="image/*" multiple style={{ display: "none" }}
          onChange={(e) => { void addPics(e.target.files); e.target.value = ""; }} />
        <input value={input} placeholder={images.length ? "补充说明这几张图…" : "输入指令…"}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) void send(); }}
          disabled={busy} />
        <button className="btn primary sm" onClick={() => void send()}
          disabled={busy || (!input.trim() && images.length === 0)}>
          <IconSend size={13} />
        </button>
      </div>
      {gen && (
        <div className="gen-card">
          <div className="gc-h">
            <b>{gen.title || "已生成"}</b>
            <span className="gc-kind">草稿</span>
          </div>
          <div className="gc-meta">
            {gen.rows > 0 && <span>{gen.rows} 行</span>}
            {gen.columns?.length > 0 && <span>列：{gen.columns.join(" / ")}</span>}
          </div>
          {gen.sum && Object.keys(gen.sum).length > 0 && (
            <div className="gc-sum">
              {Object.entries(gen.sum).map(([k, v]) => (
                <span key={k} className="gc-sum-i"><em>{k}</em>{v.toLocaleString()}</span>
              ))}
            </div>
          )}
          {gen.content && <pre className="gc-content">{gen.content.slice(0, 600)}</pre>}
          <div className="gc-file">
            <code>{gen.file}</code>
            <span className="gc-note">{gen.notes?.[0] ?? "生成不改动原表"}</span>
          </div>
        </div>
      )}

      <p className="chat-foot">
        <IconNote size={12} /> 它只建议，不会自己改表；要改的会进「待确认」等你点头。
      </p>
    </div>
  );
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
