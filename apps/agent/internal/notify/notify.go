// Package notify 是出口（spec §6）：改完表后推一条通知。
// M1 只有 console；M3 接 serverchan/pushplus/webhook（同一 Send 接口）。
package notify

import (
	"fmt"
	"log"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
)

// Message 是一条通知。
type Message struct {
	Table     string
	Summary   string
	Applied   int
	Rejected  int
	RejectedDetail []string
}

// Send 把消息推给配置的渠道（console 打印到日志）。
func Send(cfg config.Notify, msg Message) error {
	text := fmt.Sprintf("【Gridwright】%s %s：应用 %d 项、跳过 %d 项",
		msg.Table, msg.Summary, msg.Applied, msg.Rejected)
	for _, d := range msg.RejectedDetail {
		text += "\n  跳过: " + d
	}
	switch cfg.Channel {
	case "console", "":
		log.Printf("[notify] %s", text)
	case "serverchan", "pushplus", "webhook":
		// M3 实现：POST 到对应渠道。M1 先打日志提示。
		log.Printf("[notify:%s] %s（渠道实现待 M3）", cfg.Channel, text)
	default:
		return fmt.Errorf("未知通知渠道: %s", cfg.Channel)
	}
	return nil
}
