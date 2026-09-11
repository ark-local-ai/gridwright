import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { useState, useEffect, useCallback } from 'react'
// Reuse the web UI styling wholesale (tokens + shell + pages).
import '../../frontend/src/index.css'
import '../../frontend/src/layout/shell.css'
import '../../frontend/src/pages/pages.css'
import '../../frontend/src/pages/site.css'
// Desktop-only additions: custom title bar + window chrome sizing.
import './desktop.css'
import App from './App.tsx'

// 桌面版直接打本地后端 127.0.0.1:4000（构建时由 vite define 注入 VITE_API_BASE）。
const BASE = import.meta.env.VITE_API_BASE ?? 'http://127.0.0.1:4000'

function BackendGate() {
  const [ready, setReady] = useState(false)

  const probe = useCallback(async () => {
    try {
      const ctrl = new AbortController()
      const timer = setTimeout(() => ctrl.abort(), 1500)
      const res = await fetch(`${BASE}/health`, { signal: ctrl.signal })
      clearTimeout(timer)
      if (res.ok) setReady(true)
    } catch {
      /* 后端未就绪，保持未 ready */
    }
  }, [])

  useEffect(() => {
    void probe()
    const id = setInterval(() => void probe(), 2000)
    return () => clearInterval(id)
  }, [probe])

  // 就绪后才挂载工作台
  if (ready) return <App />
  return (
    <div className="desktop-root">
      <div className="be-rec-panel">
        <div className="be-rec-logo">gridwright</div>
        <h1>本地后端未运行</h1>
        <p>桌面版需要一个本地后端服务（127.0.0.1:4000）来读写你的数据。</p>
        <p className="be-rec-cmd">请先启动后端：<code>node apps/backend/dist/server.js</code></p>
        <p className="be-rec-meta">或在项目根目录运行一键启动器 <code>npm start</code>（会同时拉起后端 + 网页预览）。</p>
        <p className="be-rec-wait">正在检测后端…后端一就绪会自动进入工作台</p>
      </div>
    </div>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BackendGate />
  </StrictMode>,
)
