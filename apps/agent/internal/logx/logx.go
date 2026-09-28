// Package logx 装配引擎的日志：一条日志只有一个时间戳、有级别、走 stdout。
//
// 为什么需要它（三个真实踩过的坑，见 docs/agent-architecture/10-踩坑与验证日志.md）：
//
//  1. **stdout vs stderr**：桌面壳把引擎的 stdout 与 stderr 分开落盘，stderr 的每一行
//     都被打上 `[stderr]` 前缀。而 Go 标准库 log 默认写 stderr —— 于是**每一个正常的
//     200 请求日志看上去都像报错**。排障时按 `[stderr]` 找不到任何东西。正常输出必须走 stdout。
//
//  2. **双时间戳**：标准库 log 自带 `2026/09/22 01:08:13`，桌面壳又加一层
//     `[2026-09-22 01:08:13]`，一行日志两个时间戳，且格式不同，无法直接排序比对。
//     这里把标准库那份关掉（SetFlags(0)），统一用 slog 的那一份。
//
//  3. **没有级别**：全是无差别的一行文本，`-log-level=debug` 这种"平时不吵、要时就有"
//     的能力无从谈起。改用 slog 后新代码可以直接 Debug/Info/Warn/Error。
package logx

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

// EnvLevel 是日志级别的环境变量名。桌面壳与命令行都可用它覆盖。
const EnvLevel = "GRIDWRIGHT_LOG_LEVEL"

// Setup 安装全局 logger。level 为空时回落到环境变量，再回落到 info。
//
// 输出一律走 **stdout**：引擎跑在 GUI 里，屏幕上看不到任何东西，唯一的排障通道
// 就是桌面壳把 stdout 落成的 engine.log。
func Setup(level string) {
	if strings.TrimSpace(level) == "" {
		level = os.Getenv(EnvLevel)
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: ParseLevel(level)})
	slog.SetDefault(slog.New(h))

	// 把标准库 log 也接到 slog 上，让现存的上百处 log.Printf 自动获得
	// 单时间戳 + 级别，而不必逐个改写成 slog 调用。
	// SetFlags(0) 去掉标准库自带的时间戳（见包注释第 2 条）。
	//
	// 标准库保证每次 Write 是一条完整记录，所以逐次转发即可，无需按行切分。
	log.SetFlags(0)
	log.SetOutput(bridge{})
}

// ParseLevel 解析级别名，无法识别时返回 LevelInfo。
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Enabled 报告某个级别当前是否会被输出。给"日志内容很贵、只在需要时才拼"的调用点用
// （例如把整段提示词摘要出来）。
func Enabled(l slog.Level) bool {
	return slog.Default().Enabled(nil, l)
}

// bridge 把标准库 log 的写入转成 slog 记录。
type bridge struct{}

func (bridge) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		slog.Info(msg)
	}
	return len(p), nil
}
