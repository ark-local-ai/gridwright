package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPanicBecomes500 钉住 withCommon 的 panic 恢复。
//
// 为什么这个测试重要：恢复之前，任何一个 handler 里的 panic 都会让**整个引擎
// 进程**退出。引擎是随桌面壳拉起的 sidecar，进程一死，界面看到的是"突然连不上"，
// engine.log 里只剩 Go 运行时那串栈，之后所有请求全部失败，只能重启应用。
// 恢复之后：这一次请求拿到 500、线索（栈）留在日志里、进程继续服务。
func TestPanicBecomes500(t *testing.T) {
	h := withCommon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom: 故意的")
	}))

	rec := httptest.NewRecorder()
	// ServeHTTP 在这里**不能**把 panic 抛出来 —— 抛出来就说明没被恢复。
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 未转成 500，实际状态码 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "服务内部错误") {
		t.Errorf("500 的响应体里没有给用户的说明，实际：%s", rec.Body.String())
	}
}

// TestPanicAfterWriteHeaderDoesNotDoubleWrite 已经写出状态码后再 panic，
// 不能再去写一次 500 —— 那会触发 "superfluous response.WriteHeader call"，
// 而且会把已经发出去的正常响应污染掉。
func TestPanicAfterWriteHeaderDoesNotDoubleWrite(t *testing.T) {
	h := withCommon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		panic("写完头之后才炸")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("已写出的状态码被 panic 恢复覆盖成了 %d", rec.Code)
	}
	// httptest.ResponseRecorder 允许重复 WriteHeader 而不报错，所以这里
	// 直接看体：不该被追加 500 的 JSON。
	if strings.Contains(rec.Body.String(), "服务内部错误") {
		t.Errorf("头已写出后仍追写了 500 的内容：%s", rec.Body.String())
	}
}

// TestOptionsStillShortCircuits CORS 预检不能被这次重构改动行为。
func TestOptionsStillShortCircuits(t *testing.T) {
	reached := false
	h := withCommon(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/plan", nil))

	if reached {
		t.Error("OPTIONS 预检不该进到业务 handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS 应回 204，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS 头丢了，实际 %q", got)
	}
}
