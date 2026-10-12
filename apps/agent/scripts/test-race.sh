#!/usr/bin/env bash
# 跑竞态检测测试（go test -race）。
#
# 为什么需要这个脚本：Go 的 -race 在 Windows/amd64 上要求 cgo，而 cgo 又要一个
# gcc 兼容编译器。本机没把 gcc 放进 PATH，于是 -race 会报
# "-race requires cgo; enable cgo by setting CGO_ENABLED=1"。这里自动去找 gcc
# （$CC 优先，其次 winget 装的 WinLibs MinGW、msys64），找到就设好 CC/PATH 再跑。
#
# 一次性装编译器：
#   winget install BrechtSanders.WinLibs.POSIX.UCRT
#
# 用法：
#   bash apps/agent/scripts/test-race.sh                      # 全部包
#   bash apps/agent/scripts/test-race.sh ./internal/api/...   # 只跑指定包
set -euo pipefail

cd "$(dirname "$0")/.." # -> apps/agent

find_cc() {
  if [ -n "${CC:-}" ] && command -v "$CC" >/dev/null 2>&1; then
    echo "$CC"
    return
  fi
  local c
  for c in \
    "$HOME"/AppData/Local/Microsoft/WinGet/Packages/*/mingw64/bin/gcc.exe \
    /c/msys64/mingw64/bin/gcc.exe \
    /c/ProgramData/mingw64/mingw64/bin/gcc.exe \
    "$(command -v gcc 2>/dev/null || true)"; do
    if [ -n "$c" ] && [ -x "$c" ]; then
      echo "$c"
      return
    fi
  done
}

CC_BIN="$(find_cc || true)"
if [ -z "$CC_BIN" ]; then
  echo "找不到 gcc：-race 需要 cgo + gcc。" >&2
  echo "装一个：winget install BrechtSanders.WinLibs.POSIX.UCRT" >&2
  echo "或用 CC=/path/to/gcc 指定已有编译器。" >&2
  exit 1
fi

export CC="$CC_BIN"
export PATH="$(dirname "$CC_BIN"):$PATH"
echo "CC=$CC"
CGO_ENABLED=1 go test -race -count=1 "$@"
