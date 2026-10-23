package pathsafe

import (
	"os"
	"path/filepath"
	"strings"
)

// canon 是规范化后的路径：卷 + 路径组件。
// 比较只在这两层上做，绝不比较整串字符串。
type canon struct {
	// path 是保留原始大小写的完整路径，用于 os 调用与给用户看。
	path string
	// vol 是卷标识，小写：盘符为 "c:"，UNC 为 "\\server\share"。
	vol string
	// comps 是卷之后的路径组件，小写。
	comps []string
}

// equal 判断两个规范化路径是否指向同一位置（大小写不敏感）。
func (c canon) equal(o canon) bool {
	if c.vol != o.vol || len(c.comps) != len(o.comps) {
		return false
	}
	for i := range c.comps {
		if c.comps[i] != o.comps[i] {
			return false
		}
	}
	return true
}

// contains 判断 o 是否在 c 之内（含 c 本身）。
// 卷必须相同且组件前缀相同——这样 `c:\data2` 不会被 `c:\data` 涵盖。
func (c canon) contains(o canon) bool {
	if c.vol != o.vol || len(o.comps) < len(c.comps) {
		return false
	}
	for i := range c.comps {
		if c.comps[i] != o.comps[i] {
			return false
		}
	}
	return true
}

// canonicalize 把任意用户路径变成可信的 canon。
//
// 顺序很重要：先词法校验与规范化（不碰文件系统），再解析符号链接，
// 最后把解析结果再规范化一次（链接可能跨卷，卷必须重新取）。
func canonicalize(raw string) (canon, error) {
	if strings.TrimSpace(raw) == "" {
		return canon{}, errPath("路径为空")
	}
	if strings.ContainsRune(raw, 0) {
		return canon{}, errPath("路径含 NUL 字符")
	}
	if r, ok := firstControl(raw); ok {
		return canon{}, errPath("路径含控制字符 %U", r)
	}

	// 统一分隔符：调用方可能给 `/`、`\` 或混合。
	p := strings.ReplaceAll(raw, "/", `\`)
	if hasDevicePrefix(p) {
		return canon{}, errPath("拒绝设备/扩展长度前缀: %s", raw)
	}

	// 盘符相对路径：`C:foo` 的含义是"当前目录在 C 盘上的相对位置"，
	// 取决于进程的 per-drive 当前目录，不能当成 `C:\foo`，也不能靠 Abs 救回来。
	if len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':' {
		if len(p) == 2 || p[2] != '\\' {
			return canon{}, errPath("盘符相对路径缺少分隔符: %s", raw)
		}
	}

	// UNC 至少要有 server 与 share；`\\server` 这种拿不到卷，无法判定归属。
	if strings.HasPrefix(p, `\\`) {
		server, share, ok := splitUNCHead(p)
		if !ok || server == "" || share == "" {
			return canon{}, errPath("UNC 路径缺少服务器或共享名: %s", raw)
		}
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return canon{}, errPath("无法解析为绝对路径 %q: %v", raw, err)
	}
	first, err := canonFromClean(filepath.Clean(abs))
	if err != nil {
		return canon{}, err
	}

	// 解析符号链接：最深已存在祖先的链接 + 最终路径本身的链接。
	resolved := stripDevicePrefix(resolveLinks(first.path))
	if resolved == "" || resolved == first.path {
		return first, nil
	}
	return canonFromClean(filepath.Clean(resolved))
}

// canonFromClean 把一个已 Clean 的绝对路径拆成 卷 + 组件，并逐组件做命名层校验。
//
// 这里做尾部空格与点的截断：Win32 在打开文件前会静默去掉组件末尾的空格和点，
// 所以 `C:\data\secret.txt.` 与 `C:\data\secret.txt` 是同一个文件，
// 若按原样比较就会把"多打一个点"当成另一个（可能已授权的）路径。
func canonFromClean(p string) (canon, error) {
	vol, rest := splitVolume(p)
	if vol == "" {
		return canon{}, errPath("无法识别卷（需绝对路径）: %s", p)
	}
	// 组件里的冒号是 NTFS 备用数据流（ADS），如 `a::$DATA`：文件可见内容与
	// 数据流是两个对象，白名单不该让它们互相借用授权。
	if strings.Contains(rest, ":") {
		return canon{}, errPath("路径组件含冒号（ADS 或非法盘符）: %s", p)
	}

	raw := splitComps(rest)
	comps := make([]string, 0, len(raw))
	for _, c := range raw {
		t := trimSuffixDotsSpaces(c)
		if t == "" {
			// 截断后为空（如 `...`）：Win32 下等价于不存在该组件，跳过。
			continue
		}
		comps = append(comps, t)
	}

	lower := make([]string, len(comps))
	for i, c := range comps {
		lower[i] = strings.ToLower(c)
	}
	return canon{
		path:  joinVolume(vol, comps),
		vol:   strings.ToLower(vol),
		comps: lower,
	}, nil
}

// trimSuffixDotsSpaces 去掉组件末尾的空格与点，但保留 `.` / `..` 的语义
// （正常情况下 Clean 已经把这两个解析掉了，这里只是不被它们破坏）。
func trimSuffixDotsSpaces(s string) string {
	if s == "." || s == ".." {
		return s
	}
	return strings.TrimRight(s, " .")
}

// splitVolume 拆出卷标识与卷之后的相对部分（不带前导分隔符）。
// 卷的两种形态：盘符 `C:`、UNC `\\server\share`。
func splitVolume(p string) (vol, rest string) {
	if len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':' {
		r := strings.TrimPrefix(p[2:], `\`)
		return p[:2], r
	}
	if strings.HasPrefix(p, `\\`) {
		server, share, ok := splitUNCHead(p)
		if !ok {
			return "", ""
		}
		used := len(`\\` + server + `\` + share)
		return p[:used], strings.TrimPrefix(p[used:], `\`)
	}
	return "", p
}

// splitUNCHead 从 `\\server\share\rest` 里取出 server 与 share。
func splitUNCHead(p string) (server, share string, ok bool) {
	if !strings.HasPrefix(p, `\\`) {
		return "", "", false
	}
	s := p[2:]
	server, rem, found := strings.Cut(s, `\`)
	if !found || server == "" {
		return "", "", false
	}
	share, _, _ = strings.Cut(rem, `\`)
	if share == "" {
		return "", "", false
	}
	return server, share, true
}

// splitComps 把卷之后的部分拆成组件（丢弃空串，吸收重复分隔符）。
func splitComps(rest string) []string {
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, `\`)
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// joinVolume 把卷与组件拼回完整路径。
func joinVolume(vol string, comps []string) string {
	if len(comps) == 0 {
		if strings.HasPrefix(vol, `\\`) {
			return vol
		}
		return vol + `\`
	}
	return vol + `\` + strings.Join(comps, `\`)
}

// appendComps 把组件拼到 base 后面，自动补分隔符。
func appendComps(base string, names []string) string {
	out := base
	for _, n := range names {
		if !strings.HasSuffix(out, `\`) {
			out += `\`
		}
		out += n
	}
	return out
}

// resolveLinks 解析符号链接 / 目录联接（junction）：
// 找到最深的已存在祖先，对它做 EvalSymlinks，再把不存在的尾部原样拼回去。
// 这样 create 预检（目标还不存在）也能拿到将来真正落地的路径。
//
// 出错一律吞掉并退回原路径：这里的目标是"尽可能拿到真实路径"，
// 拿不到时由调用方按原路径判定——宁可判为越界，也不要判为安全。
func resolveLinks(p string) string {
	vol, rest := splitVolume(p)
	if vol == "" {
		return p
	}
	all := splitComps(rest)
	comps := all
	cur := joinVolume(vol, comps)

	for {
		if _, err := os.Lstat(cur); err == nil {
			if r, err := filepath.EvalSymlinks(cur); err == nil && r != "" {
				cur = r
			}
			break
		}
		if len(comps) == 0 {
			// 连卷根都不存在（例如未挂载的 UNC）：保持原样。
			return p
		}
		comps = comps[:len(comps)-1]
		cur = joinVolume(vol, comps)
	}

	out := appendComps(stripDevicePrefix(cur), all[len(comps):])
	// 最终路径本身若仍是符号链接（尾部整个是链接时上一轮已解析，
	// 但祖先解析后尾部可能重新构成链接），再解析一次。
	if fi, err := os.Lstat(out); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if r, err := filepath.EvalSymlinks(out); err == nil && r != "" {
			out = r
		}
	}
	return out
}
