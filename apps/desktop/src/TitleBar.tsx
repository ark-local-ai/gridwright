import { useEffect, useState } from 'react'
import { getCurrentWindow, type Window } from '@tauri-apps/api/window'
import { GridwrightLogo } from '../../frontend/src/components/icons'
import { APP_VERSION } from '../../frontend/src/lib/version'

// 仅在 Tauri 宿主内可用 getCurrentWindow；纯浏览器预览（无 Tauri IPC）时返回 null，
// 使同一套前端也能在浏览器里预览工作台（窗口控制自动 no-op，Tauri 内行为不变）。
const isTauri = () =>
  typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window

const safeWindow = (): Window | null => {
  if (!isTauri()) return null
  try {
    return getCurrentWindow()
  } catch {
    return null
  }
}

// Custom title bar for the frameless window: drag region + app brand + min/max/close.
export default function TitleBar() {
  const [maximized, setMaximized] = useState(false)
  const [aboutOpen, setAboutOpen] = useState(false)

  const win = safeWindow()

  useEffect(() => {
    if (!win) return
    const sync = () => win.isMaximized().then(setMaximized).catch(() => {})
    const u1 = win.onResized(sync)
    const u2 = win.onMoved(sync)
    sync()
    return () => { u1.then((u) => u()).catch(() => {}); u2.then((u) => u()).catch(() => {}) }
  }, [win])

  const toggleMax = () => {
    if (!win) return
    if (maximized) win.unmaximize()
    else win.maximize()
  }

  return (
    <header className="tb" data-tauri-drag-region>
      <button
        className="tb-brand"
        data-tauri-drag-region
        aria-label="关于 gridwright"
        title="关于 gridwright"
        onClick={() => setAboutOpen(true)}
      >
        <span className="tb-title" data-tauri-drag-region><GridwrightLogo h={14} /></span>
      </button>

      <div className="tb-controls">
        <button
          className="tb-btn"
          aria-label="最小化"
          onClick={() => win?.minimize()}
        >
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 5h10" stroke="currentColor" strokeWidth="1.1" /></svg>
        </button>
        <button
          className="tb-btn"
          aria-label={maximized ? '还原' : '最大化'}
          onClick={toggleMax}
        >
          {maximized ? (
            <svg width="10" height="10" viewBox="0 0 10 10"><path d="M2.5 2.5h5v5h-5z" fill="none" stroke="currentColor" strokeWidth="1.1" /></svg>
          ) : (
            <svg width="10" height="10" viewBox="0 0 10 10"><rect x="1" y="1" width="8" height="8" fill="none" stroke="currentColor" strokeWidth="1.1" /></svg>
          )}
        </button>
        <button
          className="tb-btn tb-close"
          aria-label="关闭"
          onClick={() => win?.close()}
        >
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M1 1l8 8M9 1l-8 8" stroke="currentColor" strokeWidth="1.1" /></svg>
        </button>
      </div>

      {aboutOpen && (
        <>
          <div className="tb-overlay" onClick={() => setAboutOpen(false)} />
          <div className="tb-about card" role="dialog" aria-label="关于 gridwright">
            <div className="tb-about-head">
              <GridwrightLogo h={28} thin />
              <span>v{APP_VERSION}</span>
            </div>
            <p>跑在办公机上的数据管家 —— 新数据一进 inbox，按你定的规矩自动改表，每一格改动都留账目、能回滚、改完就通知你。</p>
            <p className="tb-about-meta">gridwright 承载你的工作与数据，安全独立、自主可控、成果归你。</p>
            <button className="btn ghost sm" onClick={() => setAboutOpen(false)}>关闭</button>
          </div>
        </>
      )}
    </header>
  )
}
