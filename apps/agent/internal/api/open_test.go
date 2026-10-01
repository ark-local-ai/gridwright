package api

import "testing"

// firstExe 从注册表命令串里抠出 exe 路径——这是"用 Excel/WPS 打开"能否命中
// 的关键一环，格式来自真实注册表（带引号 + /dde 参数，或 App Paths 的纯路径）。
func TestFirstExe(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"C:\Program Files\Microsoft Office\root\Office16\EXCEL.EXE" /dde`, `C:\Program Files\Microsoft Office\root\Office16\EXCEL.EXE`},
		{`C:\path\et.exe`, `C:\path\et.exe`},
		{`  "C:\path with space\et.exe" /dde`, `C:\path with space\et.exe`},
		{``, ``},
	}
	for _, c := range cases {
		if got := firstExe(c.in); got != c.want {
			t.Errorf("firstExe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDetectOpenTargetsHasDefaultFirst(t *testing.T) {
	ts := detectOpenTargets()
	if len(ts) == 0 || ts[0].ID != "default" {
		t.Fatalf("第一个目标必须是默认程序，得到 %+v", ts)
	}
}
