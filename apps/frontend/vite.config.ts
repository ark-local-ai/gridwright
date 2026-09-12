import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
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
