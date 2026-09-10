import { useCallback, useEffect, useState } from "react";
import {
  listUsers, setUserDisabled, deleteUser,
  type AdminUserDto,
} from "../api";

// 设置面板内的「用户管理」区（M68）：账号/角色/用量 + 禁用/删除。
// 从独立管理页收敛到这里，仅管理员可读（后端 users 路由会 403 非管理员）。
export default function SettingsUsers() {
  const [users, setUsers] = useState<AdminUserDto[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try { setUsers(await listUsers()); setErr(null); }
    catch (e) { setUsers(null); setErr(e instanceof Error ? e.message : "加载失败（可能非管理员或后端未启动）"); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const act = async (label: string, fn: () => Promise<void>) => {
    setBusy(label);
    try { await fn(); await load(); }
    catch (e) { setErr(e instanceof Error ? e.message : "操作失败"); }
    finally { setBusy(null); }
  };

  if (err && !users) {
    return (
      <div className="tcard" style={{ maxWidth: 560 }}>
        <div className="empty" style={{ height: 90, justifyContent: "center" }}>{err}</div>
        <button className="btn ghost" onClick={load}>重试</button>
      </div>
    );
  }

  const activeUsers = (users ?? []).filter((u) => !u.disabled).length;
  const totalTasks = (users ?? []).reduce((s, u) => s + u.tasks, 0);

  return (
    <>
      <div className="hint-wrap" style={{ marginBottom: 14 }}>
        <span style={{ fontSize: 13.5 }}>多人共用服务端的账号与用量 —— 首个注册用户为管理员，可禁用/删除成员。</span>
      </div>
      <div className="stats-grid" style={{ marginBottom: 14 }}>
        <div className="tcard stat-card">
          <div className="t-h"><b>账号</b><span className="pill ghost">{users?.length ?? "—"} 人</span></div>
          <div className="stats-rows">
            <div className="cfg"><span>启用</span><span className="st-num">{activeUsers}</span></div>
            <div className="cfg"><span>管理员</span><span className="st-num">{(users ?? []).filter((u) => u.isAdmin).length}</span></div>
            <div className="cfg"><span>历史任务合计</span><span className="st-num">{totalTasks}</span></div>
          </div>
        </div>
      </div>
      {err && <div style={{ color: "var(--danger)", fontSize: 12.5, marginBottom: 8 }}>{err}</div>}
      {(!users || users.length === 0) ? (
        <div className="empty" style={{ height: 80, justifyContent: "center" }}>暂无用户</div>
      ) : (
        <table className="job-table">
          <thead>
            <tr>
              <th>用户</th><th>角色</th><th>状态</th><th>任务</th><th>记忆</th><th>会话</th><th>最近活跃</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>
                  <b>{u.displayName}</b>
                  <div style={{ fontSize: 12, color: "var(--text-3)" }}>@{u.username}</div>
                </td>
                <td>{u.isAdmin ? <span className="badge run">管理员</span> : <span className="badge queue">成员</span>}</td>
                <td>{u.disabled ? <span className="badge warn">已禁用</span> : <span className="badge done">正常</span>}</td>
                <td>{u.tasks}</td>
                <td>{u.memories}</td>
                <td>{u.sessions}</td>
                <td style={{ fontSize: 12, color: "var(--text-3)" }}>{u.lastActive ?? "—"}</td>
                <td>
                  {!u.isAdmin ? (
                    <div style={{ display: "inline-flex", gap: 6 }}>
                      <button className="btn ghost" style={{ padding: "3px 9px" }} disabled={!!busy}
                        onClick={() => act(`toggle:${u.id}`, () => setUserDisabled(u.id, !u.disabled))}>
                        {u.disabled ? "启用" : "禁用"}
                      </button>
                      <button className="btn danger" style={{ padding: "3px 9px" }} disabled={!!busy}
                        onClick={() => { if (window.confirm(`删除用户 ${u.displayName} 及其全部数据？`)) act(`del:${u.id}`, () => deleteUser(u.id)); }}>
                        删除
                      </button>
                    </div>
                  ) : (
                    <span style={{ fontSize: 12, color: "var(--text-3)" }}>—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
