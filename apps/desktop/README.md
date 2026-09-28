# gridwright — 桌面版（apps/desktop）

Tauri 2 桌面应用。界面、Rust 壳、打包配置**全在本包内**，另配一个 Go 引擎作为
sidecar（安装包会一起带上）。

> 本包**不再**跨包 import `apps/frontend/src/*`。2026-09 之前是那样复用的，
> 结果是改一次界面要同时顾及官网与桌面端两个产品的构建。现在两份界面各自自持，
> 代价是少量样式令牌各有一份副本。

## 技术栈

- **Tauri 2**（Rust 壳）：无边框窗口 + 自绘标题栏，安装包几 MB 量级
- **React 19 + TypeScript + Vite**
- **Go 引擎**（`apps/agent`）作为 sidecar，启动时由壳拉起

## 目录

```
apps/desktop/
  src/
    main.tsx          # 入口，引入本包 CSS
    App.tsx           # HashRouter + 路由表（/app 主看板，/about 关于）
    TitleBar.tsx      # 自绘标题栏：拖拽 / 最小化 / 最大化 / 关闭
    native.ts         # Tauri 能力：系统文件夹选择、原生文件拖拽
    desktop.css
    pages/            # 工作台界面（Dashboard 等，见根目录 PAGES.md）
    components/  lib/
  src-tauri/
    src/              # Rust 壳（lib.rs 还负责接管引擎的 stdout/stderr 落日志）
    tauri.conf.json   # 窗口 / 打包 / 标识；version 指向 ../package.json
    capabilities/     # 窗口控制权限
    icons/            # 应用图标（由 appicon.svg 生成）
    installer/        # NSIS 自定义（hooks.nsh）
```

## 运行（开发）

```bash
cd apps/desktop
npm install
npm run tauri dev
```

开发服务器固定在 **5174**（避开官网的 5173），并注入
`VITE_AGENT_API_BASE=http://127.0.0.1:7700` 指向本机引擎。

## 打包

```bash
# 推荐：连 Go 引擎、安装界面品牌图一起产出
bash apps/agent/scripts/build-desktop.sh

# 只重出安装包（引擎与界面都已就绪时）
npx tauri build --bundles nsis
```

产物在 `src-tauri/target/release/bundle/nsis/`：`gridwright_<版本>_x64-setup.exe`。

> 文件名取自 `tauri.conf.json` 的 `productName`，**不要**在文档或脚本里写死 exe 名。
> 早期文档写的 `ark-desktop.exe` 是改名前的旧名。

## 版本号

单一来源是**本包的 `package.json`**；`tauri.conf.json` 写的是
`"version": "../package.json"`，会跟着走，**不要**在它里面再写一个数字。
`Cargo.toml` 的 `version` 是独立的一份，发版时要一起改。

## 环境要求

- Node 18+、npm
- Rust 工具链（MSVC）+ 链接器（Windows 下 VS Build Tools）
- WebView2 运行时（Win10/11 已内置；Win7/8 用单文件版，见 `ARCHITECTURE.md`）

## 说明

- 路由用 `HashRouter`：桌面环境没有 HTTP 服务器，`BrowserRouter` 的 History API
  在 Tauri 自定义协议下不可用
- 引擎日志由壳转存到 `%APPDATA%\gridwright\engine.log`（8 MiB × 3 轮转），
  壳自己的记录在 `shell.log`
- 深入文档在 `docs/agent-architecture/`（本地，不提交）
