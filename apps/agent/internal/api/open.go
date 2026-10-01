package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 用本机的 Excel / WPS 打开某张表。
//
// 为什么由引擎（而非 Tauri 壳）来做：引擎和工作区在同一台机器上，能拿到绝对
// 路径并直接调系统；桌面壳与 Win7 单文件版走同一套接口，行为一致，不用维护两份。
//
// 为什么不直接用"默认关联"就完事：用户的机器上常常 Excel 与 WPS 并存，默认关联
// 未必是他此刻想用的那个。所以先探测装了哪些，让用户选；选不了再退回默认程序。
//
// 安全边界：只允许打开**当前工作区里确实存在的** .xlsx（走 pickTable），
// 不接受任意路径——否则这个接口就成了本机文件执行的跳板。

type openTarget struct {
	ID    string `json:"id"`    // default | excel | wps
	Label string `json:"label"` // 显示名
}

// handleOpenTargets GET /api/v1/open/targets?file=
// 告诉界面这张表能用哪些程序打开（默认程序永远在；Excel / WPS 装了才有）。
func (s *Server) handleOpenTargets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	target, err := s.pickTable(r.URL.Query().Get("file"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":    filepath.Base(target),
		"path":    target,
		"targets": detectOpenTargets(),
	})
}

// handleOpenFile POST /api/v1/open  {file, target}
// target 为空或 "default" 时用系统默认程序。
func (s *Server) handleOpenFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var body struct {
		File   string `json:"file"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是 JSON："+err.Error())
		return
	}
	path, err := s.pickTable(body.File)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := openWith(path, body.Target); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "target": body.Target})
}

// ---------- 平台实现（本产品只出 Windows 产物；其它平台给一句人话） ----------

func detectOpenTargets() []openTarget {
	if runtime.GOOS != "windows" {
		return []openTarget{{ID: "default", Label: "默认程序"}}
	}
	out := []openTarget{{ID: "default", Label: "默认程序"}}
	if excelExe() != "" {
		out = append(out, openTarget{ID: "excel", Label: "Excel"})
	}
	if wpsExe() != "" {
		out = append(out, openTarget{ID: "wps", Label: "WPS 表格"})
	}
	return out
}

func openWith(path, target string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("当前系统不支持直接打开表格")
	}
	switch target {
	case "", "default":
		// 交给系统默认关联。rundll32 比 `cmd /c start` 少一层引号坑。
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
	case "excel":
		exe := excelExe()
		if exe == "" {
			return fmt.Errorf("没找到 Excel，可改用默认程序或 WPS")
		}
		return exec.Command(exe, path).Start()
	case "wps":
		exe := wpsExe()
		if exe == "" {
			return fmt.Errorf("没找到 WPS 表格，可改用默认程序或 Excel")
		}
		return exec.Command(exe, path).Start()
	}
	return fmt.Errorf("不认识的目标程序 %q", target)
}

// excelExe 找本机 Excel 的 exe。优先用 App Paths（最准），退到文件关联的 progid。
func excelExe() string {
	return firstNonEmpty(
		appPathExe(`HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\excel.exe`),
		appPathExe(`HKEY_CURRENT_USER\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\excel.exe`),
		commandExe(`HKEY_CLASSES_ROOT\Excel.Sheet.12\shell\Open\command`),
	)
}

// wpsExe 找本机 WPS 表格（ET）的 exe。WPS 的注册表键在不同版本里不太一样，
// 所以多试几个：App Paths\et.exe、ET.Sheet.12 的打开命令。
func wpsExe() string {
	return firstNonEmpty(
		appPathExe(`HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\et.exe`),
		appPathExe(`HKEY_CURRENT_USER\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\et.exe`),
		commandExe(`HKEY_CLASSES_ROOT\ET.Sheet.12\shell\Open\command`),
		commandExe(`HKEY_CLASSES_ROOT\wps.exe\shell\Open\command`),
	)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// regDefaultRaw 读注册表某个键的默认值（REG_SZ 那一段原文）。
// 不引第二依赖：直接调 Windows 自带的 reg.exe（Win7 也有）。
func regDefaultRaw(key string) string {
	out, err := exec.Command("reg", "query", key, "/ve").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, "REG_SZ")
		if i < 0 {
			continue
		}
		return strings.TrimSpace(line[i+len("REG_SZ"):])
	}
	return ""
}

// appPathExe：App Paths 的值**就是完整路径**（可能带引号，可能含空格），整体取。
func appPathExe(key string) string {
	return strings.Trim(regDefaultRaw(key), `"`)
}

// commandExe：shell\Open\command 的值是 `"exe" 参数…`，取第一个 token。
func commandExe(key string) string {
	return firstExe(regDefaultRaw(key))
}

// firstExe 从命令串里抽出可执行文件路径。
// 形如：`"C:\...\EXCEL.EXE" /dde`。
func firstExe(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	if val[0] == '"' {
		if j := strings.Index(val[1:], `"`); j >= 0 {
			return val[1 : 1+j]
		}
	}
	if k := strings.IndexAny(val, " \t\r\n"); k >= 0 {
		return val[:k]
	}
	return val
}
