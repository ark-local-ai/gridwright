import { useCallback, useEffect, useMemo, useState } from "react";
import { agentApi } from "../api-agent";
import type { Task, TaskStep, Zone } from "../api-agent";
import { IconClock, IconNote, IconPlus, IconRefresh, IconX, IconCheck } from "../components/icons";
import "./ledger.css";
import "./zone.css";

/* 作业区看板。
 *
 * 一个作业区 = 一条「输入 → 收件箱 → 处理」的流水线（见 internal/zones）。
 * 卡片本身就是运行入口：点「试运行」只扫描与去重（不动任何文件），
 * 点「运行」把新文件投递进 inbox 再交给既有 agent（规则/模型、备份、记账）。
 *
 * 一次只跑一个任务（后端 tasks.Queue 单 worker）。看板最多展示 6 个作业区；
 * 运行记录超过 6 条的部分在「历史工作」里，不会被丢掉。
 */

const MAX_CARDS = 6;

const defaultSteps: TaskStep[] = [
  { key: "scan", label: "收料", status: "pending" },
  { key: "dedupe", label: "去重", status: "pending" },
  { key: "deliver", label: "投递", status: "pending" },
  { key: "process", label: "处理", status: "pending" },
  { key: "done", label: "归档", status: "pending" },
];

export default function ZoneBoard({ onOpenLedger, onChanged, syncToken }: {
  onOpenLedger: () => void;
  onChanged?: () => void;
  /** 外部（对话采纳任务）递增：立刻重拉一次，不等下一轮轮询。 */
  syncToken?: number;
}) {
  const [zones, setZones] = useState<Zone[]>([]);
  const [snap, setSnap] = useState<{ runningId: string; items: Task[]; queued: number }>({
    runningId: "", items: [], queued: 0,
  });
  const [history, setHistory] = useState<Task[]>([]);
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState("");
  const [formOpen, setFormOpen] = useState(false);
  const [histOpen, setHistOpen] = useState(false);

  const load = useCallback(async () => {
    try {
      const [z, t, h] = await Promise.all([
        agentApi.zones(),
        agentApi.tasks(),
        agentApi.tasksHistory(80),
      ]);
      setZones(z.items ?? []);
      setSnap(t);
      setHistory(h.items ?? []);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (syncToken) void load();
  }, [syncToken, load]);

  const active = snap.items.some((t) => t.status === "running" || t.status === "queued");
  useEffect(() => {
    if (!active) return;
    const id = window.setInterval(() => { void load(); }, 1500);
    return () => window.clearInterval(id);
  }, [active, load]);

  // 每个作业区取最近一条任务：运行中/排队优先，其次才是最近完成。
  const latest = useMemo(() => {
    const m = new Map<string, Task>();
    for (const t of snap.items) if (!m.has(t.zoneId)) m.set(t.zoneId, t);
    for (const t of history) if (!m.has(t.zoneId)) m.set(t.zoneId, t);
    // 运行中/排队中的任务永远覆盖已完成的同区任务。
    const live = new Map<string, Task>();
    for (const t of [...snap.items, ...history]) {
      if (t.status === "running" || t.status === "queued") {
        if (!live.has(t.zoneId)) live.set(t.zoneId, t);
      }
    }
    for (const [k, v] of live) m.set(k, v);
    return m;
  }, [snap.items, history]);

  const run = async (z: Zone) => {
    setBusy(z.id);
    setMsg("");
    try {
      const dry = z.mode !== "apply";
      const r = await agentApi.runZone(z.id, dry);
      setMsg(`${dry ? "试运行" : "运行"}已加入队列：${z.name}`);
      setSnap((s) => ({ ...s, items: [r.task, ...s.items.filter((x) => x.id !== r.task.id)].slice(0, 6), queued: s.queued + 1 }));
      await load();
      onChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const cancel = async (id: string) => {
    setBusy(id);
    try {
      await agentApi.cancelTask(id);
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const shown = zones.slice(0, MAX_CARDS);
  const rest = zones.length - shown.length;

  return (
    <section className="dcard span2 zone-board">
      <div className="dcard-h">
        <h3>作业区</h3>
        <span className="dcard-n">
          {zones.length} 个 · {snap.queued > 0 ? `${snap.queued} 个排队` : "一次只跑一个"}
        </span>
        <button className="btn ghost sm" onClick={onOpenLedger}><IconNote size={13} />账目 / 回滚</button>
        <button className="btn ghost sm" onClick={() => setHistOpen(true)}><IconClock size={13} />历史工作</button>
        <button className="btn ghost sm" onClick={() => void load()}><IconRefresh size={12} />刷新</button>
        <button className="btn primary sm" onClick={() => setFormOpen((v) => !v)}><IconPlus size={12} />新建</button>
      </div>

      {msg && <p className="zone-msg">{msg}</p>}

      {formOpen && <ZoneForm onDone={() => { setFormOpen(false); void load(); }} onErr={setMsg} />}

      {zones.length === 0 && !formOpen && (
        <p className="dash-muted">还没有作业区。新建一个，把“哪个文件夹的新文件 → 哪张表”定下来，就能点卡片运行。</p>
      )}

      <div className="zone-grid">
        {shown.map((z) => {
          const task = latest.get(z.id);
          const live = task && (task.status === "running" || task.status === "queued");
          const steps = task?.steps?.length ? task.steps : defaultSteps;
          return (
            <article key={z.id} className={`zone-card${task?.status === "running" ? " running" : ""}${live ? " live" : ""}`}>
              <header className="zone-card-h">
                <b title={z.name}>{z.name}</b>
                <span className={`zone-mode${z.mode === "apply" ? " apply" : ""}`}>
                  {z.mode === "apply" ? "真改" : "试运行"}
                </span>
              </header>

              <p className="zone-src" title={z.input.path || ""}>
                {z.input.type === "folder" ? (z.input.path || "未配置目录") : z.input.type === "manual" ? "手工：从收件箱取件" : "系统来源（暂未接入）"}
                {z.target.file ? ` → ${z.target.file}` : ""}
              </p>

              <ol className="zone-chain">
                {steps.map((s) => (
                  <li key={s.key} className={`zone-step ${s.status}`} title={s.detail || ""}>
                    <i aria-hidden />
                    <span>{s.label}</span>
                    {s.detail ? <em>{s.detail}</em> : null}
                  </li>
                ))}
              </ol>

              <footer className="zone-card-f">
                <span className="zone-when">
                  {task
                    ? `${statusText(task)}${task.endedAt ? ` · ${task.endedAt.slice(5, 16)}` : ""}`
                    : "未运行"}
                </span>
                {live ? (
                  <button className="btn ghost sm" disabled={busy === task!.id} onClick={() => void cancel(task!.id)}>
                    {task!.status === "queued" ? "取消排队" : "停止"}
                  </button>
                ) : (
                  <button className="btn primary sm" disabled={busy === z.id} onClick={() => void run(z)}>
                    {busy === z.id ? "提交中…" : z.mode === "apply" ? "运行" : "试运行"}
                  </button>
                )}
              </footer>
            </article>
          );
        })}
      </div>

      {rest > 0 && (
        <p className="dash-muted">另有 {rest} 个作业区，在「历史工作」里查看。</p>
      )}

      {histOpen && (
        <div className="lg-overlay" onClick={() => setHistOpen(false)}>
          <div className="lg-panel" onClick={(e) => e.stopPropagation()}>
            <header className="lg-head">
              <IconClock size={15} />
              <b>历史工作</b>
              <span className="lg-sub">每次运行的步骤、结果与时间；数据不会因为超过 6 个而丢失</span>
              <button className="lg-x" onClick={() => setHistOpen(false)} aria-label="关闭历史"><IconX size={14} /></button>
            </header>
            <div className="lg-body">
              {history.length === 0
                ? <p className="lg-empty">还没有运行记录。点作业区卡片上的「试运行」开始。</p>
                : (
                  <ul className="zone-hist">
                    {history.map((t) => (
                      <li key={t.id} className={`zone-hist-row ${t.status}`}>
                        <span className="zh-title">{t.title}</span>
                        <span className={`ib-badge ${t.status}`}>{statusText(t)}</span>
                        <span className="zh-when">{t.createdAt?.slice(5, 16)}</span>
                        <span className="zh-sum" title={t.error || t.summary || ""}>{t.error || t.summary || "—"}</span>
                        <span className="zh-counts">
                          {(t.counts?.candidates ?? 0)} 候选 · {(t.counts?.delivered ?? 0)} 投递 · {(t.counts?.processed ?? 0)} 成功
                          {t.counts?.failed ? ` · ${t.counts.failed} 失败` : ""}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

function statusText(t: Task): string {
  switch (t.status) {
    case "queued": return "排队中";
    case "running": return "运行中";
    case "ok": return "完成";
    case "failed": return "失败";
    case "canceled": return "已取消";
    default: return t.status;
  }
}

/** 新建作业区的最小表单：名称 / 输入目录 / 目标表 / 模式。 */
function ZoneForm({ onDone, onErr }: { onDone: () => void; onErr: (s: string) => void }) {
  const [name, setName] = useState("");
  const [type, setType] = useState<"folder" | "manual">("folder");
  const [path, setPath] = useState("");
  const [pattern, setPattern] = useState("");
  const [target, setTarget] = useState("");
  const [mode, setMode] = useState<"dryRun" | "apply">("dryRun");
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!name.trim()) { onErr("给作业区起个名字"); return; }
    setSaving(true);
    try {
      // 输入目录是用户亲手填的：先登记路径授权（只读），否则后端会拒绝保存。
      if (type === "folder" && path.trim()) {
        await agentApi.saveGrant(path.trim(), "read");
      }
      await agentApi.saveZone({
        name: name.trim(),
        input: { type, path: type === "folder" ? path.trim() : "", pattern: pattern.trim(), watch: "manual" },
        target: { file: target.trim() },
        mode,
        rules: [],
      });
      onDone();
    } catch (e) {
      onErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="zone-form">
      <input className="lg-input" placeholder="名称（如：发票目录 → 台账）" value={name} onChange={(e) => setName(e.target.value)} />
      <select className="lg-input" value={type} onChange={(e) => setType(e.target.value as "folder" | "manual")}>
        <option value="folder">文件夹输入</option>
        <option value="manual">手工（收件箱）</option>
      </select>
      <select className="lg-input" value={mode} onChange={(e) => setMode(e.target.value as "dryRun" | "apply")}>
        <option value="dryRun">默认试运行</option>
        <option value="apply">允许真改</option>
      </select>
      {type === "folder" && (
        <>
          <input className="lg-input zone-wide" placeholder="输入目录（绝对路径，需已授权）" value={path} onChange={(e) => setPath(e.target.value)} />
          <input className="lg-input" placeholder="文件名匹配（如 *.xlsx，可空）" value={pattern} onChange={(e) => setPattern(e.target.value)} />
        </>
      )}
      <input className="lg-input" placeholder="目标表文件名（工作区内，可空）" value={target} onChange={(e) => setTarget(e.target.value)} />
      <button className="btn primary sm" disabled={saving} onClick={() => void save()}>
        {saving ? "保存中…" : <><IconCheck size={12} />保存</>}
      </button>
    </div>
  );
}
