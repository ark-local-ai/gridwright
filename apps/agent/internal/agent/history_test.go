package agent

import (
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

func TestFormatHistorySkipsSystemAndDuplicate(t *testing.T) {
	msgs := []convo.Message{
		{Role: convo.RoleUser, Text: "记一笔 A03 收到 8 月租金"},
		{Role: convo.RoleAgent, Text: "好的，我先算出要改哪些格"},
		{Role: convo.RoleSystem, Text: "模型通道超时"},
		{Role: convo.RoleUser, Text: "A03 那行改成累加"},
	}
	got := formatHistory(msgs, "A03 那行改成累加")
	if strings.Contains(got, "模型通道超时") {
		t.Error("系统消息不该进背景")
	}
	if strings.Contains(got, "A03 那行改成累加") {
		t.Error("与当前指令相同的最后一条用户消息不该重复")
	}
	if !strings.Contains(got, "用户：记一笔 A03 收到 8 月租金") {
		t.Errorf("应保留原始任务，得到：%s", got)
	}
	if !strings.Contains(got, "助手：好的，我先算出要改哪些格") {
		t.Errorf("应保留助手回应，得到：%s", got)
	}
}

func TestFormatHistoryTruncates(t *testing.T) {
	long := strings.Repeat("很", 500)
	got := formatHistory([]convo.Message{{Role: convo.RoleUser, Text: long}}, "另一句")
	if len([]rune(got)) > 420 {
		t.Fatalf("单条应截断到 400 字，得到 %d 字", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("截断应有省略号")
	}
}

func TestFormatHistoryEmptyInstructionKeepsAll(t *testing.T) {
	msgs := []convo.Message{{Role: convo.RoleUser, Text: "唯一一句"}}
	got := formatHistory(msgs, "")
	if !strings.Contains(got, "唯一一句") {
		t.Errorf("instruction 为空时不应丢消息，得到：%s", got)
	}
}
