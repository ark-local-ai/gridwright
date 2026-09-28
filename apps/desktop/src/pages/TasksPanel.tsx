import { useEffect, useState } from "react";
import "./tasks.css";
import { agentApi } from "../api-agent";
import type { JobDto, JobRunDto } from "../api-agent";
import { IconRefresh, IconClock, IconCheck, IconX } from "../components/icons";

/* 自动化任务（见 docs/agent-architecture/29-UI升级与剩余功能.md）
   让"每天下班前体检一次"这句话真的能落地成一条会跑的定时任务。
   默认**暂停**：定时改你文件的开关必须由你亲自打开。 */

export default function TasksPanel({ onClose }: { onClose: () => void }) {
  const [jobs, setJobs] = useState<JobDto[]>([]);
  const [runs, setRuns] = useState<JobRunDto[]>([]);
  const [name, setName] = useState("");
  const [when, setWhen] = useState("每天 17:30");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [tab, setTab] = useState<"jobs" | "runs">("jobs");

  const load = async () => {
    try {
      const [j, r] = await Promise.all([
        agentApi.jobs().catch(() => ({ jobs: [] })),
        agentApi.jobRuns().catch(() => ({ runs: [] })),
      ]);
      setJobs(j.jobs ?? []);
      setRuns(r.runs ?? []);
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

  return (
    <div className="tk-overlay" onClick={onClose}>
      <div className="tk-panel" onClick={(e) => e.stopPropagation()}>
        <header className="tk-head">
          <IconClock size={15} />
          <b>自动化任务</b>
          <span className="tk-sub">到点自己跑；默认暂停，开关由你打开</span>
          <button className="tk-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="tk-tabs">
          <button className={tab === "jobs" ? "on" : ""} onClick={() => setTab("jobs")}>
            任务 <em>{jobs.length}</em>
          </button>
          <button className={tab === "runs" ? "on" : ""} onClick={() => setTab("runs")}>
            运行记录 <em>{runs.length}</em>
          </button>
          <button className="btn ghost sm tk-refresh" onClick={() => void load()}><IconRefresh size={12} />刷新</button>
        </div>

        {msg && <p className="tk-msg">{msg}</p>}

        {tab === "jobs" ? (
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
        ) : (
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
