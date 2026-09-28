package logx

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"
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
	if strings.Contains(out, "/") {		t.Errorf("日志里出现了标准库的时间戳（含斜杠日期），双时间戳没修掉：%q", out)
	}
}
