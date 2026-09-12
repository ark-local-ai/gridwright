package ledger

import (
	"os"
	"testing"
	"time"
)

// TestAppendAndRead 新格式（含 sheet 列）：写入 → 读回，字段对齐。
func TestAppendAndReadNewFormat(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir + "/ledger.csv")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(time.Now(), "收款表.xlsx", "各月租金", "H8", "set",
		"19354.02", "38708.04", "8月租金", "quote.csv", "同步", "m", "ok"); err != nil {
		t.Fatal(err)
	}
	es, err := l.Entries(10)
	if err != nil {
		t.Fatalf("读回应成功: %v", err)
	}
	if len(es) != 1 {
		t.Fatalf("应有 1 条，得到 %d", len(es))
	}
	e := es[0]
	if e.Table != "收款表.xlsx" || e.Sheet != "各月租金" || e.Cell != "H8" {
		t.Errorf("字段错位：%+v", e)
	}
	if e.Old != "19354.02" || e.New != "38708.04" || e.Status != "ok" {
		t.Errorf("值错位：%+v", e)
	}
}

// TestReadOldFormat 兼容旧账目（无 sheet 列的 11 列格式）。
// **加了列不能让历史账目读不出来**——真实用户已经有旧文件。
func TestReadOldFormat(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ledger.csv"
	// 旧格式：ts,table,cell,op,old,new,reason,source,rule,model,status
	content := "ts,table,cell,op,old,new,reason,source,rule,model,status\n" +
		"2026-09-01 10:00,销售.xlsx,C2,set,320,455,报价,quote.csv,同步,deepseek,ok\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	es, err := l.Entries(10)
	if err != nil {
		t.Fatalf("旧格式应能读（%v）", err)
	}
	if len(es) != 1 {
		t.Fatalf("应有 1 条，得到 %d", len(es))
	}
	e := es[0]
	// 旧格式没有 sheet → Sheet 为空，但其他字段必须对
	if e.Table != "销售.xlsx" || e.Cell != "C2" {
		t.Errorf("旧格式字段错位：%+v", e)
	}
	if e.Old != "320" || e.New != "455" || e.Status != "ok" {
		t.Errorf("旧格式值错位：%+v", e)
	}
}

// TestActivityByNode 活跃度按 文件!sheet 统计，只算 ok 的、且带时间衰减。
func TestActivityByNode(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir + "/ledger.csv")
	now := time.Now()
	// 今天改 2 次，40 天前改 1 次（超出 30 天窗口，应被排除）
	_ = l.Append(now, "收款表.xlsx", "每日实收", "E2", "set", "0", "1", "", "", "", "", "ok")
	_ = l.Append(now, "收款表.xlsx", "每日实收", "E3", "set", "0", "1", "", "", "", "", "ok")
	_ = l.Append(now.AddDate(0, 0, -40), "收款表.xlsx", "每日实收", "E4", "set", "0", "1", "", "", "", "", "ok")
	// 被拒的不算
	_ = l.Append(now, "收款表.xlsx", "每日实收", "E5", "set", "0", "1", "", "", "", "", "rejected")

	act, err := l.ActivityByNode(30)
	if err != nil {
		t.Fatal(err)
	}
	got := act["收款表!每日实收"]
	if got <= 0 {
		t.Fatalf("应有活跃度，得到 %v", act)
	}
	// 40 天前那条不该计入：如果计入了，值会明显偏大
	if got > 30 {
		t.Errorf("窗口外/被拒的不该计入，得到 %d", got)
	}
}

// TestOpenExistingKeepsHeader 已存在的账目不应被重写表头。
func TestOpenExistingKeepsHeader(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/ledger.csv"
	l, _ := Open(p)
	_ = l.Append(time.Now(), "t", "s", "A1", "set", "a", "b", "", "", "", "", "ok")
	l2, _ := Open(p) // 再次打开
	_ = l2.Append(time.Now(), "t", "s", "A2", "set", "a", "b", "", "", "", "", "ok")
	es, _ := l2.Entries(0)
	if len(es) != 2 {
		t.Fatalf("应有 2 条（表头不该重复写），得到 %d", len(es))
	}
}
