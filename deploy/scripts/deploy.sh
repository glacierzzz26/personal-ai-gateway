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
ssh "$HOST" "cd '$REMOTE_DIR' && docker compose up -d"

echo
echo "部署完成(image=$IMAGE,host=$HOST,dir=$REMOTE_DIR)。"
echo "  数据面  https://192.168.0.202:17080/v1   /  https://47.116.65.140:17080/v1 (frp)"
echo "  管理台  https://192.168.0.202:17090      /  https://47.116.65.140:17090    (frp)"
echo "  CA      $CERTS_DIR/ca.crt —— 导入信任后两端口均可验真"
