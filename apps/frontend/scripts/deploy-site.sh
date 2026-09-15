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
# SSH 私钥：默认用 ~/.ssh/gw-server.pem，可用 SSH_KEY 环境变量覆盖。
SSH_KEY="${SSH_KEY:-$HOME/.ssh/gw-server.pem}"
if [ -z "$TARGET" ]; then
  echo "用法：bash apps/frontend/scripts/deploy-site.sh <user@host> [远端目录]" >&2
  echo "例：  bash apps/frontend/scripts/deploy-site.sh ubuntu@1.2.3.4 /var/www/gridwright" >&2
  echo "" >&2
  echo "私钥默认读 \$HOME/.ssh/gw-server.pem，可覆盖：SSH_KEY=~/.ssh/other.pem ..." >&2
  exit 1
fi
if [ ! -f "$SSH_KEY" ]; then
  echo "找不到私钥：$SSH_KEY（用 SSH_KEY 指定）" >&2
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
# 产物里必须能查到当前版本号，否则可能是读到了旧 dist。
# 用原生 node 脚本读而不是 node -p：路径里有中文/反斜杠时，命令行内联的字符串
# 转义会炸（Windows 上实测 MODULE_NOT_FOUND），所以把逻辑写成独立文件。
VER="$(node "$FRONTEND/scripts/read-app-version.mjs")"
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
# 不用 rsync：Windows（Git Bash）上没有这个命令，而本项目的开发机就是 Windows。
# 先打包成 tar 再 scp，两端都不需要额外依赖（服务器上的 tar 一定有）。
# 排除项在打包时生效——workspace/ 是运行期数据，绝不能上公网。
TMP_TGZ="$(mktemp -t gw-site-XXXXXX).tgz"
tar czf "$TMP_TGZ" \
  --exclude=workspace \
  --exclude='*.map' \
  -C dist assets index.html favicon.svg icons.svg
echo "    包大小 $(du -h "$TMP_TGZ" | cut -f1)"

REMOTE_TGZ="/tmp/gw-site-$$.tgz"
scp -i "$SSH_KEY" -o StrictHostKeyChecking=accept-new "$TMP_TGZ" "$TARGET:$REMOTE_TGZ"
rm -f "$TMP_TGZ"

# 在服务器上解包。用 sudo：web 根通常归 root（Caddy 以 root 读），
# 而 tar 不会自己清理旧文件——前端资源名带内容哈希，不清就只会越堆越多。
# 解完把属主改回登录用户，这样以后不带 sudo 也能清理（除非要求 root 属主）。
ssh -i "$SSH_KEY" "$TARGET" "
  set -e
  sudo mkdir -p '$REMOTE_DIR'
  sudo find '$REMOTE_DIR' -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  sudo tar xzf '$REMOTE_TGZ' -C '$REMOTE_DIR'
  sudo chown -R \$(id -u):\$(id -g) '$REMOTE_DIR'
  rm -f '$REMOTE_TGZ'
  echo '    远端已更新，文件数：'\$(find '$REMOTE_DIR' -type f | wc -l)
"

cat <<EOF

发布完成：$TARGET:$REMOTE_DIR

如果 Web 服务器还没指向该目录（示例：Caddy，本机服务器当前用的就是它）：

  :80 {
      root * $REMOTE_DIR
      file_server
      try_files {path} /index.html
  }

改完 reload 即可（不停机）：sudo systemctl reload caddy

EOF
