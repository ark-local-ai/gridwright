import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Desktop (Tauri) build config.
// - Reuses the web UI directly from ../frontend (cross-package relative imports).
// - No browser APIs are needed by the UI, so no special shims.
// - port 5174 to avoid colliding with the web dev server (5173).
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  base: './',
  // Desktop 通过 Tauri 自定义协议加载（非 HTTP 服务），没有 Vite 代理可用，
  // 必须把后端地址在构建时钉死，否则 API 会打到相对 /api 打不到 127.0.0.1:4000。
  define: {
    'import.meta.env.VITE_API_BASE': JSON.stringify('http://127.0.0.1:4000'),
  },
  server: {
    port: 5174,
    strictPort: true,
    // Allow Vite to serve files outside this package root (../frontend/src).
    fs: { allow: ['..'] },
  },
})
