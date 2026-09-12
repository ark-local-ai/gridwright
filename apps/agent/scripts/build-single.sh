#!/usr/bin/env bash
# 打包 gridwright 单文件版（Win7/8 用；也可在 Win10/11 上当"免安装"版）
#
# 产物：dist/gridwright-<平台>.exe —— **自带界面**（前端 embed 进二进制），
#       双击不需要 WebView2、不需要额外文件、不需要联网。
#
# 用法：
#   bash apps/agent/scripts/build-single.sh              # 默认 windows/amd64（Win7 起）
#   bash apps/agent/scripts/build-single.sh windows 386  # 32 位（老机器）
#
# 依赖：本机 Go（>=1.25 即可，会自动拉 1.21 工具链）、Node（编前端）。
set -euo pipefail

GOOS="${1:-windows}"
GOARCH="${2:-amd64}"

# 用 Go 1.21 工具链：1.22+ 已把 Windows 7 移出官方支持，这是最后支持的线。
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.21.13}"
# 直连 GitHub 常被墙，走代理；**不要设 GOSUMDB=off**，否则拉不到工具链。
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
AGENT="$ROOT/apps/agent"
DESKTOP="$ROOT/apps/desktop"

echo "==> 1/4 构建前端（桌面工作台界面）"
cd "$DESKTOP"
if [ ! -d node_modules ]; then
  echo "    npm install ..."
  npm install --silent
fi
npm run build

echo "==> 2/4 把前端产物复制进 Go 源码树（供 go:embed）"
rm -rf "$AGENT/internal/webui/dist"
mkdir -p "$AGENT/internal/webui/dist"
cp -r "$DESKTOP/dist/." "$AGENT/internal/webui/dist/"

echo "==> 3/4 交叉编译单文件（GOOS=$GOOS GOARCH=$GOARCH, 工具链 $GOTOOLCHAIN）"
cd "$AGENT"
mkdir -p dist
OUT="$AGENT/dist/gridwright-$GOOS-$GOARCH.exe"
# CGO off = 纯静态，无运行时依赖；-s -w 去符号表，体积更小
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
  go build -trimpath -ldflags="-s -w" -o "$OUT" ./cmd/gridwright

echo "==> 4/4 校验"
ls -lh "$OUT"
# 确认 PE 子系统版本（6.01 = NT 6.1 = Windows 7 起）
if command -v file >/dev/null 2>&1; then
  file "$OUT" || true
fi

cat <<EOF

打包完成：$OUT

在 Windows 上跑：
  1) 双击它（或命令行）即可，会自动起本地服务
  2) 浏览器打开 http://127.0.0.1:7700  —— **界面就在里面**
  3) 首次会让你选工作区（放表的文件夹）

工作区若已存在，可用环境变量直接指定：
  set WORKSPACE=D:\\你的\\台账文件夹
  gridwright-$GOOS-$GOARCH.exe

说明：
  - 界面已嵌进 exe，不需要额外文件、不需要 WebView2（Win7 可用）
  - 离线可用：看表/体检/联动图/账目都不需要联网；改表要配模型（设置页里填）
  - 随时可换成新版：直接替换 exe，配置与工作区都在外面，不受影响
EOF
