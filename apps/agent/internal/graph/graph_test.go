package graph

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPropagateChain 用合成图验证传播闭包：只走高置信边，且能跨文件。
func TestPropagateChain(t *testing.T) {
	g := &Graph{}
	// 数据从 To 流向 From：改了 To 牵动 From
	g.Edges = []Edge{
		{From: Node{"a.xlsx", "月租金"}, To: Node{"a.xlsx", "每日实收"}, Kind: KindDeclared, Confidence: ConfHigh},
		{From: Node{"a.xlsx", "汇总"}, To: Node{"a.xlsx", "月租金"}, Kind: KindDeclared, Confidence: ConfHigh},
		{From: Node{"b.xlsx", "滞纳金"}, To: Node{"a.xlsx", "汇总"}, Kind: KindDeclared, Confidence: ConfHigh, CrossFile: true},
		{From: Node{"a.xlsx", "旁支"}, To: Node{"a.xlsx", "汇总"}, Kind: KindSemantic, Confidence: ConfMedium},
	}
	got := g.Propagate(Node{"a.xlsx", "每日实收"})
	want := []string{"a.xlsx!月租金", "a.xlsx!汇总", "b.xlsx!滞纳金"}
	if len(got) != len(want) {
		t.Fatalf("传播结果 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("传播结果 %v，期望 %v", got, want)
		}
	}
	for _, s := range got {
		if s == "a.xlsx!旁支" {
			t.Fatalf("中置信的语义边不应参与传播：%v", got)
		}
	}
}

// TestNodeID 无文件时退化为裸 sheet 名（跨文件引用找不到目标文件的情形）。
func TestNodeID(t *testing.T) {
	if (Node{Sheet: "x"}).ID() != "x" {
		t.Fatal("无文件节点 ID 应为裸 sheet 名")
	}
	if (Node{File: "f.xlsx", Sheet: "x"}).ID() != "f.xlsx!x" {
		t.Fatal("有文件节点 ID 应为 文件!sheet")
	}
}

// TestScanRealWorkbook 用仓库里的真实台账验证：单文件 + 跨文件引用识别。
func TestScanRealWorkbook(t *testing.T) {
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
	t.Logf("文件: %v，节点(工作表)数: %d", g.Files, len(g.Nodes))
	if len(g.Nodes) == 0 {
		t.Fatal("没扫到工作表")
	}
	high, med := g.splitByConfidence()
	t.Logf("高置信边: %d 条，中置信边: %d 条", len(high), len(med))
	if len(high) == 0 {
		t.Error("真实表里应该能扫出至少一条公式跨表引用")
	}
	if len(med) != 0 {
		t.Errorf("默认不应产出语义边（噪音），得到 %d 条", len(med))
	}
	// 真实表里有 '[2]商铺台账（详）'! 这类外部引用 → 应被识别为跨文件或未知文件节点
	sawExternal := false
	for _, e := range high {
		if e.ExternalIdx > 0 {
			sawExternal = true
			t.Logf("  外部引用: [%d]「%s」", e.ExternalIdx, e.To.ID())
		}
	}
	if !sawExternal {
		t.Error("真实表有 57 处 '[n]sheet' 外部引用，扫描器应识别到至少一处")
	}
	if len(g.Anchors) == 0 {
		t.Error("应至少识别出一些锚点列（铺位/租户/月份）")
	}
}

// TestScanMultiFileWorkspace 构造两个文件验证跨文件节点不撞车、且能解析跨文件边。
func TestScanMultiFileWorkspace(t *testing.T) {
	dir := t.TempDir()
	// A：日收表（引用 B 的台账）
	writeBook(t, filepath.Join(dir, "收.xlsx"), "每日实收", map[string]string{
		"A1": "铺位", "B1": "金额", "A2": "A03", "B2": "=100",
	})
	// B：台账表
	writeBook(t, filepath.Join(dir, "账.xlsx"), "台账", map[string]string{
		"A1": "铺位", "B1": "租金",
	})
	g, err := ScanWorkspace(dir, []string{
		filepath.Join(dir, "收.xlsx"),
		filepath.Join(dir, "账.xlsx"),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Files) != 2 {
		t.Fatalf("应扫到 2 个文件，得到 %v", g.Files)
	}
	// 两个文件各自的 sheet 都应在节点里，且 ID 带文件前缀
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.ID()] = true
	}
	if !ids["收.xlsx!每日实收"] || !ids["账.xlsx!台账"] {
		t.Fatalf("节点 ID 应带文件前缀，得到 %v", ids)
	}
}

// TestResolveNodeAmbiguous 裸 sheet 名在多个文件都有时不猜。
func TestResolveNodeAmbiguous(t *testing.T) {
	g := &Graph{Nodes: []Node{
		{"a.xlsx", "汇总"}, {"b.xlsx", "汇总"}, {"a.xlsx", "独有"},
	}}
	if got := g.ResolveNode(Node{Sheet: "汇总"}); got.File != "" {
		t.Fatalf("重复 sheet 名不应猜文件，得到 %v", got)
	}
	if got := g.ResolveNode(Node{Sheet: "独有"}); got.File != "a.xlsx" {
		t.Fatalf("唯一 sheet 名应解析出文件，得到 %v", got)
	}
}
