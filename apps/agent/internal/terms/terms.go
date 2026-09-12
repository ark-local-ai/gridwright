// Package terms 是语义映射文件 term-map（见 docs/agent-architecture/24-语义映射与安全边界.md）。
//
// 作用：把用户嘴里的模糊词映射到明确的表/列/键/类别，让"那笔钱""老李那家"这种说法
// 第一次问清、以后复用——**澄清的代价是一次问答，产出永久复用**。
//
// 存成 YAML 放**工作区里**（术语跟着业务走，换工作区该换术语），人手可编辑。
// 优先级：**manual（人写的）永远压过 clarified（澄清得来）** —— 财务上人手写的是硬规则。
package terms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Target 是"这个词最终指向什么"。四选一（或组合）：
type Target struct {
	Sheet string            `yaml:"sheet,omitempty" json:"sheet,omitempty"` // 指向某工作表
	Field string            `yaml:"field,omitempty" json:"field,omitempty"` // 指向某字段列
	Key   map[string]string `yaml:"key,omitempty"   json:"key,omitempty"`   // 指向某个业务键（租户/铺位）
	Kind  string            `yaml:"kind,omitempty"  json:"kind,omitempty"`  // 指向语义类别（收租/售房/保证金）
}

// Empty 判断是否什么都没指向。
func (t Target) Empty() bool {
	return t.Sheet == "" && t.Field == "" && t.Kind == "" && len(t.Key) == 0
}

// Term 是一条映射。
type Term struct {
	Word     string `yaml:"word"                 json:"word"`
	Resolves Target `yaml:"resolves_to"          json:"resolves_to"`
	Note     string `yaml:"note,omitempty"       json:"note,omitempty"`
	// Source: manual（人手写，优先）| clarified（澄清得来）
	Source   string `yaml:"source"              json:"source"`
	Approved string `yaml:"approved,omitempty"  json:"approved,omitempty"`
	Hits     int    `yaml:"hits,omitempty"      json:"hits,omitempty"`
}

// ClarifyPrompt 是提问模板（可配，避免每次问法不一）。
type ClarifyPrompt struct {
	UnknownTerm     string `yaml:"unknown_term,omitempty"`
	AmbiguousSheet  string `yaml:"ambiguous_sheet,omitempty"`
	CategoryUnclear string `yaml:"category_unclear,omitempty"`
}

// File 是 term-map.yaml 的形状。
type File struct {
	Terms          []Term        `yaml:"terms"`
	ClarifyPrompts ClarifyPrompt `yaml:"clarify_prompts,omitempty"`
}

// Store 是某工作区的术语表。
type Store struct {
	mu   sync.Mutex
	path string
	f    File
}

// Open 打开（或创建）工作区的 term-map.yaml。
func Open(workspaceRoot string) (*Store, error) {
	p := filepath.Join(workspaceRoot, "term-map.yaml")
	s := &Store{path: p}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = yaml.Unmarshal(b, &s.f) // 损坏当空表，不阻塞
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 term-map: %w", err)
	}
	return s, nil
}

// Path 返回文件路径。
func (s *Store) Path() string { return s.path }

// All 返回全部映射。
func (s *Store) All() []Term {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Term, len(s.f.Terms))
	copy(out, s.f.Terms)
	return out
}

// Prompts 返回提问模板（带默认值）。
func (s *Store) Prompts() ClarifyPrompt {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.f.ClarifyPrompts
	if p.UnknownTerm == "" {
		p.UnknownTerm = "「{word}」指的是哪一项？"
	}
	if p.AmbiguousSheet == "" {
		p.AmbiguousSheet = "这笔要记到 {a} 还是 {b}？"
	}
	if p.CategoryUnclear == "" {
		p.CategoryUnclear = "这笔属于收租、售房，还是保证金？"
	}
	return p
}

// Lookup 查一个词（精确匹配优先，再做包含匹配）。
// **manual 优先**：同一词既有 manual 又有 clarified 时，返回 manual。
func (s *Store) Lookup(word string) (Term, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupLocked(word)
}

func (s *Store) lookupLocked(word string) (Term, bool) {
	w := strings.TrimSpace(word)
	if w == "" {
		return Term{}, false
	}
	var manualHit, clarifiedHit *Term
	// 精确
	for i := range s.f.Terms {
		t := &s.f.Terms[i]
		if t.Word != w {
			continue
		}
		if t.Source == "manual" && manualHit == nil {
			manualHit = t
		} else if clarifiedHit == nil {
			clarifiedHit = t
		}
	}
	if manualHit != nil {
		return *manualHit, true
	}
	if clarifiedHit != nil {
		return *clarifiedHit, true
	}
	// 包含匹配（"那笔 8 月的钱" 命中 "那笔钱" 之类）：取最长的词，避免短词误命中
	best := -1
	bestLen := 0
	for i := range s.f.Terms {
		t := &s.f.Terms[i]
		if t.Word == "" || len([]rune(t.Word)) < 2 {
			continue
		}
		if strings.Contains(w, t.Word) && len([]rune(t.Word)) > bestLen {
			best = i
			bestLen = len([]rune(t.Word))
		}
	}
	if best >= 0 {
		return s.f.Terms[best], true
	}
	return Term{}, false
}

// Resolve 在一段自由文本里找出所有命中的映射（供组装 prompt / 判定类别用）。
func (s *Store) Resolve(text string) []Term {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Term
	seen := map[string]bool{}
	for i := range s.f.Terms {
		t := &s.f.Terms[i]
		if t.Word == "" || len([]rune(t.Word)) < 2 {
			continue
		}
		if strings.Contains(text, t.Word) && !seen[t.Word] {
			seen[t.Word] = true
			// manual 优先：同词若有 manual 版本，用它
			if alt, ok := s.lookupLocked(t.Word); ok {
				out = append(out, alt)
			} else {
				out = append(out, *t)
			}
		}
	}
	return out
}

// Save 写入一条映射（manual=true 表示人手写）。
// 同词同来源则更新；manual 不会被 clarified 覆盖。
func (s *Store) Save(t Term) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Source == "" {
		t.Source = "clarified"
	}
	if t.Word == "" {
		return fmt.Errorf("缺少 word")
	}
	if t.Resolves.Empty() {
		return fmt.Errorf("「%s」没有指向任何表/字段/键/类别", t.Word)
	}
	for i := range s.f.Terms {
		if s.f.Terms[i].Word != t.Word {
			continue
		}
		// 已有 manual 时，clarified 不许覆盖（人写的更硬）
		if s.f.Terms[i].Source == "manual" && t.Source != "manual" {
			return fmt.Errorf("「%s」已有人工定义的映射，不被自动结果覆盖", t.Word)
		}
		s.f.Terms[i] = t
		return s.saveLocked()
	}
	s.f.Terms = append(s.f.Terms, t)
	return s.saveLocked()
}

// Bump 记一次命中（用于排序/淘汰）。
func (s *Store) Bump(word string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.f.Terms {
		if s.f.Terms[i].Word == word {
			s.f.Terms[i].Hits++
		}
	}
	_ = s.saveLocked()
}

// Delete 删掉一条（人手写的一般不该删，但保留能力）。
func (s *Store) Delete(word string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.f.Terms[:0]
	for _, t := range s.f.Terms {
		if t.Word != word {
			out = append(out, t)
		}
	}
	s.f.Terms = out
	return s.saveLocked()
}

// Describe 格式化成 prompt 片段（组装给脑时带上命中的映射）。
func Describe(ts []Term) string {
	if len(ts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# 术语（用户常这么说，按此理解）\n")
	for _, t := range ts {
		var to []string
		if t.Resolves.Sheet != "" {
			to = append(to, "表="+t.Resolves.Sheet)
		}
		if t.Resolves.Field != "" {
			to = append(to, "字段="+t.Resolves.Field)
		}
		if t.Resolves.Kind != "" {
			to = append(to, "类别="+t.Resolves.Kind)
		}
		for k, v := range t.Resolves.Key {
			to = append(to, k+"="+v)
		}
		line := fmt.Sprintf("  - 「%s」→ %s", t.Word, strings.Join(to, " "))
		if t.Note != "" {
			line += "（" + t.Note + "）"
		}
		if t.Source == "manual" {
			line += "［人工定义，必须遵守］"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (s *Store) saveLocked() error {
	b, err := yaml.Marshal(&s.f)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}
