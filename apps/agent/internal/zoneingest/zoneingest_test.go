package zoneingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这组用例钉住取件的边界：列什么、跳过什么、凭什么判定“已经处理过”，
// 以及投递绝不覆盖、演习绝不写盘。

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", p, err)
	}
	return p
}

// layout 造一个最小工作区：root/inbox + root/inbox/done + root/input。
func layout(t *testing.T) (root, src, inbox, done string) {
	t.Helper()
	root = t.TempDir()
	src = filepath.Join(root, "input")
	inbox = filepath.Join(root, "inbox")
	done = filepath.Join(inbox, "done")
	for _, d := range []string{src, inbox, done} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}
	return root, src, inbox, done
}

func opts(src, inbox, done string) Options {
	return Options{SourceDir: src, InboxDir: inbox, DoneDir: done}
}

func names(items []Candidate) []string {
	out := make([]string, len(items))
	for i, c := range items {
		out[i] = c.Name
	}
	return out
}

func find(t *testing.T, items []Candidate, name string) Candidate {
	t.Helper()
	for _, c := range items {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("候选项里没有 %q，实际有 %v", name, names(items))
	return Candidate{}
}

func TestScanDefaultPatternAndSort(t *testing.T) {
	_, src, inbox, done := layout(t)
	for _, n := range []string{"b.xlsx", "A.XLSM", "c.csv", "d.xls", "e.txt", "f.json"} {
		writeFile(t, src, n, "x")
	}
	items, err := Scan(opts(src, inbox, done))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := strings.Join(names(items), ",")
	want := "A.XLSM,b.xlsx,c.csv,d.xls"
	if got != want {
		t.Fatalf("默认后缀命中 = %s，期望 %s", got, want)
	}
}

func TestScanPatternCaseInsensitive(t *testing.T) {
	_, src, inbox, done := layout(t)
	writeFile(t, src, "C.CSV", "x")
	writeFile(t, src, "d.csv", "x")
	writeFile(t, src, "e.xlsx", "x")

	opt := opts(src, inbox, done)
	opt.Pattern = "*.csv"
	items, err := Scan(opt)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := strings.Join(names(items), ","); got != "C.CSV,d.csv" {
		t.Fatalf("*.csv 大小写不敏感匹配 = %s", got)
	}

	opt.Pattern = "["
	if _, err := Scan(opt); err == nil {
		t.Fatal("非法 glob 应当报错")
	}
}

func TestScanSkipsHiddenLockAndTemp(t *testing.T) {
	_, src, inbox, done := layout(t)
	for _, n := range []string{
		".hidden.xlsx", "~$lock.xlsx", "a.tmp", "b.xlsx.crdownload",
		"c.part", "d.xlsx~", "good.xlsx",
	} {
		writeFile(t, src, n, "x")
	}
	if err := os.MkdirAll(filepath.Join(src, "sub.xlsx"), 0o755); err != nil {
		t.Fatal(err)
	}
	items, err := Scan(opts(src, inbox, done))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := strings.Join(names(items), ","); got != "good.xlsx" {
		t.Fatalf("应只剩 good.xlsx，实际 %s", got)
	}
}

func TestScanDuplicateInInbox(t *testing.T) {
	_, src, inbox, done := layout(t)
	writeFile(t, src, "a.xlsx", "new")
	writeFile(t, inbox, "a.xlsx", "pending")

	c := find(t, mustScan(t, opts(src, inbox, done)), "a.xlsx")
	if !c.Duplicate {
		t.Fatal("inbox 已有同名文件，应判重复")
	}
	if c.Reason != "收件箱里已有同名文件" {
		t.Fatalf("Reason = %q", c.Reason)
	}
	if c.DuplicateOf != filepath.Join(inbox, "a.xlsx") {
		t.Fatalf("DuplicateOf = %q", c.DuplicateOf)
	}
}

func TestScanDuplicateNumberedInDone(t *testing.T) {
	_, src, inbox, done := layout(t)
	writeFile(t, src, "a.xlsx", "same")
	writeFile(t, done, "1-a.xlsx", "other content")

	c := find(t, mustScan(t, opts(src, inbox, done)), "a.xlsx")
	if !c.Duplicate {
		t.Fatal("done 里 1-a.xlsx 应判为同名重复")
	}
	if c.Reason != "已处理过同名文件" {
		t.Fatalf("Reason = %q", c.Reason)
	}
	if c.DuplicateOf != filepath.Join(done, "1-a.xlsx") {
		t.Fatalf("DuplicateOf = %q", c.DuplicateOf)
	}

	// 带编号的重复名（2-1-a.xlsx）也认；非数字前缀不认。
	if !doneNameMatches("2-1-a.xlsx", "a.xlsx") {
		t.Fatal("2-1-a.xlsx 应匹配 a.xlsx")
	}
	if doneNameMatches("copy-a.xlsx", "a.xlsx") {
		t.Fatal("copy-a.xlsx 不该被当成编号重复")
	}
}

func TestScanDuplicateRenamedContent(t *testing.T) {
	_, src, inbox, done := layout(t)
	writeFile(t, src, "renamed.xlsx", "同一张发票的内容")
	writeFile(t, done, "old-name.xlsx", "同一张发票的内容")
	// 同尺寸不同内容：内容去重不能误杀。
	writeFile(t, src, "samesize.xlsx", "0123456789")
	writeFile(t, done, "done-samesize.xlsx", "9876543210")

	items := mustScan(t, opts(src, inbox, done))
	c := find(t, items, "renamed.xlsx")
	if !c.Duplicate {
		t.Fatal("改名但内容相同，应判重复")
	}
	if !strings.Contains(c.Reason, "改名") {
		t.Fatalf("Reason = %q，期望提到改名", c.Reason)
	}
	if c.DuplicateOf != filepath.Join(done, "old-name.xlsx") {
		t.Fatalf("DuplicateOf = %q", c.DuplicateOf)
	}

	same := find(t, items, "samesize.xlsx")
	if same.Duplicate {
		t.Fatalf("同尺寸不同内容不该判重复，Reason=%q", same.Reason)
	}
}

func TestDeliverNoOverwrite(t *testing.T) {
	_, src, inbox, _ := layout(t)
	p := writeFile(t, src, "a.xlsx", "source")

	dst, err := Deliver(p, inbox)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if dst != filepath.Join(inbox, "a.xlsx") {
		t.Fatalf("dst = %q", dst)
	}
	if b, _ := os.ReadFile(dst); string(b) != "source" {
		t.Fatalf("内容不符: %q", b)
	}

	if _, err := Deliver(p, inbox); err == nil {
		t.Fatal("目标已存在时必须报错，不能覆盖")
	}
	if b, _ := os.ReadFile(dst); string(b) != "source" {
		t.Fatalf("第二次投递改动了已有文件: %q", b)
	}
}

func TestDeliverPreservesModTime(t *testing.T) {
	_, src, inbox, _ := layout(t)
	p := writeFile(t, src, "a.xlsx", "x")
	want := time.Date(2024, 3, 4, 5, 6, 7, 0, time.Local)
	if err := os.Chtimes(p, want, want); err != nil {
		t.Fatal(err)
	}
	dst, err := Deliver(p, inbox)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(want) {
		t.Fatalf("mtime = %v，期望 %v", info.ModTime(), want)
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	_, src, inbox, done := layout(t)
	writeFile(t, src, "a.xlsx", "x")
	writeFile(t, src, "b.csv", "y")

	res, err := Run(opts(src, inbox, done), true)
	if err != nil {
		t.Fatalf("Run dry: %v", err)
	}
	if res.Delivered != 0 {
		t.Fatalf("dryRun 不该有投递，Delivered=%d", res.Delivered)
	}
	if res.Scanned != 2 || res.Matched != 2 || len(res.Items) != 2 {
		t.Fatalf("计数不对: %+v", res)
	}
	for _, n := range []string{"a.xlsx", "b.csv"} {
		if _, err := os.Stat(filepath.Join(inbox, n)); !os.IsNotExist(err) {
			t.Fatalf("dryRun 写了文件 %s", n)
		}
	}
	if entries, _ := os.ReadDir(inbox); len(entries) != 1 { // 只有 done 目录
		t.Fatalf("dryRun 让 inbox 多了东西: %v", entries)
	}

	res, err = Run(opts(src, inbox, done), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Delivered != 2 {
		t.Fatalf("Delivered = %d", res.Delivered)
	}
	for _, c := range res.Items {
		if c.Duplicate {
			t.Fatalf("投递成功的项不该标重复: %+v", c)
		}
		if c.Reason != "已投递" {
			t.Fatalf("Reason = %q", c.Reason)
		}
		if c.DuplicateOf != filepath.Join(inbox, c.Name) {
			t.Fatalf("未记录投递目标: %+v", c)
		}
	}

	// 再跑一次：现在都在 inbox 里，应全部判重复、不再投递。
	res, err = Run(opts(src, inbox, done), false)
	if err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if res.Duplicates != 2 || res.Delivered != 0 {
		t.Fatalf("第二轮: %+v", res)
	}
}

func TestScanRejectsSourceEqualToInbox(t *testing.T) {
	root, _, inbox, done := layout(t)
	if _, err := Scan(opts(inbox, inbox, done)); err == nil {
		t.Fatal("源目录 = inbox 应被拒绝")
	}
	if _, err := Scan(opts(root, inbox, done)); err == nil {
		t.Fatal("源目录 = 工作区根应被拒绝")
	}
	if _, err := Scan(opts(done, inbox, done)); err == nil {
		t.Fatal("源目录 = done 应被拒绝")
	}
	if _, err := Scan(Options{SourceDir: filepath.Join(root, "nope"), InboxDir: inbox, DoneDir: done}); err == nil {
		t.Fatal("不存在的源目录应报错")
	}
}

func TestScanDirEntryCap(t *testing.T) {
	_, src, inbox, done := layout(t)
	for i := 0; i < 6; i++ {
		writeFile(t, src, string(rune('a'+i))+".xlsx", "x")
	}
	old := maxDirEntries
	maxDirEntries = 3
	defer func() { maxDirEntries = old }()

	items, err := Scan(opts(src, inbox, done))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("目录项上限 3，返回 %d 个", len(items))
	}
	res, err := Run(opts(src, inbox, done), true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Scanned != 3 {
		t.Fatalf("Scanned = %d，期望 3", res.Scanned)
	}
}

func TestScanMaxFilesTruncates(t *testing.T) {
	_, src, inbox, done := layout(t)
	for _, n := range []string{"d.xlsx", "b.xlsx", "a.xlsx", "c.xlsx"} {
		writeFile(t, src, n, "x")
	}
	opt := opts(src, inbox, done)
	opt.MaxFiles = 2
	items, err := Scan(opt)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := strings.Join(names(items), ","); got != "a.xlsx,b.xlsx" {
		t.Fatalf("MaxFiles=2 应按名字取前两件，实际 %s", got)
	}
	res, err := Run(opt, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Matched != 4 || len(res.Items) != 2 {
		t.Fatalf("Matched 应为截断前 4，Items 2，实际 %+v", res)
	}
}

func mustScan(t *testing.T, opt Options) []Candidate {
	t.Helper()
	items, err := Scan(opt)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return items
}

// TestRunDestDirAndOnly DestDir 决定复制到哪（扫描/去重仍在 inbox），Only 按基名过滤。
//
// 这两条是作业区 staged 投递与「只重试失败文件」的基础：不带 DestDir 时行为
// 必须与以前完全一致，否则既有调用方（收件箱面板）会被悄悄改道。
func TestRunDestDirAndOnly(t *testing.T) {
	_, src, inbox, done := layout(t)
	writeFile(t, src, "a.csv", "a")
	writeFile(t, src, "b.csv", "b")
	writeFile(t, src, "c.csv", "c")
	dest := filepath.Join(inbox, "staged", "t_1")

	opt := Options{SourceDir: src, InboxDir: inbox, DoneDir: done, DestDir: dest, Only: []string{"b.csv"}}
	res, err := Run(opt, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Matched != 1 || res.Delivered != 1 {
		t.Fatalf("Only 应只命中 1 个并投递 1 个，得到 %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dest, "b.csv")); err != nil {
		t.Fatalf("b.csv 应复制到 DestDir：%v", err)
	}
	if _, err := os.Stat(filepath.Join(inbox, "b.csv")); !os.IsNotExist(err) {
		t.Fatalf("不该再投到 inbox 根：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.csv")); !os.IsNotExist(err) {
		t.Fatalf("Only 之外的文件不该投递：%v", err)
	}

	// 演习：同样只扫这些文件，但一个都不复制。
	dest2 := filepath.Join(inbox, "staged", "t_2")
	dry, err := Run(Options{SourceDir: src, InboxDir: inbox, DoneDir: done, DestDir: dest2, Only: []string{"c.csv"}}, true)
	if err != nil {
		t.Fatalf("dryRun: %v", err)
	}
	if dry.Matched != 1 || dry.Delivered != 0 {
		t.Fatalf("演习不该投递，得到 %+v", dry)
	}
	if _, err := os.Stat(dest2); !os.IsNotExist(err) {
		t.Fatalf("演习不该创建目标目录：%v", err)
	}

	// 空 DestDir == inbox（既有行为不变）。
	plain, err := Run(Options{SourceDir: src, InboxDir: inbox, DoneDir: done, Only: []string{"a.csv"}}, false)
	if err != nil {
		t.Fatalf("Run(空 DestDir): %v", err)
	}
	if plain.Delivered != 1 {
		t.Fatalf("空 DestDir 应投到 inbox，得到 %+v", plain)
	}
	if _, err := os.Stat(filepath.Join(inbox, "a.csv")); err != nil {
		t.Fatalf("空 DestDir 应投到 inbox 根：%v", err)
	}
}
