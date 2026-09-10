import { BrowserRouter, Routes, Route } from "react-router-dom";
import Layout from "./layout/Layout";
import Site from "./pages/Site";
import Home from "./pages/Home";
import Chat from "./pages/Chat";
import Experts from "./pages/Experts";
import Skills from "./pages/Skills";
import Connectors from "./pages/Connectors";
import Prompts from "./pages/Prompts";
import Templates from "./pages/Templates";
import Automation from "./pages/Automation";
import Settings from "./pages/Settings";
import Workspace from "./pages/Workspace";
import TaskPage from "./pages/TaskPage";
import Stats from "./pages/Stats";
import Memories from "./pages/Memories";
import ImBridge from "./pages/ImBridge";

import "./index.css";
import "./layout/shell.css";
import "./pages/pages.css";
import "./pages/site.css";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        {/* 官网落地页（营销） */}
        <Route path="/" element={<Site />} />
        {/* App 主体 */}
        <Route path="/app" element={<Layout />}>
          <Route index element={<Home />} />
          <Route path="chat" element={<Chat />} />
          <Route path="experts" element={<Experts />} />
          <Route path="skills" element={<Skills />} />
          <Route path="connectors" element={<Connectors />} />
          <Route path="prompts" element={<Prompts />} />
          <Route path="templates" element={<Templates />} />
          <Route path="automation" element={<Automation />} />
          <Route path="settings" element={<Settings />} />
          <Route path="workspace" element={<Workspace />} />
          <Route path="stats" element={<Stats />} />
          <Route path="memories" element={<Memories />} />
            <Route path="im" element={<ImBridge />} />
          <Route path="task" element={<TaskPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
