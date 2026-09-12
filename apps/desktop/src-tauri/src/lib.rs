// =====================================================================
// gridwright 桌面壳（Tauri 2）
//
// 职责：把「空 Tauri 壳」变成「双击即用、离线可用」的桌面应用：
//   1. 启动时探测本地引擎（Go，127.0.0.1:7700），未运行则拉起随包 sidecar；
//   2. 把工作区目录放到 per-user 应用数据目录（不写安装目录/程序目录）；
//   3. 退出时清理引擎子进程；单实例防止重复开。
//
// 界面由 Tauri 自带协议加载打包好的前端资源（../dist）——**不指向任何 http 地址、
// 不依赖 dev server、不依赖网络**（见 docs/agent-architecture/20-离线可用与桌面交付.md）。
//
// 离线可用：引擎在没有 LLM api_key / 没有网络时也能启动，看表、体检、联动图、
// 账目照常工作；只有需要"判断"的改动类动作才要求配好脑。
// =====================================================================

use std::io::{Read, Write};
use std::net::TcpStream;
use std::sync::Mutex;
use std::time::Duration;

use tauri::Manager;
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;
use tauri_plugin_single_instance::init as single_instance;

/// 引擎监听的端口（与 internal/api 默认一致）
const ENGINE_PORT: u16 = 7700;

// tauri-plugin-shell 的 Command::spawn() 返回 (事件接收器, 子进程句柄)。
type Spawned = (tauri::async_runtime::Receiver<CommandEvent>, CommandChild);

struct EngineProcess(Mutex<Option<Spawned>>);

/// 探测本地引擎是否就绪（TCP 到 127.0.0.1:7700 发 /api/v1/health，看是否 200）。
fn engine_up() -> bool {
    let addr = format!("127.0.0.1:{ENGINE_PORT}");
    if let Ok(mut s) = TcpStream::connect(&addr) {
        let _ = s.write_all(b"GET /api/v1/health HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n");
        let mut buf = [0u8; 64];
        if let Ok(n) = s.read(&mut buf) {
            let head = String::from_utf8_lossy(&buf[..n]).to_ascii_lowercase();
            return head.contains("200");
        }
    }
    false
}

/// 拉起随包的 Go 引擎（sidecar `gridwright`），工作区落在 per-user 应用数据目录。
fn spawn_engine(app: &tauri::App) -> Option<Spawned> {
    let data_dir = app.path().app_data_dir().ok()?;
    let ws_dir = data_dir.join("workspace");
    let _ = std::fs::create_dir_all(&ws_dir);

    let cmd = app
        .shell()
        .sidecar("gridwright")
        .ok()?
        // 把本壳的 PID 传给引擎：壳退出（含被强杀）时引擎监视到父进程消失会自行退出，
        // 不会留下占着 7700 端口的孤儿进程。
        .args(["-api", &format!("127.0.0.1:{ENGINE_PORT}"), "-parent-pid", &std::process::id().to_string()])
        .env("WORKSPACE", ws_dir.to_string_lossy().to_string());
    match cmd.spawn() {
        Ok(child) => Some(child),
        Err(e) => {
            eprintln!("[gridwright] 引擎启动失败: {e}");
            None
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(single_instance(|app, _args, _cwd| {
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.show();
                let _ = w.set_focus();
            }
        }))
        .manage(EngineProcess(Mutex::new(None)))
        .setup(|app| {
            if !engine_up() {
                let child = spawn_engine(app);
                *app.state::<EngineProcess>().0.lock().unwrap() = child;
                // 短等就绪；前端会自己轮询并显示「正在启动」状态
                for _ in 0..16 {
                    if engine_up() {
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(500));
                }
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building gridwright desktop");

    app.run(|app_handle, event| {
        // 退出时清理引擎子进程，避免残留端口占用
        if let tauri::RunEvent::Exit = event {
            if let Some(child) = app_handle
                .state::<EngineProcess>()
                .0
                .lock()
                .unwrap()
                .take()
            {
                let _ = child.1.kill();
            }
        }
    });
}
