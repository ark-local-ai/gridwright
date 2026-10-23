package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
)

// 账目筛选与 CSV 导出（见 internal/ledger/export.go）。
//
// 导出的边界：
//   - 只读：不追加账目、不碰工作区文件
//   - 带 BOM：Excel/WPS 打开中文不乱码
//   - 公式注入转义：账目里的 reason/source 可能来自模型或文件名
//   - 文件名只用 ASCII，避免不同浏览器对 Content-Disposition 的编码差异

// ledgerFilter 从 query 解析筛选条件。since/until 支持 "YYYY-MM-DD"
// 与账目里的两种时间格式；解析失败按“不筛选”处理并在响应头提示？
// 不：解析失败应明确报错，静默忽略会让用户以为筛选生效了。
func ledgerFilter(r *http.Request) (ledger.Filter, error) {
	q := r.URL.Query()
	f := ledger.Filter{
		Table:  strings.TrimSpace(q.Get("table")),
		Status: strings.TrimSpace(q.Get("status")),
		Source: strings.TrimSpace(q.Get("source")),
		Op:     strings.TrimSpace(q.Get("op")),
		Zone:   strings.TrimSpace(q.Get("zone")),
	}
	parse := func(name, v string, endOfDay bool) (time.Time, error) {
		v = strings.TrimSpace(v)
		if v == "" {
			return time.Time{}, nil
		}
		for _, layout := range []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02 15:04:05"} {
			if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
				if layout == "2006-01-02" && endOfDay {
					t = t.Add(24*time.Hour - time.Nanosecond)
				}
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("%s 的时间格式不对：%q（用 2026-01-31 或 2026-01-31 15:04）", name, v)
	}
	var err error
	if f.Since, err = parse("since", q.Get("since"), false); err != nil {
		return f, err
	}
	if f.Until, err = parse("until", q.Get("until"), true); err != nil {
		return f, err
	}
	return f, nil
}

// handleLedgerExport GET /api/v1/ledger/export?table=&status=&source=&op=&zone=&since=&until=&limit=
func (s *Server) handleLedgerExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	f, err := ledgerFilter(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _, led, _ := s.cur()
	entries, err := led.Entries(0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered := ledger.FilterEntries(entries, f)
	limit := 0
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			limit = n
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	name := "gridwright-ledger-" + time.Now().Format("20060102-1504") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Ledger-Rows", strconv.Itoa(len(filtered)))
	if err := ledger.WriteCSV(w, filtered); err != nil {
		// 响应头已经写出去了：只能记日志 + 中断，无法再改状态码。
		return
	}
}
