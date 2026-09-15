import { useEffect, useState } from "react";
import { SkPanel } from "../components/Skeleton";
import { useLocation, NavLink } from "react-router-dom";
import { IconUsers, IconSpark, IconLink } from "../components/icons";
import { listConnectors, type ConnectorDto } from "../api";

const TABS = [
  { to: "/app/experts", label: "专家", icon: <IconUsers size={14} />, desc: "内置专家与技能库 —— 按专业流程拆解任务、逐项执行。交付可验收的成果，而不是聊天记录。" },
  { to: "/app/skills", label: "技能", icon: <IconSpark size={14} />, desc: "一套 Markdown 即技能 —— 改完下一条任务就生效。" },
  { to: "/app/connectors", label: "连接器", icon: <IconLink size={14} />, desc: "连通外部服务与通道，把成果推送到需要的地方。" },
];

export default function Connectors() {
  const loc = useLocation();
  const active = TABS.find((t) => t.to === loc.pathname) ?? TABS[2];
  const [conns, setConns] = useState<ConnectorDto[]>([]);
  const [err, setErr] = useState(false);

  const load = () => {
    setErr(false);
    listConnectors().then(setConns).catch(() => setErr(true));
  };
  useEffect(load, []);

  return (
    <div className="page">
      <header className="pn-hd">
        <nav className="pn-tabs">
          {TABS.map((t) => (
            <NavLink key={t.to} to={t.to}
              className={({ isActive }) => `pn-tab${isActive ? " on" : ""}`}>
              {t.icon}{t.label}
            </NavLink>
          ))}
        </nav>
        <p className="pn-desc">{active.desc}</p>
      </header>

      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
        <span style={{ fontSize: 12, color: "var(--text-3)" }}>连接器实时状态（本地探测，数据不出本机）</span>
        <button className="btn ghost sm" style={{ marginLeft: "auto" }} onClick={load}>刷新</button>
      </div>
      {err && <div style={{ fontSize: 12, color: "var(--warn)", marginBottom: 10 }}>后端未启动，状态读不到</div>}

      <div className="conn-grid">
        {conns.map((c) => (
          <div className="conn-card card" key={c.name}>
            <div className="conn-top">
              <span className="conn-dot" style={{ background: c.dot }} />
              <b>{c.name}</b>
              <span className={`badge${c.online ? " done" : " queue"}`}>{c.online ? "已连接" : "未连接"}</span>
            </div>
            <p className="conn-note">{c.note}</p>
          </div>
        ))}
        {conns.length === 0 && !err && <div className="sk-group" style={{ maxWidth: 320 }}><SkPanel rows={4} /></div>}
      </div>
    </div>
  );
}
