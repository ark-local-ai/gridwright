package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 收件箱的可观测状态（见 internal/api/inbox.go）。
//
// 为什么失败原因要单独落文件：RunInboxFile 的失败原因过去只进 engine.log，
// 而日志是给排障的人看的、会被轮转，界面拿不到。收件箱面板要回答的是
// "这个文件为什么还躺在这里"，所以必须有一条结构化、可读、不会被日志轮转带走的记录。
//
// 落点：工作区根的 inbox_failures.json（key = 文件名）。这是工作区状态的一部分，
// 跟着文件夹走，换机器也能解释"上次为什么没处理"。
const inboxFailureFile = "inbox_failures.json"

// InboxFailure 是一个文件最近一次失败。
type InboxFailure struct {
	At       string `json:"at"`
	Stage    string `json:"stage,omitempty"`
	Message  string `json:"message"`
	Attempts int    `json:"attempts"`
}

type inboxFailDoc struct {
	Version int                     `json:"version"`
	Items   map[string]InboxFailure `json:"items"`
}

// inboxFailMu 保护文件读写：API 的列表请求与后台处理可能并发。
// 用包级锁而不是 Agent 字段：Agent 在切换工作区时会被重建，锁必须比它活得久。
var inboxFailMu sync.Mutex

func inboxFailPath(root string) string { return filepath.Join(root, inboxFailureFile) }

func loadInboxFailures(root string) map[string]InboxFailure {
	inboxFailMu.Lock()
	defer inboxFailMu.Unlock()
	return loadInboxFailuresLocked(root)
}

func loadInboxFailuresLocked(root string) map[string]InboxFailure {
	items := map[string]InboxFailure{}
	b, err := os.ReadFile(inboxFailPath(root))
	if err != nil {
		return items
	}
	var doc inboxFailDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return items // 坏了就当没有：它只是提示，不影响处理
	}
	for k, v := range doc.Items {
		items[k] = v
	}
	return items
}

func saveInboxFailuresLocked(root string, items map[string]InboxFailure) error {
	if len(items) == 0 {
		err := os.Remove(inboxFailPath(root))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	doc := inboxFailDoc{Version: 1, Items: items}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	path := inboxFailPath(root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// recordFailure 记下一次失败；同一文件多次失败累加 attempts。
// 失败细节仍会进日志（见 RunInboxFiles），这里存的是界面要显示的那部分。
func (a *Agent) recordFailure(name, stage string, cause error) {
	if a == nil || a.Layout == nil || a.Layout.Root == "" || cause == nil {
		return
	}
	name = filepath.Base(name)
	inboxFailMu.Lock()
	defer inboxFailMu.Unlock()
	items := loadInboxFailuresLocked(a.Layout.Root)
	prev := items[name]
	items[name] = InboxFailure{
		At:       time.Now().Format("2006-01-02 15:04:05"),
		Stage:    stage,
		Message:  cause.Error(),
		Attempts: prev.Attempts + 1,
	}
	if err := saveInboxFailuresLocked(a.Layout.Root, items); err != nil {
		// 记录失败本身不能影响处理流程。
		return
	}
}

// clearFailure 在文件处理成功/被跳过后清除失败记录。
func (a *Agent) clearFailure(name string) {
	if a == nil || a.Layout == nil || a.Layout.Root == "" {
		return
	}
	name = filepath.Base(name)
	inboxFailMu.Lock()
	defer inboxFailMu.Unlock()
	items := loadInboxFailuresLocked(a.Layout.Root)
	if _, ok := items[name]; !ok {
		return
	}
	delete(items, name)
	_ = saveInboxFailuresLocked(a.Layout.Root, items)
}

// InboxItem 是收件箱面板的一行。Status 取值：
//   - waiting：等待处理（含超时未清的旧锁，下一轮会重试）
//   - processing：有未超时的认领锁
//   - failed：上次处理失败，文件仍在 inbox
//
// "已完成"不在这个列表里：成功即移入 inbox/done，由现有 files 接口展示。
type InboxItem struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Modified   string `json:"modified"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorAt    string `json:"errorAt,omitempty"`
	ClaimedAt  string `json:"claimedAt,omitempty"`
	ClaimStale bool   `json:"claimStale,omitempty"`
}

// InboxItems 列出 inbox 待处理文件及其状态。
func (a *Agent) InboxItems() ([]InboxItem, error) {
	if a == nil || a.Layout == nil {
		return nil, fmt.Errorf("还没有工作区")
	}
	files, err := a.Layout.InboxFiles()
	if err != nil {
		return nil, err
	}
	failures := loadInboxFailures(a.Layout.Root)
	out := make([]InboxItem, 0, len(files))
	for _, p := range files {
		name := filepath.Base(p)
		it := InboxItem{Name: name, Status: "waiting"}
		if fi, err := os.Stat(p); err == nil {
			it.Size = fi.Size()
			it.Modified = fi.ModTime().Format("2006-01-02 15:04")
		}
		// 认领锁：与 RunInboxFiles 用同一后缀与同一 10 分钟阈值。
		if fi, err := os.Stat(p + claimSuffix); err == nil {
			age := time.Since(fi.ModTime())
			it.ClaimedAt = fi.ModTime().Format("2006-01-02 15:04:05")
			if age < 10*time.Minute {
				it.Status = "processing"
			} else {
				it.ClaimStale = true
			}
		}
		if f, ok := failures[name]; ok && it.Status != "processing" {
			it.Status = "failed"
			it.Attempts = f.Attempts
			it.Error = f.Message
			it.ErrorAt = f.At
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ProcessInboxFile 认领并处理单个文件（界面上的「现在处理」）。
// 已被别的运行认领时返回 claimed=false、error=nil——这不是错误，界面应显示"处理中"。
func (a *Agent) ProcessInboxFile(ctx context.Context, name string) (claimed bool, err error) {
	if a == nil || a.Layout == nil {
		return false, fmt.Errorf("还没有工作区")
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return false, fmt.Errorf("文件名不合法")
	}
	src := filepath.Join(a.Layout.Inbox, name)
	got, err := claim(src)
	if err != nil {
		return false, err
	}
	if got == "" {
		return false, nil
	}
	defer func() {
		// 成功时文件已移入 done，锁也应删掉；失败时释放以便下一轮重试。
		_ = release(got)
	}()
	if err := a.RunInboxFile(ctx, got); err != nil {
		a.recordFailure(name, "process", err)
		return true, err
	}
	a.clearFailure(name)
	return true, nil
}

// SkipInboxFile 人工跳过：移到 inbox/skipped，**不是 done**（它没有被处理）。
// 先认领再移动，避免与后台处理并发抢同一个文件。
func (a *Agent) SkipInboxFile(name, reason string) error {
	if a == nil || a.Layout == nil {
		return fmt.Errorf("还没有工作区")
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("缺少文件名")
	}
	src := filepath.Join(a.Layout.Inbox, name)
	got, err := claim(src)
	if err != nil {
		return err
	}
	if got == "" {
		return fmt.Errorf("这个文件正在处理中，稍后再试")
	}
	defer func() { _ = release(got) }()
	if err := a.Layout.MoveToSkipped(src, reason); err != nil {
		return err
	}
	a.clearFailure(name)
	return nil
}
