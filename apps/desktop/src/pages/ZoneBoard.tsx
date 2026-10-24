import { useCallback, useEffect, useMemo, useState } from "react";
import { agentApi } from "../api-agent";
import type { Proposal, Task, TaskStep, Zone } from "../api-agent";
import { IconClock, IconNote, IconPlus, IconRefresh, IconX, IconCheck, IconSpark } from "../components/icons";
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

export default function ZoneBoard({ onOpenLedger, syncToken, onOpenChat, onOpenConversation, onOpenProposal, onOpenInbox }: {
  onOpenLedger: () => void;
  /** 外部（对话采纳任务）递增：立刻重拉一次，不等下一轮轮询。 */
  syncToken?: number;
  /** 看板只展示对话创建的任务；没有时给一个去对话的入口。 */
  onOpenChat?: () => void;
  /** 回到「创建该任务的那段对话」，并带上「再跑一次」的预填文案。 */
  onOpenConversation?: (convoId: string, prefill: string) => void;
  /** 卡片上有待确认改动时，把取回的清单交给上层展示。 */
  onOpenProposal?: (p: Proposal) => void;
  /** 有待确认文件时，跳到收件箱。 */
  onOpenInbox?: () => void;
}) {
  const [zones, setZones] = useState<Zone[]>([]);
  const [snap, setSnap] = useState<{ runningId: string; items: Task[]; queued: number }>({
    runningId: "", items: [], queued: 0,
  });
  const [history, setHistory] = useState<Task[]>([]);
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState("");
  const [formOpen, setFormOpen] = useState(false);
  /** 正在编辑的作业区（null = 新建）：复用同一张表单预填。 */
  const [editZone, setEditZone] = useState<Zone | null>(null);
  const [histOpen, setHistOpen] = useState(false);
  /** 历史工作搜索词：按标题/状态客户端过滤。 */
  const [histQ, setHistQ] = useState("");
  /** 展开了右上角小菜单的卡片 id。 */
  const [menuFor, setMenuFor] = useState<string | null>(null);
  /** 正在取清单的卡片 id（防重复点）。 */
  const [propBusy, setPropBusy] = useState("");

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

  // 看板只展示**对话创建**的任务：手动触发的运行只进历史，不占卡片。
  const chatTasks = useMemo(
    () => [...snap.items, ...history].filter((t) => t.source === "chat"),
    [snap.items, history],
  );

  // 每个作业区取最近一条对话任务：运行中/排队优先，其次才是最近完成。
  const latest = useMemo(() => {
    const m = new Map<string, Task>();
    for (const t of chatTasks) if (!m.has(t.zoneId)) m.set(t.zoneId, t);
    const live = new Map<string, Task>();
    for (const t of chatTasks) {
      if (t.status === "running" || t.status === "queued") {
        if (!live.has(t.zoneId)) live.set(t.zoneId, t);
      }
    }
    for (const [k, v] of live) m.set(k, v);
    return m;
  }, [chatTasks]);

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

  // 重试失败文件：把这条任务重新排队，成功后立刻刷新看板。
  const retry = async (task: Task) => {
    setBusy(task.id);
    try {
      await agentApi.retryTask(task.id);
      await load();
      setMsg(`已重新排队：${task.title}`);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  // 查看待确认：取第一条清单 id 回看，交给上层弹出待确认面板。
  const openProposal = async (task: Task) => {
    const id = task.proposalIds?.[0];
    if (!id) return;
    setPropBusy(task.id);
    try {
      const p = await agentApi.planGet(id);
      if (onOpenProposal) onOpenProposal(p);
      else setMsg("这份清单已过期，请在对话里重新派单");
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setPropBusy("");
    }
  };

  // 删除作业区：只删这条流水线配置，已跑过的账目与任务记录都还在。
  const removeZone = async (z: Zone) => {
    if (!window.confirm(`删除作业区「${z.name}」？\n只删这条流水线配置，已跑过的账目和历史记录都还在。`)) return;
    setBusy(z.id);
    setMenuFor(null);
    try {
      await agentApi.deleteZone(z.id);
      await load();
      setMsg(`已删除作业区：${z.name}`);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const startEdit = (z: Zone) => {
    setEditZone(z);
    setMenuFor(null);
    setFormOpen(true);
  };

  // 历史工作：按标题/状态过滤（客户端，不改动“只展示对话创建”的口径）。
  const histShown = useMemo(() => {
    const q = histQ.trim().toLowerCase();
    if (!q) return history;
    return history.filter((t) =>
      t.title.toLowerCase().includes(q) ||
      statusText(t).toLowerCase().includes(q) ||
      t.status.toLowerCase().includes(q));
  }, [history, histQ]);

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
        <button className="btn primary sm" onClick={() => onOpenChat?.()}><IconSpark size={12} />去对话派单</button>
        <button className="btn ghost sm" onClick={() => {
          // 正在编辑时点「新建」：清掉预填、表单留着开一个新的。
          if (editZone) { setEditZone(null); setFormOpen(true); return; }
          setFormOpen((v) => !v);
        }}><IconPlus size={12} />新建</button>
      </div>

      {msg && <p className="zone-msg">{msg}</p>}

      {formOpen && (
        <ZoneForm
          key={editZone?.id ?? "new"}
          zone={editZone ?? undefined}
          onDone={() => { setFormOpen(false); setEditZone(null); setMsg(""); void load(); }}
          onCancel={editZone ? () => { setFormOpen(false); setEditZone(null); } : undefined}
          onErr={setMsg}
        />
      )}

      {zones.length === 0 && !formOpen && (
        <p className="dash-muted">还没有作业区。新建一个，把“哪个文件夹的新文件 → 哪张表”定下来；之后在对话里说“把某个区跑一次”，任务会出现在这里。</p>
      )}

      <div className="zone-grid">
        {shown.map((z) => {
          const task = latest.get(z.id);
          const live = task && (task.status === "running" || task.status === "queued");
          const steps = task?.steps?.length ? task.steps : defaultSteps;
          const prefill = z.mode === "apply" ? `把「${z.name}」再跑一次` : `把「${z.name}」再跑一次（试运行）`;
          const convoId = task?.conversationId;
          const pending = task?.counts?.pending ?? 0;
          const proposals = task?.proposalIds?.length ?? 0;
          const failed = task?.failedFiles?.length ?? 0;
          // 卡片即入口：优先回到创建该任务的对话（在那里再派单），否则去对话页。
          const openConvo = () => {
            if (convoId && onOpenConversation) onOpenConversation(convoId, prefill);
            else onOpenChat?.();
          };
          return (
            <article
              key={z.id}
              role="button"
              tabIndex={0}
              aria-label={`回到创建「${z.name}」的对话并再跑一次`}
              className={`zone-card${task?.status === "running" ? " running" : ""}${live ? " live" : ""}`}
              onClick={openConvo}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " " || e.key === "Spacebar") {
                  e.preventDefault();
                  openConvo();
                }
              }}
            >
              <header className="zone-card-h">
                <b title={z.name}>{z.name}</b>
                {convoId ? <span className="zone-from" title={convoId}>来自对话</span> : null}
                <span className={`zone-mode${z.mode === "apply" ? " apply" : ""}`}>
                  {z.mode === "apply" ? "真改" : "试运行"}
                </span>
                {/* 轻量菜单：编辑复用同一张表单，删除只删这条配置。 */}
                <span className="zone-more-wrap">
                  <button
                    className="zone-more"
                    aria-label={`${z.name} 的更多操作`}
                    aria-haspopup="menu"
                    aria-expanded={menuFor === z.id}
                    title="更多操作"
                    onClick={(e) => { e.stopPropagation(); setMenuFor((v) => (v === z.id ? null : z.id)); }}
                  >⋯</button>
                  {menuFor === z.id && (
                    <span className="zone-menu" role="menu" onClick={(e) => e.stopPropagation()}>
                      <button role="menuitem" className="zone-menu-i" onClick={() => startEdit(z)}>编辑</button>
                      <button role="menuitem" className="zone-menu-i danger" disabled={busy === z.id}
                        onClick={() => void removeZone(z)}>删除</button>
                    </span>
                  )}
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
                  <button
                    className="btn ghost sm"
                    disabled={busy === task!.id}
                    onClick={(e) => { e.stopPropagation(); void cancel(task!.id); }}
                  >
                    {task!.status === "queued" ? "取消排队" : "停止"}
                  </button>
                ) : (
                  <>
                    {proposals > 0 ? (
                      <button
                        className="btn ghost sm"
                        disabled={propBusy === task!.id}
                        onClick={(e) => { e.stopPropagation(); void openProposal(task!); }}
                        title="查看这次运行待确认的改动"
                      >
                        查看待确认 {proposals} 条
                      </button>
                    ) : null}
                    {failed > 0 ? (
                      <button
                        className="btn ghost sm"
                        disabled={busy === task!.id}
                        onClick={(e) => { e.stopPropagation(); void retry(task!); }}
                        title={task!.failedFiles?.join("、") || "重试处理失败的文件"}
                      >
                        重试失败 {failed} 个
                      </button>
                    ) : null}
                    <button
                      className="btn ghost sm"
                      onClick={(e) => { e.stopPropagation(); openConvo(); }}
                      title="回到创建它的对话，在那里再派单一次"
                    >
                      回到对话
                    </button>
                    {pending > 0 ? (
                      <button
                        className="btn ghost sm"
                        onClick={(e) => { e.stopPropagation(); onOpenInbox?.(); }}
                        title="有文件等你确认"
                      >
                        去收件箱 {pending}
                      </button>
                    ) : null}
                  </>
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
              <input
                className="lg-input zh-search"
                value={histQ}
                placeholder="按标题 / 状态搜索…"
                aria-label="搜索历史工作"
                onChange={(e) => setHistQ(e.target.value)}
              />
              <button className="lg-x" onClick={() => setHistOpen(false)} aria-label="关闭历史"><IconX size={14} /></button>
            </header>
            <div className="lg-body">
              {history.length === 0
                ? <p className="lg-empty">还没有运行记录。点作业区卡片上的「试运行」开始。</p>
                : histShown.length === 0
                  ? <p className="lg-empty">没有标题或状态匹配「{histQ.trim()}」的记录。</p>
                  : (
                  <ul className="zone-hist">
                    {histShown.map((t) => (
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

/** 作业区表单：名称 / 输入目录 / 目标表 / 模式。传 zone 即为编辑（预填，保存时合并回原对象）。 */
function ZoneForm({ zone, onDone, onCancel, onErr }: {
  /** 要编辑的作业区；不传 = 新建。 */
  zone?: Zone;
  onDone: () => void;
  onCancel?: () => void;
  onErr: (s: string) => void;
}) {
  const [name, setName] = useState(zone?.name ?? "");
  const [type, setType] = useState<"folder" | "manual">(zone?.input?.type === "manual" ? "manual" : "folder");
  const [path, setPath] = useState(zone?.input?.path ?? "");
  const [pattern, setPattern] = useState(zone?.input?.pattern ?? "");
  const [target, setTarget] = useState(zone?.target?.file ?? "");
  const [mode, setMode] = useState<"dryRun" | "apply">(zone?.mode === "apply" ? "apply" : "dryRun");
  // 编辑已有作业区时路径早已授权过，不必再让用户勾一次（改路径会重新登记）。
  const [granted, setGranted] = useState(Boolean(zone));
  const [saving, setSaving] = useState(false);
  const needGrant = type === "folder";
  const canSave = !saving && (!needGrant || granted);

  const save = async () => {
    if (!name.trim()) { onErr("给作业区起个名字"); return; }
    if (needGrant && !granted) { onErr("先勾选授权：该作业区只读访问此目录"); return; }
    setSaving(true);
    try {
      // 输入目录是用户亲手填的：先登记路径授权（只读），否则后端会拒绝保存。
      if (type === "folder" && path.trim()) {
        await agentApi.saveGrant(path.trim(), "read");
      }
      await agentApi.saveZone({
        ...(zone ?? {}),
        name: name.trim(),
        input: { ...(zone?.input ?? {}), type, path: type === "folder" ? path.trim() : "", pattern: pattern.trim(), watch: "manual" },
        target: { ...(zone?.target ?? {}), file: target.trim() },
        mode,
        rules: zone?.rules ?? [],
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
      {zone && <p className="zone-form-title zone-wide">编辑作业区：{zone.name}</p>}
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
          <label className="zone-grant zone-wide">
            <input type="checkbox" checked={granted} onChange={(e) => setGranted(e.target.checked)} required />
            <span>我已授权该作业区只读访问此目录（可随时撤销）</span>
          </label>
          <p className="zone-grant-note zone-wide">只授予该目录的只读权限，不会获得整台电脑的访问权。</p>
        </>
      )}
      <input className="lg-input" placeholder="目标表文件名（工作区内，可空）" value={target} onChange={(e) => setTarget(e.target.value)} />
      <button className="btn primary sm" disabled={!canSave} onClick={() => void save()}>
        {saving ? "保存中…" : <><IconCheck size={12} />保存</>}
      </button>
      {onCancel && (
        <button className="btn ghost sm" onClick={onCancel}>取消</button>
      )}
    </div>
  );
}
