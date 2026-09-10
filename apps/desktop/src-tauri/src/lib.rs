// =====================================================================
// Ark · 方舟 桌面壳（Tauri 2）
//
// 职责：把「空 Tauri 壳」变成「自包含、双击即用」的桌面应用——
//   1. 启动时探测本地后端，未运行则拉起随包分发的后端可执行文件（sidecar，
//      由 apps/backend/scripts/build-sea.mjs 产出的单文件 exe）；
//   2. 把后端数据目录（SQLite / 工作空间 / 技能）重定向到 per-user 应用数据目录，
//      避免写进安装目录；
//   3. 后端就绪 UI 由前端 BackendGate（src/main.tsx）负责（「后端未运行」面板 + 自动进入）；
//   4. 退出时清理后端子进程；
//   5. 单实例：再次打开聚焦既有窗口。
//
// ⚠️ 需 Rust 工具链编译验证：本机无 cargo/rustc/MSVC，此文件**无法在此编译**。
//    在有 Rust (stable + x86_64-pc-windows-msvc + VS Build Tools) 的机器上运行
//    `npm --prefix apps/desktop run desktop:build` 之前，请先：
//      a) 构建后端 exe：node apps/backend/scripts/build-sea.mjs
//      b) 复制为 sidecar：apps/desktop/src-tauri/binaries/ark-backend-x86_64-pc-windows-msvc.exe
//     （tauri.conf.json 的 externalBin: ["binaries/ark-backend"] 会按平台后缀寻找）。
// =====================================================================

use std::io::{Read, Write};
use std::net::TcpStream;
use std::sync::Mutex;
use std::time::Duration;

use tauri::Manager;
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;
use tauri_plugin_single_instance::init as single_instance;

struct BackendProcess(Mutex<Option<CommandChild>>);

/// 探测本地后端是否已就绪（TCP 到 127.0.0.1:4000 发 /health 请求，看是否 200）
fn backend_up() -> bool {
    if let Ok(mut s) = TcpStream::connect("127.0.0.1:4000") {
        let _ = s.write_all(b"GET /health HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n");
        let mut buf = [0u8; 64];
        if let Ok(n) = s.read(&mut buf) {
            let head = String::from_utf8_lossy(&buf[..n]).to_ascii_lowercase();
            return head.contains("200");
        }
    }
    false
}

/// 拉起随包分发、单文件打包的后端可执行文件（sidecar `ark-backend`），
/// 并把数据目录重定向到 per-user 应用数据目录。
fn spawn_backend(app: &tauri::App) -> Option<CommandChild> {
    let data_dir = app.path().app_data_dir().ok()?;
    let ws_dir = data_dir.join("workspace");
    let skill_dir = data_dir.join("skills");
    let _ = std::fs::create_dir_all(&ws_dir);
    let _ = std::fs::create_dir_all(&skill_dir);

    let cmd = app
        .shell()
        .sidecar("ark-backend")
        .ok()?
        .env("ARK_DATA_DIR", data_dir.to_string_lossy().to_string())
        .env("ARK_WORKSPACE_DIR", ws_dir.to_string_lossy().to_string())
        .env("ARK_SKILLS_DIR", skill_dir.to_string_lossy().to_string())
        .env("PORT", "4000");
    match cmd.spawn() {
        Ok(child) => Some(child),
        Err(e) => {
            eprintln!("[ark] 后端启动失败: {e}");
            None
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(single_instance(|app, _args, _cwd| {
            // 已有实例在跑：把主窗口调到前台
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .manage(BackendProcess(Mutex::new(None)))
        .setup(|app| {
            // 若后端没起，拉起并短等就绪（前端 BackendGate 会接管后续 UI/轮询）
            if !backend_up() {
                let child = spawn_backend(app);
                *app.state::<BackendProcess>().0.lock().unwrap() = child;
                for _ in 0..12 {
                    if backend_up() {
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(500));
                }
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building Ark · 方舟 desktop");

    app.run(|app_handle, event| {
        // 退出时清理后端子进程，避免残留 4000 端口占用
        if let tauri::RunEvent::Exit = event {
            if let Some(child) = app_handle
                .state::<BackendProcess>()
                .0
                .lock()
                .unwrap()
                .take()
            {
                let _ = child.kill();
            }
        }
    });
}
