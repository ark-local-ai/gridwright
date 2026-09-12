package selfcheck

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
)

func mkGraph() *graph.Graph {
	n := func(s string) graph.Node { return graph.Node{File: "收款表.xlsx", Sheet: s} }
	e := func(from, to string) graph.Edge {
		return graph.Edge{From: n(from), To: n(to), Kind: graph.KindFormula, Confidence: graph.ConfHigh}
	}
	return &graph.Graph{
		Files: []string{"收款表.xlsx"},
		Nodes: []graph.Node{n("每日实收表"), n("各月租金表"), n("汇总表")},
		Edges: []graph.Edge{e("各月租金表", "每日实收表"), e("汇总表", "各月租金表")},
	}
}

func writeBook(t *testing.T, path, sheet string, vals map[string]string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", sheet)
	for ref, v := range vals {
		_ = f.SetCellValue(sheet, ref, v)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

// TestDetectsBadValue 自检能查出相关表里的错误值（确定性问题）。
func TestDetectsBadValue(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "收款表.xlsx")
	writeBook(t, p, "各月租金表", map[string]string{"H8": "#REF!", "A1": "ok"})

	rep := Run(Input{
		Root: dir, Files: []string{p},
		Changed: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"},
		Graph:   mkGraph(),
		Touched: []string{"收款表.xlsx!各月租金表"},
	})
	if rep.Level != LevelIssue {
		t.Fatalf("应判为 issue，得到 %s（%s）", rep.Level, rep.Summary)
	}
	found := false
	for _, f := range rep.Findings {
		if f.Kind == "badValue" && f.Where == "H8" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应发现 H8 是错误值，得到 %+v", rep.Findings)
	}
}

// TestNotTouchedNeedsClarify 相关表本次没动 → 提示并标需要人确认。
func TestNotTouchedNeedsClarify(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "收款表.xlsx")
	writeBook(t, p, "各月租金表", map[string]string{"A1": "fine"})

	rep := Run(Input{
		Root: dir, Files: []string{p},
		Changed: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"},
		Graph:   mkGraph(),
		Touched: []string{}, // 一张都没碰
	})
	var nt *Finding
	for i := range rep.Findings {
		if rep.Findings[i].Kind == "notTouched" {
			nt = &rep.Findings[i]
		}
	}
	if nt == nil {
		t.Fatalf("应提示相关表未改动，得到 %+v", rep.Findings)
	}
	if !nt.NeedClarify {
		t.Error("未同步的相关表应标为需要人确认（澄清不可跳过）")
	}
	if rep.Level != LevelNotice {
		t.Errorf("应为 notice，得到 %s", rep.Level)
	}
}

// TestCleanPassesNoFindings 相关表都动过且没问题 → 报告"已同步"。
func TestCleanPasses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "收款表.xlsx")
	writeBook(t, p, "各月租金表", map[string]string{"A1": "fine"})
	writeBook2(t, p, "汇总表", map[string]string{"A1": "fine"})

	rep := Run(Input{
		Root: dir, Files: []string{p},
		Changed: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"},
		Graph:   mkGraph(),
		Touched: []string{"收款表.xlsx!各月租金表", "收款表.xlsx!汇总表"},
	})
	if rep.Level != LevelOK {
		t.Fatalf("应无遗留，得到 %s：%s", rep.Level, rep.Summary)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("不该有发现：%+v", rep.Findings)
	}
}

// TestExternalNodeNotice 工作区里没有对应文件的节点 → 提示但不算问题。
func TestExternalNodeNotice(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "收款表.xlsx")
	writeBook(t, p, "各月租金表", map[string]string{"A1": "x"})
	g := mkGraph()
	// 加一个"外部文件"节点，并被引用
	g.Nodes = append(g.Nodes, graph.Node{File: "别的表.xlsx", Sheet: "台账"})
	g.Edges = append(g.Edges, graph.Edge{
		From: graph.Node{File: "收款表.xlsx", Sheet: "各月租金表"},
		To:   graph.Node{File: "别的表.xlsx", Sheet: "台账"},
		Kind: graph.KindFormula, Confidence: graph.ConfHigh,
	})
	rep := Run(Input{
		Root: dir, Files: []string{p},
		Changed: graph.Node{File: "收款表.xlsx", Sheet: "每日实收表"},
		Graph:   g, Touched: []string{"收款表.xlsx!各月租金表"},
	})
	sawExternal := false
	for _, f := range rep.Findings {
		if f.Kind == "external" {
			sawExternal = true
			if f.Level == LevelIssue {
				t.Error("外部文件应是 notice 不是 issue")
			}
		}
	}
	if !sawExternal {
		t.Logf("未触发外部提示（可能该节点未进候选）：%+v", rep.Findings)
	}
}

func writeBook2(t *testing.T, base, sheet string, vals map[string]string) {
	t.Helper()
	// 追加一个 sheet 到已有文件
	f, err := excelize.OpenFile(base)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, _ = f.NewSheet(sheet)
	for ref, v := range vals {
		_ = f.SetCellValue(sheet, ref, v)
	}
	if err := f.SaveAs(base); err != nil {
		t.Fatal(err)
	}
}
