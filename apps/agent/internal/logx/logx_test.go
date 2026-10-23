package logx

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestParseLevel 级别名解析，含大小写/空白与无法识别的回落。
func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		" debug ": slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"乱写":      slog.LevelInfo, // 认不出来时不能把日志整个静音
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v，期望 %v", in, got, want)
		}
	}
}

// TestSetupHonoursEnvLevel 参数为空时读环境变量——桌面壳只设环境变量，不传命令行。
func TestSetupHonoursEnvLevel(t *testing.T) {
	t.Setenv(EnvLevel, "debug")
	defer Setup("")
	Setup("")
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("设了 " + EnvLevel + "=debug 后 debug 仍被过滤")
	}
}

// TestSetupQuietByDefault 默认不输出 debug：
// 模型请求/响应正文是 Debug，默认必须在文件里看不到（否则日志会大到没法看）。
func TestSetupQuietByDefault(t *testing.T) {
	t.Setenv(EnvLevel, "")
	Setup("")
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("默认级别不该放行 debug")
	}
}

// TestLogGoesToStdoutNotStderr 这是本项目最贵的一个坑：
// 桌面壳把 stderr 的每一行都打上 [stderr]，若正常日志写 stderr，
// 排障时会以为满屏都是错误。标准库 log 默认就是 stderr，所以必须显式改掉。
// 这里同时钉住"单时间戳"：标准库自带的那份时间戳必须被关掉。
func TestLogGoesToStdoutNotStderr(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	Setup("info")
	// 走标准库 log —— 现存的上百处 log.Printf 就是这条路径。
	log.Printf("MUST-NOT-BE-ON-STDERR")

	// 关掉写端才会 φEOF，ReadAll 不会挂住：若日志错走了 stderr，
	// 这里读到的会是空串，断言下面就会以正确的理由失败。
	_ = w.Close()
	os.Stdout = old
	outb, _ := io.ReadAll(r)
	out := string(outb)

	if !strings.Contains(out, "MUST-NOT-BE-ON-STDERR") {
		t.Fatalf("stdout 里没有这条日志，说明它没走 stdout：%q", out)
	}
	// 标准库的格式是 "2026/09/22 01:08:13 msg"。关掉 SetFlags 后不该再有斜杠日期。
	if strings.Contains(out, "/") {
		t.Errorf("日志里出现了标准库的时间戳（含斜杠日期），双时间戳没修掉：%q", out)
	}
}

// —— 以下是「按天分文件」与「代码块打印」的测试 ——

// freezeNow 把包内时钟钉在 ts，测试结束自动还原。
// 时钟必须是可注入的（见 file.go 的 now）：否则"跨天换文件"只能靠真的等到半夜，
// 或者把日期硬编码进实现——两条路都比替换一个变量差。
func freezeNow(t *testing.T, ts time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return ts }
	t.Cleanup(func() { now = old })
}

// captureStdout 把进程 stdout 换成管道，返回「关掉写端并读回全部输出」的函数。
// 日志必须走 stdout 是本项目的硬约束（见 logx.go 包注释第 1 条），所以测试要真的从
// stdout 读——只从 handler 里读，测不到落点。
// 注意：调用方要保证两次 read 之间写出的日志量小于管道缓冲区。
func captureStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })
	return func() string {
		_ = w.Close() // 关掉写端才会 EOF，ReadAll 不会挂住
		os.Stdout = old
		b, _ := io.ReadAll(r)
		return string(b)
	}
}

// logPath 是某一天的日志文件路径，与实现里的命名保持一致。
func logPath(dir string, day time.Time) string {
	return filepath.Join(dir, "engine-"+day.Format(dayLayout)+".log")
}

// readDay 读回某一天的日志文件；不存在时返回 ""（用来断言"这天根本没写过"）。
func readDay(t *testing.T, dir string, day time.Time) string {
	t.Helper()
	b, err := os.ReadFile(logPath(dir, day))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("读日志文件 %s: %v", logPath(dir, day), err)
	}
	return string(b)
}

// TestSetupWithFileWritesToStdoutAndFile 一条日志必须**同时**出现在 stdout 与按天文件里：
// stdout 是桌面壳 engine.log 的来源，文件是"自己翻历史"的那份，缺哪个都算退化。
func TestSetupWithFileWritesToStdoutAndFile(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	freezeNow(t, day)
	read := captureStdout(t)

	if err := SetupWithFile("info", dir); err != nil {
		t.Fatalf("SetupWithFile: %v", err)
	}
	defer Setup("") // 关掉文件句柄，别拖住 t.TempDir 的清理

	// 延迟打开：一条日志都还没写，文件就不该存在（进程可能整天不写日志，
	// setup 时就开句柄是白占一个文件）。
	if _, err := os.Stat(logPath(dir, day)); err == nil {
		t.Error("第一条日志之前文件就被创建了，不是延迟打开")
	}

	log.Printf("PATH-BRIDGE") // 走标准库 → bridge → slog：仓里上百处 log.Printf 就是这条路径
	slog.Info("PATH-SLOG")    // 走 slog 直接调用

	out := read()
	for _, want := range []string{"PATH-BRIDGE", "PATH-SLOG"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout 里没有 %s，说明它没走 stdout：%q", want, out)
		}
	}

	got := readDay(t, dir, day)
	for _, want := range []string{"PATH-BRIDGE", "PATH-SLOG"} {
		if !strings.Contains(got, want) {
			t.Errorf("文件 %s 里没有 %s：%q", logPath(dir, day), want, got)
		}
	}
	// 每条记录只能有一个时间戳（见包注释第 2 条）。两条日志 = 两个 time=；
	// 若 bridge 又把标准库那份时间戳带回来，这里会数出 4 个。
	if n := strings.Count(got, "time="); n != 2 {
		t.Errorf("文件里有 %d 个 time=，期望 2（每条记录一个）：%q", n, got)
	}
}

// TestSetupWithFileEmptyDirIsSetup dir 为空（或全空白）时等价于 Setup：不落任何文件。
func TestSetupWithFileEmptyDirIsSetup(t *testing.T) {
	read := captureStdout(t)
	if err := SetupWithFile("info", "  "); err != nil {
		t.Fatalf("dir 为空不该报错: %v", err)
	}
	slog.Info("ONLY-STDOUT")
	if out := read(); !strings.Contains(out, "ONLY-STDOUT") {
		t.Fatalf("dir 为空时 stdout 也不工作了：%q", out)
	}
}

// TestSetupWithFileRotatesByDay 跨天必须换到新日期的文件，且**旧文件从此不再被写**。
// 时钟是注入的，所以这个用例在一天里的任何时刻跑结果都一样。
func TestSetupWithFileRotatesByDay(t *testing.T) {
	dir := t.TempDir()
	day1 := time.Date(2026, 9, 22, 23, 59, 0, 0, time.Local)
	day2 := day1.Add(2 * time.Minute)

	// stdout 不参与这个用例的断言：接走就行，别把日志刷进 go test 的输出。
	defer captureStdout(t)()

	cur := day1
	old := now
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = old })

	if err := SetupWithFile("info", dir); err != nil {
		t.Fatalf("SetupWithFile: %v", err)
	}
	defer Setup("")

	slog.Info("DAY-ONE")
	if got := readDay(t, dir, day1); !strings.Contains(got, "DAY-ONE") {
		t.Fatalf("第一天的文件里没有第一天的日志：%q", got)
	}
	if got := readDay(t, dir, day2); got != "" {
		t.Fatalf("还没跨天就写出了第二天文件的内容 %q", got)
	}

	cur = day2 // 跨天
	slog.Info("DAY-TWO")

	if got := readDay(t, dir, day1); strings.Contains(got, "DAY-TWO") {
		t.Errorf("跨天后旧文件还在被写：%q", got)
	}
	if got := readDay(t, dir, day2); !strings.Contains(got, "DAY-TWO") {
		t.Errorf("新日期的文件里没有日志：%q", got)
	}

	// 时钟往回走（NTP 校时/用户改系统时间）时按当前日期重新打开：日志宁可分到两个
	// 文件里，也不能丢，更不能写进名字与内容对不上的文件。
	cur = day1
	slog.Info("DAY-ONE-AGAIN")
	if got := readDay(t, dir, day1); !strings.Contains(got, "DAY-ONE-AGAIN") {
		t.Errorf("时钟回拨后日志没落回当天文件：%q", got)
	}
	if got := readDay(t, dir, day1); !strings.Contains(got, "DAY-ONE") {
		t.Errorf("时钟回拨把旧内容弄丢了：%q", got)
	}
}

// TestSetupWithFileDegradesWhenDirUnusable 目录建不出来（这里拿一个已存在的**文件**当目录）
// 时不能 panic、更不能让引擎起不来：降级为仅 stdout，并把原因返回给调用方。
// 取舍见 file.go 里 SetupWithFile 的注释：日志是排障工具，不能反过来变成故障源。
func TestSetupWithFileDegradesWhenDirUnusable(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := captureStdout(t)

	if err := SetupWithFile("info", notADir); err == nil {
		t.Error("目录建不出来却没返回 error")
	}
	slog.Info("STILL-ALIVE")
	if out := read(); !strings.Contains(out, "STILL-ALIVE") {
		t.Fatalf("降级之后 stdout 也不工作了：%q", out)
	}
}

// TestFileConcurrentWrites 并发写：行数一条不少、每行都完整。
// 交错出来的"半行"日志比没有日志更坏——读的人会以为自己看懂了。
// 这个用例同时是 -race 的靶子（scripts/test-race.sh ./internal/logx/...）。
func TestFileConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 9, 22, 3, 0, 0, 0, time.Local)
	freezeNow(t, day)

	// stdout 引到临时文件：400 行日志别刷满 go test 的输出。
	// 不能用没人读的 os.Pipe —— 缓冲区写满会把写入方堵死，测试直接挂住。
	sink, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	oldOut := os.Stdout
	os.Stdout = sink
	t.Cleanup(func() { os.Stdout = oldOut; _ = sink.Close() })

	if err := SetupWithFile("info", dir); err != nil {
		t.Fatalf("SetupWithFile: %v", err)
	}
	defer Setup("")

	const (
		workers = 8
		perG    = 50
	)
	pad := strings.Repeat("x", 200)
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				slog.Info("concurrent", "g", g, "i", i, "pad", pad)
			}
		}(g)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(readDay(t, dir, day), "\n"), "\n")
	if len(lines) != workers*perG {
		t.Fatalf("文件里 %d 行，期望 %d 行", len(lines), workers*perG)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, "time=") || !strings.Contains(ln, "pad="+pad) {
			t.Fatalf("第 %d 行不完整（被并发写切开了？）：%.90q", i+1, ln)
		}
		if n := strings.Count(ln, "time="); n != 1 {
			t.Fatalf("第 %d 行有 %d 个 time=，说明两行粘在一起了：%.90q", i+1, n, ln)
		}
	}
}

// TestBlockIndentsBody 标题独占一行、正文每行缩进两格、空行照缩进保留。
// 缩进是 slog 的纯文本流里唯一能把"这几行属于同一条记录"表示出来的手段。
func TestBlockIndentsBody(t *testing.T) {
	read := captureStdout(t)
	Setup("debug")

	Block("改动清单", "- A03 本月实收 → H8\n\n- B07 上期欠款 → I8")
	out := read()

	title := strings.Index(out, "msg=改动清单")
	l1 := strings.Index(out, `msg="  - A03 本月实收 → H8"`)
	l2 := strings.Index(out, `msg="  - B07 上期欠款 → I8"`)
	if title < 0 || l1 < 0 || l2 < 0 {
		t.Fatalf("标题或正文没按缩进格式输出：%q", out)
	}
	if !(title < l1 && l1 < l2) {
		t.Fatalf("标题/正文顺序乱了：title=%d l1=%d l2=%d %q", title, l1, l2, out)
	}
	// 空行也要缩进保留：表格行预览/JSON 里空行是结构的一部分。
	if !strings.Contains(out, `msg="  "`) {
		t.Errorf("正文里的空行被丢掉了：%q", out)
	}
	// Block 是 Debug 级：这里不能出现 INFO 记录。
	if strings.Contains(out, "level=INFO") {
		t.Errorf("Block 不该用 INFO 级：%q", out)
	}
}

// TestStepFieldsAndLevel Block 是 Debug（默认不出），Step 是 Info（默认要出）——
// 这个区别就是"平时不吵、要时就有"的落点。
func TestStepFieldsAndLevel(t *testing.T) {
	read := captureStdout(t)
	Setup("info")

	Block("提示词", "这段正文默认级别下不该出现")
	Step("plan", "locate", "sheet", "2026年1月", "cells", 12)
	out := read()

	if strings.Contains(out, "这段正文默认级别下不该出现") {
		t.Errorf("info 级别下 Block 还在输出正文：%q", out)
	}
	for _, want := range []string{
		"level=INFO", "flow=plan", "step=locate", "sheet=2026年1月", "cells=12",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Step 的输出里缺少 %s：%q", want, out)
		}
	}
}

// TestStepKeepsOddKV Step 不替调用方兜住"键值不成对"：该错误必须在日志里看得见
// （slog 会渲染成 !BADKEY），悄悄吞掉只会让调用点一直错下去。
func TestStepKeepsOddKV(t *testing.T) {
	read := captureStdout(t)
	Setup("info")
	Step("plan", "locate", "只有键没有值")
	if out := read(); !strings.Contains(out, "!BADKEY") {
		t.Errorf("奇数个 kv 没在日志里暴露出来：%q", out)
	}
}
