#!/usr/bin/env bash
# 把官网（apps/frontend 的静态产物）发布到你的服务器。
#
# 为什么要有这个脚本而不是手敲 rsync：手敲的那条命令没人记得住，
# 而"只发静态产物、绝不带上运行期数据"这件事必须每次都对——
# apps/frontend/dist 里可能混着 workspace/（本机跑出来的交付物），
# 一起推上去就是把你的数据传到了公网。
# 脚本从构建 → 挑文件 → 上传全程负责，并显式排除那些目录。
#
# 用法：
#   bash apps/frontend/scripts/deploy-site.sh <user@host> [远端目录]
#
# 例：
#   bash apps/frontend/scripts/deploy-site.sh root@1.2.3.4 /var/www/gridwright
#
# 需要：本机能 ssh 到目标机（密钥或密码），目标机有 rsync。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
FRONTEND="$ROOT/apps/frontend"

TARGET="${1:-}"
REMOTE_DIR="${2:-}"
if [ -z "$TARGET" ]; then
  echo "用法：bash apps/frontend/scripts/deploy-site.sh <user@host> [远端目录]" >&2
  echo "例：  bash apps/frontend/scripts/deploy-site.sh root@1.2.3.4 /var/www/gridwright" >&2
  exit 1
fi
if [ -z "$REMOTE_DIR" ]; then
  echo "未给远端目录，默认 /var/www/gridwright" >&2
  REMOTE_DIR="/var/www/gridwright"
fi

echo "==> 1/3 构建官网"
cd "$FRONTEND"
[ -d node_modules ] || npm install --silent
npm run build

echo "==> 2/3 校验产物"
# 产物里必须能查到当前版本号，否则可能是读到了旧 dist
VER="$(node -p "require('$ROOT/apps/desktop/package.json').version")"
if ! grep -rq "$VER" dist/assets/*.js 2>/dev/null; then
  echo "构建产物里找不到版本 $VER —— 拒绝发布（可能是旧 dist）" >&2
  exit 1
fi
echo "    产物版本 $VER 已确认"
# workspace/ 是运行期数据（本机跑出来的交付物），绝不能上公网
if [ -d dist/workspace ]; then
  echo "    注意：dist/workspace 存在，将被排除（那是运行期数据）"
fi

echo "==> 3/3 上传到 $TARGET:$REMOTE_DIR"
ssh "$TARGET" "mkdir -p '$REMOTE_DIR'"
rsync -az --delete \
  --exclude 'workspace/' \
  --exclude '*.map' \
  dist/ "$TARGET:$REMOTE_DIR/"

cat <<EOF

发布完成：$TARGET:$REMOTE_DIR

接下来在服务器上确认 Web 服务器指向该目录（示例，nginx）：

  server {
      listen 80;
      server_name 你的域名;
      root $REMOTE_DIR;
      index index.html;
      # 单页应用：所有路径回退到 index.html
      location / { try_files \$uri \$uri/ /index.html; }
  }

EOF
