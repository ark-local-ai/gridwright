package memory2

import (
	"path/filepath"
	"testing"
)

func TestPutRetrieveDecision(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 一条关于 B31 的事实
	if err := s.PutFact(Fact{
		ID: "f1", Kind: "rent", Key: map[string]string{"铺位": "B31"},
		Value: "19354.02", Unit: "元/月", Source: "商铺台账!B45",
	}); err != nil {
		t.Fatal(err)
	}
	// 一条关于 B45-1 的决策
	if err := s.PutDecision(Decision{
		ID: "d1", Key: map[string]string{"铺位": "B45-1"},
		Text: "按 455 计入（含运费，不含税）", Source: "conversation:c1", By: "user",
	}); err != nil {
		t.Fatal(err)
	}

	// 检索 B31：应只命中事实，不该带出 B45-1 的决策
	got := s.Retrieve("每日实收表", map[string]string{"铺位": "B31"}, "本月实收")
	if len(got.Facts) != 1 || got.Facts[0].ID != "f1" {
		t.Fatalf("应命中 1 条 B31 事实，得到 %+v", got.Facts)
	}
	if len(got.Decisions) != 0 {
		t.Fatalf("不该带出无关铺位的决策，得到 %+v", got.Decisions)
	}

	// 检索 B45-1：应命中决策
	got2 := s.Retrieve("每日实收表", map[string]string{"铺位": "B45-1"}, "")
	if len(got2.Decisions) != 1 {
		t.Fatalf("应命中 B45-1 的决策，得到 %+v", got2.Decisions)
	}

	// 无任何条件：宁可不给，也不给噪音
	empty := s.Retrieve("", nil, "")
	if len(empty.Facts) != 0 || len(empty.Decisions) != 0 {
		t.Fatalf("无条件检索应为空，得到 %+v", empty)
	}
}

func TestDescribeIncludesSource(t *testing.T) {
	f := File{
		Facts:     []Fact{{ID: "f1", Key: map[string]string{"铺位": "B31"}, Value: "19354.02", Unit: "元/月", Source: "商铺台账!B45"}},
		Decisions: []Decision{{ID: "d1", Key: map[string]string{"铺位": "B31"}, Text: "含运费"}},
	}
	out := f.Describe()
	if out == "" {
		t.Fatal("应有描述")
	}
	for _, want := range []string{"B31", "19354.02", "商铺台账!B45", "含运费"} {
		if !contains(out, want) {
			t.Errorf("描述应含 %q：\n%s", want, out)
		}
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.PutFact(Fact{ID: "f1", Key: map[string]string{"铺位": "A03"}, Value: "1", Source: "x"})

	// 重新打开应读回
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, d, _ := s2.Counts()
	if f != 1 || d != 0 {
		t.Fatalf("应读回 1 条事实，得到 facts=%d decisions=%d", f, d)
	}
	if filepath.Base(s2.Path()) != "memory.json" {
		t.Errorf("记忆文件应为 memory.json，得到 %s", s2.Path())
	}
}

func TestStaleMarking(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := s.MarkStale(Stale{ID: "f9", Note: "该铺位租金将调整"}); err != nil {
		t.Fatal(err)
	}
	f := s.All()
	if len(f.Stale) != 1 {
		t.Fatalf("应有一条过时标记，得到 %+v", f.Stale)
	}
	if err := s.RemoveStale("f9"); err != nil {
		t.Fatal(err)
	}
	if len(s.All().Stale) != 0 {
		t.Fatal("处理后应清空过时标记")
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
