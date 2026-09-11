import { useEffect, useState } from "react";
import { login, register, type AuthUser } from "../api";

interface Props {
  open: boolean;
  me: AuthUser | null;
  hasUsers: boolean;
  onClose: () => void;
  /** 登录/注册/登出后回调（参数为当前用户或 null） */
  onChanged: (user: AuthUser | null) => void;
}

/** 本地多用户登录 / 注册弹窗（数据不出本机） */
export default function AuthModal({ open, me, hasUsers, onClose, onChanged }: Props) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      setErr("");
      setMode(hasUsers && !me ? "login" : "register");
    }
  }, [open, hasUsers, me]);

  if (!open) return null;

  const submit = async () => {
    if (!username.trim() || !password) { setErr("请输入用户名和密码"); return; }
    setBusy(true); setErr("");
    try {
      const user = mode === "login"
        ? await login(username.trim(), password)
        : await register(username.trim(), password, displayName.trim() || undefined);
      onChanged(user);
      onClose();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="sb-overlay" style={{ zIndex: 120 }} onClick={onClose} />
      <div className="auth-modal">
        <div className="auth-brand" style={{ fontWeight: 700, fontSize: 15, marginBottom: 2 }}>Gridwright</div>
        <div className="auth-sub" style={{ fontSize: 12, color: "var(--text-3)", marginBottom: 14 }}>
          {mode === "login" ? "登录你的本地工作区" : "创建一个本地账号"}
        </div>

        {me ? (
          <div className="auth-logged">
            <div style={{ fontSize: 13, marginBottom: 8 }}>
              已登录：<b>{me.displayName}</b>（@{me.username}）
            </div>
            <button className="btn danger sm" onClick={() => { onChanged(null); onClose(); }}>
              退出登录
            </button>
          </div>
        ) : (
          <>
            <div className="auth-tabs" style={{ display: "flex", gap: 6, marginBottom: 12 }}>
              {(["login", "register"] as const).map((m) => (
                <button key={m}
                  className={`btn ghost sm${mode === m ? "" : ""}`}
                  style={{ ...(mode === m ? { background: "var(--brand)", color: "#fff", border: "none" } : {}) }}
                  onClick={() => { setMode(m); setErr(""); }}>
                  {m === "login" ? "登录" : "注册"}
                </button>
              ))}
            </div>
            <div className="auth-fields" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              {mode === "register" && (
                <input className="auth-input" value={displayName} placeholder="显示名（可选）"
                  onChange={(e) => setDisplayName(e.target.value)} />
              )}
              <input className="auth-input" value={username} placeholder="用户名"
                autoFocus onChange={(e) => setUsername(e.target.value)} />
              <input className="auth-input" type="password" value={password} placeholder="密码"
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") submit(); }} />
            </div>
            {err && <div style={{ fontSize: 12, color: "var(--warn)", marginTop: 8 }}>{err}</div>}
            {mode === "register" && (
              <div style={{ fontSize: 11, color: "var(--text-3)", marginTop: 8 }}>
                密码用本地 scrypt 哈希保存，仅存于本机。
              </div>
            )}
            <button className="btn sm" disabled={busy} style={{ marginTop: 12, width: "100%" }}
              onClick={submit}>
              {busy ? "处理中…" : mode === "login" ? "登录" : "注册并进入"}
            </button>
          </>
        )}
      </div>
    </>
  );
}
