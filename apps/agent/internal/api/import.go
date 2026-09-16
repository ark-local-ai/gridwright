package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 导入文件到工作区。
//
// 为什么是「传路径」而不是「HTTP 上传」：桌面版（Tauri）能直接弹出系统文件
// 选择框拿到本机绝对路径，而文件本来就在这台机器上——把它读成字节流、经
// HTTP 传一圈再写回同一个磁盘，纯属绕路，还多一份内存开销与大文件限制。
// 所以走路径 + 服务端复制。
//
// 安全边界（重要）：这个接口能读任意本机路径、并把它复制进工作区。
// 单机单人本地服务下成立；若哪天这个引擎暴露到网络上，**必须**先加鉴权，
// 否则等于把整台机器的读权限交出去。见 docs 里的部署说明。
type importReq struct {
	Paths []string `json:"paths"`
}

type importResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
}

func (s *Server) handleWorkspaceImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req importReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "缺少 paths")
		return
	}
	_, layout, _, _ := s.cur()
	if layout == nil || layout.Root == "" {
		writeErr(w, http.StatusBadRequest, "还没有工作区")
		return
	}

	results := make([]importResult, 0, len(req.Paths))
	for _, src := range req.Paths {
		res := importResult{Name: filepath.Base(src)}
		if err := s.importOne(layout.Root, src); err != nil {
			res.Err = err.Error()
		} else {
			res.OK = true
		}
		results = append(results, res)
	}
	// 不用在这里刷新图/体检：前端拿到结果会自己重新拉一遍
	// （服务端缓存图反而会拿到复制完成前的旧状态）
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}

// importOne 把单个文件或目录复制进工作区根目录。
func (s *Server) importOne(root, src string) error {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("找不到：%s", filepath.Base(src))
	}
	if st.IsDir() {
		return fmt.Errorf("暂不支持整个文件夹，请把表一起选中")
	}
	// 只收表；其他类型明确拒绝并说明（而不是静默忽略，用户会以为成功了）
	switch strings.ToLower(filepath.Ext(src)) {
	case ".xlsx", ".xlsm", ".xls":
	default:
		return fmt.Errorf("不是 Excel 表（%s）", strings.ToLower(filepath.Ext(src)))
	}

	dst := filepath.Join(root, filepath.Base(src))
	// 同名不覆盖：用户原表就在工作区里时，绝不能把源文件冲掉
	if samePath(src, dst) {
		return nil // 已经在工作区里，算成功
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("工作区里已有同名文件")
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("读不到：%w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("写入失败：%w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		_ = os.Remove(dst) // 复制失败别留半个文件
		return fmt.Errorf("复制失败：%w", err)
	}
	return nil
}
