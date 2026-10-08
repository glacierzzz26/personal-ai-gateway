#!/usr/bin/env bash
# =====================================================================
# dnspod.sh — 读写 DNSPod 的 A 记录(Tencent Cloud TC3 签名,无 SDK)。
#
#   服务于 HA 自动 failover:主库失联时把 gateway/gatewayapi 的 A 记录切到备用机。
#   用 aliyun 上已有的 python3 做签名与请求(宿主无需装 tccli / SDK)。
#
# 子命令:
#   dnspod.sh get <子域> [域名]          打印该子域 A 记录:RecordId/Value/TTL
#   dnspod.sh set <子域> <IP> [ttl] [域名]  把该子域 A 记录指到 <IP>(无记录则创建)
#
# 环境变量:
#   DNSPOD_ENV   凭据文件(默认 /etc/host-infra/dnspod.env),须含 DP_Id/DP_Key
#   GW_ROOT_DOMAIN  根域名(默认 5home.online)
#   RECORD_TTL   目标 TTL(默认 600;**DNSPod 免费版下限 600,小于它会被钳到 600**)
#
# 凭据形态:DP_Id 为 AKID 开头的 SecretId,DP_Key 为 SecretKey(与 host-infra 的 certs.sh 同源)。
#   实测坑:**dp 文件里的值尾部可能带空格 → 必须 strip**;签名串里 canonical headers 之后必须留空行。
# =====================================================================
set -euo pipefail

DNSPOD_ENV="${DNSPOD_ENV:-/etc/host-infra/dnspod.env}"
GW_ROOT_DOMAIN="${GW_ROOT_DOMAIN:-5home.online}"
RECORD_TTL="${RECORD_TTL:-600}"
# DNSPod 免费版路 TTL 下限;任何小于它的请求都会被 API 拒(LimitExceeded.RecordTtlLimit)。
TTL_FLOOR=600

die() { printf '[dnspod] %s\n' "$*" >&2; exit 1; }

# 载入凭据(去行尾 CR/空白;set -a 让变量导出到 python)。
load_creds() {
  [ -f "$DNSPOD_ENV" ] || die "找不到凭据文件:$DNSPOD_ENV"
  # 只取需要的两行,剥掉尾部空白与 CR。
  local dpid dpkey
  dpid="$(grep -E '^DP_Id=' "$DNSPOD_ENV" | head -1 | cut -d= -f2- | tr -d '[:space:]')"
  dpkey="$(grep -E '^DP_Key=' "$DNSPOD_ENV" | head -1 | cut -d= -f2- | tr -d '[:space:]')"
  [ -n "$dpid" ] || die "$DNSPOD_ENV 缺 DP_Id"
  [ -n "$dpkey" ] || die "$DNSPOD_ENV 缺 DP_Key"
  export DP_Id="$dpid" DP_Key="$dpkey"
}

# tc3 <action> <json-payload> → stdout 为 API 响应 JSON。
tc3() {
  local action="$1" payload="$2"
  python3 - "$action" "$payload" <<'PY'
import sys, os, json, hmac, hashlib, time, urllib.request

action, payload = sys.argv[1], sys.argv[2]
sid  = os.environ["DP_Id"].strip()
skey = os.environ["DP_Key"].strip()
host, service, version = "dnspod.tencentcloudapi.com", "dnspod", "2021-03-23"
ts = str(int(time.time()))
date = time.strftime("%Y-%m-%d", time.gmtime(int(ts)))
ct = "application/json; charset=utf-8"

# TC3-HMAC-SHA256:canonical request 的 headers 段之后必须有【空行】再跟 signed headers。
canon_headers = f"content-type:{ct}\nhost:{host}\n"
signed_headers = "content-type;host"
hashed_payload = hashlib.sha256(payload.encode()).hexdigest()
canonical = f"POST\n/\n\n{canon_headers}\n{signed_headers}\n{hashed_payload}"

scope = f"{date}/{service}/tc3_request"
string_to_sign = ("TC3-HMAC-SHA256\n" + ts + "\n" + scope + "\n"
                  + hashlib.sha256(canonical.encode()).hexdigest())

def _hmac(key, msg):
    return hmac.new(key, msg.encode(), hashlib.sha256).digest()

secret_date    = _hmac(("TC3" + skey).encode(), date)
secret_service = _hmac(secret_date, service)
secret_signing = _hmac(secret_service, "tc3_request")
signature = hmac.new(secret_signing, string_to_sign.encode(), hashlib.sha256).hexdigest()

auth = (f"TC3-HMAC-SHA256 Credential={sid}/{scope}, "
        f"SignedHeaders={signed_headers}, Signature={signature}")
req = urllib.request.Request(
    "https://" + host + "/", data=payload.encode(),
    headers={"Authorization": auth, "Content-Type": ct, "Host": host,
             "X-TC-Action": action, "X-TC-Timestamp": ts,
             "X-TC-Version": version, "X-TC-Region": ""})
try:
    print(urllib.request.urlopen(req, timeout=15).read().decode())
except urllib.error.HTTPError as e:
    sys.stderr.write(e.read().decode() + "\n")
    sys.exit(1)
PY
}

# 从响应里挑出错误(Response.Error 存在即失败,打印到 stderr 并退出非零)。
check_resp() {
  local resp="$1"
  local err; err="$(printf '%s' "$resp" | jq -r '.Response.Error.Code // empty')"
  if [ -n "$err" ]; then
    printf '[dnspod] API 错误:%s\n' "$(printf '%s' "$resp" | jq -c '.Response.Error')" >&2
    return 1
  fi
}

cmd_get() {
  local sub="${1:?用法: dnspod.sh get <子域> [域名]}" domain="${2:-$GW_ROOT_DOMAIN}"
  load_creds
  local payload resp
  payload="$(jq -cn --arg d "$domain" --arg s "$sub" '{Domain:$d, Subdomain:$s, RecordType:"A"}')"
  resp="$(tc3 DescribeRecordList "$payload")"
  check_resp "$resp" || exit 1
  printf '%s' "$resp" | jq -r '.Response.RecordList[]? | "\(.RecordId)\t\(.Name)\t\(.Value)\tTTL=\(.TTL)"'
}

cmd_set() {
  local sub="${1:?用法: dnspod.sh set <子域> <IP> [ttl] [域名]}"
  local ip="${2:?缺 IP}" ttl="${3:-$RECORD_TTL}" domain="${4:-$GW_ROOT_DOMAIN}"
  [ "$ttl" -ge "$TTL_FLOOR" ] 2>/dev/null || { echo "[dnspod] TTL $ttl 小于下限 $TTL_FLOOR,钳到 $TTL_FLOOR" >&2; ttl="$TTL_FLOOR"; }
  load_creds

  local payload resp rid
  payload="$(jq -cn --arg d "$domain" --arg s "$sub" '{Domain:$d, Subdomain:$s, RecordType:"A"}')"
  resp="$(tc3 DescribeRecordList "$payload")"
  check_resp "$resp" || exit 1
  rid="$(printf '%s' "$resp" | jq -r '.Response.RecordList[0].RecordId // empty')"

  if [ -n "$rid" ]; then
    payload="$(jq -cn --arg d "$domain" --argjson id "$rid" --arg s "$sub" --arg v "$ip" --argjson ttl "$ttl" \
      '{Domain:$d, RecordId:$id, Subdomain:$s, RecordType:"A", RecordLine:"默认", Value:$v, TTL:$ttl}')"
    resp="$(tc3 ModifyRecord "$payload")"
  else
    payload="$(jq -cn --arg d "$domain" --arg s "$sub" --arg v "$ip" --argjson ttl "$ttl" \
      '{Domain:$d, Subdomain:$s, RecordType:"A", RecordLine:"默认", Value:$v, TTL:$ttl}')"
    resp="$(tc3 CreateRecord "$payload")"
  fi
  check_resp "$resp" || exit 1
  printf '[dnspod] %s.%s → %s (TTL=%s) 已生效\n' "$sub" "$domain" "$ip" "$ttl"
}

case "${1:-}" in
  get) shift; cmd_get "$@" ;;
  set) shift; cmd_set "$@" ;;
  *) sed -n '4,16p' "$0"; exit 2 ;;
esac
