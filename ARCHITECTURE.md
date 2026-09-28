# gridwright — 架构文档

> 概要级架构：三个交付物、代码组织、一次改表的链路、日志与取舍。
> 深入文档在 `docs/agent-architecture/`（本地，不提交到仓库）；仓库入口见 `README.md`。

## 一、这是什么

**gridwright** 是一个"本地优先"的**表格管家**：把散在一个文件夹里的 Excel 台账看明白、
找出对不上的账，并让用户用一句自然语言指挥它改表——**改动必须经人确认才落盘**，
数据默认不出本机。

它**不是**"什么都能干的工作台"。本仓早期朝"PPT / Word / 报告多品类交付 + 专家 / 技能 /
连接器广场"的方向做过一版纯前端原型，那批页面、`mock.ts` 与 `layout/` 已在 2026-09
清理删除（见第六节）。现在的产品只做表格这一件事。

## 二、三个交付物

| 交付物 | 源码 | 构建入口 | 产物 | 后端 |
|---|---|---|---|---|
| **官网** | `apps/frontend` | `npm run build`，发布用 `scripts/deploy-site.sh` | 纯静态站点 | **不需要**。零 `fetch`，只有锚点与 GitHub Releases 链接 |
| **桌面安装版** | `apps/desktop`（界面）+ `apps/desktop/src-tauri`（Rust 壳）+ `apps/agent`（引擎） | `bash apps/agent/scripts/build-desktop.sh` | `gridwright_<版本>_x64-setup.exe`（NSIS） | **自带**：安装包含 Go 引擎，启动时由壳拉起 |
| **单文件版** | `apps/agent`（界面用 `go:embed` 编进二进制） | `bash apps/agent/scripts/build-single.sh` | `gridwright-windows-amd64.exe` | **自带**：双击即起本地服务，浏览器开 `127.0.0.1:7700` |

**桌面版与单文件版共用同一个 Go 引擎**，区别只在界面从哪来：桌面版的界面走 Tauri
自定义协议（`apps/desktop/dist`），单文件版的界面是 `go:embed` 进二进制的
`apps/agent/internal/webui/dist`。

> **顺序要紧**：`internal/webui/dist` 由 `apps/desktop/dist` 同步而来，所以
> **必须先编界面、再编 Go**。桌面打包脚本此前顺序倒置（先 Go 后界面），于是 sidecar
> 内嵌的永远是上一轮的界面——同一份 Go 代码存在两个界面版本。

**官网与另外两个没有任何代码共享**：它是独立发布、独立构建的静态站点。

## 三、仓库结构

```
gridwright/
├─ apps/
│  ├─ agent/                # Go 引擎（唯一后端）
│  │  ├─ cmd/gridwright/    # 入口：解析 flag、初始化日志、装路由
│  │  ├─ internal/
│  │  │  ├─ api/            # HTTP 路由与 handler（/api/v1/*）
│  │  │  ├─ agent/          # 对话循环、规划（prompt.go / planner.go）、记忆
│  │  │  ├─ locate/         # 认表：找表头、按关键字定位行列
│  │  │  ├─ xl/             # 读写 xlsx（excelize），含新增行
│  │  │  ├─ propose/        # 改动提案与落盘（set / add / append）
│  │  │  ├─ plan/ generate/ convo/ newtable/ rollback/ scan/ jobs/ weight/
│  │  │  ├─ llm/            # 模型调用（请求/响应摘要日志，密钥脱敏）
│  │  │  ├─ logx/           # 日志：slog → stdout、单时间戳、级别可调
│  │  │  └─ webui/dist/     # 单文件版内嵌界面（构建时同步，非手改）
│  │  └─ scripts/           # build-desktop.sh / build-single.sh
│  ├─ desktop/              # Tauri 2 桌面壳 + 工作台界面（本包自持）
│  │  ├─ src/               # React 界面：Dashboard 等
│  │  └─ src-tauri/         # Rust：窗口、自绘标题栏、拉起并接管引擎日志
│  └─ frontend/             # 官网（纯静态，仅 9 个源文件）
├─ assets/diagrams/         # README 引用的工程图（SVG，脚本生成）
├─ docs/                    # 深入文档（本地，不提交）
├─ ARCHITECTURE.md  README.md  CONTRIBUTING.md  LICENSE
```

每个 app 独立包管理（各自的 `package.json` / `go.mod`），不引入 PNPM / Turborepo。

## 四、一次"改表"走完的链路

1. 用户在桌面工作台的对话抽屉里说一句需求
2. 壳 → `POST /api/v1/plan`（或 `/chat`）→ 引擎 `internal/agent`
3. `llm` 把需求连同**表结构线索**（表头、匹配键、候选行）交给模型。模型只回**语义化**
   的改动意图——改哪张表、按什么键匹配、改哪个字段、什么值——**不回坐标**
4. `locate` 在真实表里把语义变成坐标；`propose` 生成清单（含旧值 → 新值）
5. 界面显示清单，**等人确认**。确认后才 `propose.Apply` 落盘；写前先备份，可回滚
6. 按匹配键找不到时，若意图是新增，则 `xl.AppendRow` 真的新增一行

核心设计：**模型不算坐标，坐标由确定的代码算**。这是"改错格子"最主要的防线。

## 五、日志与可观测

引擎日志走 `internal/logx`：`slog` → **stdout**、单时间戳、`-log-level` /
`GRIDWRIGHT_LOG_LEVEL` 可调级别。

为什么是 stdout 而不是 stderr：壳只在 **stderr** 的每一行上打 `[stderr]` 标记。
引擎过去用标准库 `log`（默认写 stderr），于是**正常的 200 响应全被标成 `[stderr]`**，
这个标记也就失去了意义。

壳（`src-tauri/src/lib.rs`）把引擎的 stdout / stderr 逐行落进
`%APPDATA%\gridwright\engine.log`，并对其做 8 MiB × 3 轮转；壳自己的记录在
`shell.log`。引擎的行**原样落地、不再叠壳的时间戳**（否则一行里两个格式不同的时间戳）。
模型请求与响应的正文摘要按 Debug 级记录，且抹掉密钥——`engine.log` 是用户会拿去
发给别人排障的文件。

## 六、演进与清理（2026-09）

- 删除 `apps/backend`（早期 Node/Fastify 后端，早已不参与任何链路）与 `AGENTS/`（早期文档）
- 删除 19 个不可达的前端页面与组件（约 2740 行，属于第一节提到的那版原型）
- **前端拆成两个自持的包**：`apps/frontend`（官网）与 `apps/desktop`（工作台）。
  拆分前桌面端跨包 `import '../frontend/src/*'`，一次改动要同时顾及两个产品的构建
- 打包链修正：界面先于 Go 编译；官网部署脚本开始**硬拦**产物里的运行期数据
- 引擎接入 panic 恢复：此前一个未恢复的 panic 会让**整个引擎进程退出**，
  用户只看到"突然连不上"，之后所有请求全部失败

## 七、技术选型里的几条"不"

- **不换掉 Go 引擎**。单个静态 exe（`CGO_ENABLED=0`），PE 子系统 6.1 可跑 Win7，
  `excelize` 的写能力在 Node / Python / Rust 里没有对等替代。所谓"后端太重"指的是
  已删除的 `apps/backend`，不是引擎
- **不引 UI 框架**。手写 CSS 设计令牌，颜色只用于可点击 / 选中 / 强调
- **不让模型直接写坐标**（见第四节）

更完整的技术取舍见 `README.md`。

## 八、端口与数据落在哪

### 端口（只有两个，且都只监听本机）

| 端口 | 谁在听 | 什么时候 | 说明 |
|---|---|---|---|
| `7700` | Go 引擎 | 引擎运行时（桌面版由壳拉起；单文件版就是进程本身） | `-api 127.0.0.1:7700`。**只绑 127.0.0.1**：单机单人、无鉴权层，不对外 |
| `5174` | Vite dev server | 只在 `npm run tauri dev` 开发时 | 官网的开发端口是 `5173`，两者错开以免撞车 |

官网**不占任何端口**：公网上只有静态文件，没有后端可代理。

### 数据落在哪

**`%APPDATA%\gridwright\`** —— 机器相关的东西，与具体是哪份台账无关：

| 文件 | 内容 |
|---|---|
| `config.yaml` | 模型配置、上次选的工作区 |
| `gridwright-workspaces.json` | 最近打开过的工作区列表 |
| `engine.log` | 引擎日志（8 MiB × 3 轮转） |
| `shell.log` | 桌面壳自己的日志 |

**工作区文件夹** —— 用户自己选的、放表的地方：

| 路径 | 内容 |
|---|---|
| `*.xlsx` | 用户自己的台账 |
| `rules.yaml` / `state.yaml` | 该工作区的规则与状态 |
| `ledger.csv` | 账目：每格改动一条，含旧值 |
| `conversations.json` | 对话逐字流水（全自动写，不做判断） |
| `memory.json` | 沉淀下来的记忆（要精炼、会过期、**必须人点头**） |
| `inbox/`、`inbox/done/` | 新数据入口、处理完的归档 |
| `archive/` | 备份归档 |

一句话：**安装目录里没有任何用户数据**。换版本、卸载、重装都不会动工作区与 `%APPDATA%`。

日志的契约（谁写哪个文件、格式、级别、脱敏）见 `docs/agent-architecture/31-日志与可观测.md`。
