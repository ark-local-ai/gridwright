import { useEffect, useState } from "react";
import "./rules.css";
import { agentApi } from "../api-agent";
import type { RuleViewDto, RulesDryRun, RuleDryItem } from "../api-agent";
import { IconRefresh, IconCheck, IconX, IconSpark, IconNote, IconShield } from "../components/icons";

/* 规则引擎面板（见 docs/agent-architecture/30-规则引擎.md）
   这一屏只答一个问题：**我的规则，哪条真的在替我办事？**

   为什么这是唯一焦点：一条 when 缺条件的规则看起来像"一直在生效"，
   实际每次都静默退回问模型（花 token、结果不定）。"以为在跑"比"跑不了"更坏，
   所以可执行/不可执行必须是这一屏最强的视觉对比，其余一律安静。 */

export default function RulesPanel({ onClose }: { onClose: () => void }) {
  const [rules, setRules] = useState<RuleViewDto[]>([]);
  const [raw, setRaw] = useState("");
  const [path, setPath] = useState("");

  const [tab, setTab] = useState<"list" | "edit" | "try">("list");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  // 试跑
  const [dry, setDry] = useState<RulesDryRun | null>(null);

  const load = async () => {
    setErr("");
    try {
      const r = await agentApi.rules();
      setRules(r.rules ?? []);
      setRaw(r.raw ?? "");
      setPath(r.path);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  useEffect(() => { void load(); }, []);

  const saveRaw = async () => {
    setBusy(true); setMsg(""); setErr("");
    try {
      const r = await agentApi.saveRules({ raw });
      setMsg(`已保存 ${r.total} 条，其中 ${r.executable} 条可自动执行`);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };

  const dryRun = async () => {
    setBusy(true); setMsg(""); setErr("");
    try {
      const r = await agentApi.dryRunRules();
      setDry(r);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };

  const notRunnable = rules.filter((r) => !r.runnable);
  // 三类分开：它们的"做完了没有"判法不同，混在一起会误报。
  const edgeRules = rules.filter((r) => r.kind === "edge" && r.runnable);
  const linkRules = rules.filter((r) => r.kind === "link");
  const forbidRules = rules.filter((r) => r.kind === "forbid");
  // 头部读数只数"能自动改表"的——那才是用户问"哪条在替我办事"的答案。
  const nAuto = edgeRules.length;

  return (
    <div className="rl-overlay" onClick={onClose}>
      <div className="rl-panel" onClick={(e) => e.stopPropagation()}>
        <header className="rl-head">
          <span className="rl-head-ic"><IconNote size={15} /></span>
          <div className="rl-head-t">
            <b>规则</b>
            <span className="rl-sub">命中条件的由代码直接办，不花 token 问模型</span>
          </div>
          {/* 这一屏的核心读数：几条能自动跑 */}
          <div className="rl-readout">
            <span className={`rl-ro${nAuto > 0 ? " live" : ""}`}>
              <em>{nAuto}</em> 条自动执行
            </span>
            {notRunnable.length > 0 && (
              <span className="rl-ro warn"><em>{notRunnable.length}</em> 条仅提示</span>
            )}
          </div>
          <button className="rl-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="rl-tabs">
          <button className={tab === "list" ? "on" : ""} onClick={() => setTab("list")}>
            规则 <em>{rules.length}</em>
          </button>
          <button className={tab === "edit" ? "on" : ""} onClick={() => setTab("edit")}>
            编辑 rules.yaml
          </button>
          <button className={tab === "try" ? "on" : ""} onClick={() => { setTab("try"); void dryRun(); }}>
            试跑
          </button>
          <button className="btn ghost sm rl-refresh" onClick={() => void load()}>
            <IconRefresh size={12} />刷新
          </button>
        </div>

        {msg && <p className="rl-msg ok"><IconCheck size={12} />{msg}</p>}
        {err && <p className="rl-msg bad"><IconShield size={12} />{err}</p>}

        {tab === "list" && (
          <div className="rl-body">
            {/* 跑不起来的规则先说：这是用户最容易被误导的地方 */}
            {notRunnable.length > 0 && (
              <section className="rl-block">
                <div className="rl-block-h">
                  <h4>不会自动执行</h4>
                  <span className="rl-n warn">{notRunnable.length} 条</span>
                </div>
                <ul className="rl-list">
                  {notRunnable.map((r) => (
                    <li key={r.name} className="rl-rule off">
                      <div className="rl-rule-h">
                        <b>{r.name}</b>
                        <span className="rl-tag off">仅提示</span>
                      </div>
                      <p className="rl-why">
                        {r.reason || "缺 when/then，只能作为给模型的提示"}
                      </p>
                      {r.summary && <p className="rl-sum">{r.summary}</p>}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section className="rl-block">
              <div className="rl-block-h">
                <h4>会按规矩自动办</h4>
                <span className="rl-n ok">{edgeRules.length} 条</span>
              </div>
              <ul className="rl-list">
                {edgeRules.map((r) => (
                  <li key={r.name} className="rl-rule on">
                    <div className="rl-rule-h">
                      <b>{r.name}</b>
                      <span className="rl-tag on">自动执行</span>
                      {r.target && <span className="rl-target">→ {r.target}</span>}
                    </div>
                    {r.summary && <p className="rl-sum">{r.summary}</p>}
                    {(r.forbid?.length ?? 0) > 0 && (
                      <p className="rl-forbid">护栏列：{r.forbid!.join("、")}（模型也不能动）</p>
                    )}
                  </li>
                ))}
                {edgeRules.length === 0 && (
                  <li className="rl-empty">
                    还没有能自动改表的规则。在「编辑 rules.yaml」里写全 <code>when</code> 与{" "}
                    <code>then</code>，它就会替你办；也可以直接在对话里说"立一条规则"。
                  </li>
                )}
              </ul>
            </section>

            {/* 声明关联：它们不改表，但会参与"改这会牵连谁"的判断 */}
            {linkRules.length > 0 && (
              <section className="rl-block">
                <div className="rl-block-h">
                  <h4>声明的表间关联</h4>
                  <span className="rl-n ok">{linkRules.length} 条</span>
                </div>
                <ul className="rl-list">
                  {linkRules.map((r) => (
                    <li key={r.name} className="rl-rule link">
                      <div className="rl-rule-h">
                        <b>{r.name}</b>
                        <span className="rl-tag link">关系</span>
                      </div>
                      {r.summary && <p className="rl-sum">{r.summary}</p>}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {forbidRules.length > 0 && (
              <section className="rl-block">
                <div className="rl-block-h">
                  <h4>护栏</h4>
                  <span className="rl-n ok">{forbidRules.length} 条</span>
                </div>
                <ul className="rl-list">
                  {forbidRules.map((r) => (
                    <li key={r.name} className="rl-rule guard">
                      <div className="rl-rule-h">
                        <b>{r.name}</b>
                        <span className="rl-tag guard">护栏</span>
                      </div>
                      {r.summary && <p className="rl-sum">{r.summary}</p>}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {rules.length === 0 && (
              <div className="rl-intro">
                <IconSpark size={20} />
                <h4>还没有规则</h4>
                <p>
                  规则是人写的确定性约定：<b>符合条件的数据，直接按你定的方式改表</b>，
                  不问模型。它比模型更快、更稳、断网也能用，代价是只做你写清楚的事。
                </p>
                <button className="btn primary sm" onClick={() => setTab("edit")}>
                  写第一条规则
                </button>
              </div>
            )}

            {path && <p className="rl-foot">规则文件：<code>{path}</code>（也可以用记事本直接改）</p>}
          </div>
        )}

        {tab === "edit" && (
          <div className="rl-body rl-edit">
            <p className="rl-hint">
              when/then 齐全的规则会<b>自动执行</b>；只写 trigger/action 的只当提示。
              保存前会先校验，写坏了不会覆盖原文件。
            </p>
            <textarea
              className="rl-code"
              value={raw}
              spellCheck={false}
              onChange={(e) => setRaw(e.target.value)}
              placeholder={`rules:\n  - name: 收租累加进本月实收\n    when:\n      file: 实收\n      has_columns: [铺位, 实收金额]\n    then:\n      sheet: "2026年9月租金"\n      key:   { 物业位置: 铺位 }\n      field: { 本月实收: 实收金额 }\n      op: add`}
            />
            <div className="rl-edit-ops">
              <button className="btn primary sm" onClick={() => void saveRaw()} disabled={busy}>
                {busy ? "保存中…" : "保存并校验"}
              </button>
              <button className="btn ghost sm" onClick={() => void load()} disabled={busy}>放弃修改</button>
            </div>
          </div>
        )}

        {tab === "try" && (
          <div className="rl-body">
            {!dry ? (
              <p className="rl-hint">正在试跑…</p>
            ) : (
              <>
                <div className="rl-dry-head">
                  <b>试跑结果</b>
                  {dry.file && <span className="rl-dry-file">{dry.file}</span>}
                  <span className="rl-dry-safe">只计算，不写文件、不记账</span>
                </div>

                {dry.note && <p className="rl-hint">{dry.note}</p>}

                {(dry.hits?.length ?? 0) > 0 && (
                  <div className="rl-dry-hits">
                    <span className="rl-dry-lab">命中规则</span>
                    {dry.hits!.map((h) => <span key={h} className="rl-chip">{h}</span>)}
                  </div>
                )}

                {(dry.items?.length ?? 0) > 0 && (
                  <section className="rl-block">
                    <div className="rl-block-h"><h4>会这样改</h4><span className="rl-n ok">{dry.items!.length} 处</span></div>
                    <ul className="rl-dry-list">
                      {dry.items!.map((it, i) => (
                        <li key={i} className="rl-dry-item">
                          {/* 显示**坐标**（表!格）而不是行号：试跑已经真定位过，
                              坐标是能直接对上表的东西，行号只是来源。 */}
                          <span className="rl-dry-line">{[it.sheet, it.ref].filter(Boolean).join("!")}</span>
                          <span className="rl-dry-why">{it.why}</span>
                          <span className="rl-dry-new">{it.new}</span>
                        </li>
                      ))}
                    </ul>
                  </section>
                )}

                {(dry.skips?.length ?? 0) > 0 && (
                  <section className="rl-block">
                    <div className="rl-block-h">
                      <h4>会跳过</h4>
                      <span className="rl-n warn">{dry.skips!.length} 行</span>
                    </div>
                    {/* 跳过的必须说清原因：用户以为规则办全了，其实漏了几行 */}
                    <ul className="rl-dry-list">
                      {dry.skips!.map((s: RuleDryItem, i: number) => (
                        <li key={i} className="rl-dry-item skip">
                          {/* 跳过说明里已含"第 N 行"（后端拼的），这里只展示原因 */}
                          <span className="rl-dry-why">{s.why}</span>
                        </li>
                      ))}
                    </ul>
                  </section>
                )}

                {(dry.items?.length ?? 0) === 0 && (dry.skips?.length ?? 0) === 0 && !dry.note && (
                  <p className="rl-hint">没有规则命中这份数据。</p>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
