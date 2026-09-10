import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./layout/Layout";
import Site from "./pages/Site";
import Home from "./pages/Home";
import Chat from "./pages/Chat";
import Experts from "./pages/Experts";
import Skills from "./pages/Skills";
import Connectors from "./pages/Connectors";
import Prompts from "./pages/Prompts";

import "./index.css";
import "./layout/shell.css";
import "./pages/pages.css";
import "./pages/site.css";

// 网页版 = 轻端：新建任务 + 助理对话 + 专家·技能·连接器 + 场景库。
// 桌面版（apps/desktop）复用同一套页面并以 scope="desktop" 呈现完整工作台
// （另含 自动化 / 资料库 / 设置 等）。
export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        {/* 官网落地页（营销） */}
        <Route path="/" element={<Site />} />
        {/* 轻端工作台 */}
        <Route path="/app" element={<Layout scope="web" />}>
          <Route index element={<Home />} />
          <Route path="chat" element={<Chat />} />
          <Route path="experts" element={<Experts />} />
          <Route path="skills" element={<Skills />} />
          <Route path="connectors" element={<Connectors />} />
          <Route path="prompts" element={<Prompts />} />
        </Route>
        <Route path="*" element={<Navigate to="/app" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
