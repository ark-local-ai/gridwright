package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer 是一个跑在 net.Pipe 另一端的假 MCP server。
// 它按行读 JSON-RPC，按 ID 回响应；通知只记录不回复。
type fakeServer struct {
	handler func(method string, params json.RawMessage) (any, *rpcError)

	mu      sync.Mutex
	methods []string
	params  []json.RawMessage
}

func (f *fakeServer) record(method string, params json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, method)
	f.params = append(f.params, params)
}

// recorded 返回已收到的调用快照（带锁拷贝，避免与读协程竞争）。
func (f *fakeServer) recorded() ([]string, []json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	methods := append([]string(nil), f.methods...)
	params := append([]json.RawMessage(nil), f.params...)
	return methods, params
}

func (f *fakeServer) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.methods {
		if m == method {
			return true
		}
	}
	return false
}

// startFakeServer 建一对 net.Pipe，服务端 goroutine 用 handler 应答，
// 返回客户端的 client（已完成握手）。t.Cleanup 负责收尾。
func startFakeServer(t *testing.T, handler func(method string, params json.RawMessage) (any, *rpcError)) (*client, *fakeServer) {
	t.Helper()
	fs := &fakeServer{handler: handler}
	cliConn, srvConn := net.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		sc := bufio.NewScanner(srvConn)
		sc.Buffer(make([]byte, 0, 4096), maxLineBytes)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			fs.record(msg.Method, msg.Params)
			if len(msg.ID) == 0 || string(msg.ID) == "null" {
				continue // 通知：不回包
			}
			result, rerr := fs.handler(msg.Method, msg.Params)
			if _, quiet := result.(noReply); quiet {
				continue // 故意不回包，用于超时用例
			}
			var out map[string]any
			if rerr != nil {
				out = map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": rerr}
			} else {
				out = map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result}
			}
			payload, err := json.Marshal(out)
			if err != nil {
				return
			}
			payload = append(payload, '\n')
			// 客户端可能已关闭连接（例如 Close 用例），别让写入永久阻塞。
			_ = srvConn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, err := srvConn.Write(payload); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := newClient(ctx, cliConn, "fake")
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	t.Cleanup(func() {
		_ = c.Close()
		_ = srvConn.Close()
		_ = cliConn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("假 server goroutine 未退出")
		}
	})
	return c, fs
}

// noReply 是 handler 的哨兵返回值：表示「收到但故意不回包」。
type noReply struct{}

// defaultHandler 覆盖 initialize / tools/list / tools/call 的正常路径。
func defaultHandler(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake", "version": "0"},
		}, nil
	case "tools/list":
		return map[string]any{"tools": []map[string]any{
			{"name": "echo", "description": "回显", "inputSchema": map[string]any{"type": "object"}},
		}}, nil
	case "tools/call":
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
}

func TestNewClientHandshake(t *testing.T) {
	c, fs := startFakeServer(t, defaultHandler)
	if c == nil {
		t.Fatal("client 为 nil")
	}
	// 再发一条请求：写在同一根管道上，服务端顺序处理，
	// 等它回包就说明 initialized 通知已经被读到（否则断言会和读协程抢跑）。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.listTools(ctx); err != nil {
		t.Fatalf("listTools: %v", err)
	}
	if !fs.called("initialize") {
		t.Fatal("未发送 initialize")
	}
	if !fs.called("notifications/initialized") {
		t.Fatal("未发送 notifications/initialized")
	}

	// 校验 initialize 参数：协议版本与客户端名是服务端识别我们的依据。
	var initParams struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	methods, params := fs.recorded()
	for i, m := range methods {
		if m == "initialize" {
			if err := json.Unmarshal(params[i], &initParams); err != nil {
				t.Fatalf("解析 initialize 参数失败: %v", err)
			}
		}
	}
	if initParams.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, 想要 2024-11-05", initParams.ProtocolVersion)
	}
	if initParams.ClientInfo.Name != "gridwright" {
		t.Errorf("clientInfo.name = %q, 想要 gridwright", initParams.ClientInfo.Name)
	}
}

func TestListToolsPagination(t *testing.T) {
	handler := func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/list" {
			return defaultHandler(method, params)
		}
		var p struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(params, &p)
		switch p.Cursor {
		case "":
			return map[string]any{
				"tools": []map[string]any{
					{"name": "b", "description": "第二", "inputSchema": map[string]any{"type": "object"}},
					{"name": "a", "description": "第一", "inputSchema": map[string]any{"type": "object"}},
				},
				"nextCursor": "page2",
			}, nil
		case "page2":
			return map[string]any{
				"tools": []map[string]any{
					{"name": "c", "description": "第三", "inputSchema": map[string]any{"type": "object"}},
				},
			}, nil
		}
		return nil, &rpcError{Code: -32602, Message: "bad cursor: " + p.Cursor}
	}
	c, _ := startFakeServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := c.listTools(ctx)
	if err != nil {
		t.Fatalf("listTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("工具数 = %d, 想要 3（分页应被跟随）", len(tools))
	}
	names := []string{tools[0].Name, tools[1].Name, tools[2].Name}
	if strings.Join(names, ",") != "b,a,c" {
		t.Errorf("顺序 = %v, 想要 [b a c]", names)
	}
	if tools[0].Description != "第二" || string(tools[0].InputSchema) != `{"type":"object"}` {
		t.Errorf("工具元数据未解析: %+v", tools[0])
	}
}

func TestCallToolTextConcat(t *testing.T) {
	handler := func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			return defaultHandler(method, params)
		}
		return map[string]any{"content": []map[string]any{
			{"type": "text", "text": "hello, "},
			{"type": "image", "data": "ignored"},
			{"type": "text", "text": "world"},
		}}, nil
	}
	c, fs := startFakeServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := c.callTool(ctx, "echo", map[string]any{"q": "hi"})
	if err != nil {
		t.Fatalf("callTool: %v", err)
	}
	if got != "hello, world" {
		t.Errorf("文本 = %q, 想要 %q", got, "hello, world")
	}

	// 校验发出去的参数形状：{name, arguments}。
	var callParams struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	methods, params := fs.recorded()
	for i, m := range methods {
		if m == "tools/call" {
			if err := json.Unmarshal(params[i], &callParams); err != nil {
				t.Fatalf("解析 tools/call 参数失败: %v", err)
			}
		}
	}
	if callParams.Name != "echo" || callParams.Arguments["q"] != "hi" {
		t.Errorf("tools/call 参数 = %+v", callParams)
	}
}

func TestCallToolIsError(t *testing.T) {
	handler := func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			return defaultHandler(method, params)
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "磁盘满了"}},
			"isError": true,
		}, nil
	}
	c, _ := startFakeServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.callTool(ctx, "echo", nil)
	if err == nil {
		t.Fatal("isError=true 时应返回 error")
	}
	if !strings.Contains(err.Error(), "磁盘满了") {
		t.Errorf("错误应带正文，实际: %v", err)
	}
}

func TestCallToolUnknownTool(t *testing.T) {
	handler := func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			return defaultHandler(method, params)
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool: nope"}
	}
	c, _ := startFakeServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.callTool(ctx, "nope", nil)
	if err == nil {
		t.Fatal("未知工具应返回 error")
	}
	if !strings.Contains(err.Error(), "unknown tool: nope") {
		t.Errorf("错误应带服务端消息，实际: %v", err)
	}
}

func TestRequestTimesOut(t *testing.T) {
	// tools/call 故意不回包：验证 ctx 超时能让请求返回，而不是永久阻塞。
	handler := func(method string, params json.RawMessage) (any, *rpcError) {
		if method == "tools/call" {
			return noReply{}, nil
		}
		return defaultHandler(method, params)
	}
	c, _ := startFakeServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.callTool(ctx, "echo", nil)
	if err == nil {
		t.Fatal("超时应返回 error")
	}
	if !strings.Contains(err.Error(), "超时") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("错误应说明超时，实际: %v", err)
	}
}

func TestClientCloseIdempotent(t *testing.T) {
	c, _ := startFakeServer(t, defaultHandler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.listTools(ctx); err != nil {
		t.Fatalf("关闭前 listTools 应成功: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("第二次 Close 应幂等返回 nil: %v", err)
	}
	if _, err := c.listTools(context.Background()); err == nil {
		t.Fatal("关闭后请求应失败")
	}
}
