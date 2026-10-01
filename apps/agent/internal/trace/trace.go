// Package trace 记录「一次运行里每一步做了什么」，随 /chat、/plan 的响应直接回给界面。
//
// 为什么单独做这一层：engine.log 只有两种记录——HTTP 请求行，
// 以及一行 "[llm] ok（29.6s，22304B）"。中间那层（读了哪些表、扫了联动图、
// 发给模型什么、把回复解析成了什么、定位到多少格）**完全没有**，
// 于是出问题时用户根本看不出「思考到了哪一步、哪一步调用了什么」。
//
// 这里把步骤收进一个结构化对象。它只服务于界面：日志仍走 slog，
// 两者互不影响。传法用 context，是因为 Plan/Chat 的签名不想为一个
// 诊断用的东西改动（调用点很多）。
package trace

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Step 是一条步骤记录。
type Step struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	MS     int64  `json:"ms"`
}

// Trace 是一次运行的步骤账。字段都带 json tag，直接进 API 响应。
type Trace struct {
	mu     sync.Mutex
	RunID  string `json:"runId"`
	Steps  []Step `json:"steps"`
	Prompt string `json:"prompt,omitempty"` // 发给模型的提示（纯文本；图不在其中）
	Raw    string `json:"raw,omitempty"`    // 模型原样返回的正文
}

// 提示/正文的保留上限：够看清「问了什么、答了什么」，又不会把响应撑爆。
const maxBody = 30000

type ctxKey struct{}

// New 在一次运行开始时建一个 Trace 并塞进 context。
// kind 只用于生成可读的 run id（如 plan-17...）。
func New(ctx context.Context, kind string) (context.Context, *Trace) {
	t := &Trace{RunID: fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())}
	return context.WithValue(ctx, ctxKey{}, t), t
}

// From 取当前 context 里的 Trace；没有则为 nil（调用方无需判空）。
func From(ctx context.Context) *Trace {
	t, _ := ctx.Value(ctxKey{}).(*Trace)
	return t
}

// Step 计时一段并返回「收尾」函数：defer/调用时把耗时与说明记进去。
//
//	t := trace.From(ctx)
//	done := t.Step("读表结构")
//	... 干活 ...
//	done("12 张工作表")
//
// t 为 nil 时全部安全跳过——这样没接 trace 的路径（inbox 自动链路等）不用改。
func (t *Trace) Step(name string) func(detail string) {
	start := time.Now()
	return func(detail string) {
		if t == nil {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		t.Steps = append(t.Steps, Step{Name: name, Detail: detail, MS: time.Since(start).Milliseconds()})
	}
}

// Add 记一条不带耗时的步骤（用于「明确发生了某件事」的注记）。
func (t *Trace) Add(name, detail string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Steps = append(t.Steps, Step{Name: name, Detail: detail})
}

// SetPrompt 记下发给模型的提示（纯文本）。
func (t *Trace) SetPrompt(s string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Prompt = clip(s)
}

// SetRaw 记下模型原样返回的正文。
func (t *Trace) SetRaw(s string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Raw = clip(s)
}

func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxBody {
		return s
	}
	return string(r[:maxBody]) + "\n…（已截断，完整内容见工作区 conversations.json 或 engine.log 的 debug 级）"
}
