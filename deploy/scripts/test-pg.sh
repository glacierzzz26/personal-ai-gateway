#!/usr/bin/env bash
# =====================================================================
# test-pg.sh — 本地跑全套测试所需的 PostgreSQL。
#
#   起一次性 postgres:16 容器(随机空闲端口),等就绪,导出 TEST_PG_DSN,
#   跑 `go test ./...`,结束即销毁容器(EXIT trap,含 Ctrl-C)。库数据不留存
#   —— 每个测试各自建独立 schema,跑完 DROP。
#
#   未设 TEST_PG_DSN 时各 DB 测试会 t.Skip(见 internal/pgtest);本脚本把它设好,
#   让整条套件真跑在 PG 上(方言、RETURNING、identity 序列都被真实校验)。
#
# 用法:
#   deploy/scripts/test-pg.sh                 # 跑全部
#   deploy/scripts/test-pg.sh ./internal/store/...   # 只跑某包(透传给 go test)
# =====================================================================
set -euo pipefail

IMG="${PG_TEST_IMAGE:-postgres:16-alpine}"
PORT="${PG_TEST_PORT:-}"
NAME="gw-pg-test-$$"

pick_port() {
  if [[ -n "$PORT" ]]; then echo "$PORT"; return; fi
  # 让内核挑一个空闲端口(绑 0 后读回),避免与本地 dev PG 撞口。
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}
PORT="$(pick_port)"

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== 启动测试 PG: $NAME ($IMG) @ 127.0.0.1:$PORT" >&2
docker run -d --rm --name "$NAME" \
  -e POSTGRES_USER=gw -e POSTGRES_PASSWORD=gw -e POSTGRES_DB=gateway \
  -p "127.0.0.1:$PORT:5432" "$IMG" >/dev/null

echo -n "== 等待 PG 就绪" >&2
for _ in $(seq 1 120); do
  if docker exec "$NAME" pg_isready -U gw -d gateway >/dev/null 2>&1; then
    echo " ok" >&2
    break
  fi
  echo -n "." >&2
  sleep 0.5
done
docker exec "$NAME" pg_isready -U gw -d gateway >/dev/null 2>&1 || { echo " PG 未就绪" >&2; exit 1; }

export TEST_PG_DSN="postgres://gw:gw@127.0.0.1:$PORT/gateway?sslmode=disable"
echo "== TEST_PG_DSN=$TEST_PG_DSN" >&2

go test "$@" ./...
