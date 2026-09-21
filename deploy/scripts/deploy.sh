#!/usr/bin/env bash
# =====================================================================
# deploy.sh — 生产部署:本地构建镜像 → docker save 经 ssh 推送到目标主机 → 远端 compose up。
#
#   - 目标主机只需 docker + docker compose v2,不需要 Go/Node/Docker Hub;
#     镜像在本地构建好,`docker save | ssh docker load` 过去。
#   - 证书首次用 gen-certs.sh 生成后稳定复用(CA 不变则客户端信任不失效);
#   - 远端 .env(GW_MASTER_KEY / GW_IMAGE_TAG)与 data/(DB)只在缺失时创建,不随重部署覆盖。
#
# 前置:目标主机可免密 ssh;远端 docker compose v2;17080/17090 空闲。
# 用法:deploy/scripts/deploy.sh [GW_HOST]         (默认 rguo@192.168.0.202)
#       REMOTE_DIR=/path deploy.sh [GW_HOST]       (覆盖远端目录,默认 <远端家目录>/ai-gateway)
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
HOST="${1:-${GW_HOST:-rguo@192.168.0.202}}"
CERTS_DIR="$REPO/deploy/certs"

echo "==> [1/6] 证书就绪(无则生成)"
[ -f "$CERTS_DIR/ca.crt" ] && [ -f "$CERTS_DIR/admin/fullchain.pem" ] && [ -f "$CERTS_DIR/api/fullchain.pem" ] \
  || bash "$SCRIPT_DIR/gen-certs.sh"

echo "==> [2/6] 构建镜像"
VER="$(cd "$REPO" && git describe --tags --always --dirty)"
VER="$VER" bash "$SCRIPT_DIR/build.sh"
IMAGE="ai-gateway:$VER"

echo "==> [3/6] 远端目录准备"
RHOME="$(ssh "$HOST" 'printf %s "$HOME"')"
REMOTE_DIR="${REMOTE_DIR:-$RHOME/ai-gateway}"
ssh "$HOST" "mkdir -p '$REMOTE_DIR'"

if ssh "$HOST" "test -f '$REMOTE_DIR/.env'"; then
  echo "    远端 .env 已存在(GW_MASTER_KEY 保持不变,更新 GW_IMAGE_TAG)"
  ssh "$HOST" "grep -q '^GW_IMAGE_TAG=' '$REMOTE_DIR/.env' || printf 'GW_IMAGE_TAG=latest\n' >> '$REMOTE_DIR/.env'; sed -i 's|^GW_IMAGE_TAG=.*|GW_IMAGE_TAG=$VER|' '$REMOTE_DIR/.env'"
else
  echo "    生成远端 .env(新主密钥)"
  umask 077
  TMPENV="$(mktemp)"
  trap 'rm -f "$TMPENV"' EXIT
  printf 'GW_MASTER_KEY=%s\nGW_IMAGE_TAG=%s\n' "$(openssl rand -hex 32)" "$VER" > "$TMPENV"
  scp -q "$TMPENV" "$HOST:$REMOTE_DIR/.env"
fi

echo "==> [4/6] 推送镜像(docker save | ssh docker load)"
docker save "$IMAGE" | ssh "$HOST" docker load

echo "==> [5/6] 上传编排与证书"
scp -q "$REPO/deploy/docker-compose.yml" "$HOST:$REMOTE_DIR/docker-compose.yml"
rsync -az --delete "$CERTS_DIR/" "$HOST:$REMOTE_DIR/certs/"

echo "==> [6/6] 远端 compose up -d"
# 显式覆盖 GW_IMAGE 为本地短名 —— compose 缺省指向 ghcr,而本条应急链路推的是
# docker save 过去的本地镜像 ai-gateway:$VER,不能让 compose 去 ghcr 拉。
ssh "$HOST" "cd '$REMOTE_DIR' && GW_IMAGE=ai-gateway docker compose up -d"

echo
echo "部署完成(image=$IMAGE,host=$HOST,dir=$REMOTE_DIR)。"

# 清理构建/部署产物:本机的 ai-gateway 镜像与 lab 上非运行中的旧版本镜像。
# 刚部署的版本在运行,必然被跳过。留产物排查用 KEEP_ARTIFACTS=1 关掉。
# 清理是尽力而为 —— 失败也不影响本次部署结果。
if [ "${KEEP_ARTIFACTS:-0}" = "1" ] || [ "${NO_CLEAN:-0}" = "1" ]; then
  echo "跳过产物清理(KEEP_ARTIFACTS/NO_CLEAN 已置位)。"
else
  echo
  echo "==> 清理构建产物"
  CLEAN_SH="$HOME/.claude/skills/clean-docker-artifacts/scripts/clean.sh"
  if [ -x "$CLEAN_SH" ]; then
    bash "$CLEAN_SH" --host "$HOST" || true
  else
    echo "    未找到清理脚本($CLEAN_SH),跳过。"
  fi
fi
echo "  数据面  https://5home.online:17080/v1  (域名,公信证书)  /  旧 IP https://47.116.65.140:17080/v1 (自签)"
echo "  管理台  https://5home.online           (域名,公信证书)  /  旧 IP https://47.116.65.140:17090"
echo "  自签CA  $CERTS_DIR/ca.crt —— 仅旧 IP 入口/回源用;域名入口无需导入"
echo "  边缘    若首次上域名:deploy/scripts/setup-edge.sh(生产主机一次性建 Nginx + 公信证书)"
