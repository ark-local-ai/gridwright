package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

// TestRenderHistoryKeepsRecentAndVerbatim 历史要原样带上（一字不改），
// 且“当前这句话”排在历史之后。
//
// 这条钉的是“会话失忆”：以前 assembleChatPrompt 只吃当前一句，模型每轮都把
// 同一件事重问一遍（实测连问 4 轮）。历史段必须真的出现在 prompt 里，
// 且短句也不能被吞掉——用户那句回答就叫“表。”。
func TestRenderHistoryKeepsRecentAndVerbatim(t *testing.T) {
	history := []convo.Message{
		{Role: convo.RoleUser, Text: "按 D 列的名字把 E、F 列填进销售明细表，查不到就新增"},
		{Role: convo.RoleAgent, Text: "好，我会按 D 列匹配。"},
		{Role: convo.RoleUser, Text: "表。"},
	}
	p := assembleChatPrompt("就用这张图的数据", []string{"表.xlsx（销售明细表）"}, history)

	if !strings.Contains(p, "按 D 列的名字把 E、F 列填进销售明细表，查不到就新增") {
		t.Error("历史里的用户原话应原样出现在 prompt 里")
	}
	if !strings.Contains(p, "用户：表。") {
		t.Errorf("短句也必须逐字保留（旧渲染会把“表。”吞掉）：\n%s", p)
	}
	hi, ci := strings.Index(p, "这段对话之前说过什么"), strings.Index(p, "# 用户说")
	if hi < 0 || ci < 0 || hi > ci {
		t.Errorf("历史段应排在当前消息之前（hi=%d ci=%d）", hi, ci)
	}
	if !strings.Contains(p, "# 用户说\n就用这张图的数据") {
		t.Errorf("当前消息应原样跟在“# 用户说”之后：\n%s", p)
	}
	// 历史里的图不能被当成 data URL 塞进来（那会把 prompt 撑成几百 KB），
	// 但“当时有图”这件事必须说出来。
	withImg := renderHistory([]convo.Message{{Role: convo.RoleUser, Text: "用这张", Images: []string{"data:image/png;base64,AAAA"}}})
	if strings.Contains(withImg, "base64") {
		t.Error("历史里不该内联图片数据，只该写“（附图 N 张）”")
	}
	if !strings.Contains(withImg, "附图 1 张") {
		t.Errorf("历史应说明当时带了图，得到：%s", withImg)
	}
}

// TestRenderHistoryLimits 历史必须有上限：token 不能随轮次线性涨。
func TestRenderHistoryLimits(t *testing.T) {
	var history []convo.Message
	for i := 0; i < 30; i++ {
		history = append(history, convo.Message{Role: convo.RoleUser, Text: fmt.Sprintf("第%d句", i)})
	}
	out := renderHistory(history)
	if strings.Contains(out, "第0句") {
		t.Error("太老的消息不该带（只留最近 8 条）")
	}
	if !strings.Contains(out, "第29句") {
		t.Error("最近一句必须带上")
	}
	if n := strings.Count(out, "\n"); n > historyMaxMsgs+2 {
		t.Errorf("行数应被压到 ≤%d，得到 %d", historyMaxMsgs+2, n)
	}

	long := []convo.Message{{Role: convo.RoleUser, Text: strings.Repeat("长", 5000)}}
	if got := len([]rune(renderHistory(long))); got > historyMaxRunes+len([]rune(historyHead))+3 {
		t.Errorf("超长历史应被截断，得到 %d 字", got)
	}
	if renderHistory(nil) != "" {
		t.Error("没有历史时应返回空串（不要多出一段空标题）")
	}
}

// TestChatPromptNoFalseCapability 会话提示不能把“不能新增行”当一条限制列出来，
// 必须给出新增的路。
//
// 以前这里列着“系统做不到的事 → 不能新增行”，于是模型把用户
// “查不到就新增”的正当要求**当成做不到**顶回去，绕成无穷澄清；
// 而底层本来就支持新增行，断的只是“提示词”这一段。
func TestChatPromptNoFalseCapability(t *testing.T) {
	p := assembleChatPrompt("按 D 列名字填 E、F，查不到就新增", nil, nil)
	// “不能新增行”只允许以“别说这句话”的形式出现，不允许作为一条限制存在。
	for _, bad := range []string{"- 不能新增行", "不能新增行：", "系统做不到"} {
		if strings.Contains(p, bad) {
			t.Errorf("会话提示不该把 %q 当成一条做不到的限制", bad)
		}
	}
	if !strings.Contains(p, "新增一行") {
		t.Error("会话提示应明确“查不到就新增一行”是能做的")
	}
	if !strings.Contains(p, "不要再问") {
		t.Error("会话提示应要求“已经答过的不再问第二次”")
	}
}

// TestPlanPromptOffersUpsert 出清单的提示必须给出 upsert 契约 ——
// 否则模型想新增也无处表达，只能塞进 skip_reasons，清单永远是空的。
func TestPlanPromptOffersUpsert(t *testing.T) {
	p := assemblePlanPrompt("按 D 列名字填 E、F，查不到就新增", nil, nil, "", false)
	for _, bad := range []string{"- 不能新增行", "不能新增行：", "只能改已存在的格", "系统做不到"} {
		if strings.Contains(p, bad) {
			t.Errorf("出清单提示不该把 %q 当成一条限制", bad)
		}
	}
	if !strings.Contains(p, "upsert") {
		t.Error("出清单提示必须给出 upsert 契约")
	}
}
