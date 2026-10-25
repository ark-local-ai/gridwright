package mcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ConfigFile 是工作区根下的 MCP 配置文件。
const ConfigFile = "mcp.json"

// stderrTailLines 是错误信息里附带的 stderr 行数。
const stderrTailLines = 40

// stdioRW 把子进程的 stdout/标准输入拼成一个 io.ReadWriteCloser。
type stdioRW struct {
	in  io.WriteCloser
	out io.ReadCloser
}

func (s *stdioRW) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *stdioRW) Write(p []byte) (int, error) { return s.in.Write(p) }

// Close 先关 stdin（给子进程一个体面退出的信号），再关 stdout 读端。
func (s *stdioRW) Close() error {
	errIn := s.in.Close()
	errOut := s.out.Close()
	if errIn != nil && !errors.Is(errIn, os.ErrClosed) {
		return errIn
	}
	if errOut != nil && !errors.Is(errOut, os.ErrClosed) {
		return errOut
	}
	return nil
}

// tailBuffer 保留 stderr 的最后若干行，用于把失败原因写进错误。
// stderr 绝不会混进协议通道：它只进这里。
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	lines []string
}

func newTailBuffer(limit int) *tailBuffer {
	if limit <= 0 {
		limit = stderrTailLines
	}
	return &tailBuffer{limit: limit}
}

func (t *tailBuffer) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.limit {
		t.lines = t.lines[len(t.lines)-t.limit:]
	}
}

// text 返回尾部内容；为空时返回空串，调用方据此决定是否附在错误后面。
func (t *tailBuffer) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return ""
	}
	return strings.Join(t.lines, "\n")
}

// errSuffix 返回可直接拼进错误消息的 stderr 片段。
func (t *tailBuffer) errSuffix() string {
	s := t.text()
	if s == "" {
		return ""
	}
	return "\nstderr:\n" + s
}

// pumpStderr 把子进程 stderr 逐行收进 tailBuffer。stderr 不参与协议。
func pumpStderr(r io.Reader, tail *tailBuffer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for sc.Scan() {
		tail.add(sc.Text())
	}
}

// startServer 启动一个 server 子进程，完成握手与工具发现。
// 任何一步失败都会收拾干净（杀进程）后返回错误，调用方只需跳过这个 server。
func startServer(root string, cfg serverConfig) (*serverConn, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if root != "" {
		cmd.Dir = root
	}
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("mcp " + cfg.Name + ": 取 stdin 管道失败: " + err.Error())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, errors.New("mcp " + cfg.Name + ": 取 stdout 管道失败: " + err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, errors.New("mcp " + cfg.Name + ": 取 stderr 管道失败: " + err.Error())
	}

	tail := newTailBuffer(stderrTailLines)
	if err := cmd.Start(); err != nil {
		return nil, errors.New("mcp: 启动 server " + cfg.Name + "（" + cfg.Command + "）失败: " + err.Error() + tail.errSuffix())
	}
	go pumpStderr(stderr, tail)

	rw := &stdioRW{in: stdin, out: stdout}
	sc := &serverConn{name: cfg.Name, cmd: cmd, stderr: tail, rw: rw}

	hsCtx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	c, err := newClient(hsCtx, rw, cfg.Name)
	cancel()
	if err != nil {
		sc.abort()
		return nil, errors.New(err.Error() + tail.errSuffix())
	}
	sc.c = c

	listCtx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	tools, err := c.listTools(listCtx)
	cancel()
	if err != nil {
		sc.abort()
		return nil, errors.New(err.Error() + tail.errSuffix())
	}
	sc.tools = tools
	return sc, nil
}

// abort 在启动失败时立刻收尾，避免留下僵尸进程。
// 握手失败时 s.c 还是 nil，只能直接关底层管道。
func (s *serverConn) abort() {
	if s.c != nil {
		_ = s.c.Close()
	} else if s.rw != nil {
		_ = s.rw.Close()
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}
