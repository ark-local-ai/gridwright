package logx

// 这个文件只负责一件事：把日志**同步**再落一份到按天分文件的 dir/engine-YYYY-MM-DD.log。
//
// 为什么 stdout 之外还要落文件：桌面壳已经把 stdout 落成 engine.log，但那是**单文件**，
// 且由壳做 8MiB×3 的大小轮转（见 docs/agent-architecture/31-日志与可观测.md 第五节）——
// 翻不了几天。按天分文件的这份是"按日期翻历史"用的，也不依赖壳是否在跑。
//
// 实现取舍：**不用定时器、不用后台 goroutine**。每次 Write 现取当前本地日期，日期与
// 当前打开的文件不一致就关旧开新。理由：
//   - 定时器要有人负责停（进程退出、测试结束），漏停就是 goroutine + 句柄泄漏；
//   - "跨天"本来就是一天才发生一次的极低频事件，写在 Write 路径上最省心，也不可能漏跑。
//   - 进程被杀时不会留下"轮转到一半"的状态：文件始终是按天追加，没有重命名/删除动作。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// now 是包内「当前时间」的**唯一**来源，做成变量只为了可测：测试替换它来模拟跨天，
// 否则只能真的等到半夜才验证得了按天切换（或者把日期硬编码进实现，那更糟）。
// 生产代码不要改它。
var now = time.Now

// dayLayout 既是文件名里的日期格式，也是"一天"的判定粒度。
// 用**本地**时间而不是 UTC：用户眼里的"今天"是他本地的今天，时区搞错会在文件名上直接露馅。
const dayLayout = "2006-01-02"

// dailyWriter 是一个按本地日期切换文件的 io.Writer。
//
// 它自己带锁，而不是指望调用方替它串行化：slog 的 handler 内部虽然有锁，但 io.Writer 是
// 独立的公开契约（MultiWriter、标准库 log 的 bridge 都会直接调 Write），谁调它都得安全。
// 一次 Write 全程持锁，所以不会出现两条记录交错成的"半行"——半行日志比没有日志更坏，
// 读的人会以为自己看懂了。
type dailyWriter struct {
	dir string

	mu  sync.Mutex
	day string   // 当前打开的文件对应的日期；"" = 还没打开
	f   *os.File // 当前文件句柄

	// failDay/failErr 记下"这一天已经确认打不开文件"。
	// 不记的话，目录被删/磁盘满之后**每条日志**都要重试一次 mkdir+open，
	// 日志越写不出去越拖慢引擎。第二天再给一次机会。
	failDay string
	failErr error
}

// Write 实现 io.Writer：必要时换文件，然后把 p 整段写进去。
func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	day := now().Format(dayLayout)
	if day != w.day || w.f == nil {
		if err := w.openLocked(day); err != nil {
			return 0, err
		}
	}

	n, err := w.f.Write(p)
	if err != nil {
		// 句柄可能已经坏了（盘满、文件被外部删掉、网络盘掉线）：关掉，下一条日志重新打开。
		// 不在这里立即重试重写：丢一条日志可以接受，把写日志的调用方堵在这里不可接受。
		_ = w.f.Close()
		w.f, w.day = nil, ""
	}
	return n, err
}

// openLocked 打开 day 对应的文件。调用方必须已持有 w.mu。
func (w *dailyWriter) openLocked(day string) error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	w.day = ""
	if w.failDay == day && w.failErr != nil {
		return w.failErr
	}
	// 目录可能被外部删掉（用户清理日志目录），所以每次换日期都补一次 MkdirAll。
	// SetupWithFile 也建过目录，但那只保证「启动那一刻」可写。
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return w.rememberFail(day, fmt.Errorf("建日志目录 %s: %w", w.dir, err))
	}
	name := filepath.Join(w.dir, "engine-"+day+".log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return w.rememberFail(day, fmt.Errorf("打开日志文件 %s: %w", name, err))
	}
	w.f, w.day, w.failDay, w.failErr = f, day, "", nil
	return nil
}

func (w *dailyWriter) rememberFail(day string, err error) error {
	w.failDay, w.failErr = day, err
	return err
}

// Close 关掉当前文件句柄（让 dailyWriter 也能当 io.Closer 用）。
// 文件是直接追加写的、没有缓冲，所以关掉只是释放句柄——测试里要读回文件、重装 logger
// 时要放下旧句柄（Windows 上句柄会拖住临时目录的删除），都靠它。
func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f, w.day = nil, ""
	return err
}

var (
	activeMu   sync.Mutex
	activeFile *dailyWriter
)

// setActiveFile 记住当前装着的文件写入器，并关掉上一个。
// 需要的理由：install 会被反复调用（测试里每个用例一次，将来界面改级别时也会），
// 旧的句柄不关就会一直开着那天的文件。
func setActiveFile(w *dailyWriter) {
	activeMu.Lock()
	old := activeFile
	activeFile = w
	activeMu.Unlock()
	if old != nil && old != w {
		_ = old.Close()
	}
}

// SetupWithFile 与 Setup 同义，另外把同一条日志**同步**再落到 dir/engine-YYYY-MM-DD.log。
//
// 两处都要的理由：stdout 落进桌面壳的 engine.log，是 GUI 下唯一能看到的地方（不落就没法排障）；
// 按天分文件解决的是另外两件事——单文件无限增长、以及"翻上个礼拜的原始日志"。
//
// dir 为空（或全空白）时等价于 Setup，返回 nil。
//
// 目录在 setup 时就建好：这是唯一能把"日志写不了"回报给调用方的时刻。但**文件本身延迟到
// 第一条日志才打开**——引擎可能整天一条日志都不写，setup 时开句柄是白占一个文件。
//
// 目录建不出来时**不返回 error 就拉倒**，而是降级为"仅 stdout"并把原因返回：
// 日志系统是排障工具，它自己坏掉不能反过来把引擎搞得起不来。调用方可以只记一条警告继续跑
// （忽略返回值也是安全的：stdout 一定还在工作）。
func SetupWithFile(level, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		install(level, nil) // 等价于 Setup：只打印，不落文件
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		install(level, nil)
		return fmt.Errorf("建日志目录 %s: %w（已降级为仅 stdout）", dir, err)
	}
	install(level, &dailyWriter{dir: dir})
	return nil
}
