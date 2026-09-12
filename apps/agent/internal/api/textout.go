package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 纯文本输出（见 docs/agent-architecture/27-命令行可用.md）。
//
// 为什么需要：用户会在命令行/批处理里查这些只读结果。让 curl 直接拿到人能读的文本，
// **就不必在 .bat 里塞一段带引号的 Python**——cmd.exe 的引号规则和 bash 不同
// （cmd 用 "" 而非 \"），一段内联脚本很容易报错，且还要处理编码。
//
// 用法：在支持文本输出的路径后加 ?format=text
//
//	curl -s "http://127.0.0.1:7700/api/v1/weights?format=text"
func wantsText(r *http.Request) bool {
	f := strings.ToLower(r.URL.Query().Get("format"))
	return f == "text" || f == "txt" || f == "plain"
}

// writeText 输出纯文本（UTF-8，无 BOM）。
func writeText(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

// line 拼一行（便于各处组装）。
func line(format string, a ...any) string { return fmt.Sprintf(format, a...) + "\n" }
