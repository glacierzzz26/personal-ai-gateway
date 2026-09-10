#!/usr/bin/env bash
# =====================================================================
# deploy.sh — 生产部署:本地构建 → 组装 bundle → rsync → 远端 compose 拉起。
#
#   - 在服务器只做「COPY 组装」的 docker build(不编译),契合 1.6G 内存小鸡;
#   - 证书首次用 gen-certs.sh 生成后稳定复用(CA 不变则客户端信任不失效);
#   - 远端 .env(GW_MASTER_KEY)与 data/(DB)只在缺失时创建/保留,不会随重部署覆盖。
#
# 前置:本地 ~/.ssh/config 有 aliyun;远端 docker compose v2;安全组 17000-17100 已放行。
# 用法:deploy/scripts/deploy.sh [GW_HOST]
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
HOST="${1:-${GW_HOST:-aliyun}}"
REMOTE_DIR="/opt/ai-gateway"
CERTS_DIR="$REPO/deploy/certs"

echo "==> [1/7] 证书就绪(无则生成)"
[ -f "$CERTS_DIR/ca.crt" ] && [ -f "$CERTS_DIR/admin/fullchain.pem" ] && [ -f "$CERTS_DIR/api/fullchain.pem" ] \
  || bash "$SCRIPT_DIR/gen-certs.sh"

echo "==> [2/7] 前端构建(npm run build)"
( cd "$REPO/web-v2" && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null )

echo "==> [3/7] 网关交叉编译(linux/amd64)"
tmpbin="$(mktemp /tmp/gw-deploy-bin.XXXXXX)"
( cd "$REPO" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o "$tmpbin" ./cmd/gateway )

echo "==> [4/7] 组装 bundle"
TMP="$(mktemp -d /tmp/gw-bundle.XXXXXX)"
trap 'rm -rf "$TMP" "$tmpbin"' EXIT
mkdir -p "$TMP/gateway/web-v2" "$TMP/caddy"
cp "$REPO/deploy/Dockerfile.gateway" "$TMP/gateway/Dockerfile"
cp "$REPO/deploy/config.prod.yaml"   "$TMP/gateway/config.yaml"
cp "$tmpbin"                          "$TMP/gateway/gateway-bin"
cp -r "$REPO/web-v2/dist"             "$TMP/gateway/web-v2/dist"
cp "$REPO/deploy/Caddyfile"           "$TMP/caddy/Caddyfile"
cp -r "$CERTS_DIR"                    "$TMP/caddy/certs"

echo "==> [5/7] 远端目录与密钥准备"
ssh "$HOST" "mkdir -p '$REMOTE_DIR'"
if ssh "$HOST" "test -f '$REMOTE_DIR/.env'"; then
  echo "    远端 .env 已存在(GW_MASTER_KEY 保持不变)"
else
  umask 077
  printf 'GW_MASTER_KEY=%s\n' "$(openssl rand -hex 32)" > "$TMP/.env"
  scp -q "$TMP/.env" "$HOST:$REMOTE_DIR/.env"
  echo "    已生成远端 .env"
fi

echo "==> [6/7] 上传(gateway 构建上下文 + caddy 配置/证书 + compose)"
rsync -az --delete "$TMP/gateway/" "$HOST:$REMOTE_DIR/gateway/"
rsync -az          "$TMP/caddy/"   "$HOST:$REMOTE_DIR/caddy/"
scp -q "$REPO/deploy/docker-compose.yml" "$HOST:$REMOTE_DIR/docker-compose.yml"

echo "==> [7/7] compose up -d --build"
ssh "$HOST" "cd '$REMOTE_DIR' && docker compose up -d --build"

echo
echo "部署完成。"
echo "  数据面  https://47.116.65.140:17080/v1(cert=api)"
echo "  管理台  https://47.116.65.140:17090 (cert=admin)"
echo "  CA      $CERTS_DIR/ca.crt —— 导入信任后两端口均可验真"
