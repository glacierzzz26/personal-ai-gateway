#!/usr/bin/env bash
# =====================================================================
# upgrade.sh — 生产网关「一键升级 / 回滚」:按指定版本切换,带备份、健康校验、
#              失败自动回滚、降级护栏。装在目标宿主(生产 = aliyun /opt/ai-gateway-v2/)。
#
# 设计要点(为什么不是「改 .env + compose up」了事):
#   - 升级前**强制**先跑 backup.sh;备份失败即中止(绝不带着未备份的库升级)。
#   - 镜像 ID 幂等:目标 tag 若与当前 image ID 相同,compose 视为 no-op 不会重建容器,
#     此时若还去校验版本会看到旧版本 → 误判失败。故先比 image ID,相同即「已是该版本」。
#   - 健康校验**双重**:① 应答容器 image ID == 目标(防旧容器占着端口冒充);
#     ② /healthz 的 version == 目标。任一不满足即判失败。
#   - 失败自动回滚**仅限同 schema head**:迁移单向,若目标已把 DB 迁移到新版本,
#     退回旧二进制会读不了新库 → 崩溃循环且无法自愈。此时**不自动回滚**,交人工。
#   - .env 只 sed 单行(内含 GW_MASTER_KEY,不可再生),改前先整份备份。
#
# 依赖:docker + docker compose v2 + curl(宿主已有)。不依赖 jq/python/sqlite3。
#
# 用法:
#   upgrade.sh <版本>         升/回滚到指定版本(如 v0.0.0-4de1cde)
#   upgrade.sh --latest       升到 ghcr :latest
#   upgrade.sh --list         列出 ghcr 上可用版本(标记 current / latest)
#   upgrade.sh --rollback     撤销上次升级:换回镜像 **并还原升级前的库快照**(读状态文件)
#   upgrade.sh --force ...    越过降级护栏(--force 位置随意;跨迁移且无配套快照仍拒绝)
#
# 环境变量(缺省即生产值):
#   GW_DIR     compose 工程目录       默认 /opt/ai-gateway-v2
#   GW_IMAGE   ghcr 镜像全名          默认 ghcr.io/glacierzzz26/personal-ai-gateway
#   GW_SERVICE compose 服务名         默认 gateway
#   GW_HEALTH_TIMEOUT 健康校验总超时   默认 150 秒
#
# 退出码:
#   0 成功/幂等(已是该版本)   1 前置条件不满足     2 用法错误
#   3 并发锁被占              4 备份失败            5 目标版本拉取失败
#   6 降级被拒(需 --force)   7 校验失败但已自动回滚 8 校验失败且未回滚(跨 schema,需人工)
#   9 --rollback 无状态记录
# =====================================================================
set -euo pipefail

GW_DIR="${GW_DIR:-/opt/ai-gateway-v2}"
GW_IMAGE="${GW_IMAGE:-ghcr.io/glacierzzz26/personal-ai-gateway}"
GW_SERVICE="${GW_SERVICE:-gateway}"
GW_HEALTH_TIMEOUT="${GW_HEALTH_TIMEOUT:-150}"

COMPOSE_FILE="$GW_DIR/docker-compose.yml"
ENV_FILE="$GW_DIR/.env"
STATE_FILE="$GW_DIR/.upgrade-state"
LOCK_FILE="/tmp/gw-upgrade.lock"

log()  { printf '[upgrade] %s\n' "$*"; }
warn() { printf '[upgrade] ⚠ %s\n' "$*" >&2; }
# die <消息> [退出码]:消息用 "$1"(不是 "$*" —— 否则固定的退出码会被拼进正文)。
die()  { printf '[upgrade] ✗ %s\n' "$1" >&2; exit "${2:-1}"; }

usage() {
  sed -n '3,35p' "$0" | sed 's/^# \{0,1\}//'
}

compose() { ( cd "$GW_DIR" && docker compose "$@" ); }

# ---------- 参数 ----------
# --force 是全局开关,位置随意(upgrade.sh [--force] <ver> | --force --rollback)。
# 先把 --force 摘掉,再解析剩下的动作/版本,避免 --force --rollback 这类组合漏判。
MODE=""; TARGET=""; FORCE=0
args=()
for a in "$@"; do
  if [[ "$a" == "--force" ]]; then FORCE=1; else args+=("$a"); fi
done
set -- ${args[@]+"${args[@]}"}
case "${1:-}" in
  --help|-h|"") MODE=help ;;
  --list)       MODE=list ;;
  --rollback)   MODE=rollback ;;
  --latest)     MODE=version; TARGET="latest" ;;
  -*)           die "未知参数:$1(见 --help)" 2 ;;
  *)            MODE=version; TARGET="$1" ;;
esac
case "$MODE" in
  help) usage; exit 2 ;;
  version) [[ -n "$TARGET" ]] || { usage; exit 2; } ;;
esac

# ---------- 前置 ----------
[[ -f "$COMPOSE_FILE" ]] || die "找不到 $COMPOSE_FILE(设 GW_DIR 指向 compose 目录)" 1
[[ -f "$ENV_FILE" ]]     || die "找不到 $ENV_FILE" 1
docker info >/dev/null 2>&1 || die "docker 不可用" 1

# 读当前运行容器的字段;容器不在则回空。
cur_cid()  { compose ps -q "$GW_SERVICE" 2>/dev/null | head -1; }
cur_field() { # $1 = docker inspect 模板
  local cid; cid="$(cur_cid)"
  [[ -n "$cid" ]] || return 0
  docker inspect -f "$1" "$cid" 2>/dev/null || true
}
# 从容器内探 /healthz(零宿主依赖:镜像自带 busybox wget,compose healthcheck 也用它)。
probe_healthz() {
  compose exec -T "$GW_SERVICE" wget -qO- http://127.0.0.1:8787/healthz 2>/dev/null || true
}
# 从 JSON 串取字段(sed,不依赖 jq):json_field <json> <key>
json_field() { printf '%s' "$1" | sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p"; }

# 当前版本(用于展示/比对):优先容器 APP_VERSION+GIT_SHORT(新格式),退回 /healthz。
current_version() {
  local app short hv
  app="$(cur_field '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^APP_VERSION=//p')"
  short="$(cur_field '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^GIT_SHORT=//p')"
  if [[ -n "$app" && -n "$short" ]]; then printf '%s-%s\n' "$app" "$short"; return; fi
  [[ -n "$app" ]] && { printf '%s\n' "$app"; return; }
  hv="$(json_field "$(probe_healthz)" version)"
  [[ -n "$hv" ]] && { printf '%s\n' "$hv"; return; }
  printf '%s\n' "unknown"
}
# 当前 DB 已应用迁移号:优先 /healthz 的 schema(新格式),否则空(旧格式无法判定)。
current_schema() {
  local s; s="$(json_field "$(probe_healthz)" schema)"
  [[ -n "$s" ]] && { printf '%s\n' "$s"; return; }
  printf '%s\n' ""
}
# 目标镜像的 schema head(读 label;旧格式无此 label → 空)。
image_schema() { # $1 = image ref
  local v; v="$(docker image inspect "$1" --format '{{index .Config.Labels "io.gateway.schema-head"}}' 2>/dev/null || true)"
  [[ "$v" == "<no value>" || "$v" == "0" ]] && v=""
  printf '%s\n' "$v"
}
image_id() { docker image inspect "$1" --format '{{.Id}}' 2>/dev/null || true; }

# 最新一份库快照(backup.sh 刚写的那份即最新)。
newest_backup() { ls -1t "$GW_DIR"/data/backups/gateway-v2-*.db 2>/dev/null | head -1; }
# restore_db <snapshot>:停容器 → 清 WAL/SHM → 换库。容器停着等后面的 compose up 拉起
# (停机窗口与升级本身重叠,不额外增加停机)。WAL/SHM 必须删 —— 它们属于库里那份旧
# 数据,换库后残留会让 SQLite 读到不一致状态。
restore_db() {
  local snap="$1" db="$GW_DIR/data/gateway-v2.db"
  [[ -f "$snap" ]] || return 1
  compose stop "$GW_SERVICE" >/dev/null 2>&1 || true
  rm -f "$db-wal" "$db-shm"
  cp -p "$snap" "$db"
}

# ---------- --list:列 ghcr tag(匿名拉取 token;不依赖 jq) ----------
if [[ "$MODE" == list ]]; then
  repo="${GW_IMAGE#ghcr.io/}"
  tok="$(curl -fsS "https://ghcr.io/token?scope=repository:${repo}:pull&service=ghcr.io" 2>/dev/null \
        | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
  if [[ -z "$tok" ]]; then
    die "ghcr 匿名 token 获取失败(包未 public 或网络不通):$GW_IMAGE" 5
  fi
  cur="$(current_version)"
  log "当前运行:$cur"
  log "ghcr 可用 tag(registry 顺序,**非**时间先后;非发版构建一律 v0.0.0-<sha>,版本号不表新旧):"
  url="https://ghcr.io/v2/${repo}/tags/list?n=1000"
  while [[ -n "$url" ]]; do
    hdr="$(curl -fsS -D - -o /tmp/gw-tags.$$ -H "Authorization: Bearer $tok" "$url" 2>/dev/null || true)"
    # tags/list 是 JSON 数组;逐 tag 抽出打印
    tr ',' '\n' < /tmp/gw-tags.$$ | sed -n 's/.*"\([^"]*\)".*/\1/p' | while read -r t; do
      mark=""
      [[ "$t" == "latest" ]] && mark=" ← latest"
      [[ "$t" == "$cur" ]] && mark="$mark ← 当前"
      printf '  %s%s\n' "$t" "$mark"
    done
    url="$(printf '%s' "$hdr" | sed -n 's/.*Link: *<\([^>]*\)>; *rel="next".*/\1/p')"
  done
  rm -f /tmp/gw-tags.$$
  exit 0
fi

# ---------- 并发锁(独立于任何 updater 的锁) ----------
exec 9>"$LOCK_FILE"
flock -n 9 || die "已有 upgrade.sh 在跑(锁 $LOCK_FILE)" 3

# ---------- 解析目标版本 ----------
# ROLLBACK_BACKUP:--rollback 要一并还原的库快照(升级前那份)。空 = 只换镜像(旧状态文件兼容)。
ROLLBACK_BACKUP=""
if [[ "$MODE" == rollback ]]; then
  [[ -f "$STATE_FILE" ]] || die "--rollback 无状态记录($STATE_FILE);从未用本脚本升级过?" 9
  TARGET="$(sed -n 's/^prev_tag=//p' "$STATE_FILE")"
  [[ -n "$TARGET" ]] || die "状态文件缺 prev_tag,无法回滚" 9
  ROLLBACK_BACKUP="$(sed -n 's/^backup=//p' "$STATE_FILE")"
  log "回滚到升级前版本:$TARGET"
  if [[ -n "$ROLLBACK_BACKUP" && -f "$ROLLBACK_BACKUP" ]]; then
    log "并还原升级前的库快照:$(basename "$ROLLBACK_BACKUP")"
  elif [[ -n "$ROLLBACK_BACKUP" ]]; then
    warn "记录的库快照已不在($ROLLBACK_BACKUP),仅回退镜像;跨迁移回退将不安全。"
    ROLLBACK_BACKUP=""
  else
    warn "状态文件未记录库快照(旧格式),仅回退镜像。"
  fi
fi

PREV_VER="$(current_version)"
PREV_SCHEMA="$(current_schema)"
PREV_REF="$(cur_field '{{.Config.Image}}')"
PREV_IMGID="$(cur_field '{{.Image}}')"
log "当前:$PREV_VER(schema=${PREV_SCHEMA:-未知},image=${PREV_REF:-无})"

# ---------- 拉目标镜像 ----------
log "拉取 $GW_IMAGE:$TARGET ..."
if ! docker pull -q "$GW_IMAGE:$TARGET" >/dev/null; then
  die "拉取失败(版本不存在或网络不通):$GW_IMAGE:$TARGET" 5
fi
TGT_IMGID="$(image_id "$GW_IMAGE:$TARGET")"
TGT_LABEL="$(docker image inspect "$GW_IMAGE:$TARGET" --format '{{index .Config.Labels "org.opencontainers.image.version"}}' 2>/dev/null || true)"
TGT_SCHEMA="$(image_schema "$GW_IMAGE:$TARGET")"
# 目标对外版本号:label 即 lib-version 的 VER(如 v0.0.0-4de1cde);缺失退回 tag。
TGT_VER="$TGT_LABEL"; [[ -z "$TGT_VER" || "$TGT_VER" == "<no value>" ]] && TGT_VER="$TARGET"

# ---------- 幂等:image ID 相同即已是该版本 ----------
# 例外:--rollback 即便镜像 ID 相同(升级只动了库、镜像没换),只要记了库快照就还要还原。
if [[ -n "$PREV_IMGID" && "$PREV_IMGID" == "$TGT_IMGID" && -z "$ROLLBACK_BACKUP" ]]; then
  log "已是该版本($TGT_VER),无需操作。"
  exit 0
fi

# ---------- 降级护栏 ----------
# 迁移单向:目标 schema head < 当前库已应用号 ⇒ 旧二进制读不了新库。
# 未知(旧格式镜像无 label)同样危险 —— 无法证明安全,按「不可验证的降级」处理。
#
# 例外:--rollback 且**将还原配套库快照** —— 快照与目标版本同属一代(升级前那份),
# 还原后库版本与目标二进制匹配,不存在「旧二进制读新库」。故跳过护栏(这正是一次
# 升级失败后唯一正确的救命路径)。仅回退镜像(无快照)时仍需 --force。
if [[ -n "$ROLLBACK_BACKUP" ]]; then
  log "降级护栏:回滚至带配套库快照的目标($TGT_SCHEMA),护栏跳过。"
elif [[ -n "$TGT_SCHEMA" && -n "$PREV_SCHEMA" && "$TGT_SCHEMA" -lt "$PREV_SCHEMA" ]]; then
  if [[ "$FORCE" != 1 ]]; then
    die "拒绝降级:目标 schema head=$TGT_SCHEMA < 当前 DB 已应用=$PREV_SCHEMA。
     迁移单向,旧版本读不了新库。确需降级请加 --force(风险自负)。" 6
  fi
  warn "--force:越过降级护栏(schema $TGT_SCHEMA < $PREV_SCHEMA),DB 可能不兼容!"
elif [[ -n "$PREV_SCHEMA" && -z "$TGT_SCHEMA" ]]; then
  # 当前库已有迁移记录,但目标镜像无 schema 标识 —— 无法证明它认得这个库。
  if [[ "$FORCE" != 1 ]]; then
    die "拒绝:目标镜像无 schema 标识(旧格式),而当前 DB 已应用到迁移 $PREV_SCHEMA;
     无法验证它读不读得了此库。确认安全请加 --force。" 6
  fi
  warn "--force:目标 schema 未知,降级护栏未生效(当前 DB 迁移=$PREV_SCHEMA)。"
elif [[ -z "$PREV_SCHEMA" && -z "$TGT_SCHEMA" ]]; then
  warn "schema 标识缺失(新旧皆旧格式),降级护栏**未生效**。"
fi

# ---------- 备份(强制) ----------
log "升级前备份(backup.sh)..."
if ! REMOTE_DIR="$GW_DIR" bash "$GW_DIR/backup.sh"; then
  die "备份失败,中止升级(不带着未备份的库升级)" 4
fi
# 记住这份快照 = 「升级前那份库」;--rollback 要连同它一起还原(迁移单向)。
PRE_SNAP="$(newest_backup)"
# backup.sh 在无 sqlite3 时会停容器再拷;确认它把网关重新拉起来了。
if [[ -z "$(cur_cid)" ]]; then
  warn "备份后网关未在运行,尝试拉起..."
  compose up -d "$GW_SERVICE" >/dev/null 2>&1 || true
  sleep 2
  [[ -n "$(cur_cid)" ]] || die "备份后网关起不来,中止" 4
fi

# ---------- 记录 prev(供 --rollback) ----------
# prev_tag = 「升级前正在跑的 tag」;backup = 与它配套的那份库快照。失败自动回滚依赖这两个量,
# 故必须保持升级前的值。
#
# --rollback 模式下**不覆写**:此刻 PREV_* 读到的是「升级后的状态」,写进去会把「升级前」
# 覆盖成「升级后」—— 且 PRE_SNAP 是刚给升级后库拍的快照,留着状态文件才能重复 --rollback,
# 每次都回到同一个升级前基线。想再升/再切,重新执行 upgrade.sh <版本>(届时正常更新状态)。
if [[ "$MODE" == rollback ]]; then
  log "回滚模式:保留状态文件(仍指向升级前基线)。"
else
  PREV_TAG_LINE="$(sed -n 's/^GW_IMAGE_TAG=//p' "$ENV_FILE")"
  umask 077
  {
    printf 'prev_tag=%s\n'   "${PREV_TAG_LINE:-}"
    printf 'prev_ref=%s\n'   "${PREV_REF:-}"
    printf 'prev_ver=%s\n'   "$PREV_VER"
    printf 'prev_imgid=%s\n' "${PREV_IMGID:-}"
    printf 'backup=%s\n'     "${PRE_SNAP:-}"
    printf 'ts=%s\n'         "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } > "$STATE_FILE.tmp" && mv "$STATE_FILE.tmp" "$STATE_FILE"
fi

# ---------- 切换 ----------
# --rollback:先把库换回升级前那份(容器停着),再换镜像;两者在随后的 compose up 里
# 一起生效,不额外增加停机。库不还原则跨迁移回退会崩(旧二进制读新库)。
if [[ -n "$ROLLBACK_BACKUP" ]]; then
  log "还原库快照:$(basename "$ROLLBACK_BACKUP")"
  restore_db "$ROLLBACK_BACKUP" || die "库快照还原失败:$ROLLBACK_BACKUP" 8
fi

cp -p "$ENV_FILE" "$ENV_FILE.bak-$(date +%Y%m%d-%H%M%S)"   # 内含 GW_MASTER_KEY,先整份留底
log "切换 .env:GW_IMAGE_TAG=$TARGET"
if grep -q '^GW_IMAGE_TAG=' "$ENV_FILE"; then
  sed -i "s|^GW_IMAGE_TAG=.*|GW_IMAGE_TAG=$TARGET|" "$ENV_FILE"
else
  printf '\nGW_IMAGE_TAG=%s\n' "$TARGET" >> "$ENV_FILE"
fi
# 仓库切换(本地短名 ai-gateway → ghcr 全名):把 GW_IMAGE 也改过来,否则 compose 仍指本地。
if [[ "$PREV_REF" == ai-gateway:* ]]; then
  if grep -q '^GW_IMAGE=' "$ENV_FILE"; then
    sed -i "s|^GW_IMAGE=.*|GW_IMAGE=$GW_IMAGE|" "$ENV_FILE"
  else
    printf 'GW_IMAGE=%s\n' "$GW_IMAGE" >> "$ENV_FILE"
  fi
  log "仓库由本地短名切到 ghcr(GW_IMAGE=$GW_IMAGE)"
fi
chmod 600 "$ENV_FILE"

log "重建容器..."
compose up -d --force-recreate "$GW_SERVICE" >/dev/null

# ---------- 健康校验:image ID + /healthz 版本,轮询 ----------
# $1 = 期望版本串, $2 = 期望 image ID(升级用目标的、回滚用回滚目标的 —— 不可写死)。
# 连续 3 次(~9s)见 exited/dead 才提前判失败;否则轮询到超时(容器 erstwhile 会在
# created→running 间瞬态,单次观测不足以定生死)。
verify_target() {
  local want_ver="$1" want_imgid="$2"
  local deadline=$(( $(date +%s) + GW_HEALTH_TIMEOUT )) cid imgid status body ver dead=0
  while (( $(date +%s) < deadline )); do
    cid="$(cur_cid)"
    if [[ -n "$cid" ]]; then
      imgid="$(docker inspect -f '{{.Image}}' "$cid" 2>/dev/null || true)"
      status="$(docker inspect -f '{{.State.Status}}' "$cid" 2>/dev/null || true)"
      if [[ "$imgid" == "$want_imgid" && "$status" == "running" ]]; then
        dead=0
        body="$(probe_healthz)"
        ver="$(json_field "$body" version)"
        if [[ "$ver" == "$want_ver" && "$(json_field "$body" ok)" == "true" ]]; then
          return 0
        fi
      elif [[ "$status" == "exited" || "$status" == "dead" ]]; then
        dead=$(( dead + 1 ))
        # 容器确实起不来(而非瞬态):连续 3 次即认命,不空等满超时。
        (( dead >= 3 )) && return 1
      else
        dead=0
      fi
    fi
    sleep 3
  done
  return 1
}

capture_logs() { # 失败留证:容器即将被替换,日志会被冲掉
  local f="$GW_DIR/.upgrade-failed-$(date +%Y%m%d-%H%M%S).log"
  compose logs --tail=300 "$GW_SERVICE" > "$f" 2>&1 || true
  printf '%s\n' "$f"
}

if verify_target "$TGT_VER" "$TGT_IMGID"; then
  if [[ "$MODE" == rollback ]]; then
    log "✓ 已回滚到 $TGT_VER"
  else
    log "✓ 已升级到 $TGT_VER(原 $PREV_VER)。回滚:bash $0 --rollback"
  fi
  exit 0
fi

# ---------- 校验失败:回滚 or 交人工 ----------
LOGF="$(capture_logs)"
warn "健康校验失败(目标 $TGT_VER);失败日志:$LOGF"
# --rollback 本身失败:没有「更早的基线」可退(状态文件指的就是升级前)。直接交人工,
# 不去做二次自动回滚(那会拿升级后的 PREV_* 当目标,语义错乱)。
if [[ "$MODE" == rollback ]]; then
  die "回滚后仍不健康(目标 $TGT_VER)!需人工介入。失败日志:$LOGF;备份:$GW_DIR/data/backups/" 8
fi
if [[ -n "$TGT_SCHEMA" && -n "$PREV_SCHEMA" && "$TGT_SCHEMA" -gt "$PREV_SCHEMA" ]]; then
  # 回滚不止换镜像:库已被新版本迁移推进,必须连库一起退回升级前那份快照。
  die "目标已把 DB 迁移推进($PREV_SCHEMA → $TGT_SCHEMA),迁移单向,**不自动回滚**(旧二进制读不了新库会崩溃循环)。
     请人工确认后执行(会一并还原升级前的库快照):
        bash $0 --rollback
     失败日志 $LOGF;备份在 $GW_DIR/data/backups/。" 8
fi

log "回滚到 $PREV_VER(tag=${PREV_TAG_LINE:-?})..."
if [[ -z "$PREV_TAG_LINE" ]]; then
  die "无升级前 tag,无法自动回滚;失败日志 $LOGF" 8
fi
sed -i "s|^GW_IMAGE_TAG=.*|GW_IMAGE_TAG=$PREV_TAG_LINE|" "$ENV_FILE"
[[ "$PREV_REF" == ai-gateway:* ]] && sed -i "s|^GW_IMAGE=.*|GW_IMAGE=ai-gateway|" "$ENV_FILE"
compose up -d --force-recreate "$GW_SERVICE" >/dev/null
# 回滚校验用「回滚目标」的 image ID(而非失败目标的),否则永远比不中。
if verify_target "$PREV_VER" "${PREV_IMGID:-}"; then
  die "已回滚到 $PREV_VER(升级 $TGT_VER 失败);详见 $LOGF" 7
else
  die "回滚后仍不健康!需人工介入。失败日志:$LOGF;备份:$GW_DIR/data/backups/" 8
fi
