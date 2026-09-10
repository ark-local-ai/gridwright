# 1 · 入门说明 / README（Agent 架构详解）

> 本目录（`AGENTS/`）是**给人类和 AI 助手一起看**的架构详解。每一项都回答「用什么工具、为什么这么用、这步做到哪、下一步做什么」。
> 每周同步更新；每完成一个里程碑回来看一眼。

---

## 一、这个项目是什么

**Ark · 方舟** —— 一个「本地优先」的 AI Agent 生产力工作台。
用户用一句话描述需求，系统把它拆成步骤、调用专家/技能/工具逐步执行，最后交付**能直接打开可编辑**的真实成果文件（PPT / Word / Excel / 报告），数据默认不出本机。

当前阶段（2026-09-07）：**前后端全栈骨架已打通**，跑通了「首页提交一句话 → 后端任务闭环 → 任务页实时点亮」的完整链路。

---

## 二、仓库结构

```
aiWork/
├─ apps/
│  ├─ frontend/   # Web 前端：React 19 + TS + Vite（已实现 11 页 + 场景库）
│  ├─ backend/    # 后端 API + Agent 编排层（Fastify + TS + SQLite）
│  └─ desktop/    # Tauri 桌面壳：复用 frontend/src，加自绘标题栏/窗口控制
├─ AGENTS/        # 本文档目录（本仓库的路标）
├─ docs/          # 早期产品/规划文档 + agent-architecture 工作副本（gitignored）
├─ ARCHITECTURE.md
└─ README.md
```

**【网页 / 桌面边界】** 同一套前端 UI、两个入口：
- **网页版（`frontend`）= 轻端**：新建任务 + 助理对话 + 专家·技能·连接器 + 场景库（`/app` 落 Home 输入台；侧栏显这四项）。适合部署到服务器由多人访问（登录在那里有意义）。
- **桌面版（`desktop`）= 完整工作台**：另含 自动化 / 资料库 / 设置 等完整能力，`scope="desktop"`。`VITE_API_BASE` 由 `desktop/vite.config.ts` 的 `define` 钉死为本地后端 `http://127.0.0.1:4000`；前端启动时 `main.tsx` 探测后端，未运行显示「后端未运行」恢复面板。数据不出本机。

实现要点：共享 `Layout/Sidebar` 接受 `scope: "web" | "desktop"`（默认 `desktop`），由 `scope` 决定侧栏显哪些项——是**一套代码两入口**，不是维护两份独立前端。

---

## 三、技术栈总图

```
                   前端 (已有，Vite dev :5173)
┌──────────────────────────────────────────────┐
│  React 19 + TypeScript + Vite 8              │
│  React Router 7 · 手写 CSS 设计令牌(无UI库)    │
│  页面: Home(场景库/输入台) Task Chat Experts   │
│        Skills Prompts(场景库) Automation      │
│        Settings Workspace Site Connectors     │
│  关键页: Home → 提交 → POST → TaskPage(SSE 点亮)│
└──────────────────────────────────────────────┘
        │ REST + SSE (Vite proxy /api → :4000)
        ▼
             后端 (本次新加，Fastify :4000)
┌──────────────────────────────────────────────┐
│  Fastify 5 · TypeScript · Node 22            │
│  ├ POST /api/tasks       创建并触发编排        │
│  ├ GET  /api/tasks/:id   查询任务快照          │
│  ├ GET  /api/tasks/:id/events  SSE 实时进度   │
│  ├ GET  /api/workspace/* 成果文件下载          │
│  └ Agent 编排层 (orchestrator)：拆步骤→执行→    │
│     产物→交付→校验，事件总线广播步进            │
└──────────────────────────────────────────────┘
        │
┌───────┼───────────────┬───────────────────┐
│       ▼               ▼                   ▼
│  Agent 编排(状态机)   工具执行          数据存储
│  事件总线(events.ts)  文件生成(md)     SQLite
│  产物落盘             (未来:Office/搜索) (node:sqlite)
└──────────────────────────────────────────────┘
```

---

## 四、工具选型 —— 为什么这么做（关键）

| 层 | 选型 | 为什么（对比了什么） |
|---|---|---|
| 后端框架 | **Fastify 5** | Node 生态，异步原生，比 Express 快约 2-3 倍；自带 schema 校验、插件化；SSE 原生支持。前端已用 TS，同语言共享心智模型 |
| 语言 | **TypeScript（Node 22）** | 与前端 React 同语言，前后端**共享类型定义**；严格模式在编译期拦截错误 |
| 数据库 | **Node 内置 `node:sqlite`** | 零依赖、零原生编译、Node 22 自带。**关键决策**：原本用 better-sqlite3（更快），但它在 Windows 需要 VS C++ 编译工具，打包失败；换内置 SQLite 后安装即用。见踩坑日志 7 |
| 实时推送 | **SSE（Server-Sent Events）** | 任务进度是「服务器单向推给浏览器」，SSE 正好；比 WebSocket 简单（无双向、无连接池管理），文本/JSON 足够。断线可回放（replay 缓存） |
| 编排 | **自写状态机**（先不引 LangGraph） | 里程碑1 用预置计划即可跑通闭环，避免过早引入重型依赖；预留 LLM 拆解接入点 |
| 前端状态 | React 原生 `useState` + 路由 state | 页面少、局部状态，够用；不引 Redux/Zustand 减负担 |
| 样式 | 手写 CSS 设计令牌 | 项目已锁定「暖米白/茶褐」设计系统，无 UI 框架能完全贴合；令牌集中在 index.css |
| 文档生成 | **自写 md 文件** | 里程碑1 用文本文件验证「真落盘」；后续换 pptxgenjs/exceljs/docx |

---

## 五、如何跑起来

```bash
# 终端 1：后端
cd apps/backend
npm install
npm run dev        # Fastify 起在 http://127.0.0.1:4000

# 终端 2：前端
cd apps/frontend
npm install
npm run dev        # Vite 起在 http://127.0.0.1:5173 （/api 自动代理到 :4000）
```

打开 http://127.0.0.1:5173/app → 在输入框输一句话 → 点发送 → 跳转任务页，步骤逐个点亮，最后生成可下载的 `.md` 成果文件。

---

## 六、当前做到哪一步（进度看板，每周同步）

| 里程碑 | 内容 | 状态 |
|---|---|---|
| Block-2a | 后端骨架（Fastify+TS+SQLite 起服务） | ✅ 完成 |
| Block-2b | 任务闭环 API（POST/GET/SSE） | ✅ 完成 |
| Block-2c | Agent 编排（拆步骤→执行→产物→交付→校验） | ✅ 完成 |
| 前端接线 | Home 提交→POST；TaskPage SSE 实时点亮 | ✅ 完成 |
| E2E 验证 | 创建→执行→SSE→文件→checks 全绿 | ✅ 完成 |
| —— 里程碑2 —— | 接真实 LLM（openai 兼容 + Ollama） | ⏳ 下一步 |
| —— 里程碑3 —— | 真实 Office 工具（PPT/Excel/Word 生成） | ⏳ 规划 |

**下一步正在做**：编写本目录 9 份 Agent 架构文档，同步每一大步的工具选型与进度（本轮正在做这一步）。
