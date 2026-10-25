package mcp

import "context"

// Runner 是对话层唯一需要知道的 MCP 能力面：有哪些工具，以及怎么调一个。
//
// 单独抽接口而不是让 agent 直接依赖 *Registry，是为了让对话循环可以在
// 没有子进程、没有 mcp.json 的情况下被测到（假实现只回一段文本）。
// *Registry 已满足它。
type Runner interface {
	// Catalog 返回全部可用工具，按 server + name 排序。
	Catalog() []Tool
	// Call 调用某个 server 上的工具，返回拼接后的文本结果。
	Call(ctx context.Context, server, tool string, args map[string]any) (string, error)
}

// 编译期确认：*Registry 就是 Runner。
var _ Runner = (*Registry)(nil)
