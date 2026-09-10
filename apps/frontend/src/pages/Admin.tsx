import { useCallback, useEffect, useState } from "react";
import {
  listUsers, listAudit, runCleanup, setUserDisabled, deleteUser,
  type AdminUserDto, type AuditEntryDto,
} from "../api";

// 审计动作标签太长时截断（保留要点）
function shortAction(a: string): string {
  const map: Record<string, string> = {
    "创建任务": "创建任务", "重试任务": "重试任务", "删除任务": "删除任务", "归档/取消归档任务": "归档任务",
    "另存为模板": "另存模板", "创建渠道": "创建渠道", "设置默认渠道": "置默认渠道", "验活渠道": "验活渠道",
    "创建技能": "创建技能", "创建专家": "创建专家", "创建模板": "创建模板",
    "禁用用户": "禁用用户", "启用用户": "启用用户", "删除用户": "删除用户", "注册账号": "注册账号",
    "登录": "登录", "登出": "登出", "手动清理": "手动清理",
  };
  return map[a] ?? a;
}

export default function Admin() {
  const [users, setUsers] = useState<AdminUserDto[] | null>(null);
  const [audit, setAudit] = useState<AuditEntryDto[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null); // 正在处理的目标 id

  const load = useCallback(async () => {
    try {
      const [u, a] = await Promise.all([listUsers(), listAudit(120)]);
      setUsers(u);
      setAudit(a);
      setErr(null);
    } catch (e) {
      setUsers(null);
      setErr(e instanceof Error ? e.message : "加载失败（可能非管理员或后端未启动）");
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const act = async (label: string, fn: () => Promise<void>) => {
    setBusy(label);
    try {
      await fn();
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "操作失败");
    } finally {
      setBusy(null);
    }
  };

  // 非管理员 / 后端不可达
  if (err && !users) {
    return (
      <div className="page">
        <div className="tcard" style={{ maxWidth: 560 }}>
          <div className="t-h"><b>管理控制台</b></div>
          <div className="empty" style={{ height: 90, justifyContent: "center" }}>{err}</div>
          <button className="btn ghost" onClick={load}>重试</button>
        </div>
      </div>
    );
  }

  const totalTasks = (users ?? []).reduce((s, u) => s + u.tasks, 0);
  const activeUsers = (users ?? []).filter((u) => !u.disabled).length;

  return (
    <div className="page">
      <div className="stats-grid" style={{ marginBottom: 14 }}>
        <div className="tcard stat-card">
          <div className="t-h"><b>用户</b><span className="pill ghost">{users?.length ?? "—"} 人</span></div>
          <div className="stats-rows">
            <div className="cfg"><span>启用</span><span className="st-num">{activeUsers}</span></div>
            <div className="cfg"><span>管理员</span><span className="st-num">{(users ?? []).filter((u) => u.isAdmin).length}</span></div>
            <div className="cfg"><span>历史任务合计</span><span className="st-num">{totalTasks}</span></div>
          </div>
        </div>
        <div className="tcard stat-card">
          <div className="t-h"><b>运维动作</b></div>
          <div className="stats-rows">
            <div className="cfg"><span>清理过期任务/交付</span>
              <button className="btn ghost" style={{ padding: "3px 10px" }} disabled={!!busy}
                onClick={() => act("cleanup", () => runCleanup().then(() => undefined))}>
                {busy === "cleanup" ? "清理中…" : "手动清理"}
              </button>
            </div>
            <div className="cfg"><span>审计日志</span><span className="st-num">{audit.length} 条最近</span></div>
          </div>
        </div>
      </div>

      {/* 用户管理 */}
      <div className="tcard" style={{ marginBottom: 14 }}>
        <div className="t-h"><b>用户管理</b><span className="pill ghost">首个注册用户为管理员</span></div>
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
                    {!u.isAdmin && (
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
                    )}
                    {u.isAdmin && <span style={{ fontSize: 12, color: "var(--text-3)" }}>—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* 审计日志 */}
      <div className="tcard">
        <div className="t-h"><b>最近操作（审计）</b><button className="btn ghost" onClick={load} disabled={!!busy}>刷新</button></div>
        {audit.length === 0 ? (
          <div className="empty" style={{ height: 80, justifyContent: "center" }}>暂无审计记录</div>
        ) : (
          <table className="job-table">
            <thead>
              <tr><th>时间</th><th>用户</th><th>动作</th><th>目标</th></tr>
            </thead>
            <tbody>
              {audit.slice(0, 40).map((a) => (
                <tr key={a.id}>
                  <td style={{ whiteSpace: "nowrap", fontSize: 12, color: "var(--text-3)" }}>{a.ts}</td>
                  <td style={{ fontSize: 12.5 }}>{a.userName ?? "匿名"}</td>
                  <td><span className="badge queue">{shortAction(a.action)}</span></td>
                  <td style={{ fontSize: 12.5, color: "var(--text-3)", wordBreak: "break-all" }}>{a.target ?? a.detail ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
