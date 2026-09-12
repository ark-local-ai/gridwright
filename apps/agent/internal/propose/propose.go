// Package propose 是"待改清单"模型（见 docs/agent-architecture/19-界面设计.md 阶段 4）。
//
// 用户的规则：**看清单 → 你确认 → 才改**。所以改动分两步：
//
//	Plan  ：算出"要改哪些格、旧值新值、会牵动谁"，**不落盘**，产出 Proposal。
//	Apply ：用户确认后，按 Proposal 执行：备份 → 写 → 记账。
//
// Proposal 存在服务端（带文件指纹与过期），Apply 只认 ID。
// 这样用户确认的是**服务端算出来的那份**，客户端无法篡改；
// 且文件在确认期间被别人改过会被指纹拦下，不会盲改。
package propose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Item 是清单里的一条改动。
type Item struct {
	// 定位结果（由 locate 算出）
	File  string `json:"file"`  // 文件名
	Sheet string `json:"sheet"` // 工作表
	Ref   string `json:"ref"`   // A1 坐标
	Row   int    `json:"row"`
	Col   int    `json:"col"`

	// 语义坐标（人能看懂的部分，界面按这个展示）
	Key   map[string]string `json:"key,omitempty"` // 铺位/租户 等
	Month string            `json:"month,omitempty"`
	Field string            `json:"field"` // 字段列名

	Op     string `json:"op"`  // set | add
	Old    any    `json:"old"` // 旧值（算出来的）
	New    any    `json:"new"` // 新值（将写入）
	Reason string `json:"reason,omitempty"`
	Source string `json:"source,omitempty"` // 这条来自哪份新数据 / 哪句指令

	// 联动：改这一格会牵动哪些 文件!工作表（来自联动图）
	Affects []string `json:"affects,omitempty"`
}

// Proposal 是一份待确认的清单。
type Proposal struct {
	ID      string `json:"id"`
	Root    string `json:"root"`    // 工作区根
	Target  string `json:"target"`  // 目标表绝对路径
	Finger  string `json:"finger"`  // 目标表指纹（Apply 前校验，防盲改）
	Summary string `json:"summary"` // 一句话说明
	Items   []Item `json:"items"`
	// 被挡下的条目（命中 forbid / 定位失败等），要让人看见，不静默
	Blocked []Blocked `json:"blocked,omitempty"`
	Created time.Time `json:"created"`
}

// Blocked 是一条"想改但没让改"的记录。
type Blocked struct {
	Sheet  string `json:"sheet,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

// Store 暂存待确认清单（内存，带过期）。
type Store struct {
	mu    sync.Mutex
	items map[string]*Proposal
	ttl   time.Duration
}

// NewStore 建一个暂存区。ttl<=0 用默认 30 分钟。
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &Store{items: map[string]*Proposal{}, ttl: ttl}
}

// Put 存一份清单，返回其 ID。
func (s *Store) Put(p *Proposal) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	p.ID = newID()
	p.Created = time.Now()
	s.items[p.ID] = p
	return p.ID
}

// Get 取一份清单（不存在或已过期返回 nil）。
func (s *Store) Get(id string) *Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	p, ok := s.items[id]
	if !ok {
		return nil
	}
	if time.Since(p.Created) > s.ttl {
		delete(s.items, id)
		return nil
	}
	return p
}

// Drop 删除一份清单（Apply 成功后调用，避免重复应用）。
func (s *Store) Drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
}

// gcLocked 清理过期项（调用方已持锁）。
func (s *Store) gcLocked() {
	now := time.Now()
	for id, p := range s.items {
		if now.Sub(p.Created) > s.ttl {
			delete(s.items, id)
		}
	}
}

var idSeq uint64

func newID() string {
	idSeq++
	return fmt.Sprintf("p%d-%d", time.Now().UnixNano(), idSeq)
}

// Fingerprint 计算文件指纹，用于 Apply 前确认"文件没被换过"。
func Fingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Changed 判断文件是否与记录的指纹不一致（不一致=期间被人改过，不该盲改）。
func Changed(path, finger string) bool {
	cur, err := Fingerprint(path)
	if err != nil {
		return true
	}
	return cur != finger
}
