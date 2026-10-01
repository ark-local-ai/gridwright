// Package convo 是"会话"存储（见 docs/agent-architecture/7-对话与自动化任务.md、15-对话与提示词.md）。
//
// 定位：**不是聊天软件**，是"配置入口 + 澄清记录 + 决策留痕"。
// 用户说一句话 → 模型回话并附一个结构化 proposal（任务/规则/工具申请/澄清问题）
// → 用户在界面上确认后才生效。
//
// 存储：工作区里的 conversations.json（单机单人，量小；不引入 SQLite 依赖）。
// 按工作区隔离——每个工作区有自己的一份会话，跟着文件夹走。
package convo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Role 是消息角色。
const (
	RoleUser   = "user"
	RoleAgent  = "agent"
	RoleSystem = "system"
)

// Message 是一条消息。
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
	// Images 是这条消息附的图（data URL）。存下来才能回看当时给的是什么图——
	// 只送给模型不落库的话，翻会话时用户会看到一段没有上下文的回复。
	Images []string `json:"images,omitempty"`
	// 结构化产物：这条回复把用户的话变成了什么（任务/规则/工具申请/澄清问题）
	Proposal *Proposal `json:"proposal,omitempty"`
	Time     string    `json:"time"`
}

// Proposal 是结构化建议（机器可执行的那部分）。
type Proposal struct {
	Kind     string   `json:"kind"`               // task | rule | tool_request | clarify | plan
	Title    string   `json:"title"`              // 一句话给人看
	Detail   string   `json:"detail"`             // 展开说明
	Schedule string   `json:"schedule,omitempty"` // 任务：cron
	Action   string   `json:"action,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	// 澄清问询的选项（人点一个即可回答）
	Options []string `json:"options,omitempty"`
	// 待确认改动的清单 id（kind=plan 时，指向 propose.Store 里的清单）
	PlanID string `json:"planId,omitempty"`
	// 是否已被采纳（人点过确认）
	Accepted bool `json:"accepted,omitempty"`
}

// Conversation 是一条会话。
type Conversation struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Messages []Message `json:"messages"`
	Created  string    `json:"created"`
	Updated  string    `json:"updated"`
}

// Store 是某工作区的会话存储（落盘 conversations.json）。
type Store struct {
	mu    sync.Mutex
	path  string
	items []Conversation
}

// Open 打开（或创建）某工作区里的会话存储。
func Open(workspaceRoot string) (*Store, error) {
	p := filepath.Join(workspaceRoot, "conversations.json")
	s := &Store{path: p}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(b, &s.items) // 损坏则当空表，不阻塞启动
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取会话: %w", err)
	}
	return s, nil
}

// List 返回会话（最近更新在前），不含消息正文（列表页只需要标题）。
func (s *Store) List() []Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Conversation, 0, len(s.items))
	for _, c := range s.items {
		out = append(out, Conversation{ID: c.ID, Title: c.Title, Created: c.Created, Updated: c.Updated})
	}
	// 最近更新在前
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Updated > out[i].Updated {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Get 取一条会话（含消息）。
func (s *Store) Get(id string) *Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			c := s.items[i]
			return &c
		}
	}
	return nil
}

// Ensure 取一条会话；不存在则按 title 新建。
func (s *Store) Ensure(id, title string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		for i := range s.items {
			if s.items[i].ID == id {
				return &s.items[i], nil
			}
		}
	}
	now := time.Now().Format("2006-01-02 15:04")
	c := Conversation{ID: newID(), Title: title, Created: now, Updated: now}
	if c.Title == "" {
		c.Title = "新对话"
	}
	s.items = append(s.items, c)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return &s.items[len(s.items)-1], nil
}

// Append 往会话里追加一条消息。
func (s *Store) Append(id string, m Message) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		if m.Time == "" {
			m.Time = time.Now().Format("2006-01-02 15:04")
		}
		s.items[i].Messages = append(s.items[i].Messages, m)
		s.items[i].Updated = m.Time
		// 首句用户话当标题
		if s.items[i].Title == "" || s.items[i].Title == "新对话" {
			if m.Role == RoleUser && m.Text != "" {
				s.items[i].Title = firstLine(m.Text, 20)
			}
		}
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
		c := s.items[i]
		return &c, nil
	}
	return nil, fmt.Errorf("会话不存在：%s", id)
}

// Accept 把某条消息上的提案标成"已采纳"并落盘。
//
// 为什么要落盘：采纳是一个决定，重启后要还在——否则界面上又会让人再点一次。
// 返回被采纳的提案，调用方据此决定额外动作（task 要落成定时任务）。
func (s *Store) Accept(id string, index int) (*Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		if index < 0 || index >= len(s.items[i].Messages) {
			return nil, fmt.Errorf("消息序号越界：%d", index)
		}
		p := s.items[i].Messages[index].Proposal
		if p == nil {
			return nil, fmt.Errorf("这条消息上没有可采纳的提案")
		}
		p.Accepted = true
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
		return p, nil
	}
	return nil, fmt.Errorf("会话不存在：%s", id)
}

// Delete 删除一条会话。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.items[:0]
	for _, c := range s.items {
		if c.ID != id {
			out = append(out, c)
		}
	}
	s.items = out
	return s.saveLocked()
}

// Rename 改一条会话的标题。
//
// 为什么需要：标题默认取首句前 20 字，而首句未必说得清这件事是什么
// （"帮我看下这个" 这种开场很常见）。记录要能翻回来，就得能起个记得住的名字。
// 空标题不接受——那会让列表里出现一条无名的记录。
func (s *Store) Rename(id, title string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("标题不能为空")
	}
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60])
	}
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Title = title
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			c := s.items[i]
			return &c, nil
		}
	}
	return nil, fmt.Errorf("会话不存在：%s", id)
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

var seq uint64

func newID() string {
	seq++
	return fmt.Sprintf("c%d-%d", time.Now().UnixNano(), seq)
}

func firstLine(s string, n int) string {
	r := []rune(s)
	for i, ch := range r {
		if ch == '\n' {
			r = r[:i]
			break
		}
	}
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
