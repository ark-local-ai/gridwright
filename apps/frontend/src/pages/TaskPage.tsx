import { useEffect, useRef, useState } from "react";
import { SkPanel } from "../components/Skeleton";
import { useSearchParams, useNavigate } from "react-router-dom";
import { IconCheck } from "../components/icons";
import { ftColor, ftLabel } from "../data/mock";
import { getTask, subscribeTask, retryTask, deleteTask, workspaceUrl, type TaskDto } from "../api";

export default function TaskPage() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const taskId = params.get("taskId");
  const [task, setTask] = useState<TaskDto | null>(null);
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  const taskRef = useRef(task);
  taskRef.current = task;

  // 拉取任务快照
  useEffect(() => {
    if (!taskId) return;
    setMissing(false);
    getTask(taskId)
      .then(setTask)
      .catch(() => setMissing(true));
  }, [taskId]);

  // 订阅 SSE 实时进度
  useEffect(() => {
    if (!taskId) return;
    const unsub = subscribeTask(taskId, (ev) => {
      const cur = taskRef.current;
      if (!cur) return;
      const next = { ...cur, steps: [...cur.steps] };
      if (ev.type === "plan") {
        next.steps = (ev.data as TaskDto["steps"]).map((s, i) => ({
          ...(cur.steps[i] ?? { id: 0 }), ...s,
        }));
      } else if (ev.type === "step") {
        const d = ev.data as { stepId: number; status: string };
        const st = next.steps.find((s) => s.id === d.stepId)
          ?? next.steps.find((s) => s.status === "running");
        if (st) st.status = d.status as TaskDto["steps"][0]["status"];
      } else if (ev.type === "deliver") {
        next.deliverable = ev.data as TaskDto["deliverable"];
      } else if (ev.type === "check") {
        const checks = ev.data as TaskDto["checks"];
        next.checks = checks;
        if (checks.every((c) => c.ok)) next.status = "done";
      } else if (ev.type === "done") {
        next.status = "done";
      } else if (ev.type === "error") {
        // M16 失败兜底广播 error：UI 立即翻转为失败态
        next.status = "failed";
      }
      setTask(next);
    });
    return unsub;
  }, [taskId]);

  if (missing) {
    return (
      <div className="page">
        <div className="empty" style={{ height: 300 }}>
          任务不存在或后端未启动
        </div>
      </div>
    );
  }
  if (!task) {
    return (
      <div className="page">
        <div className="sk-group" style={{ height: 300, padding: 24 }}><SkPanel rows={6} /></div>
      </div>
    );
  }

  const badge =
    task.status === "done" ? <span className="badge done">已完成</span>
    : task.status === "running" ? <span className="badge run"><span className="live-pulse" style={{ width: 6, height: 6, borderRadius: 999, background: "currentColor", display: "inline-block" }} />执行中</span>
    : task.status === "failed" ? <span className="badge warn">失败</span>
    : <span className="badge queue">排队中</span>;

  // 管道四阶段：输入 → 处理 → 校验 → 交付（映射当前任务状态）
  const stages = (() => {
    const running = task.status === "running";
    const done = task.status === "done";
    const hasChecks = task.checks.length > 0;
    const allChecked = task.checks.length > 0 && task.checks.every((c) => c.ok);
    return [
      { label: "输入", s: "done" as const },
      { label: "处理", s: done ? ("done" as const) : running ? ("run" as const) : ("wait" as const) },
      { label: "校验", s: done || (allChecked && running) ? ("done" as const) : running && hasChecks ? ("run" as const) : ("wait" as const) },
      { label: "交付", s: done ? ("done" as const) : running ? ("wait" as const) : ("wait" as const) },
    ];
  })();

  const doRetry = async () => {
    if (!taskId || busy) return;
    setBusy(true);
    try {
      await retryTask(taskId);
      setTask({ ...task, status: "queue" });
      // 重新连 SSE 拿进度（retry 消息已发出，订阅覆盖后续）
      getTask(taskId).then(setTask).catch(() => {});
    } catch {
      // 提示由调用失败静默（后端不可达等）
    } finally {
      setBusy(false);
    }
  };

  const doDelete = async () => {
    if (!taskId) return;
    try {
      await deleteTask(taskId);
      nav("/app/task");
    } catch { /* 忽略 */ }
  };


  return (
    <div className="page">
      <div className="taskwrap">
        <div className="task-col">
          <div className="pipe-stages">
            {stages.map((st, i) => (
              <span key={st.label} style={{ display: "contents" }}>
                <span className={`ps ${st.s}`}>
                  <span className="ps-n">{st.s === "done" ? <IconCheck size={11} /> : i + 1}</span>{st.label}
                </span>
                {i < stages.length - 1 && (
                  <span className={`ps-line ${stages[i + 1].s === "done" ? "done" : stages[i + 1].s === "run" ? "run" : ""}`} />
                )}
              </span>
            ))}
          </div>
          <div className="tcard">
            <div className="t-h">
              <b>{task.title}</b>{badge}
              <span className="t-actions">
                {(task.status === "failed" || task.status === "done") && (
                  <button className="btn ghost sm" disabled={busy} onClick={doRetry}>重试</button>
                )}
                <button className="btn ghost sm danger" onClick={doDelete}>删除</button>
              </span>
            </div>
            <div className="prompt-bar"><span>{task.prompt}</span></div>
            <ul className="steps">
              {task.steps.map((s) => {
                const cls = s.status;
                const dot = cls === "done" ? "✓" : cls === "running" ? "" : "";
                return (
                  <li key={s.id} className={`step ${cls === "running" ? "cur" : ""}`}>
                    <span className={`dot ${cls === "done" ? "done" : cls === "running" ? "run" : "wait"}`}>
                      {cls === "running" ? "●" : dot}
                    </span>
                    <span className="txt">{s.title}</span>
                    <span className="sub">
                      {cls === "done" ? "已完成" : cls === "running" ? "执行中" : "待执行"}
                    </span>
                  </li>
                );
              })}
            </ul>
          </div>

          {task.deliverable && (
            <div className="tcard">
              <div className="t-h"><b>交付成果</b></div>
              <div className="file deliver">
                <div className="ftext" style={{ background: ftColor[task.deliverable.kind] }}>
                  {ftLabel(task.deliverable.kind)}
                </div>
                <div>
                  <div className="fn">{task.deliverable.name}</div>
                  <div className="fm">{task.deliverable.note}</div>
                </div>
                <a className="btn primary sm" href={workspaceUrl(task.deliverable.name)} download>
                  下载
                </a>
              </div>
            </div>
          )}
        </div>

        <div className="rail">
          <div className="rbox">
            <h3>目标验收清单</h3><div className="rsub">每轮执行后自动核对</div>
            {task.checks.map((c, i) => (
              <div key={i} className={`ck${c.ok ? " on" : ""}`}>
                <span className="bx">{c.ok && <IconCheck size={11} />}</span>{c.label}
              </div>
            ))}
          </div>
          <div className="rbox">
            <h3>本次执行配置</h3><div className="rsub">仅对本次任务生效</div>
            <div className="cfg">模型 <span>{task.model}</span></div>
            <div className="cfg">专家 <span>{task.expert}</span></div>
            <div className="cfg">技能 <span>{task.skills.join("、")}</span></div>
            <div className="cfg">工作空间 <span>{task.workspace}</span></div>
          </div>
          <div className="rbox">
            <h3>执行时间线</h3>
            <ul className="tl">
              {task.timeline.map((t, i) => (
                <li key={i}><span className="tdot"></span><span className="tm">{t.time}</span>{t.label}</li>
              ))}
            </ul>
          </div>
        </div>
      </div>
    </div>
  );
}
