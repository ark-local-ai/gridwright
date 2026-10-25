package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// client 是协议层：一条 io.ReadWriteCloser 上的 JSON-RPC 会话。
//
// 它不关心对方是子进程还是测试里的 net.Pipe，所以 Registry 与单测共用同一份逻辑。
type client struct {
	name string
	rw   io.ReadWriteCloser

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan *rpcResponse
	err     error // 会话终止原因，非 nil 表示已结束

	writeMu sync.Mutex
	done    chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// newClient 在 rw 上完成 initialize + notifications/initialized 握手。
// 握手失败时会关闭 rw，调用方不必再关。
func newClient(ctx context.Context, rw io.ReadWriteCloser, name string) (*client, error) {
	c := &client{
		name:    name,
		rw:      rw,
		pending: make(map[string]chan *rpcResponse),
		done:    make(chan struct{}),
	}
	go c.readLoop()

	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": clientVersion,
		},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp %s: 握手失败: %w", name, err)
	}
	if err := c.notify("notifications/initialized", nil); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp %s: 发送 initialized 通知失败: %w", name, err)
	}
	return c, nil
}

// readLoop 是唯一的读协程：按行拆 JSON-RPC，按 ID 分发给等待中的请求。
func (c *client) readLoop() {
	sc := bufio.NewScanner(c.rw)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		// scanner 复用底层数组，先拷一份再交给下游。
		c.handleLine(append([]byte(nil), line...))
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.finish(fmt.Errorf("mcp %s: 读取失败: %w", c.name, err))
}

func (c *client) handleLine(line []byte) {
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		// 非协议行（例如某个服务端误把日志写到 stdout）直接丢弃，不打断会话。
		return
	}
	id := idKey(resp.ID)
	if id == "" {
		// 通知，或服务端发来的请求；本实现的协议面不需要处理。
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if !ok {
		// 未知 ID：可能已被超时清理，忽略。
		return
	}
	ch <- &resp
}

// idKey 把响应 ID 规范化成 map key。数字与字符串形态都接受。
func idKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if len(s) >= 2 && s[0] == '"' {
		if unq, err := strconv.Unquote(s); err == nil {
			return unq
		}
	}
	return s
}

// request 发送一条请求并等待同 ID 的响应。
func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *rpcResponse, 1)
	c.pending[strconv.FormatInt(id, 10)] = ch
	c.mu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	if err := c.send(req); err != nil {
		c.forget(id)
		return nil, fmt.Errorf("mcp %s: 发送 %s 失败: %w", c.name, method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s: %w", c.name, method, resp.Error)
		}
		if len(resp.Result) == 0 {
			return json.RawMessage("null"), nil
		}
		return resp.Result, nil
	case <-c.done:
		c.forget(id)
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s: %s 中断: %w", c.name, method, err)
	case <-ctx.Done():
		c.forget(id)
		return nil, fmt.Errorf("mcp %s: %s 超时或取消: %w", c.name, method, ctx.Err())
	}
}

// notify 发送一条通知（无 ID、无响应）。
func (c *client) notify(method string, params any) error {
	if err := c.send(rpcRequest{JSONRPC: "2.0", Method: method, Params: params}); err != nil {
		return fmt.Errorf("mcp %s: 发送 %s 失败: %w", c.name, method, err)
	}
	return nil
}

// send 序列化并写入一整行。写操作串行化，避免并发请求把两行搅在一起。
func (c *client) send(req rpcRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for len(data) > 0 {
		n, werr := c.rw.Write(data)
		if werr != nil {
			return werr
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (c *client) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, strconv.FormatInt(id, 10))
	c.mu.Unlock()
}

// finish 记录会话终止原因并唤醒所有等待者。只有第一次生效。
func (c *client) finish(err error) {
	if err == nil {
		err = errors.New("mcp: 连接已结束")
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
	c.mu.Unlock()
}

// Close 关闭底层连接并终止读循环。可重复调用。
func (c *client) Close() error {
	c.closeOnce.Do(func() {
		// 先标记结束，让阻塞中的请求立刻返回，不必等 rw.Close 的连锁反应。
		c.finish(fmt.Errorf("mcp %s: 连接已关闭", c.name))
		if err := c.rw.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, io.EOF) {
			c.closeErr = fmt.Errorf("mcp %s: 关闭连接失败: %w", c.name, err)
		}
	})
	return c.closeErr
}

// listTools 拉取全部工具，自动跟随 nextCursor 分页。
func (c *client) listTools(ctx context.Context) ([]toolDef, error) {
	var out []toolDef
	cursor := ""
	for page := 0; page < maxToolPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res listToolsResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp %s: 解析 tools/list 结果失败: %w", c.name, err)
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
	return nil, fmt.Errorf("mcp %s: tools/list 分页超过 %d 页，已放弃", c.name, maxToolPages)
}

// callTool 调用一个工具，把 type=text 的内容块按顺序拼接成字符串返回。
func (c *client) callTool(ctx context.Context, tool string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	params := map[string]any{"name": tool, "arguments": args}
	raw, err := c.request(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	var res callToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("mcp %s: 解析 tools/call 结果失败: %w", c.name, err)
	}
	var b strings.Builder
	for _, item := range res.Content {
		if item.Type == "text" {
			b.WriteString(item.Text)
		}
	}
	text := b.String()
	if res.IsError {
		if text == "" {
			text = "(无错误正文)"
		}
		return "", fmt.Errorf("mcp %s: 工具 %s 返回错误: %s", c.name, tool, text)
	}
	return text, nil
}
