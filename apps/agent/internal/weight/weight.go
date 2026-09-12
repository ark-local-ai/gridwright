// Package weight 给工作区里的"表 / 边 / 记忆"算注意力权重
// （见 docs/agent-architecture/22-权重设计.md）。
//
// 核心立场：**不合成一个数字**。高频 ≠ 重要——台账几乎不改却是所有月度表的来源，
// 日收天天改却只是入口。合成一个数会把这个区别抹掉，所以分为：
//
//	S 结构重要性：被多少东西依赖（联动图入度）—— 回答"别乱动它"
//	A 活跃度：最近被改多少（账目，时间衰减）—— 回答"它现在在动"
//	R 风险：改了影响面多大（主数据/公式/下游）
//	P 注意力：S/A/R 组合 —— 体检排序、界面展示用
//
// **结构权重不依赖任何使用数据**，所以从第一天起就有意义（账目还空着也能算）；
// 活跃度等有真实使用后自动生效。
//
// 每个分数都带 rationale（为什么），不做黑箱。
package weight

import (
	"math"
	"sort"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
)

// Score 是一张表（或一条边）的权重及其解释。
type Score struct {
	Node       graph.Node `json:"node"`
	Structural float64    `json:"structural"` // 0..1 结构重要性
	Activity   float64    `json:"activity"`   // 0..1 活跃度（无使用数据时为 0）
	Risk       float64    `json:"risk"`       // 0..1 风险
	Attention  float64    `json:"attention"`  // 0..1 注意力优先级

	InDegree int      `json:"inDegree"` // 被多少条边指向（去重后）
	Formulas int      `json:"formulas"` // 公式格数
	Master   bool     `json:"master"`   // 是否主数据
	Reasons  []string `json:"reasons"`  // 为什么是这个分（给人看）
	HasUsage bool     `json:"hasUsage"` // 是否已积累使用数据
}

// Input 是算权重需要的原料（都由已有能力产出，不新增存储）。
type Input struct {
	Graph    *graph.Graph
	Formulas map[string]int // 节点ID -> 公式格数（可空）
	// 活跃度：节点ID -> 近窗口内的改动次数（账目算出；无数据时传 nil）
	Activity map[string]int
	// 窗口天数（活跃度衰减用；0 用默认 30）
	WindowDays int
}

// 主数据识别关键词（真实台账里这些是"别乱动"的表）。
var masterHints = []string{"台账", "合同", "主档", "档案", "标准", "目录", "设置", "参数"}

// Compute 计算所有节点的权重。
func Compute(in Input) []Score {
	if in.Graph == nil {
		return nil
	}
	window := in.WindowDays
	if window <= 0 {
		window = 30
	}

	// 入度：按目标节点去重计数（多张表引用同一张，算多次依赖）
	inDeg := map[string]map[string]bool{} // to -> set of from
	for _, e := range in.Graph.Edges {
		if e.Confidence != graph.ConfHigh {
			continue
		}
		to := e.To.ID()
		if inDeg[to] == nil {
			inDeg[to] = map[string]bool{}
		}
		inDeg[to][e.From.ID()] = true
	}
	maxDeg := 1
	for _, s := range inDeg {
		if len(s) > maxDeg {
			maxDeg = len(s)
		}
	}
	maxFormula := 1
	for _, n := range in.Formulas {
		if n > maxFormula {
			maxFormula = n
		}
	}
	maxAct := 1
	for _, n := range in.Activity {
		if n > maxAct {
			maxAct = n
		}
	}
	hasUsage := len(in.Activity) > 0

	out := make([]Score, 0, len(in.Graph.Nodes))
	for _, node := range in.Graph.Nodes {
		id := node.ID()
		deg := len(inDeg[id])
		formulas := in.Formulas[id]
		act := in.Activity[id]

		// 主数据判定：名字命中关键词，或（被多处引用 且 几乎不改）
		master := false
		for _, h := range masterHints {
			if strings.Contains(node.Sheet, h) {
				master = true
				break
			}
		}
		if !master && deg >= 2 && hasUsage && act == 0 {
			master = true // 被依赖多、却没人改 → 事实上的主数据
		}

		s := Score{Node: node, InDegree: deg, Formulas: formulas, Master: master, HasUsage: hasUsage, Reasons: []string{}}

		// S 结构重要性
		s.Structural = 0.5*normLog(deg, maxDeg) + 0.3*b2f(master) + 0.2*normLog(formulas, maxFormula)
		if deg > 0 {
			s.Reasons = append(s.Reasons, "被 "+itoa(deg)+" 张表引用")
		}
		if master {
			s.Reasons = append(s.Reasons, "主数据")
		}
		if formulas > 0 {
			s.Reasons = append(s.Reasons, itoa(formulas)+" 个公式格")
		}

		// A 活跃度（有使用数据才算；否则保持 0 并标注）
		if hasUsage {
			s.Activity = normLog(act, maxAct)
			if act > 0 {
				s.Reasons = append(s.Reasons, "近 "+itoa(window)+" 天改 "+itoa(act)+" 次")
			}
		}

		// R 风险
		s.Risk = 0.6*b2f(master) + 0.2*b2f(formulas > 0) + 0.2*normLog(deg, maxDeg)

		// P 注意力：结构略重（财务场景"别乱动"优先于"最近在动"）
		// 并给结构分设下限：主数据不该因为最近没人改而掉到最低
		s.Attention = 0.55*s.Structural + 0.35*s.Activity + 0.10*s.Risk
		if master && s.Attention < s.Structural {
			s.Attention = s.Structural
		}
		out = append(out, s)
	}

	// 高权重在前
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Attention != out[j].Attention {
			return out[i].Attention > out[j].Attention
		}
		return out[i].Node.ID() < out[j].Node.ID()
	})
	return out
}

// Order 返回按注意力排序的节点（供体检/展示排序用）。
func (s Score) Key() string { return s.Node.ID() }

// ByID 把结果转成 map，便于按节点查。
func ByID(scores []Score) map[string]Score {
	m := make(map[string]Score, len(scores))
	for _, s := range scores {
		m[s.Node.ID()] = s
	}
	return m
}

// normLog 对数归一化到 0..1（避免一个超大值把其他都压平）。
func normLog(v, max int) float64 {
	if v <= 0 || max <= 0 {
		return 0
	}
	return math.Log(1+float64(v)) / math.Log(1+float64(max))
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
