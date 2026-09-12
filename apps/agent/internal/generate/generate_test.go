package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// mkBook 造一张租户表：物业位置/租户名称/本月实收/本月欠款
func mkBook(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "收款表.xlsx")
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "9月租金")
	hdr := []string{"物业位置", "租户名称", "本月实收", "本月欠款"}
	for i, h := range hdr {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue("9月租金", cell, h)
	}
	data := [][]any{
		{"A03", "早餐店", 19354.02, 0},
		{"B31", "养生殿", 0, 23540.0},
		{"A05", "美宜佳", 10000.0, 5000.0},
	}
	for ri, row := range data {
		for ci, v := range row {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+2)
			_ = f.SetCellValue("9月租金", cell, v)
		}
	}
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExtractFilter 筛选：只取欠款 > 0 的行。
func TestExtractFilter(t *testing.T) {
	dir := t.TempDir()
	p := mkBook(t, dir)
	f, _ := excelize.OpenFile(p)
	defer f.Close()

	cols, rows, sum, err := Extract(f, Spec{
		Kind:    KindTable,
		Source:  Source{Sheet: "9月租金"},
		Filter:  []Cond{{Column: "本月欠款", Op: "gt", Value: "0"}},
		Columns: []string{"物业位置", "租户名称", "本月欠款"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("应筛出 2 行欠款，得到 %d：%+v", len(rows), rows)
	}
	if len(cols) != 3 || cols[0] != "物业位置" {
		t.Fatalf("列不对：%+v", cols)
	}
	// 合计应只含筛出来的行
	if sum["本月欠款"] != 28540 {
		t.Errorf("欠款合计应为 28540，得到 %v", sum["本月欠款"])
	}
}

// TestExtractNoFilterAllRows 不给条件 = 全表。
func TestExtractNoFilterAllRows(t *testing.T) {
	dir := t.TempDir()
	p := mkBook(t, dir)
	f, _ := excelize.OpenFile(p)
	defer f.Close()
	_, rows, _, err := Extract(f, Spec{Source: Source{Sheet: "9月租金"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("不给条件应取全表 3 行，得到 %d", len(rows))
	}
}

// TestWriteTableLandsInGenerateDir 产物落在 生成/，且不改原表。
func TestWriteTableLandsInGenerateDir(t *testing.T) {
	dir := t.TempDir()
	p := mkBook(t, dir)
	before, _ := os.ReadFile(p)

	f, _ := excelize.OpenFile(p)
	cols, rows, sum, err := Extract(f, Spec{
		Source: Source{Sheet: "9月租金"},
		Filter: []Cond{{Column: "本月欠款", Op: "gt", Value: "0"}},
	})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}

	res, err := WriteTable(dir, Spec{Title: "9月欠租清单"}, cols, rows, sum)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatal("应成功")
	}
	// 落在 生成/
	if filepath.Base(filepath.Dir(res.Path)) != DirName {
		t.Errorf("应落在 %s/ 目录，得到 %s", DirName, res.Path)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("生成文件应存在: %v", err)
	}
	// **原表必须一字未改**
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("生成改动了原表！绝不允许")
	}
	// 名字里应含标题
	if !strings.Contains(res.File, "9月欠租清单") {
		t.Errorf("文件名应含标题：%s", res.File)
	}
}

// TestWriteTableNeverOverwrites 同一分钟内生成两次，不覆盖（加序号）。
func TestWriteTableNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	r1, err := WriteTable(dir, Spec{Title: "清单"}, []string{"a"}, [][]string{{"1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := WriteTable(dir, Spec{Title: "清单"}, []string{"a"}, [][]string{{"2"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Path == r2.Path {
		t.Fatalf("两次生成不该覆盖同一个文件：%s", r1.Path)
	}
	// 两个都在
	for _, p := range []string{r1.Path, r2.Path} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("应都保留：%s", p)
		}
	}
}

// TestWriteDocMarksDraft 文书要标明"草稿"。
func TestWriteDocMarksDraft(t *testing.T) {
	dir := t.TempDir()
	res, err := WriteDoc(dir, "催缴函", "尊敬的租户：您本月欠款 23540 元。")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.Path)
	body := string(b)
	if !strings.Contains(body, "草稿") {
		t.Errorf("文书应标明草稿：%s", body)
	}
	if !strings.Contains(body, "23540") {
		t.Errorf("应包含真实数字：%s", body)
	}
	if filepath.Ext(res.Path) != ".md" {
		t.Errorf("文书应为 .md，得到 %s", res.Path)
	}
}

// TestUnknownColumnRejected 列名不存在要明确报错，不静默返回空。
func TestUnknownColumnRejected(t *testing.T) {
	dir := t.TempDir()
	p := mkBook(t, dir)
	f, _ := excelize.OpenFile(p)
	defer f.Close()
	_, _, _, err := Extract(f, Spec{
		Source: Source{Sheet: "9月租金"},
		Filter: []Cond{{Column: "不存在的列", Op: "gt", Value: "0"}},
	})
	if err == nil {
		t.Fatal("列不存在应报错")
	}
}

// TestUnknownOpSelectsNothing 未知操作符应"少选"而非放宽（宁可漏，不可错）。
func TestUnknownOpSelectsNothing(t *testing.T) {
	dir := t.TempDir()
	p := mkBook(t, dir)
	f, _ := excelize.OpenFile(p)
	defer f.Close()
	_, rows, _, err := Extract(f, Spec{
		Source: Source{Sheet: "9月租金"},
		Filter: []Cond{{Column: "本月欠款", Op: "乱写的操作符", Value: "0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("未知操作符不该放宽匹配，得到 %d 行", len(rows))
	}
}
