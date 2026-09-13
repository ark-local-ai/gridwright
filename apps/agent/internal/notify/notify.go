// Package notify 是通知出口（spec §6）：改完表后推一条消息。
//
// 支持渠道：
//
//	console    本地日志（默认；离线可用）
//	serverchan Server酱（个人微信）—— https://sctapi.ftqq.com/<SKEY>.send
//	pushplus   PushPlus（个人微信）—— https://www.pushplus.plus/send
//	webhook    企业微信群机器人 / 任意 HTTP 端点
//
// 设计要点：
//   - **通知失败绝不阻断改表**：改表是主任务，通知是附属，失败只记日志。
//   - 超时短（10s）：通知不该拖慢流程。
//   - 只发摘要与条数，不发数据本身（数据不出本机）。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
)

// Message 是一条通知。
type Message struct {
	Table          string
	Summary        string
	Applied        int
	Rejected       int
	RejectedDetail []string
}

// Text 组装通知正文（各渠道共用）。
// 被拒的改动**必须报出来**——这是产品承诺，不装没发生。
func (m Message) Text() string {
	out := fmt.Sprintf("【gridwright】%s：%s", m.Table, m.Summary)
	if m.Applied == 0 && m.Rejected == 0 {
		return out
	}
	out += fmt.Sprintf("\n改 %d 处、跳过 %d 处", m.Applied, m.Rejected)
	for i, d := range m.RejectedDetail {
		if i >= 5 {
			out += fmt.Sprintf("\n…还有 %d 条跳过", len(m.RejectedDetail)-5)
			break
		}
		out += "\n· " + d
	}
	return out
}

// Title 短标题（部分渠道对标题长度有限制）。
func (m Message) Title() string {
	t := "gridwright：" + m.Table
	if r := []rune(t); len(r) > 32 {
		t = string(r[:32])
	}
	return t
}

// Send 把消息推给配置的渠道。
// 只有"配置有问题"（渠道名不认识 / 缺密钥）才返回错误；
// 网络失败只记日志——通知不该影响改表本身。
func Send(cfg config.Notify, msg Message) error {
	text := msg.Text()
	switch strings.ToLower(strings.TrimSpace(cfg.Channel)) {
	case "", "console":
		log.Printf("[notify] %s", text)
		return nil
	case "serverchan":
		if cfg.ServerChanSKey == "" {
			return fmt.Errorf("通知渠道 serverchan 缺少 serverchan_skey")
		}
		return postServerChan(cfg.ServerChanSKey, msg.Title(), text)
	case "pushplus":
		if cfg.PushPlusToken == "" {
			return fmt.Errorf("通知渠道 pushplus 缺少 pushplus_token")
		}
		return postPushPlus(cfg.PushPlusToken, msg.Title(), text)
	case "webhook":
		if cfg.WebhookURL == "" {
			return fmt.Errorf("通知渠道 webhook 缺少 webhook_url")
		}
		return postWebhook(cfg.WebhookURL, text)
	default:
		return fmt.Errorf("未知通知渠道: %s", cfg.Channel)
	}
}

// ---- 各渠道实现 ----

// postServerChan Server酱：POST 到 https://sctapi.ftqq.com/<SKEY>.send（title + desp）。
func postServerChan(skey, title, desp string) error {
	u := "https://sctapi.ftqq.com/" + url.PathEscape(skey) + ".send"
	form := url.Values{}
	form.Set("title", title)
	form.Set("desp", desp)
	return postForm(u, form, "serverchan")
}

// postPushPlus PushPlus：POST JSON（token/title/content/template）。
func postPushPlus(token, title, content string) error {
	return postJSON("https://www.pushplus.plus/send", buildPushPlus(token, title, content), "pushplus")
}

// buildPushPlus 单独出来便于测试载荷形状。
func buildPushPlus(token, title, content string) []byte {
	body, _ := json.Marshal(map[string]string{
		"token": token, "title": title, "content": content, "template": "txt",
	})
	return body
}

// postWebhook 通用 webhook。默认按企业微信群机器人的形状发
// （{"msgtype":"text","text":{"content":"…"}}）——它是这类端点里最常见的一种。
func postWebhook(u, text string) error {
	body, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	})
	return postJSON(u, body, "webhook")
}

// ---- 底层 POST（统一超时与容错）----

var client = &http.Client{Timeout: 10 * time.Second}

func postForm(u string, form url.Values, label string) error {
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(req, label)
}

func postJSON(u string, body []byte, label string) error {
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req, label)
}

// do 发请求。**网络失败只记日志、不返回错误**——通知不该阻断改表。
func do(req *http.Request, label string) error {
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[notify:%s] 发送失败（不影响本次改动）：%v", label, err)
		return nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[notify:%s] 渠道返回 %d：%s", label, resp.StatusCode, strings.TrimSpace(string(raw)))
	} else {
		log.Printf("[notify:%s] 已发送", label)
	}
	return nil
}
