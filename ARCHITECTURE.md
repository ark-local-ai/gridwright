# Ark · 方舟 — 架构文档

> 概要级架构：技术栈、路由、代码组织、设计系统、当前实现状态与演进方向。
> 关联文档：`docs/`（产品分析、功能模块规格书、模块坑位清单、开发日志、设计令牌）。

## 一、这是什么

**Ark · 方舟** 是一个"本地优先"的 AI Agent 生产力工作台。用户用一句话描述需求，它调用专家/技能/工具把任务拆成步骤执行，最后交付可编辑的真实成果文件（PPT / Word / Excel / 报告），数据默认不出本机。

本项目目前实现**前端**（官网 + 客户端工作台），后端 / LLM 编排待接入。

## 二、仓库结构（monorepo 单仓起步）

```
ark/
├─ apps/
│  ├─ frontend/          # Web 前端：Vite + React + TS
│  │  ├─ src/
│  │  │  ├─ App.tsx          # 路由表（/ 官网；/app/* 工作台）
│  │  │  ├─ index.css        # 设计令牌（CSS 变量）
│  │  │  ├─ layout/          # 应用壳：Sidebar + Layout + shell.css
│  │  │  ├─ components/      # 通用组件 / SVG 图标
│  │  │  ├─ data/mock.ts     # 静态类型 + 示例数据（当前为 mock，后接 API）
│  │  │  └─ pages/           # 页面组件（Home/Task/Chat/Experts/Skills/Prompts/Automation/Settings/Workspace/Site/Connectors）
│  │  └─ index.html
│  ├─ desktop/           # 桌面版（Tauri 2）· 复用 frontend 界面 ✅ 已实现
│  └─ backend/           # 后端 API / Agent 编排 · 规划中
├─ docs/                  # 全部规格 / 计划 / 日志文档（本地，不提交）
├─ assets/diagrams/       # README 引用的工程图（SVG，脚本生成）
├─ ARCHITECTURE.md        # 本文档
├─ README.md              # 仓库入口说明
├─ CONTRIBUTING.md        # 协作约定
├─ LICENSE                # 开源协议
└─ .gitignore
```

> 说明：已落地 `apps/frontend`（Web 前端）与 `apps/desktop`（Tauri 2 桌面版，复用 frontend 界面）。每个 app 独立包管理，暂不需要 PNPM/Turborepo 等 monorepo 工具。

## 三、技术栈（apps/frontend）

| 层 | 选型 |
|---|---|
| 框架 | React 19 + TypeScript |
| 构建 | Vite |
| 路由 | React Router |
| 样式 | 手写 CSS 设计令牌（无 UI 框架）|
| 数据 | 当前 mock（`src/data/mock.ts`），未来接 REST/SSE |

设计系统：暖米白 / 米灰中性底 + 单一暖调主色茶褐/琥珀 `#B98B4E`，颜色只用于可点击/选中/强调；令牌见 `src/index.css`。详见 `docs/设计令牌_TOKEN.md`。

## 四、路由

| 路由 | 页面 | 说明 |
|---|---|---|
| `/` | 官网落地页（Site）| 营销页：Hero/Skills/移动端远程/本地操作/持续交付/专家/CTA |
| `/app` | 新会话首页（Home）| 场景卡 + 输入 → 提交跳任务页 |
| `/app/task` | 任务执行页 | 步骤推进 / 中间产物 / 交付 / 验收清单（当前 mock 动画）|
| `/app/chat` | 助理对话 | 多轮消息（mock 回显）|
| `/app/experts` | 专家广场 | 专家卡 + 创建专属专家 |
| `/app/skills` | 技能与连接器 | Markdown 技能编辑器 + 连接器状态 |
| `/app/connectors` | 连接器 | 连接器列表 / 状态 / 接入配置 |
| `/app/prompts` | 提示词库 | 分类提示词 + 搜索 + 一键使用 |
| `/app/automation` | 自动化 | 定时任务列表 + 执行历史 |
| `/app/settings` | 设置 | 分区菜单 + 模型渠道管理 |
| `/app/workspace` | 工作空间 | 空间切换 + 文件树 + 成果文件 |

## 五、核心模块（前端已实现，后端是坑位）

| 模块 | 前端现状 | 待接入（真实萝卜）|
|---|---|---|
| 任务闭环 | 首页→任务页，mock 步骤动画 | `POST /api/tasks` + 编排层 + SSE + 真文件 |
| 模型与连接 | 渠道列表展示 | 渠道 CRUD + 验活 + 模型路由 |
| 专家与技能 | 专家卡 / 技能目录 | CRUD + 热加载 |
| 自动化 | 任务列表 | Cron 调度 + 执行 |
| 工作空间 | 文件树/成果网格 | 真目录扫描 + 下载 |
| 对话 | 本地回显 | `POST /api/chat` 流式 |

详情见 [docs/模块坑位清单_ROADMAP.md]。

## 六、演进方向

1. 接入真实后端 + LLM（坑位 A 任务闭环优先）
2. 数据从 mock 切到真实 API
3. 打包为桌面应用（Tauri 2，复用当前布局）
4. IM 桥 / 内置浏览器 / 记忆等进阶能力
