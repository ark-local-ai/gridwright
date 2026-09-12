// Package newtable 识别"工作区里新出现的表"并判断它像谁
// （见 docs/agent-architecture/25-澄清与自检边界.md 第二节）。
//
// 为什么需要单独做：一张新表刚进来时，图上它是**孤岛**（没人引用它、它也不引用别人）。
// 按"结构接近度"推断，什么也找不到——但这恰恰是最需要判断的时候：
//   - 它是链上的**下一节**（如 2026年10月租金表 接在 9 月后面）
//   - 它是**新类别**（如 催缴单）
//   - ⚠️ 它和已有表**重复**（同一件事两张表——危险信号，会算重）
//
// 判断"它像谁"**不需要模型**：比表头列名重叠度 + 表名相似度即可。
package newtable

import (
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
)

// Kind 是新表的判断类型。
const (
	KindNextInChain = "nextInChain" // 链上下一节
	KindNewCategory = "newCategory" // 新类别
	KindDuplicate   = "duplicate"   // 与已有表重复（危险）
	KindUnknown     = "unknown"
)

// Similar 是一张"像谁"的候选。
type Similar struct {
	Node      graph.Node `json:"node"`
	SheetSim  float64    `json:"sheetSim"`  // 表名相似度 0..1
	HeaderSim float64    `json:"headerSim"` // 表头列名重叠度 0..1
	Score     float64    `json:"score"`     // 综合
	Reasons   []string   `json:"reasons"`
}

// Assessment 是对一张新表的判断。
type Assessment struct {
	Node       graph.Node `json:"node"`
	IsIsolated bool       `json:"isIsolated"` // 图上没连任何人
	Kind       string     `json:"kind"`
	Similar    []Similar  `json:"similar"` // 像谁（按分数降序）
	Message    string     `json:"message"` // 给人看的一句话
	NeedAsk    bool       `json:"needAsk"` // 是否需要问用户
}

// Input 判断所需原料。
type Input struct {
	Graph *graph.Graph
	// Files 工作区所有 xlsx（用来读表头）
	Files []string
	// MaxSimilar 最多列几个"像谁"（默认 3）
	MaxSimilar int
}

// Detect 找出所有孤岛表并逐个评估。
func Detect(in Input) []Assessment {
	if in.Graph == nil {
		return nil
	}
	// 孤岛 = 图上没有任何 high 置信边相连
	linked := map[string]bool{}
	for _, e := range in.Graph.Edges {
		if e.Confidence != graph.ConfHigh {
			continue
		}
		linked[e.From.ID()] = true
		linked[e.To.ID()] = true
	}
	headers := readHeaders(in.Files)
	maxN := in.MaxSimilar
	if maxN <= 0 {
		maxN = 3
	}

	var out []Assessment
	for _, n := range in.Graph.Nodes {
		if linked[n.ID()] {
			continue
		}
		a := assess(n, in.Graph.Nodes, headers, maxN)
		out = append(out, a)
	}
	// 需要问的排前面（重复最危险 → 优先）
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return rank(out[i].Kind) < rank(out[j].Kind)
		}
		return out[i].Node.ID() < out[j].Node.ID()
	})
	return out
}

func rank(k string) int {
	switch k {
	case KindDuplicate:
		return 0 // 重复最该先看
	case KindNextInChain:
		return 1
	case KindNewCategory:
		return 2
	default:
		return 3
	}
}

// assess 判断一张孤岛表像谁。
func assess(n graph.Node, all []graph.Node, headers map[string][]string, maxN int) Assessment {
	a := Assessment{Node: n, IsIsolated: true, Similar: []Similar{}}
	myHeader := headers[n.ID()]

	for _, other := range all {
		if other.ID() == n.ID() || other.File != n.File {
			continue // 只比同文件（跨文件的相似通常不是"重复"）
		}
		oh := headers[other.ID()]
		if len(oh) == 0 && len(myHeader) == 0 {
			continue
		}
		ss := sheetSim(n.Sheet, other.Sheet)
		hs := headerSim(myHeader, oh)
		// 综合：表名与表头各半（两者都像才最可能重复）
		sc := 0.5*ss + 0.5*hs
		if sc < 0.3 {
			continue
		}
		s := Similar{Node: other, SheetSim: ss, HeaderSim: hs, Score: sc, Reasons: []string{}}
		if ss >= 0.5 {
			s.Reasons = append(s.Reasons, "表名相近")
		}
		if hs >= 0.5 {
			s.Reasons = append(s.Reasons, "表头列名高度重合")
		}
		a.Similar = append(a.Similar, s)
	}
	sort.SliceStable(a.Similar, func(i, j int) bool { return a.Similar[i].Score > a.Similar[j].Score })
	if len(a.Similar) > maxN {
		a.Similar = a.Similar[:maxN]
	}

	// 判定
	//
	// 关键区分（真实表上踩到的坑）：**"同一套表的另一期"不是重复**。
	// 「2026年9月租金表」和「2026年10月租金表」表头 100% 相同、表名 85% 相似，
	// 但它们是**同期数的不同期**，本来就该并列存在——把它们报成"重复会算重"是狼来了。
	//
	// 真正的重复信号是：**看不出任何期间差异**（期数相同，或压根没有期数）。
	top := 0.0
	if len(a.Similar) > 0 {
		top = a.Similar[0].Score
	}
	switch {
	case top >= 0.85 && !hasPeriodDiff(n.Sheet, a.Similar[0].Node.Sheet) &&
		diffSig(n.Sheet) == diffSig(a.Similar[0].Node.Sheet):
		// 三个条件都要：相似度够高 **且** 无期数差 **且** 特征词一致。
		// 少任何一个都会误报（真实表上三种误报都踩到过）。
		// **又高又同名**才算重复。阈值抬到 0.85 且要求"特征词一致"：
		// 真实表里「滞纳金计算表（月）」与「滞纳金计算汇总表（月末）」相似度 0.82，
		// 但一个是明细、一个是汇总，**是两张不同的表**——误报会让人不信任提示。
		a.Kind = KindDuplicate
		a.NeedAsk = true
		a.Message = "这张表和「" + a.Similar[0].Node.Sheet + "」名字与表头几乎一样，且看不出区别。" +
			"请确认是不是重复导入——若是同一件事的两份，可能算重。"
	case top >= 0.7:
		// 高度相似但（有期间差异 或 特征词不同）→ 同一套表的另一期/同类，正常
		a.Kind = KindNextInChain
		a.NeedAsk = false
		a.Message = "与「" + a.Similar[0].Node.Sheet + "」是同一套表的另一期，正常并列。" +
			"确认后我会记住它们属于同一系列，之后一并检查。"
	case top >= 0.4:
		// 中等相似：**不要断言**它是"下一期"——真实表里 保证金情况表 与 汇总表 只有
		// 0.4 左右相似，说成"延续"是误导。只客观说"有一定相似"，让人来判断。
		a.Kind = KindNextInChain
		a.NeedAsk = true
		a.Message = "与「" + a.Similar[0].Node.Sheet + "」有一定相似，可能相关（也可能是不同的表）。" +
			"如果确实相关，告诉我它们的关系，之后同类改动会一并检查。"
	case len(a.Similar) > 0:
		a.Kind = KindNewCategory
		a.NeedAsk = true
		a.Message = "这张表与现有表关系不明显，可能是新类别。它属于哪一类？（如 催缴、售房、保证金）"
	default:
		a.Kind = KindUnknown
		a.NeedAsk = false
		a.Message = "这张表还没和任何表建立关联。用起来之后我会逐步识别，也可以在关联图里手动连。"
	}
	return a
}

// sheetSim 表名相似度（0..1）：去掉数字/期间等噪声后比较。
func sheetSim(a, b string) float64 {
	na, nb := normSheet(a), normSheet(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	// 去掉"年月日"这类会变的数字后再比
	sa, sb := stripDigits(na), stripDigits(nb)
	if sa != "" && sa == sb {
		return 0.85
	}
	return lcs(sa, sb)
}

// normSheet 归一化 sheet 名：去空格/全角/括号，以及**尾部标点**。
// 尾部标点也要去：Excel 对重名工作表会追加 "."（如「租金汇总.」），
// 这类名字与原名是同一张表的重名副本。
func normSheet(s string) string {
	s = strings.TrimSpace(s)
	r := strings.NewReplacer(" ", "", "\u3000", "", "（", "", "）", "", "(", "", ")", "")
	s = strings.ToLower(r.Replace(s))
	return strings.TrimRight(s, ".-_\u00b7\u3002")
}

func stripDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// lcs 最长公共子序列比例（对中文表名友好，不要求连续）。
func lcs(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	dp := make([][]int, len(ra)+1)
	for i := range dp {
		dp[i] = make([]int, len(rb)+1)
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			if ra[i-1] == rb[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return 2 * float64(dp[len(ra)][len(rb)]) / float64(len(ra)+len(rb))
}

// headerSim 表头列名重叠度（0..1）。
func headerSim(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, h := range a {
		h = strings.TrimSpace(h)
		if h != "" {
			set[h] = true
		}
	}
	hit := 0
	total := 0
	for _, h := range b {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		total++
		if set[h] {
			hit++
		}
	}
	if total == 0 {
		return 0
	}
	// 用较小的一方为分母，避免"列多的一方"被稀释
	denom := math.Min(float64(len(set)), float64(total))
	if denom == 0 {
		return 0
	}
	return math.Min(1, float64(hit)/denom)
}

// readHeaders 读每张表的表头（节点ID -> 列名）。
func readHeaders(files []string) map[string][]string {
	out := map[string][]string{}
	for _, p := range files {
		// 键必须与 graph.Node.ID() 一致：用完整文件名（含 .xlsx），
		// 不能去掉扩展名——否则表头永远匹配不上（这是个真踩到的 bug）。
		base := filepath.Base(p)
		f, err := excelize.OpenFile(p)
		if err != nil {
			continue
		}
		for _, sh := range f.GetSheetList() {
			s, err := locate.LoadSheet(f, sh)
			if err != nil {
				continue
			}
			_, header := s.FindHeader(10)
			out[base+"!"+sh] = header
		}
		f.Close()
	}
	return out
}

// hasPeriodDiff 判断两个 sheet 名是否含**不同的期间标记**（年月日等）。
// 用于把"同一套表的另一期"和"真重复"分开。
func hasPeriodDiff(a, b string) bool {
	pa, pb := periodsOf(a), periodsOf(b)
	if len(pa) == 0 && len(pb) == 0 {
		return false // 两边都没有期数：分不清，交给相似度判
	}
	if len(pa) != len(pb) {
		return true // 一个有一位一个没有 → 视为不同期
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return true
		}
	}
	return false
}

// periodsOf 抽出表名里的期间数字（如 2026年9月 → [2026,9]；无则空）。
func periodsOf(s string) []int {
	var out []int
	cur := 0
	has := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur = cur*10 + int(r-'0')
			has = true
			continue
		}
		if has {
			out = append(out, cur)
			cur, has = 0, false
		}
	}
	if has {
		out = append(out, cur)
	}
	return out
}

// diffSig 抽表名的"特征词"（去掉期数与标点），用于区分
// 「滞纳金计算表（月）」和「滞纳金计算汇总表（月末）」这类**明细 vs 汇总**。
func diffSig(s string) string {
	s = normSheet(s)
	// 去掉期间数字
	s = stripDigits(s)
	// 去掉常见期次标记词（日/月/月末/月初/日/年），它们只表示时点，不表示表的内容
	r := strings.NewReplacer("月末", "", "月初", "", "（日）", "", "（月）", "", "月", "", "日", "", "年", "")
	return r.Replace(s)
}
