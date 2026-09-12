package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTextOutputIsPlainText 命令行用：?format=text 应回纯文本而非 JSON。
// 用户反馈 .bat 里塞 Python 解析 JSON 会因 cmd 引号规则报错，故由引擎直接吐文本。
func TestTextOutputIsPlainText(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	for _, path := range []string{
		"/api/v1/workspace?format=text",
		"/api/v1/weights?format=text",
		"/api/v1/safety?format=text",
		"/api/v1/scan?format=text",
		"/api/v1/ledger?format=text",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Errorf("%s 状态 %d：%s", path, rec.Code, rec.Body.String())
			continue
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/plain") {
			t.Errorf("%s 应回 text/plain，得到 %q", path, ct)
		}
		body := rec.Body.String()
		// 纯文本不该是 JSON 对象开头
		if strings.HasPrefix(strings.TrimSpace(body), "{") {
			t.Errorf("%s 应回文本而非 JSON：%s", path, body[:min(80, len(body))])
		}
		if body == "" {
			t.Errorf("%s 不该为空", path)
		}
	}
}

// TestJSONStillDefault 不给 format 时仍是 JSON（界面依赖）。
func TestJSONStillDefault(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workspace", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("默认应回 JSON，得到 %q", ct)
	}
	if !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{") {
		t.Errorf("默认应是 JSON 对象：%s", rec.Body.String())
	}
}

// TestWeightsTextMentionsUsage 权重的文本输出应说明"有没有使用数据"。
func TestWeightsTextMentionsUsage(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/weights?format=text", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "使用数据") {
		t.Errorf("应说明使用数据情况：%s", body)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
