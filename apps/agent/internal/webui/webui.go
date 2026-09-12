// Package webui 把前端产物**嵌进二进制**，让 Win7/8 的单文件版自带界面。
//
// 为什么需要：Win7 没有 WebView2，跑不了 Tauri 壳。那条路是"单个 Go 可执行文件
// 自带界面 + 本地服务"——所以界面必须 embed 进来，不能要求用户另外准备文件。
//
// 做法：构建前把前端产物复制到 internal/webui/dist/（见 scripts/ 里的打包脚本），
// 然后 go:embed 进来；服务根路径时返回 index.html，其余路径按静态文件返回。
// 目录为空时也能编译（只有 API、没有界面），不会因为忘了复制前端而构建失败。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var files embed.FS

// Available 表示二进制里是否嵌入了前端产物。
func Available() bool {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return false
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == "index.html" {
			return true
		}
	}
	return false
}

// Handler 返回提供界面的 http.Handler（未嵌入时返回 nil，调用方据此跳过注册）。
//
// 路由规则：命中静态文件就返回文件，否则回 index.html（前端是单页应用，
// 深链接由前端自己处理）。
func Handler() http.Handler {
	if !Available() {
		return nil
	}
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return nil
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		f, err := sub.Open(p)
		if err != nil {
			// 不是真实文件 → 交给前端路由（返回 index.html）
			serveIndex(w, sub)
			return
		}
		_ = f.Close()
		fileServer.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}
