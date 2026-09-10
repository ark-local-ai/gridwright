import { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import Sidebar from "./Sidebar";
import { subscribeGlobal } from "../api";
import { IconNote, IconExpand } from "../components/icons";

const TITLES: Record<string, string> = {
  "/app": "新会话",
  "/app/chat": "助理对话",
  "/app/experts": "专家",
  "/app/skills": "技能",
  "/app/connectors": "连接器",
  "/app/prompts": "场景库",
  "/app/templates": "任务模板",
  "/app/automation": "自动化",
  "/app/workspace": "资料库",
  "/app/settings": "设置",
  "/app/stats": "统计",
  "/app/task": "任务",
  "/app/memories": "记忆",
    "/app/im": "IM 消息桥",
};

// 这些页面自带顶部 tab 导行（pn-tabs），不再重复渲染页头标题
const TAB_BARED = ["/app/experts", "/app/skills", "/app/connectors"];

export default function Layout() {
  const loc = useLocation();
  const nav = useNavigate();
  const [collapsed, setCollapsed] = useState(false);
  const title = TITLES[loc.pathname] ?? "任务";
  const hasTabBar = TAB_BARED.includes(loc.pathname);

  // M21：浏览器通知——任务完成/失败时提醒（需用户授权），覆盖所有 /app 页
  useEffect(() => {
    if (typeof window !== "undefined" && "Notification" in window && Notification.permission === "default") {
      Notification.requestPermission().catch(() => {});
    }
    return subscribeGlobal((ev) => {
      if (ev.type !== "done" && ev.type !== "error") return;
      if (typeof window === "undefined" || !("Notification" in window)) return;
      if (Notification.permission !== "granted") return;
      const isDone = ev.type === "done";
      new Notification(isDone ? "任务完成" : "任务失败", {
        body: isDone ? `任务 ${ev.taskId ?? ""} 已生成成果` : `任务 ${ev.taskId ?? ""} 执行失败，可重试`,
      });
    });
  }, []);

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
