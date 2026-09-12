// Package impact 推断"一次改动牵连谁"（见 docs/agent-architecture/23-影响面推断.md）。
//
// 与 internal/weight 的区别（重要）：
//
//	weight  ：这张表**一般**多重要（静态，全工作区）——用于"全面体检先看谁"
//	impact  ：**这一笔**要同步改/检查谁（条件性，仅本次）——用于日常主流程
//
// 用户的关键纠正：**改动的那张表是"输入"，不给它打分**（日收表天天动是它的定义，
// 算出来是同义反复）。要算的是"被它影响的其他表"，且**影响面 ≠ 图上可达**——
// 还取决于这笔钱的**语义类别**（收租 vs 卖房 vs 保证金）。
//
// 相关性来源三路：
//
//	结构接近度：图上离改动点几步（近的更相关）
//	记忆命中  ：这类改动历史上涉及它（人纠正过的权重更高）
//	历史同期  ：账目里它常和改动点同期动（后续接）
package impact

import (
	"sort"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// Candidate 是一个"可能受这笔改动影响"的表。
type Candidate struct {
	Node  graph.Node `json:"node"`
	Score float64    `json:"score"` // 0..1 相关性
	// 为什么相关（给人看，可解释）
	Reasons []string `json:"reasons"`
	// 证据来源
	ByStructure bool `json:"byStructure"` // 图上有路径
	ByMemory    bool `json:"byMemory"`    // 这类改动历史上涉及它
	// 是否已经改过（自检时用：未改的要提醒）
	Touched bool `json:"touched"`
}

// Result 是一次影响面推断的结果。
type Result struct {
	Kind       string      `json:"kind"`       // 语义类别（收租/卖房/...；空=未判定）
	Source     string      `json:"source"`     // 类别怎么来的：memory | llm | none
	Candidates []Candidate `json:"candidates"` // 按相关性降序
	Note       string      `json:"note,omitempty"`
}

// Input 是推断所需的原料。
type Input struct {
	Graph *graph.Graph
	// From 是本次改动所在的节点（"输入表"——它自己不算候选）
	From graph.Node
	// Kind 是语义类别（由 LLM 判定；空表示没判出来）
	Kind string
	// Memory 提供"这类改动历来涉及哪些表"
	Memory *memory2.Store
	// MaxHops 结构扩散的最大跳数（默认 2）
	MaxHops int
}

// 打分权重（见文档第五节）
const (
	wStructure = 0.40
	wMemory    = 0.40
	wHistory   = 0.20 // 历史同期证据（M5 接入；现在恒为 0）
)

// Infer 推断影响面。
func Infer(in Input) Result {
	res := Result{Kind: in.Kind, Candidates: []Candidate{}}
	if in.Graph == nil {
		res.Note = "没有联动图，无法推断影响面"
		return res
	}
	if in.Kind == "" {
		res.Source = "none"
		res.Note = "没判定这笔改动的类别，只按结构给出可能相关的表"
	} else {
		res.Source = "llm"
	}

	maxHops := in.MaxHops
	if maxHops <= 0 {
		maxHops = 2
	}

	// ① 结构候选：从改动点出发，正反向都要走。
	//    正向 = "它依赖谁"（改上游要回头看）；反向 = "谁依赖它"（改它要同步下游）。
	hopOf := map[string]int{}
	bfs(in.Graph, in.From, &hopOf, maxHops, true)
	bfs(in.Graph, in.From, &hopOf, maxHops, false)
	delete(hopOf, in.From.ID()) // 改动点自己是输入，不做候选

	// ② 记忆候选：这类改动历史上涉及哪些表（人纠正过的优先级更高）
	memTables := map[string]bool{}
	memSource := ""
	if in.Memory != nil && in.Kind != "" {
		rel := in.Memory.RelationsOf(in.Kind)
		for _, t := range rel.Tables {
			memTables[stemOf(t)] = true
		}
		memSource = rel.Source
		if len(rel.Tables) > 0 {
			res.Source = "memory" // 有记忆就用记忆的类别语义
		}
	}

	// 合并候选
	byID := map[string]*Candidate{}
	add := func(n graph.Node) *Candidate {
		id := n.ID()
		if c, ok := byID[id]; ok {
			return c
		}
		c := &Candidate{Node: n, Reasons: []string{}}
		byID[id] = c
		return c
	}
	for id := range hopOf {
		n := nodeFromGraph(in.Graph, id)
		c := add(n)
		c.ByStructure = true
	}
	for _, n := range in.Graph.Nodes {
		if memTables[stemOf(n.Sheet)] || memTables[stemOf(n.ID())] {
			c := add(n)
			c.ByMemory = true
		}
	}

	// 打分
	for id, c := range byID {
		var s float64
		// 结构接近度：1 跳最相关，越远越弱
		if hops, ok := hopOf[id]; ok && c.ByStructure {
			s += wStructure * (1.0 / float64(hops+1))
		}
		// 记忆命中
		if c.ByMemory {
			s += wMemory
		}
		c.Score = s

		if c.ByStructure {
			if hops, ok := hopOf[id]; ok {
				c.Reasons = append(c.Reasons, "图上 "+itoa(hops)+" 步可达")
			}
		}
		if c.ByMemory {
			r := "这类改动（" + in.Kind + "）以前涉及它"
			if memSource == "user-correction" {
				r += "（由你纠正过）"
			}
			c.Reasons = append(c.Reasons, r)
		}
		c.Reasons = compact(c.Reasons)
	}

	for _, c := range byID {
		res.Candidates = append(res.Candidates, *c)
	}
	sort.SliceStable(res.Candidates, func(i, j int) bool {
		if res.Candidates[i].Score != res.Candidates[j].Score {
			return res.Candidates[i].Score > res.Candidates[j].Score
		}
		return res.Candidates[i].Node.ID() < res.Candidates[j].Node.ID()
	})
	return res
}

// bfs 从起点扩散，记录每个节点的跳数。forward=true 沿 From→To（它依赖谁），
// forward=false 沿 To→From（谁依赖它）。
func bfs(g *graph.Graph, start graph.Node, hopOf *map[string]int, maxHops int, forward bool) {
	adj := map[string][]string{}
	for _, e := range g.Edges {
		if e.Confidence != graph.ConfHigh {
			continue
		}
		if forward {
			adj[e.From.ID()] = append(adj[e.From.ID()], e.To.ID())
		} else {
			adj[e.To.ID()] = append(adj[e.To.ID()], e.From.ID())
		}
	}
	type item struct {
		id   string
		hops int
	}
	queue := []item{{start.ID(), 0}}
	seen := map[string]bool{start.ID(): true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.hops >= maxHops {
			continue
		}
		for _, next := range adj[cur.id] {
			if seen[next] {
				continue
			}
			seen[next] = true
			h := cur.hops + 1
			if old, ok := (*hopOf)[next]; !ok || h < old {
				(*hopOf)[next] = h
			}
			queue = append(queue, item{next, h})
		}
	}
}

func nodeFromGraph(g *graph.Graph, id string) graph.Node {
	for _, n := range g.Nodes {
		if n.ID() == id {
			return n
		}
	}
	// 找不到（如外部文件节点）：从 ID 拆
	if i := strings.Index(id, "!"); i >= 0 {
		return graph.Node{File: id[:i], Sheet: id[i+1:]}
	}
	return graph.Node{Sheet: id}
}

func stemOf(s string) string {
	return strings.TrimSuffix(s, ".xlsx")
}

func compact(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
