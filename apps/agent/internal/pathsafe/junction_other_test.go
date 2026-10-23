//go:build !windows

package pathsafe

import "errors"

// makeJunction 在非 Windows 平台上不存在，仅供测试代码统一调用。
func makeJunction(_, _ string) error {
	return errors.New("目录联接仅在 Windows 上可用")
}
