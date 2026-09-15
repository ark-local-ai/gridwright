# 参与贡献指南（Contributing）

感谢你想参与 Ark · 方舟！本文档说明怎么跑起来、怎么提 PR、以及提交规范。

## 一、快速开始

```bash
# 克隆
git clone https://github.com/ark-local-ai/gridwright.git
cd ark-ai

# 前端
cd apps/frontend
npm install
npm run dev          # http://localhost:5173  （/ 官网 · /app 工作台）
npm run build        # 生产构建
npm run preview      # 预览构建产物
```

技术栈：React 19 + TypeScript + Vite + React Router，样式为手写 CSS 设计令牌（见 `src/index.css`）。

## 二、目录结构

```
apps/frontend/   # Web 前端（官网 + 工作台）· React+TS+Vite
  src/
    pages/       # 页面组件
    components/  # 通用组件 / 图标
    data/        # mock 数据与类型
    layout/      # 应用壳（侧边栏/顶栏）
docs/            # 内部文档（本地，不提交到仓库）
```

> 桌面版前端（Tauri）与后端（Agent 编排）规划中，落地后各占 `apps/desktop/`、`apps/backend/`。

## 三、分支规范

- 主分支：`main`（已开启分支保护：**必须 PR 审查后才能合并**）
- 开发请建自己的功能分支：`feat/xxx` 或 `fix/xxx`

```bash
git checkout -b feat/my-feature
# ...开发...
git push -u origin feat/my-feature
# 然后在 GitHub 上发起 PR 合并到 main
```

## 四、提交信息规范（Commits）

提交信息用**英文**，遵循以下格式：

### 功能实现（feat）
格式：`feat: <功能实现> · <场景>`
```bash
git commit -m "feat: add task run page with step progress for PPT generation"
```

### 修复（fix）
格式：`fix: <问题> · <方案>`，正文补充方案细节
```bash
git commit -m "fix: sidebar nav highlight wrong on sub routes

Add end to the new-session NavLink so /app/chat highlights only chat,
not home and workspace."
```

### 其他
- `chore:` — 构建/工具/依赖
- `docs:` — 文档
- `refactor:` — 重构
- `style:` — 样式

> 规范：主体描述 **做了什么 + 场景/问题**，正文展开**方案/原因**。保持简洁、一个 commit 一个逻辑变更。

## 五、提 PR 流程

1. 从 `main` 拉最新：`git pull origin main`
2. 切功能分支 → 开发 → 本地验证（`npm run build` 通过）
3. 推送分支 → 在 GitHub 发起 PR → 描述改动 & 测试情况
4. 等至少 1 人 review 通过 → 合并

## 六、设计约定

- 颜色只用 `src/index.css` 里的设计令牌，**不要新造颜色**
- 基调：暖米白 / 米灰中性底（`--bg:#f6f5f1`），单一暖调主色茶褐/琥珀 `--brand:#b98b4e`
- 大面积一律白/米灰，颜色只用于「可点击 / 选中 / 强调」；绿/橙/红仅表状态
- 风格：扁平优先、柔和圆角、低频阴影、紧凑排版；尊重 `prefers-reduced-motion`
- 详情见项目讨论文档（本地 `docs/`）
