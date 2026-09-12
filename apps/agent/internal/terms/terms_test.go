package terms

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManualWinsOverClarified 核心规则：**manual 永远优先**（用户拍板）。
func TestManualWinsOverClarified(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 先澄清得来一条
	if err := s.Save(Term{
		Word: "那笔钱", Resolves: Target{Sheet: "每日实收表"}, Source: "clarified",
	}); err != nil {
		t.Fatal(err)
	}
	// 人手写覆盖同一条
	if err := s.Save(Term{
		Word: "那笔钱", Resolves: Target{Sheet: "每日实收表（日）", Field: "租金"}, Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Lookup("那笔钱")
	if !ok {
		t.Fatal("应查到")
	}
	if got.Source != "manual" || got.Resolves.Field != "租金" {
		t.Fatalf("应为人工定义并指向租金，得到 %+v", got)
	}

	// 反过来：已有 manual 时，clarified 不许覆盖
	if err := s.Save(Term{
		Word: "那笔钱", Resolves: Target{Sheet: "别的表"}, Source: "clarified",
	}); err == nil {
		t.Fatal("clarified 不该覆盖 manual，应报错")
	}
	again, _ := s.Lookup("那笔钱")
	if again.Resolves.Sheet != "每日实收表（日）" {
		t.Fatalf("manual 定义不该被改：%+v", again)
	}
}

// TestResolveInText 在一句话里找出命中的词（供组装 prompt）。
func TestResolveInText(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.Save(Term{Word: "老李那家", Resolves: Target{Key: map[string]string{"租户": "李正明"}}, Source: "manual"})
	_ = s.Save(Term{Word: "那笔钱", Resolves: Target{Kind: "收租"}, Source: "clarified"})

	got := s.Resolve("老李那家的那笔钱收到了")
	if len(got) != 2 {
		t.Fatalf("应命中 2 条，得到 %+v", got)
	}
	// 描述里要标出人工定义（模型必须遵守）
	desc := Describe(got)
	if !contains(desc, "人工定义") {
		t.Errorf("应标明人工定义：%s", desc)
	}
}

// TestPersistenceAndPrompts 落盘 + 默认提问模板。
func TestPersistenceAndPrompts(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.Save(Term{Word: "卖房收入", Resolves: Target{Kind: "售房"}, Source: "manual"})

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Lookup("卖房收入"); !ok {
		t.Fatal("应读回")
	}
	if filepath.Base(s2.Path()) != "term-map.yaml" {
		t.Errorf("文件名应为 term-map.yaml，得到 %s", s2.Path())
	}
	p := s2.Prompts()
	if p.UnknownTerm == "" || p.CategoryUnclear == "" {
		t.Error("应提供默认提问模板")
	}
}

// TestEmptyTargetRejected 没指向任何东西的映射应被拒（避免存了没用）。
func TestEmptyTargetRejected(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := s.Save(Term{Word: "糊里糊涂"}); err == nil {
		t.Fatal("没有指向应报错")
	}
}

// TestNoFileIsFine 首次运行没有文件不该报错。
func TestNoFileIsFine(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("无文件应可用: %v", err)
	}
	if _, ok := s.Lookup("任何"); ok {
		t.Error("空表不该命中")
	}
	// 也不该因为读一下就创建文件
	if _, err := os.Stat(filepath.Join(dir, "term-map.yaml")); err == nil {
		t.Error("只读不该创建文件")
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
