import { useEffect, useState } from "react";
import { agentApi } from "../api-agent";
import type { TermDto } from "../api-agent";
import { IconNote, IconTrash } from "../components/icons";

/* 术语（语义映射，见 docs/agent-architecture/24-语义映射与安全边界.md）。
 *
 * 为什么必须有个界面：这些映射以前只会「在对话里问清一次、以后悄悄复用」，
 * 用户看不见自己到底教了它什么，也没法纠正或删掉——看不见就等于不可控。
 * 而一条错的映射会一直影响后面每一次判断，和记忆是同一类风险。
 *
 * 规则：**人写的优先**，不会被系统后续的自动学习覆盖。
 */
export default function TermsPanel() {
  const [terms, setTerms] = useState<TermDto[]>([]);
  const [path, setPath] = useState("");
  const [load, setLoad] = useState(true);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [word, setWord] = useState("");
  const [sheet, setSheet] = useState("");
  const [field, setField] = useState("");
  const [kind, setKind] = useState("");
  const [saving, setSaving] = useState(false);

  const reload = async () => {
    try {
      const r = await agentApi.terms();
      setTerms(r.terms ?? []);
      setPath(r.path);
      setErr("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoad(false);
    }
  };
  useEffect(() => { void reload(); }, []);

  const remove = async (w: string) => {
    setBusy(w);
    try {
      await agentApi.termsDelete(w);
      await reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const save = async () => {
    if (!word.trim()) return;
    setSaving(true);
    setErr("");
    try {
      await agentApi.saveTerm({
        word: word.trim(),
        sheet: sheet.trim() || undefined,
        field: field.trim() || undefined,
        kind: kind.trim() || undefined,
        manual: true,
      });
      setWord(""); setSheet(""); setField(""); setKind("");
      await reload();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  /** 把「指向」渲染成一句人话：表 X · 列 Y · 类别 Z。 */
  const describe = (t: TermDto) => {
    const r = t.resolves_to || {};
    const bits: string[] = [];
    if (r.sheet) bits.push("表 " + r.sheet);
    if (r.field) bits.push("列 " + r.field);
    if (r.kind) bits.push("类别 " + r.kind);
    if (r.key) for (const [k, v] of Object.entries(r.key)) bits.push(`${k} ${v}`);
    return bits.join(" · ") || "（未指明指向）";
  };

  return (
    <section className="set-sec">
      <h3>术语</h3>
      <p className="set-hint">
        把一个说法固定指成哪张表 / 哪一列之后，它下次听到这个词就<b>不用再问</b>。
        <b>你写的优先</b>，不会被后续自动学习覆盖。
      </p>

      {err && <p className="mem-err">{err}</p>}

      <div className="term-row">
        <input className="term-in" value={word} placeholder="说法，如「老李那家」"
          onChange={(e) => setWord(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") void save(); }} />
        <input className="term-in" value={sheet} placeholder="表 / 工作表"
          onChange={(e) => setSheet(e.target.value)} />
        <input className="term-in" value={field} placeholder="列"
          onChange={(e) => setField(e.target.value)} />
        <input className="term-in" value={kind} placeholder="类别（收租/售房…）"
          onChange={(e) => setKind(e.target.value)} />
        <button className="btn primary sm" onClick={() => void save()} disabled={saving || !word.trim()}>
          {saving ? "保存中…" : "记住"}
        </button>
      </div>

      {!load && terms.length === 0 && (
        <div className="mem-empty">
          <IconNote size={16} />
          <b>还没有术语</b>
          <span>两种来源：你自己在上面写；或在对话里把问过的说法确认下来。</span>
        </div>
      )}

      {terms.length > 0 && (
        <ul className="mem-list">
          {terms.map((t) => (
            <li key={t.word} className="mem-item">
              <div className="mem-main">
                <span className="mem-text">{t.word}</span>
                <span className="mem-meta">
                  {describe(t)}
                  {t.source === "manual" ? " · 你写的" : " · 对话确认"}
                  {t.hits ? ` · 用过 ${t.hits} 次` : ""}
                </span>
              </div>
              <span className="mem-acts">
                <button className="mem-act del" disabled={busy === t.word}
                  onClick={() => void remove(t.word)} title="删掉这个说法">
                  <IconTrash size={12} />
                </button>
              </span>
            </li>
          ))}
        </ul>
      )}

      {path && (
        <p className="set-hint set-hint-sm">
          存在工作区：<code className="set-path">{path}</code>
        </p>
      )}
    </section>
  );
}
