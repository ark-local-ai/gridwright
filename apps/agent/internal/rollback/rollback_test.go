package rollback

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
)

func mkBook(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "台账.xlsx")
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "租金")
	_ = f.SetCellValue("租金", "A1", "铺位")
	_ = f.SetCellValue("租金", "B1", "本月实收")
	_ = f.SetCellValue("租金", "A2", "A03")
	_ = f.SetCellValue("租金", "B2", 20000) // 改过之后的值
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRollbackRestoresOldValue 核心：按账目把旧值写回去。
func TestRollbackRestoresOldValue(t *testing.T) {
	dir := t.TempDir()
	book := mkBook(t, dir)
	led, err := ledger.Open(filepath.Join(dir, "ledger.csv"))
	if err != nil {
		t.Fatal(err)
	}
	// 账目：B2 从 19354.02 被改成 20000（这正是当前文件里的值）
	if err := led.Append(time.Now(), "台账.xlsx", "租金", "B2", "set",
		"19354.02", "20000", "8月租金", "quote.csv", "", "m", "ok"); err != nil {
		t.Fatal(err)
	}

	items, err := List(led, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].CanRollback {
		t.Fatalf("应有一条可回滚账目，得到 %+v", items)
	}

	res, err := Rollback(led, dir, book, items, "m")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Rolled != 1 {
		t.Fatalf("应回滚 1 处，得到 %+v", res)
	}

	// 值真的还原了
	f, _ := excelize.OpenFile(book)
	defer f.Close()
	if v, _ := f.GetCellValue("租金", "B2"); v != "19354.02" {
		t.Fatalf("B2 应还原为 19354.02，得到 %q", v)
	}
}

// TestRollbackIsLedgered 回滚本身要留痕 —— 否则"回滚的回滚"就无从谈起。
func TestRollbackIsLedgered(t *testing.T) {
	dir := t.TempDir()
	book := mkBook(t, dir)
	led, _ := ledger.Open(filepath.Join(dir, "ledger.csv"))
	_ = led.Append(time.Now(), "台账.xlsx", "租金", "B2", "set", "19354.02", "20000", "", "", "", "m", "ok")
	items, _ := List(led, 0)
	if _, err := Rollback(led, dir, book, items, "m"); err != nil {
		t.Fatal(err)
	}
	es, _ := led.Entries(0)
	if len(es) != 2 {
		t.Fatalf("应有 2 条账目（原改动 + 回滚），得到 %d", len(es))
	}
	last := es[len(es)-1]
	if last.Op != "rollback" {
		t.Errorf("最后一条应是 rollback，得到 %q", last.Op)
	}
	// 回滚记录的 old 是"回滚前的值"，所以能再倒回去
	if last.Old != "20000" || last.New != "19354.02" {
		t.Errorf("回滚账目方向不对：old=%q new=%q", last.Old, last.New)
	}
}

// TestRollbackTwiceRedoesIt 再回滚一次 = 重做（历史不丢）。
func TestRollbackTwiceRedoesIt(t *testing.T) {
	dir := t.TempDir()
	book := mkBook(t, dir)
	led, _ := ledger.Open(filepath.Join(dir, "ledger.csv"))
	_ = led.Append(time.Now(), "台账.xlsx", "租金", "B2", "set", "19354.02", "20000", "", "", "", "m", "ok")
	items, _ := List(led, 0)
	if _, err := Rollback(led, dir, book, items, "m"); err != nil {
		t.Fatal(err)
	}
	// 重新列账目，回滚那条应可再回滚
	items2, _ := List(led, 0)
	var rb *Item
	for i := range items2 {
		if items2[i].Status == "ok" && items2[i].New == "19354.02" {
			rb = &items2[i]
		}
	}
	if rb == nil || !rb.CanRollback {
		t.Fatalf("回滚产生的账目应可再回滚（=重做），得到 %+v", items2)
	}
	if _, err := Rollback(led, dir, book, []Item{*rb}, "m"); err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenFile(book)
	defer f.Close()
	if v, _ := f.GetCellValue("租金", "B2"); v != "20000" {
		t.Fatalf("再回滚应重做为 20000，得到 %q", v)
	}
}

// TestAppendNotRollbackable 追加行无法自动回滚，必须明确说明（不静默当成功）。
func TestAppendNotRollbackable(t *testing.T) {
	dir := t.TempDir()
	led, _ := ledger.Open(filepath.Join(dir, "ledger.csv"))
	_ = led.Append(time.Now(), "台账.xlsx", "租金", "", "append", "", "A05/1/2", "", "", "", "m", "ok")
	items, _ := List(led, 0)
	if items[0].CanRollback {
		t.Fatal("追加行不该标为可回滚")
	}
	if items[0].WhyNot == "" {
		t.Error("应说明为什么不能回滚")
	}
}

// TestRejectedNotRollbackable 被拒的账目没改过东西，不该出现在可回滚里。
func TestRejectedNotRollbackable(t *testing.T) {
	dir := t.TempDir()
	led, _ := ledger.Open(filepath.Join(dir, "ledger.csv"))
	_ = led.Append(time.Now(), "t.xlsx", "s", "C5", "set", "88", "-", "禁止修改", "", "", "m", "rejected")
	items, _ := List(led, 0)
	if items[0].CanRollback {
		t.Fatal("被拒的账目不该可回滚")
	}
}

// TestRollbackRefusesUnsafeFile 含宏的表拒绝回滚（与写入同一套安全边界）。
func TestRollbackRefusesUnsafeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.xlsm")
	// 造一个含 vbaProject 的最小 zip
	f, _ := os.Create(p)
	zw := newZipWriter(f)
	addZipEntry(t, zw, "[Content_Types].xml", "<Types/>")
	addZipEntry(t, zw, "xl/vbaProject.bin", "macro")
	_ = zw.Close()
	f.Close()

	led, _ := ledger.Open(filepath.Join(dir, "ledger.csv"))
	items := []Item{{Ts: "x", Table: "m.xlsm", Sheet: "s", Cell: "A1",
		Old: "1", New: "2", Status: "ok", CanRollback: true}}
	if _, err := Rollback(led, dir, p, items, "m"); err == nil {
		t.Fatal("含宏的表应拒绝回滚")
	}
}

// ---- zip 小工具（造含宏文件的测试夹具）----

func newZipWriter(w io.Writer) *zip.Writer { return zip.NewWriter(w) }

func addZipEntry(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	e, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
