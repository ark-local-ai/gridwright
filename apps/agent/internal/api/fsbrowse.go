package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 服务端目录浏览（见 docs/agent-architecture/28-单文件版首次运行.md）。
//
// 为什么需要：单文件版是在**浏览器**里用的，而浏览器出于安全拿不到绝对路径
// （`<input webkitdirectory>` 只给相对路径，File System Access 也只给目录名）。
// 所以"选文件夹"这件事必须由**服务端**来做——它能真实访问文件系统。
//
// 这个接口只**列目录**、不读文件内容、不做任何修改，只监听本机回环地址。

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type dirListResp struct {
	Dir     string     `json:"dir"`     // 当前目录
	Parent  string     `json:"parent"`  // 上级目录（为空表示已在顶层）
	Entries []dirEntry `json:"entries"` // 子目录
	// Windows 下的盘符列表（非 Windows 为空）
	Drives []string `json:"drives,omitempty"`
	// 当前目录里有多少个 .xlsx（帮用户判断"是不是这里"）
	Tables int `json:"tables"`
}

// handleFSList GET /api/v1/fs/list?dir= —— 列出某个目录下的子目录
func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		// 没给就从用户主目录开始
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = home
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "路径无效："+err.Error())
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "打不开这个目录："+err.Error())
		return
	}

	resp := dirListResp{Dir: abs, Entries: []dirEntry{}, Drives: drives()}
	// 上级目录（已在盘根/顶层时留空）
	if parent := filepath.Dir(abs); parent != abs {
		resp.Parent = parent
	}

	xlsx := 0
	for _, e := range entries {
		name := e.Name()
		// 跳过隐藏目录（Windows 的 ~$ 之类）
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") {
			continue
		}
		if e.IsDir() {
			resp.Entries = append(resp.Entries, dirEntry{Name: name, Path: filepath.Join(abs, name)})
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".xlsx") {
			xlsx++
		}
	}
	resp.Tables = xlsx
	sort.Slice(resp.Entries, func(i, j int) bool { return resp.Entries[i].Name < resp.Entries[j].Name })

	if wantsText(r) {
		var b strings.Builder
		b.WriteString(line("目录：%s", resp.Dir))
		if resp.Parent != "" {
			b.WriteString(line("上级：%s", resp.Parent))
		}
		b.WriteString(line("含 .xlsx：%d 个", resp.Tables))
		b.WriteString("\n子目录：\n")
		for _, e := range resp.Entries {
			b.WriteString(line("  %s", e.Name))
		}
		if len(resp.Entries) == 0 {
			b.WriteString("  （没有子目录）\n")
		}
		writeText(w, b.String())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// drives 列出 Windows 盘符（其他平台返回空）。
func drives() []string {
	var out []string
	for c := 'A'; c <= 'Z'; c++ {
		p := string(c) + ":\\"
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}
