package api

import (
	"fmt"
	"os"
	"testing"
)

// TestMain 把**整个包**的用户配置目录重定向到临时目录。
//
// 为什么放在包级而不是每个 helper 里：这个包里有若干测试会经
// PUT /api/v1/settings 走到 cfg.Save(config.UserConfigPath())，直接写
// %APPDATA%\gridwright\config.yaml。只要有人再加一个 constructor 或一条
// 直接调 handler 的用例而忘了隔离，就会再次覆盖使用者的真实配置——
// 实测发生过：跑完测试后 workspace 变成了 Temp\TestSettingsGetPut…、
// api_key 变成 sk-test。
//
// 放在 TestMain 是**默认安全**：新用例什么都不用做，就已经被隔离了。
// 反过来（默认不安全、靠每个用例自觉）已经证明会漏。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gridwright-api-test-cfg")
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建测试配置目录失败:", err)
		os.Exit(1)
	}
	// 显式清掉（而不是 defer）：os.Exit 不跑 defer
	os.Setenv("GRIDWRIGHT_CONFIG_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
