import { useEffect, useState } from "react";
import { agentApi } from "../api-agent";
import type { MemoryFact, MemoryDecision, MemoryStale } from "../api-agent";
import { IconNote, IconShield, IconTrash, IconCheck, IconRefresh, IconSpark } from "../components/icons";

/**
 * MemoryPanel —— 「记忆」入口（设置里的第二栏，与「模型」并列）。
 *
 * 为什么它必须存在：记忆以前只能在对话里**写**、在改表时被**用**，
 * 但你没法一眼看清"我立过哪些规矩、哪条可能过期了"。看不见就等于不可控——
 * 而记忆是长期资产，一条错的会污染以后所有判断，用户却无从发现是哪条。
 *
 * 三件事，按"该先看哪个"排：
 *   ① 可能过时（stale）——这些最危险，因为系统还在照它办但依据已变
 *   ② 决策（人拍板的规矩）——它决定改表怎么算，是记忆里最有分量的
 *   ③ 事实（业务档案）——铺位租金/合同期这类，供核对
 *
 * 每条都能标"过时"或删掉。**能删与能写同等重要**：写错了不能删，
 * 那个错就永久留在库里了。
 */
export default function MemoryPanel() {
  const [facts, setFacts] = useState<MemoryFact[]>([]);
  const [decisions, setDecisions] = useState<MemoryDecision[]>([]);
  const [stale, setStale] = useState<MemoryStale[]>([]);
  const [path, setPath] = useState("");
  const [load, setLoad] = useState(true);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [tab, setTab] = useState<"decision" | "fact">("decision");

  const reload = async () => {
    try {
      const r = await agentApi.memory();
      setFacts(r.memory.facts ?? []);
      setDecisions(r.memory.decisions ?? []);
      setStale(r.memory.stale ?? []);
      setPath(r.path);
      setErr("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoad(false);
    }
  };
  useEffect(() => { void reload(); }, []);

  /** 这条记忆有过时标记吗（stale 是按 id 关联的） */
  const staleOf = (id: string) => stale.find((s) => s.id === id);

  const markStale = async (id: string) => {
    setBusy(id);
    try {
      await agentApi.memoryStale(id, "mark", "在设置里标记：依据可能已变，需核对");
      await reload();
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(""); }
  };
  const clearStale = async (id: string) => {
    setBusy(id);
    try {
      await agentApi.memoryStale(id, "clear");
      await reload();
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(""); }
  };
  const remove = async (kind: "fact" | "decision", id: string) => {
    setBusy(id);
    try {
      await agentApi.deleteMemory(kind, id);
      await reload();
    } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(""); }
  };

  /**
   * 自动查过时：只查一类可证明的事——记忆里点名的标识（铺位 B31 这类）
   * 在当前表里还找不找得到。**不比金额、不判语义**：那两类会频繁误报，
   * 而过时提示一旦开始误报，用户就会学会无视所有提示。
   */
  const [checking, setChecking] = useState(false);
  const [checkMsg, setCheckMsg] = useState("");
  const runCheck = async () => {
    setChecking(true);
    setCheckMsg("");
    try {
      const r = await agentApi.checkMemoryStale();
      await reload();
      setCheckMsg(
        r.count === 0
          ? "查过了：没发现失效的（记忆里点名的铺位/编号都还在表里）"
          : `查过了：${r.count} 条点名的东西在表里已找不到，已标为可能过时`,
      );
    } catch (e) {
      // 读不到表时会拒绝下结论——这是**有意的**，如实告诉用户为什么
      setCheckMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setChecking(false);
    }
  };

  const total = facts.length + decisions.length;

  if (load) {
    return <p className="set-hint">正在读取记忆…</p>;
  }

  return (
    <section className="set-sec">
      <h3>记忆</h3>
      <p className="set-hint">
        改表时它会**照着这些结论办**。所以错的和过期的要清掉——它们会一直影响判断。
        <b>记忆不是对话记录</b>：对话是流水，这里是沉淀下来、你确认过的结论。
      </p>

      {err && <p className="mem-err">{err}</p>}

      {total === 0 ? (
        <div className="mem-empty">
          <IconSpark size={16} />
          <b>还没有记忆</b>
          <span>
            在对话里点「提炼记忆」，把聊出来的结论（某个铺位的租户/租金、"含运费都算进去"这类规矩）
            逐条确认后就会存到这里。
          </span>
        </div>
      ) : (
        <>
          {/* 可能过时：排最前。系统还在照它办，但依据可能已经变了。 */}
          {stale.length > 0 && (
            <div className="mem-stale-box">
              <div className="mem-stale-h">
                <IconShield size={13} />
                <b>{stale.length} 条可能过时</b>
                <span>请核对后清除标记，或删掉这条记忆</span>
              </div>
              <ul className="mem-list">
                {stale.map((s) => (
                  <li key={s.id} className="mem-item stale">
                    <div className="mem-main">
                      <span className="mem-text">{s.note || "（无说明）"}</span>
                      <span className="mem-meta">标记于 {s.found || "—"}</span>
                    </div>
                    <button className="mem-act" disabled={busy === s.id}
                      onClick={() => void clearStale(s.id)} title="已核对：清除过时标记">
                      <IconCheck size={12} />已核对
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {/* 两类记忆分开看：决策决定"怎么算"，事实供"核对" */}
          <div className="mem-tabs" role="tablist">
            <button className={`mem-tab${tab === "decision" ? " on" : ""}`}
              onClick={() => setTab("decision")} role="tab" aria-selected={tab === "decision"}>
              决策 {decisions.length}
            </button>
            <button className={`mem-tab${tab === "fact" ? " on" : ""}`}
              onClick={() => setTab("fact")} role="tab" aria-selected={tab === "fact"}>
              事实 {facts.length}
            </button>
          </div>

          {tab === "decision" && (
            <ul className="mem-list">
              {decisions.length === 0 && <li className="mem-none">还没有决策记忆</li>}
              {decisions.map((d) => {
                const st = staleOf(d.id);
                return (
                  <li key={d.id} className={`mem-item${st ? " stale" : ""}`}>
                    <div className="mem-main">
                      <span className="mem-text">{d.text}</span>
                      <span className="mem-meta">
                        {d.key && Object.keys(d.key).length > 0 &&
                          Object.entries(d.key).map(([k, v]) => `${k} ${v}`).join(" · ") + " · "}
                        {d.by === "user" ? "你定的" : "系统建议"}
                        {d.source ? ` · 出处 ${d.source}` : ""}
                      </span>
                    </div>
                    <span className="mem-acts">
                      {st ? (
                        <button className="mem-act ok" disabled={busy === d.id}
                          onClick={() => void clearStale(d.id)} title="已核对：清除过时标记">
                          <IconCheck size={12} />
                        </button>
                      ) : (
                        <button className="mem-act" disabled={busy === d.id}
                          onClick={() => void markStale(d.id)} title="标记为可能过时">
                          <IconNote size={12} />
                        </button>
                      )}
                      <button className="mem-act del" disabled={busy === d.id}
                        onClick={() => void remove("decision", d.id)} title="删掉这条记忆">
                        <IconTrash size={12} />
                      </button>
                    </span>
                  </li>
                );
              })}
            </ul>
          )}

          {tab === "fact" && (
            <ul className="mem-list">
              {facts.length === 0 && <li className="mem-none">还没有事实记忆</li>}
              {facts.map((f) => {
                const st = staleOf(f.id);
                return (
                  <li key={f.id} className={`mem-item${st ? " stale" : ""}`}>
                    <div className="mem-main">
                      <span className="mem-text">{f.value}{f.unit ? ` ${f.unit}` : ""}</span>
                      <span className="mem-meta">
                        {f.key && Object.keys(f.key).length > 0 &&
                          Object.entries(f.key).map(([k, v]) => `${k} ${v}`).join(" · ") + " · "}
                        {f.source ? `出处 ${f.source}` : ""}
                        {f.updated ? ` · 更新 ${f.updated}` : ""}
                      </span>
                    </div>
                    <span className="mem-acts">
                      {st ? (
                        <button className="mem-act ok" disabled={busy === f.id}
                          onClick={() => void clearStale(f.id)} title="已核对：清除过时标记">
                          <IconCheck size={12} />
                        </button>
                      ) : (
                        <button className="mem-act" disabled={busy === f.id}
                          onClick={() => void markStale(f.id)} title="标记为可能过时">
                          <IconNote size={12} />
                        </button>
                      )}
                      <button className="mem-act del" disabled={busy === f.id}
                        onClick={() => void remove("fact", f.id)} title="删掉这条记忆">
                        <IconTrash size={12} />
                      </button>
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </>
      )}

      <div className="mem-foot">
        <button className="btn ghost sm" onClick={() => void runCheck()} disabled={checking || !!busy}>
          <IconShield size={12} />{checking ? "正在检查…" : "检查有没有过时的"}
        </button>
        <button className="btn ghost sm" onClick={() => void reload()} disabled={!!busy}>
          <IconRefresh size={12} />重新读取
        </button>
        {path && (
          <span className="set-hint-sm">
            存在本机：<code className="set-path">{path}</code>（就是记事本也能改的 JSON）
          </span>
        )}
      </div>
      {checkMsg && <p className="mem-check-msg">{checkMsg}</p>}
      <p className="set-hint set-hint-sm">
        检查只做一件事：看记忆里点名的铺位/编号还在不在表里。
        <b>金额变了不算过时</b>（升租、补缴都是正常的）；那类要你自己判断。
      </p>
    </section>
  );
}
