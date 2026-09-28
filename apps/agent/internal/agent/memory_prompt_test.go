package agent

import (
	"strings"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// TestAssemblePromptCarriesMemory 自动改表那条路（assemblePrompt）必须带上记忆。
//
// 回归用例，钉住一个真实缺口：以前只有"用户在对话里下指令"那条路（planner）
// 带记忆，inbox 自动处理这条路不带——于是用户拍板过的规矩（"含运费都算进去"）
// 自动处理时根本不生效。**同一条规矩手动遵守、自动不遵守，比没有记忆更坏**，
// 因为人会以为规矩已经立住了。
func TestAssemblePromptCarriesMemory(t *testing.T) {
	memText := (&memory2.File{
		Decisions: []memory2.Decision{{ID: "d1", Text: "含运费的金额以后都算进去"}},
	}).Describe()

	got := assemblePrompt(nil, nil, "实收日报.csv", "A03 收到运费 500",
		nil, memory.RulesFile{}, memory.State{}, memText)

	if !strings.Contains(got, "含运费") {
		t.Error("prompt 里应带上相关记忆的决策内容")
	}
	if !strings.Contains(got, "相关记忆") {
		t.Error("prompt 里应有『相关记忆』这一段（让人与模型都看得出这段是什么）")
	}
	// 契约提醒也要在：记忆的"以往决定"该怎么对待，得说清
	if !strings.Contains(got, "以往决定") {
		t.Error("应说明如何对待记忆里的以往决定（照办，冲突则 skip）")
	}
}

// TestAssemblePromptSaysWhenNoMemory 没有相关记忆时也要**明说**，
// 而不是整段省略——否则分不清"没有记忆"和"忘了加这一段"。
func TestAssemblePromptSaysWhenNoMemory(t *testing.T) {
	got := assemblePrompt(nil, nil, "x.csv", "随便什么", nil,
		memory.RulesFile{}, memory.State{}, "")

	if !strings.Contains(got, "相关记忆") {
		t.Fatal("没有记忆时也该出现『相关记忆』这一段")
	}
	if !strings.Contains(got, "没有检索到") {
		t.Error("应明说这次没检索到相关记忆")
	}
}
