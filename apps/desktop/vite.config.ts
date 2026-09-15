import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { readFileSync } from 'node:fs'

// 版本号的**唯一来源**是 package.json。界面里显示的版本不手写，
// 由这里在构建时注入——否则每次发版都要记得改 4 处字符串，
// 而漏掉的那处会一直显示旧版本（界面上写死的 v0.1.0 就是这么来的）。
const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))

// Desktop (Tauri) build config.
// - Reuses the web UI directly from ../frontend (cross-package relative imports).
// - No browser APIs are needed by the UI, so no special shims.
// - port 5174 to avoid colliding with the web dev server (5173).
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  base: './',
  resolve: {
    // 这个包从 ../frontend 直接 import 页面源码，而 frontend 有自己的 node_modules。
    // 不 dedupe 的话会打进**两份 React**，生产构建下 hooks 的分发器为 null，
    // 界面直接报 "Cannot read properties of null (reading 'useState')" 并白屏。
    // （开发服务器能容忍，生产不行——所以这个问题只在打包后才暴露。）
    dedupe: ['react', 'react-dom', 'react-router-dom'],
  },
  // Desktop 通过 Tauri 自定义协议加载（非 HTTP 服务），没有 Vite 代理可用，
  // 必须把引擎地址在构建时钉死，否则 API 会打到相对路径打不到本机引擎。
  define: {
    // 数据管家 Go 引擎（随包 sidecar，127.0.0.1:7700）
    'import.meta.env.VITE_AGENT_API_BASE': JSON.stringify('http://127.0.0.1:7700'),
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(pkg.version),
  },
  server: {
    port: 5174,
    strictPort: true,
    // Allow Vite to serve files outside this package root (../frontend/src).
    fs: { allow: ['..'] },
  },
})
