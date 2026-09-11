// Package plan 定义 LLM 与执行器之间的结构化契约（spec §4 输出契约）。
// 独立成包，让 llm（产出）与 xl（执行）互不依赖。
package plan

// Edit 是一条结构化编辑指令（LLM 返回的最小操作单位）。
type Edit struct {
	Op     string         `json:"op"` // set | append
	Sheet  string         `json:"sheet"`
	Cell   string         `json:"cell,omitempty"` // set 用：A1 形式坐标
	Value  any            `json:"value,omitempty"` // set 用：新值
	Row    map[string]any `json:"row,omitempty"`  // append 用：表头列名 → 值
	Reason string         `json:"reason,omitempty"`
}

// Plan 是 LLM 的完整结构化输出。
type Plan struct {
	Edits       []Edit   `json:"edits"`
	Summary     string   `json:"summary"`
	SkipReasons []string `json:"skip_reasons"`
}

// Result 是执行一条 Edit 的结果。
type Result struct {
	Status string // ok | rejected
	Old    any    // set 的旧值（写入 ledger.old，供回滚）
	Note   string // 被拒原因 / 备注
}
