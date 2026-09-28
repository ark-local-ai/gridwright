# gridwright — 官网（apps/frontend）

产品官网落地页。**纯静态、零请求**：只有锚点链接与 GitHub Releases 下载地址，
不访问任何后端，也不需要后端。

> 工作台（改表界面）在 `apps/desktop`，不在本包。两者是两个独立发布的产品，
> 没有代码共享——本包不 import `apps/desktop` 的任何东西，反之亦然。

## 技术栈

- React 19 + TypeScript + Vite
- 纯手写 CSS 设计令牌（无 UI 框架）
- 只用 React Router 做一条路由

## 快速开始

```bash
cd apps/frontend
npm install
npm run dev        # 开发，http://localhost:5173
npm run build      # 构建到 dist/
npm run preview    # 预览构建产物
```

## 目录结构

```
src/
  main.tsx         # 入口
  App.tsx          # 路由表（只有 /）
  pages/Site.tsx   # 官网落地页
  pages/site.css
  pages/pages.css
  index.css        # 设计令牌（CSS 变量）
  layout/shell.css # 应用壳样式（与桌面端各自一份，见下）
  lib/version.ts   # 读构建期注入的版本号
scripts/
  deploy-site.sh   # 发布到服务器（含产物校验与运行期数据硬拦）
```

## 版本号从哪来

**本包自己的 `package.json`**。构建时由 `vite.config.ts` 注入
`import.meta.env.VITE_APP_VERSION`，发布脚本再用 `scripts/read-app-version.mjs`
读同一个文件、核对产物里确实带上了这个版本号。

不读 `apps/desktop/package.json`：官网与桌面端各发各的版，钉在一起会让官网被迫
跟桌面端的发版节奏走。代价是发版要 bump 两处。

## 发布

```bash
bash apps/frontend/scripts/deploy-site.sh <user@host> [远端目录]

# 先干跑看一眼要发什么（不连服务器、不需要私钥）
DRY_RUN=1 bash apps/frontend/scripts/deploy-site.sh <user@host>
```

脚本会：构建 → 校验产物版本号 → **硬拦** `dist/` 里的表格 / 数据库文件与
`workspace/`（那是本机跑出来的交付物，绝不能上公网）→ 打包 → scp → 远端解包。

运行环境需要 Git Bash（提供 `grep` / `sed` / `find`）。开发机是 Windows。

## 现状

- 一条路由、一个页面，全部为静态内容
- 深入文档在 `docs/agent-architecture/`（本地，不提交）
