import { useEffect, useState } from "react";
import { agentApi } from "../api-agent";
import type { PromptTemplate, PromptCandidate } from "../api-agent";
import { IconX, IconSpark } from "../components/icons";

/* 「沉淀为快捷任务」面板。
 *
 * 入口在对话里（因为"要总结哪几句话"的上下文就在对话里）：
 *   1) 勾选要总结的对话消息；
 *   2) 选来源——让模型总结 / 内置模板 / 粘贴模板；
 *   3) 改好 名字 / 什么时候用 / 指令模板，存成快捷任务。
 *
 * 「内置模板」是随包发布的骨架（离线、可审计）；「粘贴模板」让用户从任何地方
 * 粘一个进来。刻意**不做**"从网上下载"——模板会被喂给模型去动财务表，
 * 下载来的既要求联网、又难审阅。 */

type Msg = { role: string; text: string; time?: string };

export default function PromptDistillPanel({ msgs, convoId, onClose, onSaved }: {
  msgs: Msg[];
  convoId?: string;
  onClose: () => void;
  onSaved: (name: string) => void;
}) {
  // 只列能当素材的（跳过系统消息与空消息）
  const usable = msgs.map((m, i) => ({ m, i })).filter(({ m }) => m.role !== "system" && (m.text ?? "").trim());
  const [picked, setPicked] = useState<Record<number, boolean>>(() => {
    const p: Record<number, boolean> = {};
    usable.forEach(({ i }) => { p[i] = true; });
    return p;
  });
  const [mode, setMode] = useState<"model" | "builtin" | "paste">("model");
  const [builtins, setBuiltins] = useState<PromptTemplate[]>([]);
  const [paste, setPaste] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [cands, setCands] = useState<PromptCandidate[] | null>(null);
  const [draft, setDraft] = useState({ name: "", when: "", template: "", note: "" });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    agentApi.builtinPrompts().then((r) => setBuiltins(r.templates ?? [])).catch(() => {});
  }, []);

  const pickedCount = Object.values(picked).filter(Boolean).length;

  const summarize = async () => {
    const messages = usable.filter(({ i }) => picked[i]).map(({ m }) => ({ role: m.role, text: m.text }));
    if (!messages.length) { setErr("先勾选要总结的消息"); return; }
    setBusy(true); setErr(""); setCands(null);
    try {
      const r = await agentApi.distillPromptMessages(convoId ?? "", messages);
      const c = r.candidates ?? [];
      setCands(c);
      if (c.length) setDraft({ name: c[0].name, when: c[0].when ?? "", template: c[0].template, note: c[0].note ?? "" });
      else setErr("这次没有总结出可复用的做法——换个勾选，或用内置模板。");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const useBuiltin = (p: PromptTemplate) => {
    setDraft({ name: p.name, when: p.when ?? "", template: p.template, note: p.note ?? "" });
    setErr("");
  };
  const usePaste = () => {
    if (!paste.trim()) { setErr("先粘贴一个模板"); return; }
    setDraft((d) => ({ ...d, template: paste.trim() }));
    setErr("");
  };

  const save = async () => {
    if (!draft.name.trim() || !draft.template.trim()) return;
    setSaving(true); setErr("");
    try {
      await agentApi.savePrompt({ ...draft, source: convoId ? `对话:${convoId}` : "manual" });
      onSaved(draft.name.trim());
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="pd-overlay" onClick={onClose}>
      <div className="pd-panel" onClick={(e) => e.stopPropagation()}>
        <header className="pd-head">
          <IconSpark size={15} />
          <b>沉淀为快捷任务</b>
          <span className="pd-sub">勾选对话消息 → 总结或选模板 → 存成可复用的快捷任务</span>
          <button className="pd-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="pd-body">
          {/* 左：勾选消息 */}
          <div className="pd-msgs">
            <div className="pd-col-h">选择要总结的消息 <em>{pickedCount}/{usable.length}</em></div>
            <ul>
              {usable.map(({ m, i }) => (
                <li key={i}>
                  <label className={`pd-msg ${m.role}`}>
                    <input type="checkbox" checked={!!picked[i]}
                      onChange={(e) => setPicked((p) => ({ ...p, [i]: e.target.checked }))} />
                    <span className="pd-who">{m.role === "user" ? "你" : "agent"}</span>
                    <span className="pd-text">{m.text}</span>
                  </label>
                </li>
              ))}
              {usable.length === 0 && <li className="pd-empty">这条对话还没有可总结的内容。</li>}
            </ul>
          </div>

          {/* 右：来源 + 编辑 */}
          <div className="pd-build">
            <div className="pd-tabs">
              <button className={mode === "model" ? "on" : ""} onClick={() => setMode("model")}>让模型总结</button>
              <button className={mode === "builtin" ? "on" : ""} onClick={() => setMode("builtin")}>内置模板</button>
              <button className={mode === "paste" ? "on" : ""} onClick={() => setMode("paste")}>粘贴模板</button>
            </div>

            {mode === "model" && (
              <button className="btn primary sm" onClick={() => void summarize()} disabled={busy}>
                {busy ? "正在总结…" : `总结勾选的 ${pickedCount} 条`}
              </button>
            )}

            {mode === "builtin" && (
              <ul className="pd-builtins">
                {builtins.map((p) => (
                  <li key={p.id || p.name}>
                    <button className={draft.template === p.template ? "on" : ""} onClick={() => useBuiltin(p)}>
                      <b>{p.name}</b>
                      <span>{p.when}</span>
                      <em>{p.template}</em>
                    </button>
                  </li>
                ))}
                {builtins.length === 0 && <li className="pd-empty">没有内置模板。</li>}
              </ul>
            )}

            {mode === "paste" && (
              <>
                <textarea className="pd-paste" rows={3} value={paste}
                  placeholder="粘贴一个指令模板，用 {槽位} 占位，例如：记一笔：{铺位} 收到 {月份} 租金 {金额}"
                  onChange={(e) => setPaste(e.target.value)} />
                <button className="btn sm" onClick={usePaste}>用这个模板</button>
              </>
            )}

            {/* 编辑区：有候选或已有模板时出现 */}
            {draft.template && (
              <div className="pd-edit">
                {cands && cands.length > 1 && (
                  <div className="pd-choose">
                    {cands.map((c, i) => (
                      <button key={i} className={draft.template === c.template ? "on" : ""}
                        onClick={() => setDraft({ name: c.name, when: c.when ?? "", template: c.template, note: c.note ?? "" })}>
                        {c.name}
                      </button>
                    ))}
                  </div>
                )}
                <label className="pd-field"><span>名字</span>
                  <input value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} /></label>
                <label className="pd-field"><span>什么时候用</span>
                  <input value={draft.when} onChange={(e) => setDraft((d) => ({ ...d, when: e.target.value }))} /></label>
                <label className="pd-field col"><span>指令模板（用 {"{"}槽位{"}"} 占位）</span>
                  <textarea rows={3} value={draft.template}
                    onChange={(e) => setDraft((d) => ({ ...d, template: e.target.value }))} /></label>
              </div>
            )}
          </div>
        </div>

        <footer className="pd-foot">
          {err && <span className="pd-err">{err}</span>}
          <button className="btn ghost sm" onClick={onClose} disabled={saving}>取消</button>
          <button className="btn primary sm" onClick={() => void save()}
            disabled={saving || !draft.name.trim() || !draft.template.trim()}>
            {saving ? "正在存…" : "存为快捷任务"}
          </button>
        </footer>
      </div>
    </div>
  );
}
