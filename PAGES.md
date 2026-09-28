# gridwright — 页面清单

> 只登记**当前真实存在**的页面。早期那版"专家 / 技能 / 连接器 / 提示词 / 自动化"
> 工作台原型的页面已在 2026-09 删除，不再登记（见 `ARCHITECTURE.md` 第六节）。

## 一、桌面工作台（`apps/desktop`）

入口用 `HashRouter`：桌面环境没有 HTTP 服务器，`BrowserRouter` 的 History API
在 Tauri 自定义协议下不可用。

| 路由 | 页面 | 组件 | 用途 |
|---|---|---|---|
| `/app` | 主看板 | `pages/Dashboard.tsx` | 一屏答一个问题：我的表有没有事、它要动什么 |
| `/about` | 关于 / 引导 | `pages/Site.tsx` | 与官网同一份实现（在应用内打开） |
| `*` | → `/app` | — | 其余一律回主看板 |

主看板里的面板**不是独立路由**，按需弹出或展开：

| 面板 | 组件 | 什么时候出现 |
|---|---|---|
| 待确认清单 | `pages/Pending.tsx` · `PendingList` | 有改动提案待确认 |
| 对话抽屉 | `pages/Pending.tsx` · `ChatPane` | 点顶栏的对话图标 |
| 表格预览（整屏） | `pages/SheetView.tsx` | 点格子或问题条目 |
| 账目 / 回滚 | `pages/LedgerPanel.tsx` | 折叠块"账目"里的查看 |
| 定时任务 | `pages/TasksPanel.tsx` | 顶栏时钟图标 |
| 规则（rules.yaml） | `pages/RulesPanel.tsx` | 顶栏规则图标 |
| 设置 | `pages/Settings2.tsx` | 顶栏齿轮 |
| 记忆 | `pages/MemoryPanel.tsx` | 由设置页进入 |
| 联动图 | `Dashboard` 内的 `GraphOverlay` | 折叠块"表的连接"展开 |
| 拖放区 | `components/DropZone.tsx` | 工作区为空（或首次运行）时 |
| 注意力列表 | `components/AttentionList.tsx` | 体检有问题时 |
| 工作区切换 | `Dashboard` 内的 `WorkspaceSwitcher` | 点顶栏的工作区名 |
| 文件夹选择 | `Dashboard` 内的 `FolderPicker` | 浏览器环境（无 Tauri API）下选工作区 |

## 二、官网（`apps/frontend`）

| 路由 | 页面 | 组件 |
|---|---|---|
| `/` | 官网落地页 | `pages/Site.tsx` |

官网是**纯静态**：只有锚点链接与 GitHub Releases 下载地址，**零 `fetch`**，
不访问任何后端。发布走 `scripts/deploy-site.sh`（会硬拦 dist 里的运行期数据）。

## 三、界面上的两条约定

- **"读取失败"与"没有数据"必须分开显示**。一个挂掉的请求和"真的没有"在界面上
  长得一模一样，这正是把"空清单"误当"没事"的根源。主看板顶栏下方那条警示带
  就是为它存在的：写清哪一项没读到、为什么、以及下面的空白可信不可信
- 顶栏不放并列读数。"现在怎么样"由主看板那个**唯一的主角**回答

深入文档在 `docs/agent-architecture/`（本地，不提交）。
