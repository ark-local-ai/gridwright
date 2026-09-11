import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Site from "./pages/Site";

import "./index.css";
import "./layout/shell.css";
import "./pages/pages.css";
import "./pages/site.css";

// 网页端 = 官网（落地/营销）only。
// 工作台（/app 子树）由桌面客户端承载：apps/desktop 复用同一套页面，
// 用 HashRouter + 自绘标题栏壳。页面文件未删除，恢复网页工作台 = 还原下方路由。
export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Site />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
