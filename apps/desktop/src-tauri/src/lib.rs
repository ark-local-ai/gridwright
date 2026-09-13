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

/// 拉起随包的 Go 引擎（sidecar `gridwright`）。
///
/// **不要传 WORKSPACE**：工作区由用户在界面里选，引擎自己记在配置文件里
/// （%APPDATA%\gridwright\config.yaml）。早期版本在这里固定传一个默认目录，
/// 会把用户选的工作区覆盖掉——每次打开都回到默认目录，用户等于白选。
/// 只在首次运行（用户还没选）时，引擎自己落到临时目录并引导去选。
/// 写一行日志到 %APPDATA%\gridwright\shell.log。
/// GUI 程序没有控制台，eprintln 看不到——排障必须落文件。
fn log_line(msg: &str) {
    use std::io::Write;
    let dir = std::env::var("APPDATA").unwrap_or_else(|_| ".".into());
    let dir = std::path::Path::new(&dir).join("gridwright");
    let _ = std::fs::create_dir_all(&dir);
    if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(dir.join("shell.log")) {
        let _ = writeln!(f, "[{}] {}", chrono_like(), msg);
    }
}

/// 简单的本地时间串（避免为一行日志引入时间库）。
fn chrono_like() -> String {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    format!("unix:{}", now)
}

fn spawn_engine(app: &tauri::App) -> Option<Spawned> {
    // 配置目录**明确指定**给引擎：%APPDATA%\gridwright
    // （引擎默认也是这里，但不能靠"默认恰好一致"——早期版本里壳用的
    //   app_config_dir 是反向域名 com.gridwright.app，于是配置被分成两个目录，
    //   用户看到"设置没保留"却找不到原因。）
    let cfg_dir = std::env::var("APPDATA")
        .map(|a| std::path::Path::new(&a).join("gridwright"))
        .unwrap_or_else(|_| std::path::PathBuf::from("."));
    let _ = std::fs::create_dir_all(&cfg_dir);
    log_line(&format!("config_dir={:?}", cfg_dir));

    let sidecar = match app.shell().sidecar("gridwright") {
        Ok(c) => c,
        Err(e) => {
            log_line(&format!("sidecar 解析失败: {e}"));
            return None;
        }
    };
    // 把本壳的 PID 传给引擎：壳退出（含被强杀）时引擎监视到父进程消失会自行退出，
    // 不会留下占着 7700 端口的孤儿进程。
    let cmd = sidecar
        .args([
            "-api",
            &format!("127.0.0.1:{ENGINE_PORT}"),
            "-parent-pid",
            &std::process::id().to_string(),
        ])
        .env("GRIDWRIGHT_CONFIG_DIR", cfg_dir.to_string_lossy().to_string());
    match cmd.spawn() {
        Ok(child) => {
            log_line(&format!("引擎已拉起 pid={}", child.1.pid()));
            Some(child)
        }
        Err(e) => {
            log_line(&format!("引擎启动失败: {e}"));
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
            log_line("=== 应用启动 ===");
            if !engine_up() {
                log_line("引擎未就绪，尝试拉起");
                let child = spawn_engine(app);
                *app.state::<EngineProcess>().0.lock().unwrap() = child;
                let mut ok = false;
                for _ in 0..16 {
                    if engine_up() {
                        ok = true;
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(500));
                }
                log_line(if ok { "引擎就绪" } else { "等待引擎超时（8s）" });
            } else {
                log_line("引擎已在运行");
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
