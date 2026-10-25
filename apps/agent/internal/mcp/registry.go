package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// handshakeTimeout 是握手（initialize + tools/list）的时限。
	handshakeTimeout = 15 * time.Second
	// callTimeout 是单次 tools/call 的时限。
	callTimeout = 60 * time.Second
	// closeGrace 是 Close 等进程自行退出的宽限期，超时即 kill。
	closeGrace = 3 * time.Second
)

// Tool 是注册表对外的工具描述。
type Tool struct {
	Server      string
	Name        string
	Description string
	Schema      json.RawMessage
}

// spawn 是 startServer 的接缝：单测替换它，就能在不拉起任何子进程的情况下
// 验证 Open 的启用过滤与失败隔离逻辑。
var spawn = startServer

// Registry 是全部 MCP server 的集合。零值可用（等价于空注册表）。
type Registry struct {
	mu      sync.Mutex
	servers map[string]*serverConn
	closed  bool
}

// serverConn 是一个已握手、已发现工具的 server。
type serverConn struct {
	name   string
	cmd    *exec.Cmd
	c      *client
	rw     io.ReadWriteCloser
	stderr *tailBuffer
	tools  []toolDef

	once sync.Once
	err  error
}

// Open 读取 <root>/mcp.json 并拉起所有启用项。
// 没有配置文件、配置解析失败、单个 server 启动失败都不影响返回值：
// 返回的注册表是当前可用的那一部分，失败原因写日志（以及随后的错误信息）。
func Open(root string) *Registry {
	r := &Registry{servers: make(map[string]*serverConn)}
	path := filepath.Join(root, ConfigFile)

	servers, err := loadConfig(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("mcp: 读取配置失败，按空注册表处理", "path", path, "err", err)
		}
		return r
	}

	for _, cfg := range servers {
		if !cfg.enabled() {
			continue
		}
		if cfg.Name == "" || cfg.Command == "" {
			slog.Warn("mcp: 跳过不完整的 server 配置（需要 name 与 command）", "path", path, "name", cfg.Name)
			continue
		}
		sc, err := spawn(root, cfg)
		if err != nil {
			// 单个 server 失败只记日志：别的 server 照常可用。
			slog.Warn("mcp: server 启动失败，已跳过", "server", cfg.Name, "err", err)
			continue
		}
		r.mu.Lock()
		if _, dup := r.servers[cfg.Name]; dup {
			slog.Warn("mcp: server 名重复，后出现者覆盖", "server", cfg.Name)
		}
		r.servers[cfg.Name] = sc
		r.mu.Unlock()
	}
	slog.Info("mcp: 注册表就绪", "servers", len(r.servers), "tools", len(r.Catalog()))
	return r
}

// Catalog 返回全部工具，按 server + name 排序。
func (r *Registry) Catalog() []Tool {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []Tool
	for _, sc := range r.servers {
		for _, t := range sc.tools {
			out = append(out, Tool{
				Server:      sc.name,
				Name:        t.Name,
				Description: t.Description,
				Schema:      t.InputSchema,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Call 调用指定 server 的工具，返回拼接后的文本。
// 错误里带 server 名、工具名与 stderr 尾部，便于诊断。
func (r *Registry) Call(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	if r == nil {
		return "", errors.New("mcp: 注册表为空")
	}
	r.mu.Lock()
	sc, ok := r.servers[server]
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return "", fmt.Errorf("mcp %s: 注册表已关闭", server)
	}
	if !ok {
		return "", fmt.Errorf("mcp: 未知 server %q", server)
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	text, err := sc.c.callTool(callCtx, tool, args)
	if err != nil {
		return "", fmt.Errorf("%w%s", err, sc.stderrSuffix())
	}
	return text, nil
}

// Close 关闭所有 server：关 stdin、等进程退出，超时 kill。可重复调用。
func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	list := make([]*serverConn, 0, len(r.servers))
	for _, sc := range r.servers {
		list = append(list, sc)
	}
	r.mu.Unlock()

	// 并发收尾：每个 server 各自最多等 closeGrace，串行会让总时长堆成 N 倍。
	// 结果一律通过 channel 回传，避免任何共享写入。
	errs := make(chan error, len(list))
	for _, sc := range list {
		go func(sc *serverConn) { errs <- sc.close() }(sc)
	}
	var joined []error
	for range list {
		if err := <-errs; err != nil {
			joined = append(joined, err)
		}
	}
	return errors.Join(joined...)
}

// close 关连接、等退出、超时 kill。sync.Once 保证可重复调用。
func (s *serverConn) close() error {
	s.once.Do(func() {
		s.err = s.doClose()
	})
	return s.err
}

func (s *serverConn) stderrSuffix() string {
	if s.stderr == nil {
		return ""
	}
	return s.stderr.errSuffix()
}

func (s *serverConn) doClose() error {
	// 关 stdin / stdout：子进程通常因此读到 EOF 后自行退出。
	if s.c != nil {
		_ = s.c.Close()
	}
	if s.cmd == nil {
		return nil
	}

	waitc := make(chan error, 1)
	go func() { waitc <- s.cmd.Wait() }()

	select {
	case <-waitc:
		// 进程自己退出；退出码非 0 也算正常收尾，不当作 Close 失败。
		return nil
	case <-time.After(closeGrace):
		if s.cmd.Process != nil {
			if err := s.cmd.Process.Kill(); err != nil {
				<-waitc
				return fmt.Errorf("mcp %s: kill 失败: %w", s.name, err)
			}
		}
		<-waitc
		return nil
	}
}
