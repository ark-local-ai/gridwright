import { useCallback, useEffect, useState } from "react";
import { useLocation, NavLink } from "react-router-dom";
import {
  listUsers, listAudit, runCleanup, setUserDisabled, deleteUser,
  type AdminUserDto, type AuditEntryDto,
} from "../api";
import { IconUsers, IconShield, IconGear } from "../components/icons";

const TABS = [
  { to: "/app/admin", label: "用户", icon: <IconUsers size={14} />, desc: "账号、角色与用量 —— 谁在共用这台机器，管理/禁用/删除。" },
  { to: "/app/admin/audit", label: "审计", icon: <IconShield size={14} />, desc: "最近操作记录 —— 谁在何时做了什么写操作。" },
  { to: "/app/admin/ops", label: "运维", icon: <IconGear size={14} />, desc: "运维动作 —— 清理过期任务与交付、查看服务状态。" },
];

export default function Admin() {
  const loc = useLocation();
  const active = TABS.find((t) => t.to === loc.pathname) ?? TABS[0];
  const view = active.label === "审计" ? "audit" : active.label === "运维" ? "ops" : "users";

  return (
    <div className="page">
      <header className="pn-hd">
        <nav className="pn-tabs">
          {TABS.map((t) => (
            <NavLink key={t.to} to={t.to} className={({ isActive }) => `pn-tab${isActive ? " on" : ""}`}>
              {t.icon}{t.label}
            </NavLink>
          ))}
        </nav>
        <p className="pn-desc">{active.desc}</p>
      </header>

      {view === "users" && <UsersView />}
      {view === "audit" && <AuditView />}
      {view === "ops" && <OpsView />}
    </div>
  );
}

// ---- 用户管理 ----
function UsersView() {
  const [users, setUsers] = useState<AdminUserDto[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setUsers(await listUsers());
      setErr(null);
    } catch (e) {
      setUsers(null);
      setErr(e instanceof Error ? e.message : "加载失败（可能非管理员或后端未启动）");
    }
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

  const totalTasks = (users ?? []).reduce((s, u) => s + u.tasks, 0);
  const activeUsers = (users ?? []).filter((u) => !u.disabled).length;

  return (
    <>
      <div className="stats-grid" style={{ marginBottom: 14 }}>
        <div className="tcard stat-card">
          <div className="t-h"><b>用户</b><span className="pill ghost">{users?.length ?? "—"} 人</span></div>
          <div className="stats-rows">
            <div className="cfg"><span>启用</span><span className="st-num">{activeUsers}</span></div>
            <div className="cfg"><span>管理员</span><span className="st-num">{(users ?? []).filter((u) => u.isAdmin).length}</span></div>
            <div className="cfg"><span>历史任务合计</span><span className="st-num">{totalTasks}</span></div>
          </div>
        </div>
      </div>

      <div className="tcard">
        <div className="t-h"><b>账号列表</b><span className="pill ghost">首个注册用户为管理员</span></div>
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
    </>
  );
}

// ---- 审计日志 ----
function AuditView() {
  const [audit, setAudit] = useState<AuditEntryDto[]>([]);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    try { setAudit(await listAudit(120)); setErr(null); }
    catch (e) { setErr(e instanceof Error ? e.message : "加载失败"); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const shortAction = actionShort;

  return (
    <div className="tcard">
      <div className="t-h"><b>最近操作</b><button className="btn ghost" onClick={load}>刷新</button></div>
      {err && <div style={{ color: "var(--danger)", fontSize: 12.5, marginBottom: 8 }}>{err}</div>}
      {audit.length === 0 ? (
        <div className="empty" style={{ height: 80, justifyContent: "center" }}>暂无审计记录</div>
      ) : (
        <table className="job-table">
          <thead>
            <tr><th>时间</th><th>用户</th><th>动作</th><th>目标</th></tr>
          </thead>
          <tbody>
            {audit.slice(0, 50).map((a) => (
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
  );
}

function actionShort(a: string): string {
  const map: Record<string, string> = {
    "创建任务": "创建任务", "重试任务": "重试任务", "删除任务": "删除任务", "归档/取消归档任务": "归档任务",
    "另存为模板": "另存模板", "创建渠道": "创建渠道", "设置默认渠道": "置默认渠道", "验活渠道": "验活渠道",
    "创建技能": "创建技能", "创建专家": "创建专家", "创建模板": "创建模板",
    "禁用用户": "禁用用户", "启用用户": "启用用户", "删除用户": "删除用户", "注册账号": "注册账号",
    "登录": "登录", "登出": "登出", "手动清理": "手动清理",
  };
  return map[a] ?? a;
}

// ---- 运维 ----
function OpsView() {
  const [auditCount, setAuditCount] = useState<number | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try { const a = await listAudit(1); setAuditCount(a.length); setMsg(null); }
    catch { /* ignore */ }
  }, []);

  useEffect(() => { load(); }, [load]);

  const cleanup = async () => {
    setBusy(true);
    try {
      const r = await runCleanup();
      setMsg(`清理完成${r.summary ? `：${r.summary}` : ""}`);
      load();
    } catch (e) {
      setMsg(e instanceof Error ? `清理失败：${e.message}` : "清理失败");
    } finally { setBusy(false); }
  };

  return (
    <div style={{ display: "grid", gap: 14, maxWidth: 720 }}>
      <div className="tcard">
        <div className="t-h"><b>清理</b></div>
        <div className="stats-rows">
          <div className="cfg"><span>清理过期任务与交付文件、裁剪审计日志</span></div>
          <div className="cfg">
            <span>触发方式</span>
            <button className="btn ghost" style={{ padding: "4px 12px" }} disabled={busy} onClick={cleanup}>
              {busy ? "清理中…" : "立即清理"}
            </button>
          </div>
          {msg && <div className="cfg"><span>结果</span><span className="st-num" style={{ color: "var(--ok)", fontSize: 12.5 }}>{msg}</span></div>}
        </div>
      </div>
      <div className="tcard">
        <div className="t-h"><b>服务信息</b></div>
        <div className="stats-rows">
          <div className="cfg"><span>审计日志</span><span className="st-num">{auditCount != null ? `${auditCount}+ 条` : "—"}</span></div>
          <div className="cfg"><span>数据存放</span><span className="st-num" style={{ fontSize: 12 }}>本地 SQLite（数据不出机器）</span></div>
          <div className="cfg"><span>说明</span><span className="st-num" style={{ fontSize: 12, color: "var(--ink3)" }}>自动清理每 6 小时执行一次，也可在此手动触发</span></div>
        </div>
      </div>
    </div>
  );
}
