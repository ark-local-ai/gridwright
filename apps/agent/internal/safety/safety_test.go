package safety

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestCleanWorkbookIsSafe(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clean.xlsx")
	f := excelize.NewFile()
	_ = f.SetCellValue("Sheet1", "A1", "铺位")
	_ = f.SetCellValue("Sheet1", "B1", "金额")
	_ = f.SaveAs(p)
	f.Close()

	rep, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Level != LevelOK {
		t.Errorf("普通数据表应无风险，得到 %s：%+v", rep.Level, rep.Risks)
	}
	if !rep.CanWrite {
		t.Error("普通表应可写")
	}
}

// TestMacroBlocked 含宏的工作簿必须被拦下——excelize 会丢宏。
func TestMacroBlocked(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.xlsm")
	writeZip(t, p, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"xl/vbaProject.bin":   "fake-macro-bytes",
		"xl/workbook.xml":     `<workbook/>`,
	})
	rep, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Level != LevelBlock {
		t.Fatalf("含宏应判为 block，得到 %s", rep.Level)
	}
	if rep.CanWrite {
		t.Error("含宏不该允许写入")
	}
	if !containsStr(rep.Describe(), "宏") {
		t.Errorf("说明应提到宏：%s", rep.Describe())
	}
}

// TestPivotWarned 透视表：警告但仍允许（会备份）。
func TestPivotWarned(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "p.xlsx")
	writeZip(t, p, map[string]string{
		"xl/pivotTables/pivotTable1.xml": `<pivotTableDefinition/>`,
		"xl/workbook.xml":                `<workbook/>`,
	})
	rep, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Level != LevelWarn {
		t.Fatalf("透视应判 warn，得到 %s", rep.Level)
	}
	if !rep.CanWrite {
		t.Error("warn 级别应仍可写（有备份）")
	}
	if !containsStr(rep.Describe(), "透视") {
		t.Errorf("说明应提到透视：%s", rep.Describe())
	}
}

// TestRealWorkbook 真实台账：应无 block 级风险（我们实测它是保真的）。
func TestRealWorkbook(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	m, _ := filepath.Glob(filepath.Join(root, "docs", "*.xlsx"))
	if len(m) == 0 {
		t.Skip("未找到真实表")
	}
	rep, err := Check(m[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("真实表：level=%s canWrite=%v", rep.Level, rep.CanWrite)
	for _, r := range rep.Risks {
		t.Logf("  [%s] %s: %s", r.Level, r.Kind, r.Detail)
	}
	if rep.Level == LevelBlock {
		t.Error("真实台账不该判为 block（实测 excelize 对它保真）")
	}
}

// TestCheckDoesNotModify 安全检查绝不能改动文件。
func TestCheckDoesNotModify(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.xlsx")
	f := excelize.NewFile()
	_ = f.SetCellValue("Sheet1", "A1", "v")
	_ = f.SaveAs(p)
	f.Close()
	before, _ := os.ReadFile(p)
	if _, err := Check(p); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if len(before) != len(after) {
		t.Fatal("检查改动文件了！必须只读")
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
