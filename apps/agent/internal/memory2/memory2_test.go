package memory2

import (
	"path/filepath"
	"testing"
)

// TestRetrieveByTextChineseRecall 检索必须能召回**中文**记忆。
//
// 这是回归用例，钉住一个真实缺陷：原来按空格分词 + 子串包含，
// 而中文没有空格，整句被当成一个"词"，永远匹配不到——
// 表现就是"用户拍板过的规矩在改表时根本没生效"。
func TestRetrieveByTextChineseRecall(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.PutFact(Fact{ID: "f1", Value: "B31 铺位租户是早餐店，月租金 23540 元",
		Key: map[string]string{"铺位": "B31"}})
	_ = s.PutDecision(Decision{ID: "d1", Text: "含运费的金额以后都算进去，不再单独确认"})
	_ = s.PutDecision(Decision{ID: "d2", Text: "滞纳金那张表不要动，那是财务自己算的"})

	cases := []struct {
		query string
		facts int
		decs  int
	}{
		// 业务键命中：最可靠的一类
		{"B31 收到 8 月租金 23540", 1, 0},
		// ★ 就是这条以前召回为 0：查询里出现"运费"，该带出那条决策
		{"记一笔：A03 收了运费 500", 0, 1},
		// "滞纳金"命中另一条决策
		{"更新一下滞纳金", 0, 1},
		// 完全无关的话：什么都不该带（否则记忆就是噪音）
		{"今天天气怎么样", 0, 0},
	}
	for _, c := range cases {
		got := s.RetrieveByText(c.query)
		if len(got.Facts) != c.facts || len(got.Decisions) != c.decs {
			t.Errorf("查询 %q：facts=%d(期望%d) decisions=%d(期望%d)",
				c.query, len(got.Facts), c.facts, len(got.Decisions), c.decs)
		}
	}
}

// TestBigramsChinese 拆字对：中文按相邻字对切，标点空白剔除。
func TestBigramsChinese(t *testing.T) {
	g := bigrams("含运费，不含税")
	for _, want := range []string{"含运", "运费", "不含", "含税"} {
		if !g[want] {
			t.Errorf("应含 bigram %q，得到 %v", want, g)
		}
	}
	if g["费不"] {
		t.Error("标点两侧不该连成一个 bigram（逗号应被剔除）")
	}
	if len(bigrams("")) != 0 {
		t.Error("空文本应得到空集合")
	}
	if !bigrams("租")["租"] {
		t.Error("单字应退回该字本身，否则单字查询永远不命中")
	}
}

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

// TestRemoveMemory 删掉一条记忆，并连带清掉它的过时标记。
//
// 为什么这条值得测：记忆会写错（提炼偏了、人后来改主意），而错的记忆会
// 污染以后所有判断。不能删就等于永久留着那个错。同时注意：**留下的 stale
// 标记必须一起清**——否则会有一条指向不存在记忆的过时提示，纯属垃圾。
func TestRemoveMemory(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.PutFact(Fact{ID: "f1", Value: "B31 月租金 23540"})
	_ = s.PutFact(Fact{ID: "f2", Value: "A03 月租金 19354.02"})
	_ = s.PutDecision(Decision{ID: "d1", Text: "含运费都算进去"})
	_ = s.PutDecision(Decision{ID: "d2", Text: "滞纳金表不要动"})
	_ = s.MarkStale(Stale{ID: "d1", Note: "口径可能变了"})

	if err := s.RemoveDecision("d1"); err != nil {
		t.Fatal(err)
	}
	f := s.All()
	if len(f.Decisions) != 1 || f.Decisions[0].ID != "d2" {
		t.Errorf("应只剩 d2，得到 %+v", f.Decisions)
	}
	if len(f.Stale) != 0 {
		t.Errorf("删掉记忆后它的过时标记也该清掉，却还剩 %+v", f.Stale)
	}

	if err := s.RemoveFact("f1"); err != nil {
		t.Fatal(err)
	}
	f = s.All()
	if len(f.Facts) != 1 || f.Facts[0].ID != "f2" {
		t.Errorf("应只剩 f2，得到 %+v", f.Facts)
	}

	// 删不存在的 id：不该报错，也不该影响其他条目
	if err := s.RemoveFact("不存在"); err != nil {
		t.Errorf("删不存在的 id 不该报错：%v", err)
	}
	if len(s.All().Facts) != 1 {
		t.Error("删不存在的 id 不该动到其他条目")
	}
}
