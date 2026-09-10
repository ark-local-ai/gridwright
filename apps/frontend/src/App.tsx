import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./layout/Layout";
import Site from "./pages/Site";
import Home from "./pages/Home";
import Chat from "./pages/Chat";
import Experts from "./pages/Experts";
import Skills from "./pages/Skills";
import Connectors from "./pages/Connectors";
import Prompts from "./pages/Prompts";
import Automation from "./pages/Automation";
import Settings from "./pages/Settings";
import Workspace from "./pages/Workspace";
import TaskPage from "./pages/TaskPage";

import "./index.css";
import "./layout/shell.css";
import "./pages/pages.css";
import "./pages/site.css";

// 网页版 = 完整工作台（不做功能裁剪，与桌面版同一套页面/导航）。
// 桌面版（apps/desktop）复用同一套页面，仅用 HashRouter + 自绘标题栏壳。
export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        {/* 官网落地页（营销） */}
        <Route path="/" element={<Site />} />
        {/* 完整工作台 */}
        <Route path="/app" element={<Layout />}>
          <Route index element={<Home />} />
          <Route path="chat" element={<Chat />} />
          <Route path="experts" element={<Experts />} />
          <Route path="skills" element={<Skills />} />
          <Route path="connectors" element={<Connectors />} />
          <Route path="prompts" element={<Prompts />} />
          <Route path="automation" element={<Automation />} />
          <Route path="settings" element={<Settings />} />
          <Route path="workspace" element={<Workspace />} />
          <Route path="task" element={<TaskPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/app" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
