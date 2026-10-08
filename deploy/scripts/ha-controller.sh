#!/usr/bin/env bash
# =====================================================================
# ha-controller.sh — 自动 failover 判定(PG 主库失联 → 提升本机从库 → 切 DNS → 告警)。
#
#   跑在 **从库(aliyun)** 上,由 systemd timer 每 60s 触发 `once`。判据用**两条独立信号**
#   (都不经网关),两者同时为「坏」并连续 N 轮才动手 —— 目的是区分「主库真死」与「网络抖」:
#
#     repl_ok : 本机 pg_stat_wal_receiver.status == streaming(复制在流)
#     ssh_ok  : ssh 得通主库主机(独立于网关的判活通道)
#
#     repl_ok && ssh_ok        → 健康,清零计数
#     repl_ok && !ssh_ok       → 主库 DB 还活着(只是 ssh 探针不通)→ 只告警,不提升
#     !repl_ok && ssh_ok       → 主机活、复制断(多半隧道/网络)→ 只告警,不提升
#     !repl_ok && !ssh_ok      → 候选故障 → 计数 +1;≥ 阈值(默认 3)→ 提升
#
#   提升后**自锁**(写 STATE_FILE),绝不重复触发;旧主恢复**不自动回切**(人工重建,见
#   pg-replica/README.md)。防脑裂:多重判活 + 连续阈值 + 自锁 + 提升前 best-effort fence。
#
# 子命令:
#   once     单轮评估(供 timer 调用)
#   status   打印判据/计数/锁态(不改动)
#   reset    清自锁(人工重建完成后调用)
# 选项:
#   --dry-run         只判定并打印,不提升/不切 DNS/不告警(演练)
#   --force-failover  跳过判据,直接走完整 failover(人工触发/演练)
#
# 环境变量见 deploy/ha.env.example。默认 COMPOSE_DIR=/opt/ai-gateway-v2。
# =====================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
COMPOSE_DIR="${COMPOSE_DIR:-/opt/ai-gateway-v2}"
LOCK_FILE="${LOCK_FILE:-/tmp/gw-ha-controller.lock}"
LOG_FILE="${LOG_FILE:-/var/log/gw-ha-controller.log}"

# 载入配置(ha.env 可覆盖上面默认值)。存在才载。
[ -f "$COMPOSE_DIR/ha.env" ] && { set -a; . "$COMPOSE_DIR/ha.env"; set +a; }

PRIMARY_SSH="${PRIMARY_SSH:-tencent}"
STANDBY_IP="${STANDBY_IP:-}"
GATEWAY_DOMAIN="${GATEWAY_DOMAIN:-gateway.5home.online}"
GATEWAY_API_DOMAIN="${GATEWAY_API_DOMAIN:-gatewayapi.5home.online}"
GW_ROOT_DOMAIN="${GW_ROOT_DOMAIN:-5home.online}"
RECORD_TTL="${RECORD_TTL:-600}"
FAIL_THRESHOLD="${FAIL_THRESHOLD:-3}"
STATE_FILE="${STATE_FILE:-$COMPOSE_DIR/.ha-state}"
DB_SERVICE="${DB_SERVICE:-db}"
PG_USER="${PG_USER:-gw}"
PG_DB="${PG_DB:-gateway}"

DRY=0
FORCE=0

log() { local m="[ha $(date '+%F %T')] $*"; echo "$m"; echo "$m" >>"$LOG_FILE" 2>/dev/null || true; }
notify() {
  local title="$1" body="${2:-}" level="${3:-warn}"
  [ "$DRY" = 1 ] && { echo "(dry-run) notify[$level]: $title | $body"; return 0; }
  [ -x "$SCRIPT_DIR/notify.sh" ] && "$SCRIPT_DIR/notify.sh" "$title" "$body" "$level" || true
}

# ---------- 状态文件 ----------
state_get() { [ -f "$STATE_FILE" ] || return 0; grep -E "^$1=" "$STATE_FILE" | head -1 | cut -d= -f2-; }
state_set() {
  local k="$1" v="$2" tmp; tmp="$(mktemp)"
  [ -f "$STATE_FILE" ] && grep -vE "^$k=" "$STATE_FILE" >"$tmp" 2>/dev/null || true
  printf '%s=%s\n' "$k" "$v" >>"$tmp"; mv "$tmp" "$STATE_FILE"
}

# ---------- 探针 ----------
# 本机从库复制是否在流。db 容器不在(例如已提升)/连不上时 → 非 streaming。
repl_ok() {
  local st
  st="$( cd "$COMPOSE_DIR" && docker compose exec -T "$DB_SERVICE" \
         psql -U "$PG_USER" -d "$PG_DB" -tA -c \
         'select status from pg_stat_wal_receiver;' 2>/dev/null | tr -d '[:space:]' )"
  [ "$st" = "streaming" ]
}
# 主库主机是否可达(独立通道)。
ssh_ok() { ssh -o ConnectTimeout=5 -o BatchMode=yes -o StrictHostKeyChecking=accept-new "$PRIMARY_SSH" 'true' 2>/dev/null; }
# 本机是否仍是从库(提升成功后应为 f)。
local_in_recovery() {
  ( cd "$COMPOSE_DIR" && docker compose exec -T "$DB_SERVICE" \
      psql -U "$PG_USER" -d "$PG_DB" -tA -c 'select pg_is_in_recovery();' 2>/dev/null | tr -d '[:space:]' )
}

# ---------- 提升 + 切 DNS ----------
do_failover() {
  local reason="$1"
  # 必备前提:提升要把流量切到本机,STANDBY_IP 必须已知。
  if [ -z "$STANDBY_IP" ]; then
    log "✗ STANDBY_IP 未设,拒绝 failover(无法切 DNS)"
    notify "HA failover 中止" "STANDBY_IP 未配置,无法切换 DNS" error
    return 1
  fi
  log "开始 failover:$reason"
  notify "HA failover 开始" "$reason;正在提升本机从库" warn

  # best-effort fence:尝试停掉旧主网关(分区时必失败,仅尽力,降低双写窗口)。
  if [ "$DRY" = 0 ]; then
    ssh -o ConnectTimeout=5 -o BatchMode=yes "$PRIMARY_SSH" \
      "cd '$COMPOSE_DIR' && docker compose stop gateway" 2>/dev/null \
      && log "已 fence 旧主网关" || log "fence 旧主网关失败(预期内,继续)"
  else
    echo "(dry-run) 跳过 best-effort fence 旧主网关"
  fi

  if [ "$DRY" = 1 ]; then
    echo "(dry-run) 将执行:pg-replication.sh promote --force"
    echo "(dry-run) 将切 DNS:$GATEWAY_DOMAIN,$GATEWAY_API_DOMAIN → $STANDBY_IP"
    echo "(dry-run) 将写自锁 $STATE_FILE failed_over=1"
    return 0
  fi

  # 复用既有提升脚本(内含 pg_promote + 起网关;守卫由本 controller 的判活替代,故 --force)。
  if ! "$SCRIPT_DIR/pg-replication.sh" promote --force; then
    log "✗ pg-replication.sh promote 失败"
    notify "HA failover 失败" "promote 失败,需人工介入" error
    return 1
  fi
  if [ "$(local_in_recovery)" != "f" ]; then
    log "✗ 提升后本机仍处于 recovery"
    notify "HA failover 失败" "promote 后本机仍是从库,需人工介入" error
    return 1
  fi
  log "本机已提升为可写主库"

  # 切 DNS(两家各改一次);失败不阻断 —— 至少库已提升,人工可补切 DNS。
  local d fail=0
  for d in "$GATEWAY_DOMAIN" "$GATEWAY_API_DOMAIN"; do
    local sub="${d%%.*}"
    if "$SCRIPT_DIR/dnspod.sh" set "$sub" "$STANDBY_IP" "$RECORD_TTL" "$GW_ROOT_DOMAIN"; then
      log "DNS: $d → $STANDBY_IP"
    else
      log "✗ DNS 切换失败: $d"; fail=1
    fi
  done

  state_set failed_over 1
  log "已自锁(failed_over=1)"
  if [ "$fail" = 0 ]; then
    notify "HA failover 完成" "本机(aliyun)已提升为主库,DNS:$GATEWAY_DOMAIN / $GATEWAY_API_DOMAIN → $STANDBY_IP" error
  else
    notify "HA failover 部分完成" "库已提升,但 DNS 切换有失败 —— 请人工核对" error
  fi
  return 0
}

# ---------- once ----------
cmd_once() {
  if [ "$(state_get failed_over)" = 1 ]; then
    log "已自锁(failed_over=1),跳过。人工重建后跑 'ha-controller.sh reset'"
    return 0
  fi

  if [ "$FORCE" = 1 ]; then
    log "--force-failover:跳过判据,直接切换"
    do_failover "人工触发 --force-failover"
    return $?
  fi

  local r=0 s=0
  repl_ok && r=1
  ssh_ok  && s=1

  if [ "$r" = 1 ] && [ "$s" = 1 ]; then
    [ "$DRY" = 1 ] && { echo "(dry-run) 健康:双信号正常"; return 0; }
    if [ "$(state_get fail_count)" != 0 ]; then
      log "恢复正常,清零失败计数"
      state_set fail_count 0
    fi
    return 0
  fi

  if [ "$r" = 1 ] && [ "$s" = 0 ]; then
    log "复制在流,但 ssh 主库不通 → 判为主库 DB 存活,不提升(告警)"
    if [ "$(state_get fail_count)" != 0 ]; then state_set fail_count 0; fi
    [ "$DRY" = 1 ] || notify "HA 探针异常" "本机复制正常但 ssh $PRIMARY_SSH 不通,不提升" warn
    return 0
  fi

  if [ "$r" = 0 ] && [ "$s" = 1 ]; then
    log "主库主机可达,但复制停滞 → 判为隧道/网络问题,不提升(告警)"
    if [ "$(state_get fail_count)" != 0 ]; then state_set fail_count 0; fi
    [ "$DRY" = 1 ] || notify "HA 探针异常" "主库可达但复制停滞,疑似隧道/网络,不提升" warn
    return 0
  fi

  # 两条信号都坏 → 候选故障
  local c=$(( $(state_get fail_count) + 1 ))
  log "双信号失效(repl×, ssh×),失败计数 $c/$FAIL_THRESHOLD"
  if [ "$DRY" = 1 ]; then
    echo "(dry-run) fail_count=$c/$FAIL_THRESHOLD(计数在 dry-run 下不回写)"
  else
    state_set fail_count "$c"
  fi
  if [ "$c" -ge "$FAIL_THRESHOLD" ]; then
    do_failover "主库双信号连续 $c 轮不可用(≥$FAIL_THRESHOLD)"
  else
    [ "$DRY" = 1 ] || notify "HA 疑似主库失联" "第 $c/$FAIL_THRESHOLD 轮,未达阈值不切换" warn
  fi
}

# ---------- status ----------
cmd_status() {
  local r s fc fo
  repl_ok && r=streaming || r="✗非streaming"
  ssh_ok  && s=ok        || s="✗不可达"
  fc="$(state_get fail_count)"; fo="$(state_get failed_over)"
  echo "主库 ssh($PRIMARY_SSH) : $s"
  echo "本地复制状态         : $r"
  echo "本机 in_recovery     : $(local_in_recovery)"
  echo "失败计数 fail_count  : ${fc:-0} / 阈值 $FAIL_THRESHOLD"
  echo "自锁 failed_over     : ${fo:-0}"
  echo "STANDBY_IP           : ${STANDBY_IP:-<未设>}"
  echo "STATE_FILE           : $STATE_FILE"
}

# ---------- reset ----------
cmd_reset() {
  if [ "$(local_in_recovery)" = "f" ]; then
    log "⚠ 本机当前是可写主库(pg_is_in_recovery=f)。若你并未完成人工重建,请勿清除自锁。"
  fi
  state_set failed_over 0
  state_set fail_count 0
  log "已清除自锁与计数。确认旧主已按 pg-replica/README.md 重降级为新从库后方可继续。"
}

usage() { sed -n '4,24p' "$0"; }

# ---------- dispatch ----------
sub="${1:-}"; shift || true
for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    --force-failover) FORCE=1 ;;
    *) echo "未知参数:$a" >&2; usage; exit 2 ;;
  esac
done

# 单例锁(除 status 外)。
case "$sub" in
  once|reset)
    exec 9>"$LOCK_FILE"
    flock -n 9 || { echo "已有 ha-controller 在跑($LOCK_FILE)" >&2; exit 3; }
    ;;
esac

case "$sub" in
  once)   cmd_once ;;
  status) cmd_status ;;
  reset)  cmd_reset ;;
  *)      usage; exit 2 ;;
esac
