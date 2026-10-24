import { useEffect, useState } from "react";
import "./tasks.css";
import { agentApi } from "../api-agent";
import type { JobDto, JobRunDto, PromptTemplate } from "../api-agent";
import { IconRefresh, IconClock, IconCheck, IconX } from "../components/icons";

/* 自动化任务（见 docs/agent-architecture/29-UI升级与剩余功能.md）
   两类东西放一个入口：
     · 定时任务——到点自己跑（只读体检；定时改表要走确认）
     · 快捷任务——一次成功的做法存成模板，一点即用（仍出清单等你确认）
   这里只负责**选择与启动**；"把某段对话沉淀成模板"在对话面板里（上下文在那儿）。
   默认**暂停**：定时改你文件的开关必须由你亲自打开。 */

export default function TasksPanel({ onClose, onRunPrompt }: {
  onClose: () => void;
  /** 运行一个快捷任务：把渲染好的指令交回主界面，走对话 → 出清单的同一条路。 */
  onRunPrompt?: (text: string) => void;
}) {
  const [jobs, setJobs] = useState<JobDto[]>([]);
  const [runs, setRuns] = useState<JobRunDto[]>([]);
  const [name, setName] = useState("");
  const [when, setWhen] = useState("每天 17:30");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [tab, setTab] = useState<"jobs" | "runs" | "quick">("jobs");

  // ---------- 快捷任务：只列表 + 运行 ----------
  const [prompts, setPrompts] = useState<PromptTemplate[]>([]);
  const [quickTpl, setQuickTpl] = useState<PromptTemplate | null>(null);
  const [quickVals, setQuickVals] = useState<Record<string, string>>({});
  const [quickErr, setQuickErr] = useState("");

  const load = async () => {
    try {
      const [j, r, p] = await Promise.all([
        agentApi.jobs().catch(() => ({ jobs: [] })),
        agentApi.jobRuns().catch(() => ({ runs: [] })),
        agentApi.prompts().catch(() => ({ prompts: [], path: "" })),
      ]);
      setJobs(j.jobs ?? []);
      setRuns(r.runs ?? []);
      setPrompts(p.prompts ?? []);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    }
  };
  useEffect(() => { void load(); }, []);

  const create = async () => {
    if (!name.trim()) return;
    setBusy(true);
    setMsg("");
    try {
      // 把"每天 17:30"这类人话翻成 cron（就这几档，够了）
      const schedule = toCron(when);
      if (!schedule) {
        setMsg("时间看不懂。可以写「每天 17:30」「每小时」「每 30 分钟」「工作日 9:00」");
        setBusy(false);
        return;
      }
      await agentApi.createJob({ name: name.trim(), schedule, kind: "scan", enabled: false });
      setName("");
      await load();
      setMsg("已创建（默认暂停，确认无误再打开开关）");
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (j: JobDto) => {
    try {
      await agentApi.updateJob(j.id, { enabled: !j.enabled });
      await load();
    } catch (e) { setMsg(e instanceof Error ? e.message : String(e)); }
  };

  const runNow = async (j: JobDto) => {
    setBusy(true);
    try {
      await agentApi.runJobNow(j.id);
      await load();
      setTab("runs");
    } catch (e) { setMsg(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  };

  const remove = async (j: JobDto) => {
    try { await agentApi.deleteJob(j.id); await load(); }
    catch (e) { setMsg(e instanceof Error ? e.message : String(e)); }
  };

  // 点一个快捷任务：无槽位直接跑；有槽位先展开填写。
  const pickQuick = (p: PromptTemplate) => {
    setQuickErr("");
    if ((p.slots?.length ?? 0) === 0) { runQuick(p); return; }
    setQuickTpl(p);
    setQuickVals({});
  };

  const runQuick = (p: PromptTemplate) => {
    if ((p.slots?.length ?? 0) > 0) {
      const missing = p.slots.filter((s) => !(quickVals[s] ?? "").trim());
      if (missing.length) { setQuickErr("还差：" + missing.join("、")); return; }
    }
    let text = p.template;
    for (const s of p.slots ?? []) text = text.split(`{${s}}`).join(quickVals[s].trim());
    void agentApi.usePrompt(p.id).catch(() => {});
    setQuickTpl(null);
    onRunPrompt?.(text);
  };

  const delPrompt = async (id: string) => {
    try { await agentApi.deletePrompt(id); await load(); }
    catch (e) { setMsg(e instanceof Error ? e.message : String(e)); }
  };

  return (
    <div className="tk-overlay" onClick={onClose}>
      <div className="tk-panel" onClick={(e) => e.stopPropagation()}>
        <header className="tk-head">
          <IconClock size={15} />
          <b>自动化任务</b>
          <span className="tk-sub">定时体检 · 快捷任务；都会先出清单等你确认</span>
          <button className="tk-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="tk-tabs">
          <button className={tab === "jobs" ? "on" : ""} onClick={() => setTab("jobs")}>
            定时任务 <em>{jobs.length}</em>
          </button>
          <button className={tab === "quick" ? "on" : ""} onClick={() => setTab("quick")}>
            快捷任务 <em>{prompts.length}</em>
          </button>
          <button className={tab === "runs" ? "on" : ""} onClick={() => setTab("runs")}>
            运行记录 <em>{runs.length}</em>
          </button>
          <button className="btn ghost sm tk-refresh" onClick={() => void load()}><IconRefresh size={12} />刷新</button>
        </div>

        {msg && <p className="tk-msg">{msg}</p>}

        {tab === "jobs" && (
          <>
            <div className="tk-new">
              <input value={name} placeholder="任务名，如「下班前体检」"
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") void create(); }} />
              <input className="tk-when" value={when} placeholder="每天 17:30"
                onChange={(e) => setWhen(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") void create(); }} />
              <button className="btn primary sm" onClick={() => void create()}
                disabled={busy || !name.trim()}>创建</button>
            </div>
            <p className="tk-hint">
              时间可写：「每天 17:30」「每小时」「每 30 分钟」「工作日 9:00」。
              目前只会做<b>只读体检</b>——定时改表要走你确认，不能无人值守。
            </p>
            <ul className="tk-list">
              {jobs.map((j) => (
                <li key={j.id} className={`tk-job${j.enabled ? " on" : ""}`}>
                  <div className="tk-job-main">
                    <b>{j.name}</b>
                    <span className="tk-sched">{j.when}</span>
                  </div>
                  <div className="tk-job-meta">
                    {j.enabled
                      ? <span className="tk-next">下次 {j.nextRun || "—"}</span>
                      : <span className="tk-paused">已暂停</span>}
                    {j.lastRun && (
                      <span className={j.lastOk ? "tk-ok" : "tk-bad"}>
                        {j.lastOk ? <IconCheck size={11} /> : "!"} 上次 {j.lastRun}
                      </span>
                    )}
                  </div>
                  <div className="tk-job-ops">
                    <button className="btn ghost sm" onClick={() => void runNow(j)} disabled={busy}>现在跑</button>
                    <button className={`btn sm ${j.enabled ? "ghost" : "primary"}`}
                      onClick={() => void toggle(j)}>{j.enabled ? "暂停" : "启用"}</button>
                    <button className="tk-del" onClick={() => void remove(j)} aria-label="删除任务"><IconX size={12} /></button>
                  </div>
                </li>
              ))}
              {jobs.length === 0 && (
                <li className="tk-empty">还没有任务。也可以直接在「对话」里说："每天下班前体检一次"。</li>
              )}
            </ul>
          </>
        )}

        {tab === "quick" && (
          <>
            <p className="tk-hint">
              快捷任务把一次成功的做法存成模板，下次一点即用；触发后仍会<b>出改动清单等你确认</b>。
              要新建：直接编辑工作区的 prompts.yaml，或从下方内置模板开始。
            </p>
            <ul className="tk-list">
              {prompts.map((p) => (
                <li key={p.id} className="tk-job">
                  <div className="tk-job-main">
                    <b>{p.name}</b>
                    {p.when && <span className="tk-sched">{p.when}</span>}
                  </div>
                  <div className="tk-job-meta">
                    {(p.slots?.length ?? 0) > 0
                      ? <span className="tk-next">需填 {p.slots.length} 项</span>
                      : <span className="tk-ok">直接可跑</span>}
                    {!!p.hits && <span>用过 {p.hits} 次</span>}
                  </div>
                  {quickTpl?.id === p.id && (p.slots?.length ?? 0) > 0 && (
                    <div className="tk-slots">
                      {p.slots.map((s) => (
                        <label key={s}><span>{s}</span>
                          <input value={quickVals[s] ?? ""} placeholder={`填「${s}」`}
                            onChange={(e) => setQuickVals((v) => ({ ...v, [s]: e.target.value }))} /></label>
                      ))}
                      {quickErr && <p className="tk-msg">{quickErr}</p>}
                    </div>
                  )}
                  <div className="tk-job-ops">
                    {quickTpl?.id === p.id ? (
                      <>
                        <button className="btn ghost sm" onClick={() => { setQuickTpl(null); setQuickErr(""); }}>取消</button>
                        <button className="btn primary sm" onClick={() => runQuick(p)}>执行</button>
                      </>
                    ) : (
                      <button className="btn primary sm" onClick={() => pickQuick(p)}>
                        {(p.slots?.length ?? 0) > 0 ? "填写并执行" : "运行"}
                      </button>
                    )}
                    <button className="tk-del" onClick={() => void delPrompt(p.id)} aria-label="删除快捷任务"><IconX size={12} /></button>
                  </div>
                </li>
              ))}
              {prompts.length === 0 && (
                <li className="tk-empty">还没有快捷任务。编辑工作区的 prompts.yaml，或使用内置模板。</li>
              )}
            </ul>
          </>
        )}

        {tab === "runs" && (
          <ul className="tk-runs">
            {runs.map((r, i) => (
              <li key={i} className={r.ok ? "ok" : "bad"}>
                <span className="tr-time">{r.started}</span>
                <span className="tr-name">{r.name}</span>
                <span className="tr-sum">{r.summary}</span>
              </li>
            ))}
            {runs.length === 0 && <li className="tk-empty">还没有运行记录。</li>}
          </ul>
        )}
      </div>
    </div>
  );
}

/** 把人话时间翻成 cron。只支持几档常用说法 —— 不猜。 */
export function toCron(s: string): string | null {
  const t = s.trim();
  let m = /^每天\s*(\d{1,2})[:：点](\d{1,2})?/.exec(t);
  if (m) {
    const h = +m[1], mi = m[2] ? +m[2] : 0;
    if (h > 23 || mi > 59) return null;
    return `${mi} ${h} * * *`;
  }
  m = /^工作日\s*(\d{1,2})[:：点](\d{1,2})?/.exec(t);
  if (m) {
    const h = +m[1], mi = m[2] ? +m[2] : 0;
    if (h > 23 || mi > 59) return null;
    return `${mi} ${h} * * 1-5`;
  }
  if (/^每小时$/.test(t)) return "0 * * * *";
  m = /^每\s*(\d{1,3})\s*分钟$/.exec(t);
  if (m) {
    const n = +m[1];
    if (n <= 0 || n > 59) return null;
    return `*/${n} * * * *`;
  }
  // 用户直接写 cron 也接受
  if (t.split(/\s+/).length === 5) return t;
  return null;
}
