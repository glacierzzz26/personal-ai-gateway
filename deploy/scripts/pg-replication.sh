#!/usr/bin/env bash
# =====================================================================
# pg-replication.sh — 双云 PostgreSQL 流复制的运维脚本(主/从/提升/核验)。
#
#   拓扑:tencent = 主(生产);aliyun = 从(热备,只读,网关停用)。
#   从库经 SSH 隧道(aliyun→tencent)连主库 loopback:5433,PG 不暴露公网。
#   资产(隧道单元 + 两份 compose override)在 deploy/pg-replica/,本脚本据之作部署。
#
# 子命令(在【对应主机】上跑;见 deploy/pg-replica/README.md):
#   primary   在【主库】上幂等配置:复制角色 repl + 物理槽 + pg_hba 放行 + WAL 安全阀
#   standby   在【从库】上装隧道 + pg_basebackup 重建 standby(破坏性,需 --yes)
#   status    打印本机视角的复制状态(主/从都能跑)
#   promote   在【从库】上把只读从提升为可写(故障切换;含防脑裂守卫,需 --force 跳过)
#
# 环境变量(默认值见下):
#   COMPOSE_DIR(/opt/ai-gateway-v2) DB_SERVICE(db) PG_USER(gw) PG_DB(gateway)
#   PRIMARY_SSH(tencent)  SLOT(aliyun)  TUNNEL_PORT(15432)  PRIMARY_PORT(5433)
#   REPL_USER(repl)  REPL_PASS(缺省读 $COMPOSE_DIR/.env 的 GW_REPL_PASSWORD)
#   ASSET_DIR(deploy/pg-replica;含 gw-pg-tunnel.service 及两份 override 模板)
# =====================================================================
set -euo pipefail

COMPOSE_DIR="${COMPOSE_DIR:-/opt/ai-gateway-v2}"
DB_SERVICE="${GW_DB_SERVICE:-db}"
PG_USER="${GW_PG_USER:-gw}"
PG_DB="${GW_PG_DB:-gateway}"
PRIMARY_SSH="${PRIMARY_SSH:-tencent}"
SLOT="${SLOT:-aliyun}"
TUNNEL_PORT="${TUNNEL_PORT:-15432}"
PRIMARY_PORT="${PRIMARY_PORT:-5433}"
REPL_USER="${REPL_USER:-repl}"
# 脚本默认从仓库部署目录取资产;从 repo 跑时自动定位 deploy/pg-replica。
_default_assets="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/../pg-replica" 2>/dev/null && pwd || true)"
ASSET_DIR="${ASSET_DIR:-$_default_assets}"
UNIT_NAME="gw-pg-tunnel"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# psql 经 compose 在 db 容器内执行(stdin 作为 SQL)。
psqlq()  { ( cd "$COMPOSE_DIR" && docker compose exec -T "$DB_SERVICE" psql -U "$PG_USER" -d "$PG_DB" -tA -v ON_ERROR_STOP=1 ); }
psqlx()  { ( cd "$COMPOSE_DIR" && docker compose exec -T "$DB_SERVICE" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 "$@" ); }

read_repl_pass() {
  if [ -n "${REPL_PASS:-}" ]; then return; fi
  REPL_PASS="$(grep -E '^GW_REPL_PASSWORD=' "$COMPOSE_DIR/.env" 2>/dev/null | head -1 | cut -d= -f2- | tr -d '\r')"
  [ -n "$REPL_PASS" ] || die "未取到复制口令:设 REPL_PASS=… 或在 $COMPOSE_DIR/.env 写 GW_REPL_PASSWORD"
}

require_assets() {
  [ -n "$ASSET_DIR" ] && [ -d "$ASSET_DIR" ] || die "找不到资产目录 ASSET_DIR(含 gw-pg-tunnel.service);请设 ASSET_DIR=/path/to/deploy/pg-replica"
}

# ---------------------------------------------------------------- primary
cmd_primary() {
  read_repl_pass
  log "配置主库:复制角色 $REPL_USER + 物理槽 $SLOT + pg_hba 放行 + WAL 安全阀"

  # 1) 复制角色(幂等:不存在才建,存在则仅刷新口令/属性)
  psqlq <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$REPL_USER') THEN
    CREATE ROLE $REPL_USER LOGIN REPLICATION;
  END IF;
END \$\$;
ALTER ROLE $REPL_USER WITH LOGIN REPLICATION PASSWORD '$REPL_PASS';
SQL
  log "角色 $REPL_USER 就绪"

  # 2) 物理复制槽(幂等)
  if [ "$(psqlq <<< "SELECT 1 FROM pg_replication_slots WHERE slot_name='$SLOT';")" = "1" ]; then
    log "物理槽 $SLOT 已存在,跳过"
  else
    psqlx -c "SELECT pg_create_physical_replication_slot('$SLOT');" >/dev/null
    log "物理槽 $SLOT 已创建"
  fi

  # 3) pg_hba:放行 docker 网段(172.16.0.0/12)的复制连接。直接改 bind-mount 的宿主文件。
  local hba="$COMPOSE_DIR/pgdata/pg_hba.conf"
  local line="host    replication     all             172.16.0.0/12            scram-sha-256"
  [ -f "$hba" ] || die "找不到 $hba(先起过 db 容器初始化数据目录?)"
  if grep -qF "172.16.0.0/12" "$hba"; then
    log "pg_hba 已含 172.16.0.0/12 放行,跳过"
  else
    printf '\n# 双云流复制:放行从库经隧道(docker 网段)回连\n%s\n' "$line" >> "$hba"
    psqlx -c "SELECT pg_reload_conf();" >/dev/null
    log "pg_hba 已追加复制放行并 reload"
  fi

  # 4) WAL 安全阀:从库长期失联时,槽最多积压 512MB WAL 即作废(防主库磁盘被撑爆)。
  psqlx -c "ALTER SYSTEM SET max_slot_wal_keep_size='512MB';" >/dev/null
  psqlx -c "SELECT pg_reload_conf();" >/dev/null
  log "max_slot_wal_keep_size=512MB 已设"

  warn "别忘了在主库宿主放 compose-primary.override.yml 为 docker-compose.override.yml(发布 loopback:$PRIMARY_PORT)。"
  cmd_status
}

# ---------------------------------------------------------------- standby
cmd_standby() {
  require_assets
  read_repl_pass
  local yes=""
  for a in "$@"; do [ "$a" = "--yes" ] && yes=1; done
  [ -n "$yes" ] || die "standby 会清空 $COMPOSE_DIR/pgdata —— 确认后加 --yes 重跑"

  log "1/5 安装并启用隧道单元 $UNIT_NAME(→ $PRIMARY_SSH 的 PG)"
  install -m 644 "$ASSET_DIR/gw-pg-tunnel.service" "/etc/systemd/system/${UNIT_NAME}.service"
  systemctl daemon-reload
  systemctl enable --now "${UNIT_NAME}.service"
  systemctl is-active "${UNIT_NAME}.service"
  sleep 1

  log "2/5 停本机容器,清空 pgdata"
  ( cd "$COMPOSE_DIR" && docker compose stop gateway "$DB_SERVICE" || true )
  rm -rf "${COMPOSE_DIR:?}/pgdata"/* "${COMPOSE_DIR:?}/pgdata"/.[!.]* 2>/dev/null || true

  log "3/5 pg_basebackup 拉基线(经隧道 host.docker.internal:$TUNNEL_PORT)"
  docker run --rm --add-host host.docker.internal:host-gateway \
    -e PGPASSWORD="$REPL_PASS" \
    -v "$COMPOSE_DIR/pgdata:/var/lib/postgresql/data" \
    postgres:16-alpine \
    pg_basebackup -h host.docker.internal -p "$TUNNEL_PORT" -U "$REPL_USER" \
      -D /var/lib/postgresql/data -Fp -Xs -R -P -S "$SLOT"

  log "4/5 覆写连接串为已知良好值(host.docker.internal / 槽名)"
  local auto="$COMPOSE_DIR/pgdata/postgresql.auto.conf"
  # 去掉 pg_basebackup 写的 primary_conninfo/primary_slot_name,再写死本环境的值。
  grep -vE '^[[:space:]]*(primary_conninfo|primary_slot_name)' "$auto" > "$auto.tmp" 2>/dev/null || : > "$auto.tmp"
  cat >> "$auto.tmp" <<EOF
primary_conninfo = 'user=$REPL_USER password=$REPL_PASS host=host.docker.internal port=$TUNNEL_PORT sslmode=disable'
primary_slot_name = '$SLOT'
EOF
  mv "$auto.tmp" "$auto"
  touch "$COMPOSE_DIR/pgdata/standby.signal"

  log "5/5 起 db(只起 db,不起 gateway)"
  install -m 644 "$ASSET_DIR/compose-standby.override.yml" "$COMPOSE_DIR/docker-compose.override.yml"
  ( cd "$COMPOSE_DIR" && docker compose up -d "$DB_SERVICE" )
  sleep 3
  cmd_status
}

# ---------------------------------------------------------------- status
cmd_status() {
  echo "---- 本机是否从库(recovery) ----"
  psqlx -c "SELECT pg_is_in_recovery() AS in_recovery, current_setting('transaction_read_only') AS read_only;"
  if [ "$(psqlq <<< 'SELECT pg_is_in_recovery();')" = "t" ]; then
    echo "---- 从库:接收/回放位点 ----"
    psqlx -c "SELECT status, flushed_lsn, latest_end_lsn FROM pg_stat_wal_receiver;"
    psqlx -c "SELECT pg_last_wal_replay_lsn() AS replay_lsn, pg_last_xact_replay_timestamp() AS last_replay_ts;"
  else
    echo "---- 主库:槽 + 下游 ----"
    psqlx -c "SELECT slot_name, active, wal_status FROM pg_replication_slots;"
    psqlx -c "SELECT client_addr, state, sync_state, pg_wal_lsn_diff(sent_lsn, replay_lsn) AS lag_bytes FROM pg_stat_replication;"
  fi
}

# ---------------------------------------------------------------- promote
cmd_promote() {
  local force=""
  for a in "$@"; do [ "$a" = "--force" ] && force=1; done

  if [ -z "$force" ]; then
    log "防脑裂守卫:确认旧主 $PRIMARY_SSH 不可达…"
    if ssh -o ConnectTimeout=5 -o BatchMode=yes "$PRIMARY_SSH" 'docker ps >/dev/null 2>&1'; then
      die "旧主 $PRIMARY_SSH 仍可达 —— 提升会脑裂。确认它已下线,或用 --force 强制。"
    fi
    log "旧主不可达,继续"
  fi

  log "提升从库为可写(pg_promote)"
  psqlx -c "SELECT pg_promote(true, 60);"
  # 等它真的转正
  for i in $(seq 1 30); do
    [ "$(psqlq <<< 'SELECT pg_is_in_recovery();')" = "f" ] && break
    sleep 1
  done
  [ "$(psqlq <<< 'SELECT pg_is_in_recovery();')" = "f" ] || die "提升超时,仍处于 recovery"

  log "起网关(从库已转正)"
  ( cd "$COMPOSE_DIR" && docker compose up -d gateway )
  warn "紧接着切 DNS:gateway / gatewayapi 的 A 记录 → 本机公网 IP(见 deploy/HA.md §5.1)。"
  warn "旧主恢复后【不可直接重启】,须 pg_rewind 或重做 pg_basebackup 降级为新从库,否则脑裂。"
}

# ---------------------------------------------------------------- dispatch
case "${1:-}" in
  primary) shift; cmd_primary "$@" ;;
  standby) shift; cmd_standby "$@" ;;
  status)  shift; cmd_status  "$@" ;;
  promote) shift; cmd_promote "$@" ;;
  *) sed -n '2,20p' "$0"; exit 2 ;;
esac
