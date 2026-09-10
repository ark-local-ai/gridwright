# Ark · 方舟 — 桌面端 Tauri 打包说明

> 目标：在**装有 Rust 工具链的 Windows 10/11 机器**上，把桌面版打成自包含安装包（NSIS `.exe` / MSI）。
> 桌面端前端已就绪、后端已打包为 SEA 单文件 exe 并作为 sidecar 分发，Rust 壳代码已写好。本文件只讲「把这一切编译成安装包」这一件事。

---

## 一、前置依赖（缺一不可）

| 依赖 | 用途 | 装法 |
|---|---|---|
| **Microsoft C++ Build Tools（VS Build Tools，含 MSVC `link.exe` + Windows SDK）** | Tauri 的 Rust 壳在 Windows 上链接需要 MSVC 链接器 | GUI 或命令行安装（见下） |
| **rustup + 稳定版 MSVC 工具链（cargo/rustc）** | 编译 Tauri 壳 | `rustup-init -y --profile minimal --default-toolchain stable-msvc --default-host x86_64-pc-windows-msvc` |
| **WebView2 运行时** | Tauri 2 的渲染引擎 | Win10/11 通常自带；缺失时安装 Microsoft Edge WebView2 Evergreen Runtime（桌面应用在首次启动时也可自动引导安装） |
| **Node.js ≥ 22** | 桌面前端构建（Tauri `beforeBuildCommand` 会跑 `npm run build`） | 已有 |

> **装 MSVC 关键点**：Tauri 需要的是「MSVC 工具集（x64）+ Windows 10/11 SDK」。命令行装（长耗时）：
> ```bat
> :: 下载 VisualStudio Build Tools 引导器
> curl -sSL -o vs_buildtools.exe https://aka.ms/vs/17/release/vs_buildtools.exe
> :: 静默安装：MSVC x64 工具集 + Win11 SDK + 中文字体(可选)
> vs_buildtools.exe --quiet --wait --norestart ^
>   --add Microsoft.VisualStudio.Workload.VCTools ^
>   --add Microsoft.VisualStudio.Component.VC.Tools.x86.x64 ^
>   --add Microsoft.VisualStudio.Component.Windows10SDK.19041 ^
>   --includeRecommended
> ```
> 体积较大（数 GB）。安装后需**新开一个终端**让 PATH 生效，或确认 `link.exe` 存在于
> `C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Tools\MSVC\<ver>\bin\Hostx64\x64\link.exe`。

---

## 二、本仓库打好一切后，只需三步

```bash
# 1) 确认后端 sidecar 已就位（M23 之后已复制好；若被 .gitignore 忽略了，重新生成一次）
cd apps/backend && node scripts/build-sea.mjs            # 产出 dist/ark-backend.exe
cp dist/ark-backend.exe ../desktop/src-tauri/binaries/ark-backend-x86_64-pc-windows-msvc.exe

# 2) 安装 Tauri CLI（cargo 侧）
cargo install tauri-cli --version "^2" --locked

# 3) 打包 —— 产物在 apps/desktop/src-tauri/target/release/bundle/
cd apps/desktop/src-tauri && cargo tauri build
```

打包产物：
- **NSIS 安装包**：`target/release/bundle/nsis/*.exe`
- **便携版**：`target/release/bundle/msi/*.msi`（若开 MSI）与 `target/release/ark-desktop.exe`

---

## 三、shell 里已经写好的行为（无需改代码）

`apps/desktop/src-tauri/src/lib.rs` 负责桌面应用前后端闭环：
1. **启动探测**：`.setup()` 里先探测 `127.0.0.1:4000/health`；
2. **自动拉后端**：未就绪则用 `tauri-plugin-shell` 的 `sidecar("ark-backend")` 拉起随包的 SEA 后端 exe（`binaries/ark-backend-x86_64-pc-windows-msvc.exe`），并注入 `ARK_DATA_DIR` / `ARK_WORKSPACE_DIR` / `ARK_SKILLS_DIR` / `PORT=4000`；
3. **就绪再显窗**：后端健康才显示主窗口（前端还有 `BackendGate` 恢复面板兜底）；
4. **单实例**：`tauri-plugin-single-instance` 防二次启动聚焦主窗；
5. **退出清理**：`RunEvent::Exit` 时杀掉后端子进程。

能力沙箱最小化：`capabilities/default.json` 仅放开窗口控制 + `shell:allow-execute`（限定到 `ark-backend` sidecar）。

---

## 四、已知坑 & 排查

| 症状 | 原因 / 处理 |
|---|---|
| `link.exe` 找不到 / `linker `x86_64-pc-windows-msvc` not found` | MSVC Build Tools 未装或 PATH 未生效；用 **新开终端** 重试，或装 VCTools workload |
| `rustc` 找不到 | rustup 未加到 PATH；`$env:USERPROFILE\.cargo\bin` 加入 PATH |
| 首次 `cargo build` 极慢 | Tauri 依赖多（数百个 crate），首次全量编译正常 5–15 分钟；之后增量 |
| `tauri build` 找不到 `ark-backend` sidecar | `binaries/ark-backend-x86_64-pc-windows-msvc.exe` 缺失；重新 `cp` 上一个 |
| 构建报 `signature seems corrupted`（postject） | 正常——未签名的 node.exe + fuse 哨兵导致，注入仍成功，可忽略 |
| 安装后双击无反应 / 白屏 | 确认释放的 `ark-backend` 能独立起：`ARK_DATA_DIR=<dir> ARK_WORKSPACE_DIR=<dir> PORT=4000 .\ark-backend.exe` 后 `curl 127.0.0.1:4000/health` |
| CSP 拦截 `127.0.0.1:4000` 请求 | `tauri.conf.json` 的 `security.csp` 已放行 `connect-src 'self' http://127.0.0.1:4000 http://localhost:4000`；若改端口需同步 |
| 企业/离线机装 WebView2 失败 | 需独立分发 Microsoft Edge WebView2 Evergreen Bootstrapper |

---

## 五、为什么桌面壳用 Rust（而不把后端也迁 Rust）

- **后端保持 Node/TS**：交付生成 docx/xlsx/pptx 无成熟 Rust 平替（xlsx 尚可，ppt/docx 弱）；迁 Rust 会把最核心交付能力置于重写风险。后端已打成 **SEA 单文件 exe**（`build-sea.mjs`，无 Node 依赖，双击即用），与 Rust 壳「分工」：Rust 管壳与生命周期，Node 管业务与交付。
- **Rust 只做 Tauri 壳**：自动拉后端 + 单实例 + 退出清理 + 沙箱，是 Rust/WebView2 的天然职责。
- **Win10/11（弃 Win7）**：Tauri 2 与 WebView2 依赖 Win10+，微软已停更 Win7 的 WebView2。

> 相关闭环：后端 SEA 构建见 `apps/backend/scripts/build-sea.mjs`；环境目录重定向见 `apps/backend/src/config/paths.ts`（`ARK_*`）。前端统一工作台见 `apps/frontend/src/App.tsx`。
