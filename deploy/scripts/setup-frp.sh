#!/usr/bin/env bash
# =====================================================================
# setup-frp.sh — 在目标(LAN)主机上架设 frpc 隧道:把本机 17080/17090 经云 frps 暴露到公网。
#
#   拓扑:公网 47.116.65.140:17080/17090 → frps → frpc(目标主机)→ 127.0.0.1:17080/17090(Go 网关 TLS 口)
#
#   为什么用 docker 容器跑 frpc(而非 systemd):目标主机用户无 sudo,装不了系统服务。
#   `--network host` + `--restart unless-stopped` 免特权绑定、随 docker 自启恢复。
#
#   token / serverAddr 取自本地 ~/frp/frpc.toml(连接块单一来源),不落仓库。
#
# 用法:
#   setup-frp.sh [GW_HOST]       默认 rguo@192.168.0.202;架设/更新隧道
#   setup-frp.sh status          查看目标主机隧道容器状态
#   setup-frp.sh stop            停掉并移除隧道容器
# =====================================================================
set -euo pipefail

FRP_DIR="${FRP_DIR:-$HOME/frp}"
FRPC_BIN="${FRPC_BIN:-$FRP_DIR/frpc}"
MAIN_CFG="${MAIN_CFG:-$FRP_DIR/frpc.toml}"
FRPC_VER="${FRPC_VER:-0.71.0}"
FRPC_IMAGE="${FRPC_IMAGE:-frpc:$FRPC_VER}"
CONTAINER="frpc-gateway"

cmd="${1:-up}"
HOST="${2:-${GW_HOST:-rguo@192.168.0.202}}"

[ -x "$FRPC_BIN" ] || { echo "找不到可执行 frpc:$FRPC_BIN" >&2; exit 1; }
[ -f "$MAIN_CFG" ] || { echo "找不到主配置:$MAIN_CFG" >&2; exit 1; }

RHOME="$(ssh "$HOST" 'printf %s "$HOME"')"
REMOTE_DIR="${REMOTE_DIR:-$RHOME/ai-gateway}"
REMOTE_CFG="$REMOTE_DIR/frpc.gateway.toml"

case "$cmd" in
  status)
    ssh "$HOST" "docker ps -a --filter name=^/$CONTAINER --format '{{.Names}}\t{{.Status}}\t{{.Ports}}'"; exit 0 ;;
  stop)
    ssh "$HOST" "docker rm -f $CONTAINER" && echo "已停掉 $CONTAINER"; exit 0 ;;
esac

# —— 生成 frpc 配置:连接块原样截取主配置(serverAddr/token/tls 单一来源)+ 两条代理 ——
server_addr="$(grep -m1 '^serverAddr' "$MAIN_CFG" | sed -E 's/.*"([^"]+)".*/\1/')"
tmpcfg="$(mktemp)"; tmpctx="$(mktemp -d)"
trap 'rm -f "$tmpcfg"; rm -rf "$tmpctx"' EXIT

awk '/^\[\[proxies\]\]/{exit} {print}' "$MAIN_CFG" > "$tmpcfg"
cat >> "$tmpcfg" <<EOF

# 网关数据面:公网 ${server_addr}:17080 → 本机 127.0.0.1:17080(Go 自终止 TLS)
[[proxies]]
name = "gateway-api"
type = "tcp"
localIP = "127.0.0.1"
localPort = 17080
remotePort = 17080

# 网关管理台:公网 ${server_addr}:17090 → 本机 127.0.0.1:17090
[[proxies]]
name = "gateway-admin"
type = "tcp"
localIP = "127.0.0.1"
localPort = 17090
remotePort = 17090
EOF
chmod 600 "$tmpcfg"

# —— 本地打 frpc 镜像(基镜像 alpine 本地已有,目标主机无需外网)——
echo "==> [1/4] 构建 frpc 镜像 $FRPC_IMAGE"
cp "$FRPC_BIN" "$tmpctx/frpc"
cat > "$tmpctx/Dockerfile" <<'DOCKER'
FROM alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates
COPY frpc /usr/local/bin/frpc
ENTRYPOINT ["/usr/local/bin/frpc"]
DOCKER
docker build -t "$FRPC_IMAGE" "$tmpctx" >/dev/null

echo "==> [2/4] 推送 frpc 镜像到 $HOST"
docker save "$FRPC_IMAGE" | ssh "$HOST" docker load

echo "==> [3/4] 上传隧道配置($REMOTE_CFG)"
ssh "$HOST" "mkdir -p '$REMOTE_DIR'"
scp -q "$tmpcfg" "$HOST:$REMOTE_CFG"

echo "==> [4/4] (重)起隧道容器"
ssh "$HOST" "docker rm -f $CONTAINER >/dev/null 2>&1 || true; \
  docker run -d --name $CONTAINER --restart unless-stopped --network host \
    -v '$REMOTE_CFG:/etc/frp/frpc.toml:ro' '$FRPC_IMAGE' -c /etc/frp/frpc.toml >/dev/null; \
  sleep 2; docker ps --filter name=^/$CONTAINER --format '  {{.Names}}  {{.Status}}'"

echo "隧道就绪:公网 https://${server_addr}:17080(数据面) / :17090(管理台)→ $HOST"
