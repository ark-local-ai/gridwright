import { useEffect, useState } from 'react'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { ArkLogo } from '../../frontend/src/components/icons'

// Custom title bar for the frameless window: drag region + app brand + min/max/close.
export default function TitleBar() {
  const [maximized, setMaximized] = useState(false)
  const [aboutOpen, setAboutOpen] = useState(false)

  useEffect(() => {
    const win = getCurrentWindow()
    const sync = () => win.isMaximized().then(setMaximized)
    win.onResized(sync)
    win.onMoved(sync)
    sync()
  }, [])

  const win = getCurrentWindow()

  const toggleMax = () => {
    if (maximized) win.unmaximize()
    else win.maximize()
  }

  return (
    <header className="tb" data-tauri-drag-region>
      <button
        className="tb-brand"
        data-tauri-drag-region
        aria-label="关于 Ark · 方舟"
        title="关于 Ark · 方舟"
        onClick={() => setAboutOpen(true)}
      >
        <ArkLogo h={13} />
        <span className="tb-title" data-tauri-drag-region>Ark · 方舟</span>
      </button>

      <div className="tb-controls">
        <button
          className="tb-btn"
          aria-label="最小化"
          onClick={() => win.minimize()}
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
          onClick={() => win.close()}
        >
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M1 1l8 8M9 1l-8 8" stroke="currentColor" strokeWidth="1.1" /></svg>
        </button>
      </div>

      {aboutOpen && (
        <>
          <div className="tb-overlay" onClick={() => setAboutOpen(false)} />
          <div className="tb-about card" role="dialog" aria-label="关于 Ark · 方舟">
            <div className="tb-about-head">
              <ArkLogo h={30} thin />
              <div>
                <b>Ark · 方舟</b>
                <span>v0.1.0</span>
              </div>
            </div>
            <p>本地运行的 AI 交付工作台 —— 把一句话需求，做成能直接打开、可编辑的交付物，数据不出本机。</p>
            <p className="tb-about-meta">方舟承载你的工作与数据，安全独立、自主可控、成果归你。</p>
            <button className="btn ghost sm" onClick={() => setAboutOpen(false)}>关闭</button>
          </div>
        </>
      )}
    </header>
  )
}
