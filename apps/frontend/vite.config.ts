import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { readFileSync } from 'node:fs'

// 版本号来源：**本包自己的** package.json。
//
// 2026-09 之前这里读的是 ../desktop/package.json——官网因此被钉在桌面端的
// 发版节奏上，属于跨包耦合。官网与桌面端是两个独立发布的产品，各读各的。
// 代价：发版要同时 bump 两个 package.json（或另立一个共享的版本来源）。
const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8'))

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(pkg.version),
  },
  // 官网是**纯静态落地页**：不访问任何后端。Site.tsx 只有锚点链接与
  // GitHub Releases 下载地址，没有一处 fetch。
  //
  // 所以这里既没有 resolve.dedupe（不再跨包共用组件），也没有任何 dev proxy。
  // 曾经有两条代理，都随旧架构一起删掉了：一条指向早已废弃的 Node 后端，
  // 一条指向 Go 引擎——但官网零请求，根本用不上。
  // 工作台在桌面端跑（apps/desktop），不在官网。
})
