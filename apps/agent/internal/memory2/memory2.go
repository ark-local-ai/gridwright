// Package memory2 是"记忆"层（见 docs/agent-architecture/21-记忆设计.md）。
//
// 与 internal/memory（rules.yaml + state.yaml 的读取）不同，本包管的是**结构化记忆**：
// 把"这张表是什么、为什么这样改"沉淀下来，按当前任务检索出相关片段喂给脑。
//
// 设计要点（关键决定）：
//   - **数字不压缩**。事实记忆从账目/表精确算出，不做"约 1.9 万"这种有损摘要——
//     财务表上四舍五入会逐月累积成错。
//   - **分层**：事实（算出来的）/ 规则（人写的）/ 决策（人拍板的）/ 叙事（LLM 压缩的）。
//     只有叙事适合压缩；其余必须精确。
//   - **人来批准**：LLM 只能"建议"一条记忆，落盘要人点确认（与工具权限同一模式）。
//   - **会过期**：带 stale 机制，合同/规则变了要能标出来，否则记忆越用越错。
package memory2

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fact 是"事实记忆"：从表/账目算出来的、相对稳定的事实。
type Fact struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`  // rent | tenant | contract | ...
	Key        map[string]string `json:"key"`   // 业务键（如 铺位: B31）
	Value      string            `json:"value"` // 精确值（字符串，避免浮点误差）
	Unit       string            `json:"unit,omitempty"`
	Source     string            `json:"source"`    // 出处（如 商铺台账（详）!B45）
	Observed   string            `json:"observed,omitempty"` // 观察依据（如 "连续6个月一致"）
	Confidence string            `json:"confidence"` // high | medium
	Updated    string            `json:"updated"`
}

// Decision 是"决策记忆"：人拍板过的判断（为什么这样记）。
type Decision struct {
	ID       string            `json:"id"`
	Key      map[string]string `json:"key,omitempty"`
	Text     string            `json:"text"`   // 如 "按 455 计入（含运费，不含税）"
	Source   string            `json:"source"` // 如 conversation:c123
	Approved string            `json:"approved"`
	By       string            `json:"by"` // user | agent
}

// Stale 是"可能过时"的标记——让记忆能自我纠错。
type Stale struct {
	ID    string `json:"id"`
	Note  string `json:"note"`
	Found string `json:"found"`
}

// File 是 memory.json 的形状。
type File struct {
	Facts     []Fact     `json:"facts"`
	Decisions []Decision `json:"decisions"`
	Stale     []Stale    `json:"stale"`
}

// Store 是某工作区的记忆存储。
type Store struct {
	mu   sync.Mutex
	path string
	f    File
}

// Open 打开（或创建）某工作区的记忆文件。
func Open(workspaceRoot string) (*Store, error) {
	p := filepath.Join(workspaceRoot, "memory.json")
	s := &Store{path: p}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &s.f) // 损坏当空，不阻塞
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取记忆: %w", err)
	}
	return s, nil
}

// Path 返回记忆文件路径。
func (s *Store) Path() string { return s.path }

// All 返回全部记忆（只读快照）。
func (s *Store) All() File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f
}

// Counts 汇总各类条数。
func (s *Store) Counts() (facts, decisions, stale int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.f.Facts), len(s.f.Decisions), len(s.f.Stale)
}

// PutFact 写入/更新一条事实（同 ID 覆盖）。
func (s *Store) PutFact(f Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Updated == "" {
		f.Updated = time.Now().Format("2006-01-02")
	}
	if f.Confidence == "" {
		f.Confidence = "high"
	}
	for i := range s.f.Facts {
		if s.f.Facts[i].ID == f.ID {
			s.f.Facts[i] = f
			return s.saveLocked()
		}
	}
	s.f.Facts = append(s.f.Facts, f)
	return s.saveLocked()
}

// PutDecision 写入/更新一条决策记忆（人去批准后调用）。
func (s *Store) PutDecision(d Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.Approved == "" {
		d.Approved = time.Now().Format("2006-01-02")
	}
	for i := range s.f.Decisions {
		if s.f.Decisions[i].ID == d.ID {
			s.f.Decisions[i] = d
			return s.saveLocked()
		}
	}
	s.f.Decisions = append(s.f.Decisions, d)
	return s.saveLocked()
}

// MarkStale 标记一条记忆可能过时。
func (s *Store) MarkStale(st Stale) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Found = time.Now().Format("2006-01-02")
	for i := range s.f.Stale {
		if s.f.Stale[i].ID == st.ID {
			s.f.Stale[i] = st
			return s.saveLocked()
		}
	}
	s.f.Stale = append(s.f.Stale, st)
	return s.saveLocked()
}

// RemoveStale 清掉过时标记（人已处理）。
func (s *Store) RemoveStale(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.f.Stale[:0]
	for _, st := range s.f.Stale {
		if st.ID != id {
			out = append(out, st)
		}
	}
	s.f.Stale = out
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// Retrieve 按当前任务检索相关记忆（**只给相关的**，不是全塞进 prompt）。
//
// 匹配规则：
//   - 键值命中（如铺位 B31）→ 相关
//   - 字段/表名出现在 value 或 key 里 → 相关
//   - 没有任何条件时返回空（宁可不给，也不给噪音）
func (s *Store) Retrieve(sheet string, keys map[string]string, field string) File {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := File{}
	need := func(kv map[string]string, text string) bool {
		// 键命中
		for k, v := range keys {
			if v == "" {
				continue
			}
			if got, ok := kv[k]; ok && strings.EqualFold(got, v) {
				return true
			}
			if strings.Contains(text, v) {
				return true
			}
		}
		// 表/字段命中
		if sheet != "" && strings.Contains(text, sheet) {
			return true
		}
		if field != "" && strings.Contains(text, field) {
			return true
		}
		return false
	}
	for _, f := range s.f.Facts {
		if need(f.Key, f.Value+" "+f.Kind) {
			out.Facts = append(out.Facts, f)
		}
	}
	for _, d := range s.f.Decisions {
		if need(d.Key, d.Text) {
			out.Decisions = append(out.Decisions, d)
		}
	}
	for _, st := range s.f.Stale {
		out.Stale = append(out.Stale, st)
	}
	return out
}

// Describe 把检索到的记忆格式化成 prompt 片段（供 agent 组装第 ⑩ 段）。
func (f File) Describe() string {
	if len(f.Facts) == 0 && len(f.Decisions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# 相关记忆（来自本工作区以往的事实与决定，供参考）\n")
	if len(f.Facts) > 0 {
		b.WriteString("已知事实：\n")
		fs := append([]Fact(nil), f.Facts...)
		sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
		for _, x := range fs {
			kv := joinKey(x.Key)
			fmt.Fprintf(&b, "  - %s：%s%s（出处 %s）\n", kv, x.Value, unit(x.Unit), x.Source)
		}
	}
	if len(f.Decisions) > 0 {
		b.WriteString("以往决定（按此处理，除非用户另有说明）：\n")
		for _, d := range f.Decisions {
			kv := joinKey(d.Key)
			if kv != "" {
				fmt.Fprintf(&b, "  - %s：%s\n", kv, d.Text)
			} else {
				fmt.Fprintf(&b, "  - %s\n", d.Text)
			}
		}
	}
	if len(f.Stale) > 0 {
		b.WriteString("注意：以下记忆可能已过时，请以现状为准并向用户确认：\n")
		for _, st := range f.Stale {
			fmt.Fprintf(&b, "  - %s\n", st.Note)
		}
	}
	return b.String()
}

func joinKey(kv map[string]string) string {
	if len(kv) == 0 {
		return "全局"
	}
	ks := make([]string, 0, len(kv))
	for k := range kv {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, 0, len(ks))
	for _, k := range ks {
		parts = append(parts, kv[k])
	}
	return strings.Join(parts, " · ")
}

func unit(u string) string {
	if u == "" {
		return ""
	}
	return " " + u
}
// RetrieveByText 按一段自由文本（如用户的指令/新数据）检索相关记忆：
// 只要某条记忆的键值或内容里出现了文本中提到的词，就算相关。
//
// 这是规划/对话时的主入口——用户说"B31 收 8 月租金"，就只带出 B31 相关的事实与决定。
func (s *Store) RetrieveByText(text string) File {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := File{}
	if strings.TrimSpace(text) == "" {
		return out
	}
	hit := func(kv map[string]string, blob string) bool {
		// 键值命中（如 铺位=B31 出现在文本里）
		for _, v := range kv {
			if v != "" && strings.Contains(text, v) {
				return true
			}
		}
		// 内容命中：把记忆文字拆成词，看是否出现在文本里（长度>=2 才判，避免误命中）
		for _, w := range splitWords(blob) {
			if len([]rune(w)) >= 2 && strings.Contains(text, w) {
				return true
			}
		}
		return false
	}
	for _, f := range s.f.Facts {
		if hit(f.Key, f.Value+" "+f.Kind) {
			out.Facts = append(out.Facts, f)
		}
	}
	for _, d := range s.f.Decisions {
		if hit(d.Key, d.Text) {
			out.Decisions = append(out.Decisions, d)
		}
	}
	// 过时提示始终带上（它本来就是提醒"别照旧用"）
	out.Stale = append(out.Stale, s.f.Stale...)
	return out
}

func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		if r == ' ' || r == '\t' || r == '\n' {
			return true
		}
		// 中英文常见分隔符
		return strings.ContainsRune("，,。.；;：:（）()/、|", r)
	})
}
