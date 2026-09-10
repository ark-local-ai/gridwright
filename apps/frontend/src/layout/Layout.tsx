import { useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import Sidebar from "./Sidebar";
import { IconNote, IconExpand } from "../components/icons";

const TITLES: Record<string, string> = {
  "/app": "新会话",
  "/app/chat": "助理对话",
  "/app/experts": "专家",
  "/app/skills": "技能",
  "/app/connectors": "连接器",
  "/app/prompts": "场景库",
  "/app/automation": "自动化",
  "/app/workspace": "资料库",
  "/app/settings": "设置",
  "/app/task": "任务",
};

// 这些页面自带顶部 tab 导行（pn-tabs），不再重复渲染页头标题
const TAB_BARED = ["/app/experts", "/app/skills", "/app/connectors"];

export default function Layout() {
  const loc = useLocation();
  const nav = useNavigate();
  const [collapsed, setCollapsed] = useState(false);
  const title = TITLES[loc.pathname] ?? "任务";
  const hasTabBar = TAB_BARED.includes(loc.pathname);

  return (
    <div className="app">
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed((c) => !c)} />
      <main className="main">
        {/* 顶栏：只有收起态才放工具（展开/新建），展开态是纯虚线分隔 */}
        <header className={`top${collapsed ? " collapsed" : ""}`}>
          <div className="top-bar">
            {collapsed && (
              <>
                <button
                  className="top-tool rail-toggle"
                  aria-label="展开侧栏"
                  onClick={() => setCollapsed(false)}
                >
                  <IconExpand size={15} />
                </button>
                <button className="top-tool new" aria-label="新建任务" onClick={() => nav("/app")}>
                  <IconNote size={16} />
                </button>
              </>
            )}
          </div>
        </header>
        <div className="canvas">
          {/* 页头标题：放回画布左上角（专家/技能/连接器页由 pn-tabs 替代，不显示） */}
          {!hasTabBar && <h1 className="page-h1">{title}</h1>}
          <Outlet />
        </div>
      </main>
    </div>
  );
}
