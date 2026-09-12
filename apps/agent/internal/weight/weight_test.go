package weight

import (
	"path/filepath"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
)

// TestStructuralBeatsFrequency 验证设计核心主张：
// **高频 ≠ 重要**。台账几乎不改，但被多张表引用，结构权重应远高于"天天改"的表。
func TestStructuralBeatsFrequency(t *testing.T) {
	g := &graph.Graph{
		Files: []string{"a.xlsx"},
		Nodes: []graph.Node{
			{File: "a.xlsx", Sheet: "商铺台账（详）"},  // 主数据，被引用，不改
			{File: "a.xlsx", Sheet: "每日实收表（日）"}, // 天天改，没人引用
			{File: "a.xlsx", Sheet: "各月租金表"},    // 引用台账
		},
		Edges: []graph.Edge{
			{From: graph.Node{File: "a.xlsx", Sheet: "各月租金表"},
				To:   graph.Node{File: "a.xlsx", Sheet: "商铺台账（详）"},
				Kind: graph.KindFormula, Confidence: graph.ConfHigh, Count: 37},
		},
	}
	// 台账 0 次改动；日收 100 次改动（高频）
	scores := Compute(Input{
		Graph:    g,
		Formulas: map[string]int{"a.xlsx!商铺台账（详）": 451, "a.xlsx!每日实收表（日）": 212},
		Activity: map[string]int{"a.xlsx!商铺台账（详）": 0, "a.xlsx!每日实收表（日）": 100},
	})
	byID := ByID(scores)

	ledger := byID["a.xlsx!商铺台账（详）"]
	daily := byID["a.xlsx!每日实收表（日）"]

	if !ledger.Master {
		t.Error("商铺台账应被识别为主数据")
	}
	if ledger.Structural <= daily.Structural {
		t.Errorf("台账结构权重应高于日收：台账 %.2f vs 日收 %.2f",
			ledger.Structural, daily.Structural)
	}
	// 日收活跃度确实更高（这是对的，但它不该因此成为"最重要"）
	if daily.Activity <= ledger.Activity {
		t.Errorf("日收活跃度应更高：日收 %.2f vs 台账 %.2f", daily.Activity, ledger.Activity)
	}
	// 但注意力上，台账不应被日收碾压（因为主数据有下限保护）
	if ledger.Attention < 0.5 {
		t.Errorf("主数据注意力不该过低：%.2f", ledger.Attention)
	}
	// 排序：台账应在日收之前（或至少不落后太多）
	if scores[0].Node.Sheet != "商铺台账（详）" {
		t.Logf("排序首项为 %s（台账 %.2f / 日收 %.2f）",
			scores[0].Node.Sheet, ledger.Attention, daily.Attention)
	}
}

// TestNoUsageStillWorks 没有使用数据时也能算（结构权重从第一天起可用）。
func TestNoUsageStillWorks(t *testing.T) {
	g := &graph.Graph{
		Nodes: []graph.Node{{File: "a.xlsx", Sheet: "台账"}, {File: "a.xlsx", Sheet: "流水"}},
		Edges: []graph.Edge{{
			From: graph.Node{File: "a.xlsx", Sheet: "流水"},
			To:   graph.Node{File: "a.xlsx", Sheet: "台账"},
			Kind: graph.KindFormula, Confidence: graph.ConfHigh,
		}},
	}
	scores := Compute(Input{Graph: g}) // 无 Activity、无 Formulas
	if len(scores) != 2 {
		t.Fatalf("应算 2 个节点，得到 %d", len(scores))
	}
	for _, s := range scores {
		if s.HasUsage {
			t.Error("没传活动数据时 HasUsage 应为 false")
		}
		if s.Activity != 0 {
			t.Errorf("无使用数据时活跃度应为 0，得到 %.2f", s.Activity)
		}
	}
	// 台账（被引用）结构分应 > 流水（无人引用）
	byID := ByID(scores)
	if byID["a.xlsx!台账"].Structural <= byID["a.xlsx!流水"].Structural {
		t.Error("被引用的表结构分应更高")
	}
}

// TestReasonsExplainable 每个分都要能解释（不做黑箱）。
func TestReasonsExplainable(t *testing.T) {
	g := &graph.Graph{
		Nodes: []graph.Node{{File: "a.xlsx", Sheet: "商铺台账（详）"}},
	}
	scores := Compute(Input{Graph: g, Formulas: map[string]int{"a.xlsx!商铺台账（详）": 451}})
	if len(scores[0].Reasons) == 0 {
		t.Fatal("应给出可解释的理由")
	}
	joined := ""
	for _, r := range scores[0].Reasons {
		joined += r + ";"
	}
	if !contains(joined, "主数据") {
		t.Errorf("理由应说明主数据：%s", joined)
	}
}

// TestRealWorkbook 用真实表跑一遍，看权重排序是否合理。
func TestRealWorkbook(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	matches, _ := filepath.Glob(filepath.Join(root, "docs", "*.xlsx"))
	if len(matches) == 0 {
		t.Skip("未找到真实表")
	}
	g, err := graph.Scan(matches[0])
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	scores := Compute(Input{Graph: g})
	if len(scores) == 0 {
		t.Fatal("应算出权重")
	}
	t.Logf("注意力最高的前 6 张表：")
	for i, s := range scores {
		if i >= 6 {
			break
		}
		t.Logf("  %.3f  %s  [%s]", s.Attention, s.Node.Sheet, joinReasons(s.Reasons))
	}
	// 至少要有一个被识别为主数据
	master := 0
	for _, s := range scores {
		if s.Master {
			master++
		}
	}
	if master == 0 {
		t.Error("真实台账里应识别出主数据表（如 商铺台账）")
	}
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += " · "
		}
		out += r
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
