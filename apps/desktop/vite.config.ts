import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { readFileSync } from 'node:fs'

// 版本号的**唯一来源**是 package.json。界面里显示的版本不手写，
// 由这里在构建时注入——否则每次发版都要记得改 4 处字符串，
// 而漏掉的那处会一直显示旧版本（界面上写死的 v0.1.0 就是这么来的）。
const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))

// Desktop (Tauri) build config.
//
// 工作台的界面源码**全部在本包内**（src/）。
// 2026-09 之前它是跨包从 ../frontend/src 直接 import 页面源码的，那带来了
// 三处耦合：两份 React（不 dedupe 就生产白屏）、server.fs.allow: ['..']、
// 以及跨包 tsconfig include。现已拆开：官网（apps/frontend）与应用各自独立，
// 本包不再向包外读任何文件。
//
// - port 5174 to avoid colliding with the web dev server (5173).
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  base: './',
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
  },
})
