import { HashRouter, Routes, Route, Navigate } from 'react-router-dom'
import Dashboard from '../../frontend/src/pages/Dashboard'
import Site from '../../frontend/src/pages/Site'
import TitleBar from './TitleBar'

// 桌面端 = 数据管家（只做这一件事）。
// Tauri 通过自定义协议加载（不是 HTTP 服务），没有 History API，故用 HashRouter。
// 旧的「交付工作台」页面（Chat/专家/技能/连接器/场景/自动化）已不属于本产品，
// 不再挂路由；页面文件保留在源码里，需要时可另建入口或删除。
export default function App() {
  return (
    <HashRouter>
      <div className="desktop-root">
        <TitleBar />
        <div className="desktop-body">
          <Routes>
            <Route path="/app" element={<Dashboard />} />
            {/* 官网（关于/引导页）保留 */}
            <Route path="/about" element={<Site />} />
            <Route path="*" element={<Navigate to="/app" replace />} />
          </Routes>
        </div>
      </div>
    </HashRouter>
  )
}
