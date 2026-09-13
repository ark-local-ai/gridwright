import { HashRouter, Routes, Route, Navigate } from 'react-router-dom'
import Dashboard from '../../frontend/src/pages/Dashboard'
import Site from '../../frontend/src/pages/Site'
import TitleBar from './TitleBar'
import { pickFolder, inTauri } from './native'

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
            {/* 传入原生文件夹选择框：桌面端能弹系统对话框选工作区 */}
            {/* 只在 Tauri 壳里注入系统文件夹选择框。
                单文件版是在普通浏览器里打开的，那里没有 Tauri API——
                若照样注入，pickFolder 会静默返回 null，"选择文件夹"就点了没反应。
                不注入时，界面会退回**服务端列目录**的浏览方式（浏览器里也能用）。 */}
            <Route path="/app" element={<Dashboard pickFolder={inTauri() ? pickFolder : undefined} />} />
            {/* 官网（关于/引导页）保留 */}
            <Route path="/about" element={<Site />} />
            <Route path="*" element={<Navigate to="/app" replace />} />
          </Routes>
        </div>
      </div>
    </HashRouter>
  )
}
