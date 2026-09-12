// Package workspace 的注册表部分：记住用过哪些工作区、当前是哪个，
// 供界面切换（见 docs/agent-architecture/19-界面设计.md 阶段 2）。
//
// 工作区 = 一个文件夹（用户定的，一一对应）。注册表只记"路径 + 名字 + 时间"，
// 落成一个很小的 JSON 文件（放在应用数据目录，不写工作区里，免得污染用户的文件夹）。
package workspace

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

// Entry 是一个登记过的工作区。
type Entry struct {
	Path    string `json:"path"`    // 文件夹绝对路径
	Name    string `json:"name"`    // 显示名（默认取目录名）
	Tables  int    `json:"tables"`  // 上次已知的表数量（供列表展示）
	Opened  string `json:"opened"`  // 上次打开时间
}

// Registry 是工作区注册表（线程安全，落盘为 JSON）。
type Registry struct {
	mu      sync.Mutex
	path    string
	current string
	entries []Entry
}

// OpenRegistry 读取（不存在则创建）注册表文件。
func OpenRegistry(path string) (*Registry, error) {
	r := &Registry{path: path}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, r); err != nil {
			// 文件损坏不该让程序起不来：当作空表重建
			r.entries = nil
			r.current = ""
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取工作区注册表: %w", err)
	}
	return r, nil
}

// Current 返回当前工作区路径（可能为空）。
func (r *Registry) Current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// List 返回登记的工作区，最近打开的在前。
func (r *Registry) List() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]Entry(nil), r.entries...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Opened > out[j].Opened })
	return out
}

// Touch 记录/更新一个工作区并设为当前（打开或新建都会调用）。
func (r *Registry) Touch(dir string, tables int) (Entry, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Entry{}, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Entry{}, fmt.Errorf("目录不存在或不可用：%s", abs)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	e := Entry{
		Path:   abs,
		Name:   filepath.Base(abs),
		Tables: tables,
		Opened: time.Now().Format("2006-01-02 15:04"),
	}
	found := false
	for i := range r.entries {
		if samePath(r.entries[i].Path, abs) {
			r.entries[i] = e
			found = true
			break
		}
	}
	if !found {
		r.entries = append(r.entries, e)
	}
	r.current = abs
	if err := r.save(); err != nil {
		return e, err
	}
	return e, nil
}

// Forget 从注册表移除（不删磁盘上的文件夹——绝不删用户数据）。
func (r *Registry) Forget(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.entries[:0]
	for _, e := range r.entries {
		if !samePath(e.Path, dir) {
			out = append(out, e)
		}
	}
	r.entries = out
	if samePath(r.current, dir) {
		r.current = ""
	}
	return r.save()
}

// save 落盘（调用方已持锁）。
func (r *Registry) save() error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(struct {
		Current string  `json:"current"`
		Entries []Entry `json:"entries"`
	}{r.current, r.entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o644)
}

// SuggestDir 为"新建工作区"给一个默认落点：<base>/<名字>（清洗掉非法字符）。
func SuggestDir(base, name string) string {
	clean := sanitizeName(name)
	if clean == "" {
		clean = "新工作区"
	}
	return filepath.Join(base, clean)
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	// 去掉 Windows 文件名非法字符
	repl := strings.NewReplacer("\\", "", "/", "", ":", "", "*", "", "?", "",
		"\"", "", "<", "", ">", "", "|", "")
	return repl.Replace(s)
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
