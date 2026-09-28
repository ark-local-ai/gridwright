package agent

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// 自动发现"可能已过时"的记忆（见 docs/agent-architecture/21-记忆设计.md 的 stale 机制）。
//
// 为什么需要它：记忆写进去之后就一直是"当前有效"的，直到有人想起来去核对。
// 但表在变（铺位改名、合同结束、表被重做），记忆不会自己知道。时间一长，
// 系统还会拿着旧结论去改表——而用户根本不知道该去检查哪一条。
//
// ★ 这个检查的**可靠性**比覆盖面重要得多。一条误标会让整套记忆失去信任，
// 用户会开始无视所有过时提示（狼来了），那时这个机制还不如没有。
// 所以这里只做一类**可证明**的判断：**引用消失**——
// 记忆里点名的标识（如 铺位 B31）在当前工作区的所有表里都找不到了。
//
// 明确**不做**的（重要）：
//   - **不比数字**。"记忆说月租 23540、表里现在是 25000，所以过时了"——
//     金额本来就会变（升租、补缴），那样报出来的几乎全是正常变化，
//     会把这个提示变成噪音。数字是否合理，得靠人判断。
//   - **不判语义**。"合同期到 2027-06，现在已过"这类需要理解业务口径，
//     属于模型该在对话里做的事，不是这里能可靠断言的。
//
// 另一个刻意的设计：**只标记，从不删除**。标记是提示，人看一眼就能清掉；
// 而误删一条记忆是不可逆的。

// StaleMark 是本次自动标记的一条。
type StaleMark struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // fact | decision
	Value  string `json:"value"`
	Note   string `json:"note"`
	Where  string `json:"where,omitempty"` // 这条记忆讲的是什么（给人认出来）
	Source string `json:"source,omitempty"`
}

// CheckMemoryStaleness 扫一遍记忆，把"引用的标识在当前表里找不到"的标成可能过时。
//
// 只读表、只写 stale 标记，不改任何记忆正文。返回本次新标了哪些。
func (a *Agent) CheckMemoryStaleness() ([]StaleMark, error) {
	mem, err := memory2.Open(a.Layout.Root)
	if err != nil {
		return nil, err
	}
	files, err := a.Layout.DataFiles()
	if err != nil {
		return nil, err
	}

	hay := newHaystack()
	for _, fp := range files {
		if err := hay.addFile(fp); err != nil {
			// 单个文件读不了不该让整次检查失败：跳过它，但记住"少看了一张表"，
			// 因为少看的表可能正是装着那个铺位的那张——那会误判成"消失"。
			hay.skipped = append(hay.skipped, fp)
		}
	}

	// 有表没读到（比如正被 Excel 占用）：**放弃这次检查**。
	// 宁可这次不报，也不能在"只看了半个工作区"的基础上说某条记忆失效了。
	if len(hay.skipped) > 0 {
		return nil, fmt.Errorf("有 %d 张表这次读不到（可能被 Excel 打开着），"+
			"为避免误判，先不做失效检查", len(hay.skipped))
	}

	out := []StaleMark{}
	snapshot := mem.All()

	for _, f := range snapshot.Facts {
		miss := hay.absentKey(f.Key)
		if miss == "" {
			continue
		}
		note := staleNote(miss)
		if err := mem.MarkStale(memory2.Stale{ID: f.ID, Note: note}); err != nil {
			return out, err
		}
		out = append(out, StaleMark{ID: f.ID, Kind: "fact", Value: f.Value, Note: note,
			Where: keyDesc(f.Key), Source: f.Source})
	}
	for _, d := range snapshot.Decisions {
		miss := hay.absentKey(d.Key)
		if miss == "" {
			continue
		}
		note := staleNote(miss)
		if err := mem.MarkStale(memory2.Stale{ID: d.ID, Note: note}); err != nil {
			return out, err
		}
		out = append(out, StaleMark{ID: d.ID, Kind: "decision", Value: d.Text, Note: note,
			Where: keyDesc(d.Key), Source: d.Source})
	}
	return out, nil
}

func staleNote(miss string) string {
	return fmt.Sprintf("在当前工作区的表里找不到「%s」。"+
		"它可能已改名或删除，也可能这份工作区不含那张表。请核对后再决定是否保留。", miss)
}

func keyDesc(kv map[string]string) string {
	if len(kv) == 0 {
		return ""
	}
	parts := make([]string, 0, len(kv))
	for k, v := range kv {
		parts = append(parts, k+" "+v)
	}
	return strings.Join(parts, "、")
}

// haystack 是工作区里全部单元格文本的集合（归一化后），用于回答
// "这个标识还在不在表里"。
type haystack struct {
	cells   map[string]bool
	skipped []string
}

func newHaystack() *haystack {
	return &haystack{cells: map[string]bool{}}
}

func (h *haystack) addFile(path string) error {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, name := range f.GetSheetList() {
		sh, err := locate.LoadSheet(f, name)
		if err != nil {
			continue // 单张表读不了：其它表照常喂进去
		}
		for _, row := range sh.Rows {
			for _, v := range row {
				n := locate.Normalize(v)
				if n != "" {
					h.cells[n] = true
				}
			}
		}
	}
	return nil
}

// absentKey 判断记忆的业务键里有没有"已经不在表里"的标识。
// 返回第一个找不到的**标识值**；全都还在（或没有可查的键）则返回空串。
func (h *haystack) absentKey(kv map[string]string) string {
	// 遍历顺序固定（按键名排序）：一次可能返回多个候选，顺序不稳会让提示飘忽
	names := make([]string, 0, len(kv))
	for k := range kv {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := strings.TrimSpace(kv[k])
		if !searchable(v) {
			continue
		}
		if !h.cells[locate.Normalize(v)] {
			return v
		}
	}
	return ""
}

// searchable 判断一个键值是否适合拿去做"存在性"检查。
//
// 判据只有一条：**含字母（拉丁或汉字）才查**。理由是这类值才是"标识"
// （铺位号 B31、合同号 HT-2024-07、租户名 早餐店），而标识消失意味着
// 记忆指向的东西已经没了，值得提示。
//
// 反过来，**纯数字与数字+分隔符的一律不查**——金额（23540）、月份（2026-08）、
// 序号（12）都在此列。它们本来就会变：升租、补缴、月份滚动。拿它们做存在性
// 判断，等于每次正常变动都报一条"记忆可能失效"，而那正是本检查最该避免的
// 噪音——过时提示一旦开始误报，用户就会学会无视全部提示，机制等于失效。
//
// 刻意接受的代价：纯数字的铺位号（如 001）不会被自动检查。这是**有意的**：
// 宁可漏报，也不要误报（今天本来也不报，漏报不是退步）。
func searchable(v string) bool {
	if len([]rune(strings.TrimSpace(v))) < 2 {
		return false
	}
	for _, r := range v {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
