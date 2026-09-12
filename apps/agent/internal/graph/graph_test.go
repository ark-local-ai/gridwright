package graph

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPropagateChain 用合成小表验证传播闭包只走高置信边。
func TestPropagateChain(t *testing.T) {
	g := &Graph{}
	// 每日实收 → 月租金 → 汇总（数据从 To 流向 From：改了 To 牵动 From）
	g.Edges = []Edge{
		{From: "月租金", To: "每日实收", Kind: KindDeclared, Confidence: ConfHigh},
		{From: "汇总", To: "月租金", Kind: KindDeclared, Confidence: ConfHigh},
		{From: "滞纳金", To: "汇总", Kind: KindDeclared, Confidence: ConfHigh},
		{From: "旁支", To: "汇总", Kind: KindSemantic, Confidence: ConfMedium},
	}
	got := g.Propagate("每日实收")
	want := []string{"月租金", "汇总", "滞纳金"}
	if len(got) != len(want) {
		t.Fatalf("传播结果 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("传播结果 %v，期望 %v", got, want)
		}
	}
	// 语义边（中置信）不应被牵动
	for _, s := range got {
		if s == "旁支" {
			t.Fatalf("中置信的语义边不应参与传播：%v", got)
		}
	}
}

// TestScanRealWorkbook 用仓库里的真实台账表验证扫描器（表在就测，不在就跳过）。
func TestScanRealWorkbook(t *testing.T) {
	// internal/graph → apps/agent → apps → repo root
	root := filepath.Join("..", "..", "..", "..")
	matches, _ := filepath.Glob(filepath.Join(root, "docs", "*.xlsx"))
	if len(matches) == 0 {
		t.Skip("未找到真实表，跳过")
	}
	path := matches[0]
	if _, err := os.Stat(path); err != nil {
		t.Skipf("真实表不可读: %v", err)
	}

	g, err := Scan(path)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	t.Logf("sheet 数: %d", len(g.Sheets))
	if len(g.Sheets) == 0 {
		t.Fatal("没扫到 sheet")
	}
	high, med := g.splitByConfidence()
	t.Logf("高置信边: %d 条，中置信边: %d 条", len(high), len(med))
	if len(high) == 0 {
		t.Error("真实表里应该能扫出至少一条公式跨表引用（实测有台账被引用）")
	}
	if len(med) != 0 {
		t.Errorf("默认不应产出语义边（噪音），得到 %d 条", len(med))
	}
	for _, e := range high {
		t.Logf("  高: 「%s」→「%s」(%s ×%d)", e.To, e.From, e.Kind, e.Count)
	}
	// 键列识别
	if len(g.Anchors) == 0 {
		t.Error("应至少识别出一些锚点列（铺位/租户/月份）")
	}
}
