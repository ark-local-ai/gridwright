package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 本机 API 鉴权的边界用例。
//
// 这些用例钉住三件事：
//  1. 有令牌时，除 health/session 外一律 401；
//  2. 非回环 Host 一律 403（DNS rebinding）；
//  3. CORS 预检必须先于鉴权放行，否则浏览器看不到 401 的真正原因。
func TestAuthRequiresToken(t *testing.T) {
	s := &Server{Token: "secret-token"}
	h := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name   string
		path   string
		auth   string
		host   string
		status int
	}{
		{"无令牌", "/api/v1/workspace", "", "127.0.0.1:7700", http.StatusUnauthorized},
		{"错误令牌", "/api/v1/workspace", "Bearer nope", "127.0.0.1:7700", http.StatusUnauthorized},
		{"正确令牌", "/api/v1/workspace", "Bearer secret-token", "127.0.0.1:7700", http.StatusOK},
		{"health 免令牌", "/api/v1/health", "", "127.0.0.1:7700", http.StatusOK},
		{"session 免令牌", "/api/v1/session", "", "127.0.0.1:7700", http.StatusOK},
		{"非回环 Host", "/api/v1/workspace", "Bearer secret-token", "evil.example", http.StatusForbidden},
		{"本机别名 localhost", "/api/v1/workspace", "Bearer secret-token", "localhost:7700", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = tc.host
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d，期望 %d（body=%s）", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestAuthDisabledWhenTokenEmpty(t *testing.T) {
	s := &Server{}
	h := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspace", nil)
	req.Host = "example.com" // 测试环境（httptest）的默认 Host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Token 为空时应跳过鉴权，status=%d", rec.Code)
	}
}

func TestSessionOnlyForLocalOrigin(t *testing.T) {
	s := &Server{Token: "t0ken"}
	h := s.withAuth(http.HandlerFunc(s.handleSession))

	// 跨站页面：拿不到令牌。
	req := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	req.Host = "127.0.0.1:7700"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨站 Origin 应 403，得到 %d", rec.Code)
	}

	// 本机页面：正常领取。
	req = httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	req.Host = "127.0.0.1:7700"
	req.Header.Set("Origin", "https://tauri.localhost")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("本机 Origin 应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if want := `"token":"t0ken"`; !containsStr(rec.Body.String(), want) {
		t.Fatalf("响应里没有令牌：%s", rec.Body.String())
	}
}

// 预检不带 Authorization，必须先于鉴权返回 204，否则浏览器会把
// "401" 显示成 CORS 失败，用户完全看不到原因。
func TestPreflightPassesBeforeAuth(t *testing.T) {
	s := &Server{Token: "secret"}
	h := withCommon(s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("预检不应进入业务 handler")
	})))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/apply", nil)
	req.Host = "127.0.0.1:7700"
	req.Header.Set("Origin", "http://localhost:5174")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("预检应 204，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !containsStr(got, "Authorization") {
		t.Fatalf("预检未放行 Authorization：%q", got)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "127.0.0.1:7700", "localhost", "localhost:5174", "[::1]:7700", "::1"} {
		if !isLoopbackHost(h) {
			t.Errorf("%q 应视为回环", h)
		}
	}
	for _, h := range []string{"", "evil.example", "192.168.1.10:7700", "127.0.0.1.evil.example"} {
		if isLoopbackHost(h) {
			t.Errorf("%q 不应视为回环", h)
		}
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
