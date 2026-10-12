package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCORSRejectsForeignOrigin 钉住这次修的安全边界：外站来源不能拿到 ACAO。
//
// 为什么重要：引擎只听 127.0.0.1 且没有鉴权，/api/v1/fs/list、/api/v1/workspace/open
// 这类接口能读写本机任意目录的文件。改之前无条件回 "Access-Control-Allow-Origin: *"，
// 于是用户随便打开一个网页，页面里的 JS 就能跨域驱动本机引擎（localhost CSRF /
// drive-by）。没有 ACAO，浏览器会拦下这个响应，攻击页面读不到任何东西。
func TestCORSRejectsForeignOrigin(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("外站来源不该拿到 ACAO，实际 %q", got)
	}
	// 请求本身仍要正常处理（服务端不因为 CORS 拒绝而改变行为）。
	if rec.Code != http.StatusOK {
		t.Errorf("请求仍应被正常处理，实际状态码 %d", rec.Code)
	}
}

// TestCORSAllowsLocalOrigin 本机来源要原样回显，并带上 Vary: Origin。
//
// 回显具体 origin（而不是 "*"）是为了将来要加凭据时不会踩坑；Vary: Origin
// 则是让中间缓存不要把"允许"的响应复用给别的来源。
func TestCORSAllowsLocalOrigin(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	rec = doCORSRequest(h, http.MethodGet, "/api/v1/health", "http://localhost:5173")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("本机来源应被回显，期望 %q，实际 %q", "http://localhost:5173", got)
	}
	if vary := rec.Header().Values("Vary"); !containsToken(vary, "Origin") {
		t.Errorf("回显 ACAO 时必须带 Vary: Origin，实际 %v", vary)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodOptions) {
		t.Errorf("允许的方法头不对：%q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
		t.Errorf("允许的请求头不对：%q", got)
	}
}

// TestCORSAllowsOriginsTable 把白名单的边界一次列清：本机各端口 + Tauri 自定义协议放行，
// 其余（外站、骗后缀域名、null、空 host）一律拒绝。
func TestCORSAllowsOriginsTable(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	cases := []struct {
		origin string
		allow  bool
	}{
		{"http://localhost", true},
		{"http://localhost:5173", true},
		{"http://127.0.0.1", true},
		{"http://127.0.0.1:7700", true},
		{"https://127.0.0.1:8443", true},
		{"tauri://localhost", true},
		{"https://tauri.localhost", true},

		{"http://evil.example", false},
		{"http://localhost.evil.example", false},
		{"http://evil.example/localhost", false},
		{"https://notlocalhost", false},
		{"null", false},
		{"file://", false},
		{"tauri://evil.example", false},
		{"ftp://localhost", false},
	}

	for _, c := range cases {
		rec := doCORSRequest(h, http.MethodGet, "/api/v1/health", c.origin)
		got := rec.Header().Get("Access-Control-Allow-Origin")
		if c.allow && got != c.origin {
			t.Errorf("origin %q 应被放行并回显，实际 ACAO=%q", c.origin, got)
		}
		if !c.allow && got != "" {
			t.Errorf("origin %q 不该被放行，实际 ACAO=%q", c.origin, got)
		}
	}
}

// TestCORSPreflight 预检：允许来源 → 204 + ACAO；不允许来源 → 仍是 204（行为不变），
// 但不能带 ACAO，否则浏览器会放行后续的真实请求。
func TestCORSPreflight(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	rec := doCORSRequest(h, http.MethodOptions, "/api/v1/plan", "http://localhost:5173")
	if rec.Code != http.StatusNoContent {
		t.Errorf("预检应回 204，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("预检的 ACAO 应为来源本身，实际 %q", got)
	}

	bad := doCORSRequest(h, http.MethodOptions, "/api/v1/plan", "http://evil.example")
	if bad.Code != http.StatusNoContent {
		t.Errorf("预检应回 204，实际 %d", bad.Code)
	}
	if got := bad.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("外站预检不该带 ACAO，实际 %q", got)
	}
}

// TestNoOriginStillServed 没有 Origin 的请求（curl、Vite dev proxy 转发的服务端请求）
// 必须照常工作，只是拿不到任何 CORS 头。
func TestNoOriginStillServed(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("无 Origin 的请求应照常处理，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("无 Origin 时不该下发 CORS 头，实际 ACAO=%q", got)
	}
}

func doCORSRequest(h http.Handler, method, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func containsToken(values []string, want string) bool {
	for _, v := range values {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), want) {
				return true
			}
		}
	}
	return false
}
