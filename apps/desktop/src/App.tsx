import { HashRouter, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { useEffect } from 'react'
import Layout from '../../frontend/src/layout/Layout'
import Site from '../../frontend/src/pages/Site'
import Home from '../../frontend/src/pages/Home'
import Chat from '../../frontend/src/pages/Chat'
import Experts from '../../frontend/src/pages/Experts'
import Skills from '../../frontend/src/pages/Skills'
import Connectors from '../../frontend/src/pages/Connectors'
import Prompts from '../../frontend/src/pages/Prompts'
import Automation from '../../frontend/src/pages/Automation'
import Settings from '../../frontend/src/pages/Settings'
import Workspace from '../../frontend/src/pages/Workspace'
import TaskPage from '../../frontend/src/pages/TaskPage'
import TitleBar from './TitleBar'

// Tauri loads via a custom protocol (not a server), so BrowserRouter's History API
// is unavailable — HashRouter keeps deep links working inside the desktop shell.
export default function App() {
  return (
    <HashRouter>
      <Shell />
    </HashRouter>
  )
}

function Shell() {
  // Land on the workbench (/app) by default instead of the marketing landing page.
  const isRoot = useLocation().pathname === '/'
  const nav = useNavigate()
  useEffect(() => {
    if (isRoot) nav('/app', { replace: true })
  }, [isRoot, nav])

  return (
    <div className="desktop-root">
      <TitleBar />
      <div className="desktop-body">
        <Routes>
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
          <Route path="/" element={<Site />} />
          <Route path="*" element={<Navigate to="/app" replace />} />
        </Routes>
      </div>
    </div>
  )
}
