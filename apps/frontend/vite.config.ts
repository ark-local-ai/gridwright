import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { readFileSync } from 'node:fs'

// 应用版本号的唯一来源：apps/desktop/package.json（桌面壳才是"应用"本身）。
// 前端界面里显示的版本由这里注入——两处 vite 配置读同一个文件，
// 就不会出现"安装包是 0.1.2、界面还写着 0.1.0"这种漂移。
const appPkg = JSON.parse(
  readFileSync(new URL('../desktop/package.json', import.meta.url), 'utf-8'),
)

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(appPkg.version),
  },
  resolve: {
    // 避免打进两份 React（与 desktop 同理：跨包 import 共用组件时会出现）。
    dedupe: ['react', 'react-dom', 'react-router-dom'],
  },
  server: {
    // 开发时把 /api 代理到本地后端（仅本机，数据不出本机）
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:4000',
        changeOrigin: true,
      },
      // 数据管家 Go 引擎（手脑分离）：/agent-api/* → 127.0.0.1:7700/api/v1/*
      '/agent-api': {
        target: 'http://127.0.0.1:7700',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/agent-api/, ''),
      },
    },
  },
})
