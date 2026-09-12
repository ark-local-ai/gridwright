package impact

import (
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// mkGraph 造一个贴近真实的链：每日实收 → 各月租金 → 汇总 → 滞纳金
func mkGraph() *graph.Graph {
	n := func(s string) graph.Node { return graph.Node{File: "收款表.xlsx", Sheet: s} }
	e := func(from, to string) graph.Edge {
		return graph.Edge{From: n(from), To: n(to), Kind: graph.KindFormula, Confidence: graph.ConfHigh}
	}
	return &graph.Graph{
		Files: []string{"收款表.xlsx"},
		Nodes: []graph.Node{n("每日实收表"), n("各月租金表"), n("租金汇总表"), n("滞纳金表"), n("无关表")},
		// 数据从 To 流向 From：每日实收 → 各月租金 → 汇总 → 滞纳金
		Edges: []graph.Edge{
			e("各月租金表", "每日实收表"),
			e("租金汇总表", "各月租金表"),
			e("滞纳金表", "租金汇总表"),
		},
	}
}

// TestInputTableNotScored 核心纠正：**改动的那张表是输入，不做候选**。
// "日收表天天动"是它的定义，给它打分是同义反复。
func TestInputTableNotScored(t *testing.T) {
	g := mkGraph()
	res := Infer(Input{Graph: g, From: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"}})
	for _, c := range res.Candidates {
		if c.Node.Sheet == "每日实收表" {
			t.Fatal("改动的那张表（输入）不该出现在候选里")
		}
	}
}

// TestDownstreamFound 改了日收，应能沿链找到下游要同步的表。
func TestDownstreamFound(t *testing.T) {
	g := mkGraph()
	res := Infer(Input{Graph: g, From: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"}})
	got := map[string]Candidate{}
	for _, c := range res.Candidates {
		got[c.Node.Sheet] = c
	}
	// 1 跳（各月租金）应比 2 跳（汇总）比 3 跳（滞纳金）更相关——但 maxHops=2 时滞纳金可能不出现
	if _, ok := got["各月租金表"]; !ok {
		t.Fatalf("应找到 1 跳下游 各月租金表，得到 %+v", res.Candidates)
	}
	if got["各月租金表"].Score <= 0 {
		t.Error("1 跳候选分数应 > 0")
	}
	// 无关表不该出现
	if _, ok := got["无关表"]; ok {
		t.Error("无关表不该出现")
	}
	// 越近分数越高
	if sum, ok := got["租金汇总表"]; ok {
		if sum.Score >= got["各月租金表"].Score {
			t.Errorf("2 跳不该 >= 1 跳：汇总 %.2f vs 各月租金 %.2f", sum.Score, got["各月租金表"].Score)
		}
	}
}

// TestMemoryRelationSurfacesTable 记忆里的关系能把**图上够不到**的表带出来——
// 这正是用户说的"这一步就是 LLM 找到相关的、检索工作区哪里关联了"。
func TestMemoryRelationSurfacesTable(t *testing.T) {
	dir := t.TempDir()
	mem, err := memory2.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 用户纠正过：收租类还涉及「保证金情况表」（图上和日收没有直接连线）
	if err := mem.AddRelation(memory2.Relation{
		Kind: "收租", Tables: []string{"保证金情况表"}, Source: "user-correction",
		Note: "用户指出漏了",
	}); err != nil {
		t.Fatal(err)
	}

	g := mkGraph()
	// 把保证金表加进图（但**不连边**）
	g.Nodes = append(g.Nodes, graph.Node{File: "收款表.xlsx", Sheet: "保证金情况表"})

	res := Infer(Input{
		Graph: g, From: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"},
		Kind: "收租", Memory: mem,
	})
	var bond *Candidate
	for i := range res.Candidates {
		if res.Candidates[i].Node.Sheet == "保证金情况表" {
			bond = &res.Candidates[i]
		}
	}
	if bond == nil {
		t.Fatalf("记忆关系应把保证金表带出来（图上够不到），得到 %+v", res.Candidates)
	}
	if !bond.ByMemory {
		t.Error("应标记为 ByMemory（记忆命中）")
	}
	// 记忆命中要能解释，且提示是人纠正过的
	joined := ""
	for _, r := range bond.Reasons {
		joined += r + ";"
	}
	if !contains(joined, "纠正") {
		t.Errorf("理由应说明是人纠正过的：%s", joined)
	}
}

// TestNoKindFallsBackToStructure 没判出类别时，仍按结构给候选（降级可用）。
func TestNoKindFallsBackToStructure(t *testing.T) {
	g := mkGraph()
	res := Infer(Input{Graph: g, From: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"}})
	if len(res.Candidates) == 0 {
		t.Fatal("没类别时也该按结构给出候选")
	}
	if res.Source != "none" {
		t.Errorf("无类别时 source 应为 none，得到 %q", res.Source)
	}
	if res.Note == "" {
		t.Error("应说明只按结构推断")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
