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

// 只对接数据管家 Go 引擎（127.0.0.1:7700，由 Tauri 壳随包拉起）。
// 离线可用：引擎在没有网络/没有配模型时也能起，看表、体检、联动图照常。
const AGENT_BASE = import.meta.env.VITE_AGENT_API_BASE ?? 'http://127.0.0.1:7700'

// 引擎就绪后进入工作台；未就绪时显示「正在启动」而不是「后端未运行」。
function BackendGate() {
  const [ready, setReady] = useState(false)
  const [waited, setWaited] = useState(false)

  const probe = useCallback(async () => {
    try {
      const ctrl = new AbortController()
      const timer = setTimeout(() => ctrl.abort(), 1500)
      const res = await fetch(`${AGENT_BASE}/api/v1/health`, { signal: ctrl.signal })
      clearTimeout(timer)
      if (res.ok) setReady(true)
    } catch {
      /* 引擎未就绪，保持未 ready */
    }
  }, [])

  useEffect(() => {
    void probe()
    const id = setInterval(() => void probe(), 1500)
    const t = setTimeout(() => setWaited(true), 8000)
    return () => { clearInterval(id); clearTimeout(t) }
  }, [probe])

  if (ready) return <App />
  return (
    <div className="desktop-root">
      <div className="be-rec-panel">
        <div className="be-rec-logo">gridwright</div>
        <h1>{waited ? '引擎启动失败' : '正在启动…'}</h1>
        <p>
          {waited
            ? '随包引擎没有响应。可尝试重新打开应用。'
            : '正在拉起本地引擎，稍候…'}
        </p>
        <p className="be-rec-meta">
          数据都在你自己的机器上，离线也能用。
        </p>
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
