// Package mcp 实现一个最小的 MCP（Model Context Protocol）stdio 客户端与工具注册表。
//
// 为什么自己写而不是用官方 go-sdk：官方 go-sdk 要求 Go 1.23，而本仓库的语言基线是
// 1.21（最后一个支持 Windows 7 的 Go 线）。这个包只用标准库，协议面也只覆盖
// gridwright 真正需要的三条：initialize、tools/list、tools/call。
package mcp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion 是握手时上报的 MCP 协议版本。
const ProtocolVersion = "2024-11-05"

// clientName 是握手时上报的客户端名，便于服务端日志识别。
const clientName = "gridwright"

// clientVersion 是握手时上报的客户端版本。
const clientVersion = "0.1.0"

// maxLineBytes 是 stdout 单行上限。工具返回值可能很大，默认 64KB 的 scanner 不够。
const maxLineBytes = 4 << 20

// maxToolPages 是 tools/list 分页的护栏，防止服务端用游标把我们拖进死循环。
const maxToolPages = 50

// rpcRequest 是一条 JSON-RPC 2.0 请求或通知。通知不带 ID。
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse 是一条 JSON-RPC 2.0 响应。ID 用 RawMessage 保留原样，
// 这样无论服务端回数字还是字符串都能对上。
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// rpcError 是 JSON-RPC 2.0 的错误对象。
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "mcp: <nil> 错误"
	}
	return fmt.Sprintf("mcp: JSON-RPC 错误 %d: %s", e.Code, e.Message)
}

// contentItem 是 tools/call 结果里的一条内容块。
type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolDef 是 tools/list 返回的单个工具定义（不含 server 归属）。
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// listToolsResult 是 tools/list 的单页响应。
type listToolsResult struct {
	Tools      []toolDef `json:"tools"`
	NextCursor string    `json:"nextCursor"`
}

// callToolResult 是 tools/call 的响应体。
type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError"`
}
