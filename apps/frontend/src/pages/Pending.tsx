import { useEffect, useRef, useState } from "react";
import "./confirm.css";
import { agentApi } from "../api-agent";
import type { Proposal, ApplyResult } from "../api-agent";
import { IconCheck, IconSpark, IconSend, IconNote } from "../components/icons";

/* 待确认（A）+ 会话（B）（见 docs/agent-architecture/19-界面设计.md 阶段 3-4）
   用户的规则：看清单 → 你确认 → 才改。会话是配置入口，产出结构化建议。 */

/* ---------- 待确认卡片 ---------- */

export function PendingList({ proposal, onApplied, onDiscarded }: {
  proposal: Proposal | null;
  onApplied: (r: { applied: number; rejected: number; results: ApplyResult[] }) => void;
  onDiscarded: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  if (!proposal) {
    return (
      <div className="dash-none">
        <IconCheck size={18} />
        <p>没有待确认的改动</p>
        <span>在「对话」里说一句（如"记一笔收款"），它会算出清单让你确认</span>
      </div>
    );
  }

  const apply = async () => {
    setBusy(true);
    setErr("");
    try {
      onApplied(await agentApi.apply(proposal.id));
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
        {proposal.items.map((it, i) => (
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
  const [busy, setBusy] = useState(false);
  const [planning, setPlanning] = useState(false);
  const [err, setErr] = useState("");
  const endRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => { endRef.current?.scrollIntoView({ behavior: "smooth" }); }, [msgs]);

  const send = async () => {
    const text = input.trim();
    if (!text || busy) return;
    setBusy(true);
    setErr("");
    setInput("");
    // 乐观显示用户这句
    setMsgs((m) => [...m, { role: "user", text, time: "" }]);
    try {
      const r = await agentApi.chat(text, convoId || undefined);
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

  // 澄清问询：点选项 = 直接把它当用户回答发出去
  const answer = (opt: string) => { setInput(opt); void send(); };

  return (
    <div className="chat">
      <div className="chat-scroll">
        {msgs.length === 0 && (
          <div className="chat-hello">
            <IconSpark size={18} />
            <p>说一句话，让它安排工作</p>
            <span className="chat-ex">“记一笔：B31 收到 8 月租金 23540”</span>
            <span className="chat-ex">“每天下班前体检一次”</span>
            <span className="chat-ex">“含运费的以后都算进去”</span>
          </div>
        )}
        {msgs.map((m, i) => (
          <div key={i} className={`msg ${m.role}`}>
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
              </div>
            )}
          </div>
        ))}
        {busy && <div className="msg agent"><div className="msg-text typing">正在想…</div></div>}
        {err && <div className="chat-err">{err}</div>}
        <div ref={endRef} />
      </div>

      <div className="chat-input">
        <input value={input} placeholder="说一句…"
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) void send(); }}
          disabled={busy} />
        <button className="btn primary sm" onClick={() => void send()} disabled={busy || !input.trim()}>
          <IconSend size={13} />
        </button>
      </div>
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
