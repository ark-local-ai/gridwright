package newtable

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
)

// TestSheetSim 表名相似度：去掉会变的数字后应识别为同一系列。
func TestSheetSim(t *testing.T) {
	cases := []struct {
		a, b string
		want float64 // 下限
	}{
		{"2026年9月租金 （日）", "2026年10月租金 （日）", 0.8}, // 同系列，仅月份不同
		{"每日实收表（日）", "每日实收表（日）", 1.0},            // 完全相同
		{"每日实收表（日）", "滞纳金计算汇总表（月末）", 0.0},        // 无关
	}
	for _, c := range cases {
		got := sheetSim(c.a, c.b)
		if got < c.want {
			t.Errorf("sheetSim(%q,%q)=%.2f 应 >= %.2f", c.a, c.b, got, c.want)
		}
	}
}

// TestHeaderSim 表头重叠：列名一致多则高。
func TestHeaderSim(t *testing.T) {
	a := []string{"序号", "物业位置", "租户名称", "本月实收"}
	b := []string{"序号", "物业位置", "租户名称", "本月欠款"}
	if got := headerSim(a, b); got < 0.5 {
		t.Errorf("应识别出高度重合，得到 %.2f", got)
	}
	c := []string{"商品", "库存", "单价"}
	if got := headerSim(a, c); got > 0.2 {
		t.Errorf("无关表头不该高，得到 %.2f", got)
	}
}

// TestDetectDuplicate 造一张和已有表几乎一样的表 → 应判为重复（最危险）。
func TestDetectDuplicate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "书.xlsx")
	f := excelize.NewFile()
	defer f.Close()
	// 原有表
	_ = f.SetSheetName("Sheet1", "2026年9月租金 （日）")
	for i, h := range []string{"序号", "物业位置", "租户名称", "本月实收", "本月欠款"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("2026年9月租金 （日）", cell, h)
	}
	// 新增"看起来一样"的表（只有月份不同）
	_, _ = f.NewSheet("2026年10月租金 （日）")
	for i, h := range []string{"序号", "物业位置", "租户名称", "本月实收", "本月欠款"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("2026年10月租金 （日）", cell, h)
	}
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}

	// 图：9月表已被引用（不是孤岛）；10月表是孤岛
	g := &graph.Graph{
		Files: []string{"书.xlsx"},
		Nodes: []graph.Node{
			{File: "书.xlsx", Sheet: "2026年9月租金 （日）"},
			{File: "书.xlsx", Sheet: "2026年10月租金 （日）"},
			{File: "书.xlsx", Sheet: "别的表"},
		},
		Edges: []graph.Edge{{
			From: graph.Node{File: "书.xlsx", Sheet: "2026年9月租金 （日）"},
			To:   graph.Node{File: "书.xlsx", Sheet: "别的表"},
			Kind: graph.KindFormula, Confidence: graph.ConfHigh,
		}},
	}
	got := Detect(Input{Graph: g, Files: []string{p}})
	if len(got) != 1 {
		t.Fatalf("应只挑出孤岛表（10月），得到 %d 个：%+v", len(got), got)
	}
	a := got[0]
	if a.Node.Sheet != "2026年10月租金 （日）" {
		t.Fatalf("应挑出 10 月表，得到 %s", a.Node.Sheet)
	}
	// 2026年10月 vs 9月：同一套表的另一期 → 应是 nextInChain，**不是** duplicate
	// （报重复就成狼来了：真实表里 24 个月度表都会被误报）
	if a.Kind != KindNextInChain {
		t.Errorf("同一套表的另一期应判为 nextInChain，得到 %s", a.Kind)
	}
	if a.NeedAsk {
		t.Error("正常的下一期不该打扰用户")
	}
	if a.Message == "" {
		t.Error("应给出可读说明")
	}
	t.Logf("判定：%s | %s", a.Kind, a.Message)
}

// TestIsolatedUnknown 完全无关的孤岛表 → unknown，且不强行问。
func TestIsolatedUnknown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "书.xlsx")
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "全新无关表")
	_ = f.SetCellValue("全新无关表", "A1", "甲")
	_ = f.SetCellValue("全新无关表", "B1", "乙")
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{
		Files: []string{"书.xlsx"},
		Nodes: []graph.Node{{File: "书.xlsx", Sheet: "全新无关表"}},
	}
	got := Detect(Input{Graph: g, Files: []string{p}})
	if len(got) != 1 {
		t.Fatalf("应有一个孤岛，得到 %d", len(got))
	}
	if got[0].Kind != KindUnknown {
		t.Errorf("无关孤岛应为 unknown，得到 %s", got[0].Kind)
	}
	if got[0].NeedAsk {
		t.Error("无关孤岛不该强行问用户")
	}
}

// TestLinkedNotReported 已有关联的表不该被当成"新表"。
func TestLinkedNotReported(t *testing.T) {
	g := &graph.Graph{
		Nodes: []graph.Node{{File: "a.xlsx", Sheet: "A"}, {File: "a.xlsx", Sheet: "B"}},
		Edges: []graph.Edge{{
			From: graph.Node{File: "a.xlsx", Sheet: "A"},
			To:   graph.Node{File: "a.xlsx", Sheet: "B"},
			Kind: graph.KindFormula, Confidence: graph.ConfHigh,
		}},
	}
	got := Detect(Input{Graph: g})
	if len(got) != 0 {
		t.Fatalf("有关联的表不该报为新表：%+v", got)
	}
}

// TestPeriodSeriesNotDuplicate 核心修正：**同一套表的另一期不是重复**。
// 真实表上踩到：2026年9月/10月租金表表头 100% 相同，若报"重复会算重"就是狼来了。
func TestPeriodSeriesNotDuplicate(t *testing.T) {
	if !hasPeriodDiff("2026年9月租金 （日）", "2026年10月租金 （日）") {
		t.Error("不同月份应识别为有期间差异")
	}
	if hasPeriodDiff("每日实收表（日）", "每日实收表（日）") {
		t.Error("同名不应有期间差异")
	}
	if hasPeriodDiff("汇总表", "汇总表") {
		t.Error("都无期数时不应判为不同期")
	}
	// 一期有一期无 → 视为不同
	if !hasPeriodDiff("2026年9月租金", "租金表") {
		t.Error("一边有期数一边没有，应视为不同期")
	}
}

// TestSameNameIsDuplicate 真正同名同表头（看不出期数差）才报重复。
func TestSameNameIsDuplicate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "书.xlsx")
	f := excelize.NewFile()
	defer f.Close()
	hdr := []string{"序号", "物业位置", "本月实收"}
	_ = f.SetSheetName("Sheet1", "租金汇总")
	for i, h := range hdr {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("租金汇总", cell, h)
	}
	// 真重复：**表名完全一致**、表头一致、无期数差
	// （excelize 允许重名 sheet 加后缀，但用户导入两份同名文件时更像这样）
	_, _ = f.NewSheet("租金汇总.")
	for i, h := range hdr {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("租金汇总.", cell, h)
	}
	_ = f.SaveAs(p)

	g := &graph.Graph{
		Files: []string{"书.xlsx"},
		Nodes: []graph.Node{
			{File: "书.xlsx", Sheet: "租金汇总"},
			{File: "书.xlsx", Sheet: "租金汇总."},
			{File: "书.xlsx", Sheet: "锚点"},
		},
		Edges: []graph.Edge{{
			From: graph.Node{File: "书.xlsx", Sheet: "租金汇总"},
			To:   graph.Node{File: "书.xlsx", Sheet: "锚点"},
			Kind: graph.KindFormula, Confidence: graph.ConfHigh,
		}},
	}
	got := Detect(Input{Graph: g, Files: []string{p}})
	if len(got) != 1 {
		t.Fatalf("应挑出 1 个孤岛，得到 %+v", got)
	}
	// 特征词一致 + 无期数差 + 相似度高 → duplicate
	// （Excel 对重名工作表会追加 "."，normSheet 已把它归一掉）
	if got[0].Kind != KindDuplicate {
		t.Fatalf("同名同表头无期数差应判为重复，得到 %+v", got[0])
	}
}

// TestDetailVsSummaryNotDuplicate 明细 vs 汇总**不是重复**（真实表里踩到）。
// 「滞纳金计算表（月）」与「滞纳金计算汇总表（月末）」相似度约 0.82，
// 但一个是明细、一个是汇总，是两张不同的表。
func TestDetailVsSummaryNotDuplicate(t *testing.T) {
	if diffSig("滞纳金计算表（月）") == diffSig("滞纳金计算汇总表（月末）") {
		t.Error("明细与汇总的特征词不该相同")
	}
	if diffSig("2026年9月租金 （日）") != diffSig("2026年10月租金 （日）") {
		t.Error("同一系列的期数差异不该影响特征词")
	}
}
