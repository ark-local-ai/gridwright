package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
)

// TestWebhookPostsJSON webhook 渠道要真的发 HTTP，而不是打日志。
func TestWebhookPostsJSON(t *testing.T) {
	var got string
	var ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctype = r.Header.Get("Content-Type")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		got = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	msg := Message{Table: "收款表.xlsx", Summary: "更新价格1处", Applied: 1, Rejected: 0}
	if err := Send(config.Notify{Channel: "webhook", WebhookURL: srv.URL}, msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctype, "application/json") {
		t.Errorf("应发 JSON，Content-Type=%q", ctype)
	}
	// 企业微信机器人形状
	if !strings.Contains(got, `"msgtype":"text"`) || !strings.Contains(got, "收款表.xlsx") {
		t.Errorf("载荷不对：%s", got)
	}
}

// TestPushPlusPostsJSON pushplus 要带 token。
func TestPushPlusPostsJSON(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		got = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	// 用 httptest 覆盖真实域名做不到，所以这里只验证载荷构造
	body := buildPushPlus("tk", "标题", "正文")
	if !strings.Contains(string(body), `"token":"tk"`) || !strings.Contains(string(body), "正文") {
		t.Errorf("pushplus 载荷不对：%s", body)
	}
	_ = srv
	_ = got
}

// TestRejectedReportedInText 被拒的改动必须出现在通知里（不装没发生）。
func TestRejectedReportedInText(t *testing.T) {
	m := Message{Table: "t.xlsx", Summary: "s", Applied: 2, Rejected: 1,
		RejectedDetail: []string{"C5 set: 命中 forbid 护栏"}}
	txt := m.Text()
	if !strings.Contains(txt, "跳过 1 处") || !strings.Contains(txt, "forbid") {
		t.Errorf("通知应报出被拒项：%s", txt)
	}
}

// TestNetworkFailureDoesNotError 网络失败不该让调用方失败（通知不阻断改表）。
func TestNetworkFailureDoesNotError(t *testing.T) {
	// 指向一个必然连不上的地址（保留端口 0 → 无服务）
	err := Send(config.Notify{Channel: "webhook", WebhookURL: "http://127.0.0.1:1/x"},
		Message{Table: "t", Summary: "s", Applied: 1})
	if err != nil {
		t.Fatalf("网络失败不该返回错误，得到 %v", err)
	}
}

// TestMissingConfigIsError 缺密钥要报错（这是配置问题，不是网络问题）。
func TestMissingConfigIsError(t *testing.T) {
	if err := Send(config.Notify{Channel: "serverchan"}, Message{}); err == nil {
		t.Fatal("缺 skey 应报错")
	}
	if err := Send(config.Notify{Channel: "pushplus"}, Message{}); err == nil {
		t.Fatal("缺 token 应报错")
	}
	if err := Send(config.Notify{Channel: "webhook"}, Message{}); err == nil {
		t.Fatal("缺 url 应报错")
	}
	if err := Send(config.Notify{Channel: "没这个渠道"}, Message{}); err == nil {
		t.Fatal("未知渠道应报错")
	}
}

// TestConsoleIsDefault 默认（空渠道）走 console，不报错。
func TestConsoleIsDefault(t *testing.T) {
	if err := Send(config.Notify{}, Message{Table: "t", Summary: "s"}); err != nil {
		t.Fatal(err)
	}
}
