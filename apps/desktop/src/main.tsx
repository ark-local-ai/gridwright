import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Component, useState, useEffect, useCallback } from 'react'
import type { ReactNode, ErrorInfo } from 'react'
// Reuse the web UI styling wholesale (tokens + shell + pages).
import '../../frontend/src/index.css'
import '../../frontend/src/layout/shell.css'
import '../../frontend/src/pages/pages.css'
import '../../frontend/src/pages/site.css'
// Desktop-only additions: custom title bar + window chrome sizing.
import './desktop.css'
import App from './App.tsx'

// 桌面版直接打本地服务：
//  - 数据管家 Go 引擎 127.0.0.1:7700（VITE_AGENT_API_BASE，见 api-agent.ts）
//  - 旧 TS 后端 127.0.0.1:4000（VITE_API_BASE，过渡期保留）
const BASE = import.meta.env.VITE_API_BASE ?? 'http://127.0.0.1:4000'
const AGENT_BASE = import.meta.env.VITE_AGENT_API_BASE ?? 'http://127.0.0.1:7700'

// 任一后端就绪即可进入工作台：优先数据管家（新），旧后端作为过渡期兜底。
function BackendGate() {
  const [ready, setReady] = useState(false)

  const probe = useCallback(async () => {
    const ping = async (url: string) => {
      try {
        const ctrl = new AbortController()
        const timer = setTimeout(() => ctrl.abort(), 1500)
        const res = await fetch(url, { signal: ctrl.signal })
        clearTimeout(timer)
        return res.ok
      } catch {
        return false
      }
    }
    if (await ping(`${AGENT_BASE}/api/v1/health`)) { setReady(true); return }
    if (await ping(`${BASE}/health`)) setReady(true)
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
        <h1>数据管家未运行</h1>
        <p>桌面版需要一个本地服务（默认 127.0.0.1:7700）来读写你的表。</p>
        <p className="be-rec-cmd">请先启动引擎：<code>gridwright -config config.yaml</code></p>
        <p className="be-rec-meta">或在项目根目录运行一键启动器 <code>npm start</code>。</p>
        <p className="be-rec-wait">正在检测…服务一就绪会自动进入工作台</p>
      </div>
    </div>
  )
}

// 渲染出错兜底：宁可显示错误，也不要白屏（白屏无法排查，也让人以为程序死了）
class ErrorBoundary extends Component<{ children: ReactNode }, { err: Error | null; stack: string }> {
  state = { err: null as Error | null, stack: '' }
  static getDerivedStateFromError(err: Error) { return { err, stack: '' } }
  componentDidCatch(err: Error, info: ErrorInfo) {
    console.error('界面渲染出错:', err, info.componentStack)
    this.setState({ stack: info.componentStack || '' })
  }
  render() {
    if (!this.state.err) return this.props.children
    return (
      <div className="desktop-root">
        <div className="be-rec-panel">
          <div className="be-rec-logo">gridwright</div>
          <h1>界面出错了</h1>
          <p className="be-rec-cmd">{String(this.state.err.message || this.state.err)}</p>
          <pre style={{ fontSize: 11, textAlign: 'left', maxHeight: 200, overflow: 'auto', color: '#565e6b', whiteSpace: 'pre-wrap' }}>
            {this.state.stack.split('\n').slice(0, 8).join('\n')}
          </pre>
          <button className="btn primary" onClick={() => this.setState({ err: null, stack: '' })}>重试</button>
        </div>
      </div>
    )
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ErrorBoundary>
      <BackendGate />
    </ErrorBoundary>
  </StrictMode>,
)
