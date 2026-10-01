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
use std::path::PathBuf;
use std::sync::{Mutex, OnceLock};
use std::time::Duration;

use chrono::{FixedOffset, Local, TimeZone};
use tauri::Manager;
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;
use tauri_plugin_single_instance::init as single_instance;

/// 引擎监听的端口（与 internal/api 默认一致）
const ENGINE_PORT: u16 = 7700;

// =====================================================================
// 日志目录
//
// 日志是**壳**在写（引擎只往 stdout 打，壳把子进程输出落盘），所以“日志放哪”
// 是壳的事。默认放在 **exe 所在的安装目录下 `logs/`**，不再塞进漫游的 %APPDATA%——
// 用户能在安装目录一眼找到它；还可在「设置 → 日志」里改到别处（存在 shell.json）。
// =====================================================================

/// 当前日志目录（进程内缓存；改设置时直接更新）。
static LOG_DIR: OnceLock<Mutex<PathBuf>> = OnceLock::new();

/// 壳自己的设置文件：%APPDATA%\gridwright\shell.json（与 config.yaml 同目录）。
fn shell_cfg_path() -> PathBuf {
    let base = std::env::var("APPDATA").unwrap_or_else(|_| ".".into());
    std::path::Path::new(&base).join("gridwright").join("shell.json")
}

/// 默认日志目录：exe 所在的安装目录下的 `logs/`。
fn default_log_dir() -> PathBuf {
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.join("logs")))
        .unwrap_or_else(|| PathBuf::from("logs"))
}

/// 解析日志目录：优先 shell.json 里的 logDir，否则用安装目录下的 logs/。
fn read_log_dir() -> PathBuf {
    if let Ok(txt) = std::fs::read_to_string(shell_cfg_path()) {
        if let Ok(v) = serde_json::from_str::<serde_json::Value>(&txt) {
            if let Some(s) = v.get("logDir").and_then(|x| x.as_str()) {
                if !s.trim().is_empty() {
                    return PathBuf::from(s.trim());
                }
            }
        }
    }
    default_log_dir()
}

/// 当前日志目录（带锁，写日志那一刻才读，所以改设置可**立即生效**）。
fn log_dir_handle() -> &'static Mutex<PathBuf> {
    LOG_DIR.get_or_init(|| Mutex::new(read_log_dir()))
}

fn current_log_dir() -> PathBuf {
    log_dir_handle()
        .lock()
        .map(|d| d.clone())
        .unwrap_or_else(|_| default_log_dir())
}

/// 设置 → 日志：读当前日志目录。
#[tauri::command]
fn get_log_dir() -> String {
    current_log_dir().to_string_lossy().to_string()
}

/// 设置 → 日志：改日志目录。立即生效（logger 在写那一刻才读路径），并落盘到 shell.json。
#[tauri::command]
fn set_log_dir(dir: String) -> Result<String, String> {
    let dir = dir.trim();
    if dir.is_empty() {
        return Err("日志目录不能为空".into());
    }
    let p = PathBuf::from(dir);
    if let Err(e) = std::fs::create_dir_all(&p) {
        return Err(format!("无法创建该目录：{e}"));
    }
    let path = shell_cfg_path();
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    let cfg = serde_json::json!({ "logDir": p.to_string_lossy() });
    if let Err(e) = std::fs::write(&path, serde_json::to_string_pretty(&cfg).unwrap_or_default()) {
        return Err(format!("保存设置失败：{e}"));
    }
    if let Ok(mut g) = log_dir_handle().lock() {
        *g = p.clone();
    }
    log_to("shell.log", &format!("日志目录已改为 {}", p.to_string_lossy()));
    Ok(p.to_string_lossy().to_string())
}

/// 设置 → 日志：用资源管理器打开日志所在文件夹。
#[tauri::command]
fn open_log_dir() -> Result<(), String> {
    let dir = current_log_dir();
    let _ = std::fs::create_dir_all(&dir);
    #[cfg(target_os = "windows")]
    let mut cmd = std::process::Command::new("explorer");
    #[cfg(not(target_os = "windows"))]
    let mut cmd = std::process::Command::new("open");
    cmd.arg(dir.as_os_str())
        .spawn()
        .map(|_| ())
        .map_err(|e| format!("打开失败：{e}"))
}

// tauri-plugin-shell 的 Command::spawn() 返回 (事件接收器, 子进程句柄)。
// 接收器一经 spawn 就交给 logs 任务接管（见 pipe_engine_log），
// 所以这里只留子进程句柄——退出时要 kill 的是它。
type Spawned = CommandChild;

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
    log_to("shell.log", msg);
}

/// 写一行到指定的日志文件（与 shell.log 同目录），**带壳的时间戳**。
/// 引擎日志单独一个文件：它比壳啰嗦得多，混在一起会把壳的几行关键记录淹掉。
/// 只用于壳自己产生的行；引擎的行走 log_raw。
fn log_to(file: &str, msg: &str) {
    append_line(file, &format!("[{}] {}", chrono_like(), msg));
}

/// 原样追加一行，**不加壳的时间戳**。
///
/// 引擎自己的日志已经带 `time=… level=…`（见 Go 侧 internal/logx）。壳若再加一层
/// `[2026-09-22 01:08:13]`，一行日志就有两个格式不同的时间戳，没法排序也没法比对。
/// 壳与引擎是两个进程，谁的时间戳都不该叠在对方头上——所以引擎的行原样落地。
fn log_raw(file: &str, msg: &str) {
    append_line(file, msg);
}

/// 单个日志文件的上限，超过就轮转（engine.log → .1 → .2 → .3，最老的丢掉）。
///
/// 为什么必须轮转：日志是**只增不减**的，而 GUI 用户永远不会手动去删
/// %APPDATA% 下的文件。平时一天几十 KB 看着不多，但只要有个反复失败的目录
/// 被挂在轮询里，一天就能涨到几十 MB。
const LOG_MAX_BYTES: u64 = 8 << 20; // 8 MiB
const LOG_KEEP: u32 = 3;

/// 追加一行到当前日志目录下的 <file>。
fn append_line(file: &str, text: &str) {
    use std::io::Write;
    let dir = current_log_dir();
    let _ = std::fs::create_dir_all(&dir);
    let path = dir.join(file);
    if let Ok(m) = std::fs::metadata(&path) {
        if m.len() >= LOG_MAX_BYTES {
            rotate(&path);
        }
    }
    if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(&path) {
        let _ = writeln!(f, "{text}");
    }
}

/// 把 path 轮转一圈：.3 丢掉，.2→.3，.1→.2，本体→.1。
fn rotate(path: &std::path::Path) {
    let base = path.to_string_lossy().into_owned();
    let _ = std::fs::remove_file(format!("{base}.{LOG_KEEP}"));
    for i in (1..LOG_KEEP).rev() {
        let _ = std::fs::rename(format!("{base}.{i}"), format!("{base}.{}", i + 1));
    }
    let _ = std::fs::rename(path, format!("{base}.1"));
}

/// 把引擎（sidecar）的 stdout/stderr 落到 %APPDATA%\gridwright\engine.log。
///
/// 为什么必须有这个：spawn 返回的**事件接收器原来被直接丢掉了**，于是引擎
/// 那句 "Gridwright API 监听 …" 和所有出错信息都进了黑洞——屏幕上没有控制台，
/// 出了问题无从查起。接管它，引擎说什么就记什么。
///
/// 引擎每行日志是独立的 CommandEvent（插件按行切好），所以这里逐行追加即可。
fn pipe_engine_log(
    mut rx: tauri::async_runtime::Receiver<CommandEvent>,
) {
    tauri::async_runtime::spawn(async move {
        log_to("engine.log", "=== 引擎输出开始 ===");
        while let Some(ev) = rx.recv().await {
            match ev {
                CommandEvent::Stdout(bytes) => {
                    let line = String::from_utf8_lossy(&bytes);
                    let line = line.trim_end();
                    if !line.is_empty() {
                        // 原样落地：引擎自己带 time=/level=，壳不再叠一层时间戳。
                        log_raw("engine.log", line);
                    }
                }
                CommandEvent::Stderr(bytes) => {
                    let line = String::from_utf8_lossy(&bytes);
                    let line = line.trim_end();
                    if !line.is_empty() {
                        // [stderr] 标记保留：Go 侧正常日志已经改走 stdout（internal/logx），
                        // 所以现在标了 [stderr] 的行**真的是**错误输出，这个标记重新变得可信。
                        log_raw("engine.log", &format!("[stderr] {line}"));
                    }
                }
                CommandEvent::Error(e) => log_to("engine.log", &format!("[spawn error] {e}")),
                CommandEvent::Terminated(p) => {
                    log_to("engine.log", &format!("=== 引擎退出 code={:?} ===", p.code));
                    break;
                }
                _ => {}
            }
        }
    });
}

/// 把“某一刻 + 某个 UTC 偏移”渲染成 yyyy-MM-dd HH:mm:ss。
///
/// 单独抽出来是为了**能被测**。时区这种错的隐蔽之处在于：偏移写错了，输出仍然
/// 是一个完全合法的时间串，只是不对——看不出任何异常。做成纯函数，就能用一个
/// “非 UTC+8”的输入把“偏移真的被用上了”钉死。
/// （本机时区恰好是 UTC+8，光跑主流程永远测不出来。）
fn fmt_local(secs: i64, offset_secs: i32) -> String {
    let offset = FixedOffset::east_opt(offset_secs)
        .or_else(|| FixedOffset::east_opt(0))
        .expect("UTC 偏移 0 总是合法的");
    match offset.timestamp_opt(secs, 0).single() {
        Some(dt) => dt.format("%Y-%m-%d %H:%M:%S").to_string(),
        None => String::new(),
    }
}

/// 本地可读时间串 yyyy-MM-dd HH:mm:ss。
///
/// 偏移**问操作系统要**（`Local::now().offset()`，已含夏令时），不再自己算。
///
/// 早先这里写的是 `secs + 8 * 3600`，注释的理由是“本产品只面向中文办公机”。
/// 这个前提站不住：时区是机器上可改的东西。用户在 UTC+2 的机器上打开，日志时间
/// 整整差 6 小时——而看到时间不对，人的第一反应是“是不是漏记了一段”，
/// 于是往完全错的方向排查。与其猜一个偏移，不如问一句。
///
/// （原先这里还写着“引 chrono 不值当”。chrono 早就在依赖树里，用它没有任何额外成本。）
fn chrono_like() -> String {
    let now = Local::now();
    fmt_local(now.timestamp(), now.offset().local_minus_utc())
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
        Ok((rx, child)) => {
            log_line(&format!("引擎已拉起 pid={}", child.pid()));
            // 接管引擎输出：不接管就等于把引擎的日志全丢掉（见 pipe_engine_log）。
            // 接收器移交后不再持有，返回的只是子进程句柄。
            pipe_engine_log(rx);
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
        .invoke_handler(tauri::generate_handler![get_log_dir, set_log_dir, open_log_dir])
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
                let _ = child.kill();
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::{chrono_like, fmt_local};
    use chrono::{Local, NaiveDate, TimeZone, Utc};

    /// 2026-01-01 00:00:00 UTC 的 unix 秒。用 chrono 自己算，不手写魔数。
    fn t2026() -> i64 {
        Utc.from_utc_datetime(
            &NaiveDate::from_ymd_opt(2026, 1, 1)
                .unwrap()
                .and_hms_opt(0, 0, 0)
                .unwrap(),
        )
        .timestamp()
    }

    #[test]
    fn offset_is_actually_used_not_hardcoded_utc8() {
        let t = t2026();
        assert_eq!(fmt_local(t, 8 * 3600), "2026-01-01 08:00:00");
        // 关键断言：换个偏移必须换出不同的墙上时间。
        // 哪天又冒出“写死东八区”的行为，这几条会红。
        assert_eq!(fmt_local(t, 2 * 3600), "2026-01-01 02:00:00");
        assert_eq!(fmt_local(t, -5 * 3600), "2025-12-31 19:00:00");
        assert_eq!(fmt_local(t, 0), "2026-01-01 00:00:00");
        assert_eq!(fmt_local(t, 14 * 3600), "2026-01-01 14:00:00");
    }

    #[test]
    fn date_rollover_follows_the_offset() {
        // 2025-12-31 20:00:00 UTC：东八区已跨年，西五区还在前一年
        let t = Utc
            .from_utc_datetime(
                &NaiveDate::from_ymd_opt(2025, 12, 31)
                    .unwrap()
                    .and_hms_opt(20, 0, 0)
                    .unwrap(),
            )
            .timestamp();
        assert_eq!(fmt_local(t, 8 * 3600), "2026-01-01 04:00:00");
        assert_eq!(fmt_local(t, -5 * 3600), "2025-12-31 15:00:00");
    }

    #[test]
    fn illegal_offset_falls_back_to_utc_without_panic() {
        // 偏移必须落在 ±24h 内；越界时退回 UTC，而不是 panic 掉整个壳
        assert_eq!(fmt_local(t2026(), 200_000), "2026-01-01 00:00:00");
        assert_eq!(fmt_local(t2026(), -200_000), "2026-01-01 00:00:00");
    }

    #[test]
    fn chrono_like_matches_the_os_local_time() {
        // 不写死期望值：要求我们的渲染与 chrono 自己对同一刻的本地渲染一致，
        // 也就证明了“偏移确实取自操作系统”。
        let now = Local::now();
        let off = now.offset().local_minus_utc();
        assert!(
            (-12 * 3600..=14 * 3600).contains(&off),
            "系统报出的时区偏移不合理: {off} 秒"
        );
        assert_eq!(
            fmt_local(now.timestamp(), off),
            now.format("%Y-%m-%d %H:%M:%S").to_string()
        );
        assert_eq!(chrono_like().len(), 19, "格式应为 yyyy-MM-dd HH:mm:ss");
    }
}
