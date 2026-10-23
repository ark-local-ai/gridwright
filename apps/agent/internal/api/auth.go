package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 本机 API 的访问令牌（见 docs/agent-architecture/31-日志与可观测.md 同级的部署说明）。
//
// 为什么需要：引擎只听 127.0.0.1，但**回环不等于安全**。
//   - 用户随便打开一个网页，页面里的 JS 可以向 127.0.0.1:7700 发请求
//     （CORS 只挡"读响应"，挡不住"发请求"；表单/简单请求仍能打进来）
//   - 同机的其它进程（含恶意脚本）可以用 curl 直接调用
//     /api/v1/fs/list、/workspace/import 这类能读任意本机路径的接口
//
// 令牌解决了第二类，也把第一类从"发得出"变成"过不了预检"：
// 带 Authorization 的请求必然触发 CORS 预检，而预检只对合法本机 Origin 放行。
//
// 分发方式：GET /api/v1/session 只对**本机 Origin 或无 Origin** 返回令牌。
// 浏览器跨站读不到（无 ACAO），DNS rebinding 被 Host 校验挡住；
// 同机进程仍能读，但它本来就能读 %APPDATA% 下的令牌文件——威胁模型内等价。
//
// 令牌文件：%APPDATA%\gridwright\engine.token（0600，尽力而为）。
// 环境变量 GRIDWRIGHT_TOKEN 可覆盖（开发/测试用，不落盘）。

// LoadOrCreateToken 读取或创建本机 API 令牌。
// 返回令牌与来源（"env" 或文件路径），便于启动日志说明，但**绝不记录令牌本身**。
func LoadOrCreateToken(path string) (string, string, error) {
	if v := strings.TrimSpace(os.Getenv("GRIDWRIGHT_TOKEN")); v != "" {
		return v, "env", nil
	}
	if path == "" {
		return "", "", fmt.Errorf("令牌文件路径为空")
	}
	if b, err := os.ReadFile(path); err == nil {
		tok := strings.TrimSpace(string(b))
		if len(tok) >= 32 {
			return tok, path, nil
		}
		// 文件损坏/过短：重新生成，而不是带着一个弱令牌跑。
	}

	tok, err := newToken()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", fmt.Errorf("创建令牌目录: %w", err)
	}
	// O_EXCL：两个引擎实例同时启动时，只有一个能写成功；
	// 另一个读回先写的那份，避免两边各拿一个令牌互相打架。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			if b, rerr := os.ReadFile(path); rerr == nil {
				if v := strings.TrimSpace(string(b)); len(v) >= 32 {
					return v, path, nil
				}
			}
		}
		return "", "", fmt.Errorf("写入令牌文件: %w", err)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		_ = f.Close()
		return "", "", fmt.Errorf("写入令牌文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", "", fmt.Errorf("关闭令牌文件: %w", err)
	}
	return tok, path, nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成令牌: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// withAuth 在 withCommon 之后执行（CORS 与 OPTIONS 预检必须先放行，
// 否则浏览器连"没有令牌"这个 401 都读不到，只会看到 CORS 错误）。
//
// Token 为空时整体跳过：单元测试直接构造 Server，不应被迫先拿令牌。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Host 校验：防 DNS rebinding（evil.com 解析到 127.0.0.1 后，
		// Host 头仍是 evil.com，而 Origin 也会是 evil.com）。
		if !isLoopbackHost(r.Host) {
			writeErr(w, http.StatusForbidden, "只接受本机回环地址的请求")
			return
		}
		// health 给探活/启动检测用；session 是令牌分发口，本身不能要令牌。
		switch r.URL.Path {
		case "/api/v1/health", "/api/v1/session":
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenOK(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gridwright"`)
			writeErr(w, http.StatusUnauthorized, "缺少或无效的本机访问令牌")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK 接受两种携带方式：
//   - Authorization: Bearer <token>（界面/脚本）
//   - Cookie gw_token=<token>（单文件版同源页面；HttpOnly 由界面侧不设置）
//
// 用常量时间比较，避免按字节计时猜令牌。
func (s *Server) tokenOK(r *http.Request) bool {
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if got == "" {
		if c, err := r.Cookie("gw_token"); err == nil {
			got = c.Value
		}
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

// handleSession GET /api/v1/session —— 向本机界面分发令牌。
//
// 只回显给本机 Origin；没有 Origin 的请求（Tauri 的部分 fetch、单文件版同源、
// curl）也放行。跨站页面拿不到响应（没有 ACAO），DNS rebinding 过不了 Host 校验。
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !isLocalOrigin(origin) {
		writeErr(w, http.StatusForbidden, "不允许的来源")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": s.Token})
}

// isLoopbackHost 判断 Host 头是否为本机回环（可带端口）。
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	switch strings.ToLower(h) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}
