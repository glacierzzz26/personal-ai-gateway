#!/usr/bin/env bash
# =====================================================================
# gen-certs.sh — 本地生成生产自签证书:一个私有 CA + admin/api 两张独立叶证书。
#
#  - admin 证书挂管理台(17090),api 证书挂数据面(17080);两张叶子 key 完全不同,
#    满足「管理/API 不得共用同一套证书」。
#  - 共用同一私域 CA → 浏览器/客户端只导入一次 ca.crt,两端口都信任。
#  - SAN = 公网 IP + 127.0.0.1(公网直连与 ssh -L 本地验证都能验)。
#
# 用法:
#   gen-certs.sh             默认输出到 deploy/certs,已存在则跳过
#   FORCE=1 gen-certs.sh     重新轮换(CA 一并换 → 所有客户端需重新信任)
#   GW_PUBLIC_IP=x gen-certs.sh   覆盖公网 IP
# =====================================================================
set -euo pipefail

PUBLIC_IP="${GW_PUBLIC_IP:-47.116.65.140}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUTDIR:-$(cd "$SCRIPT_DIR/.." && pwd)/certs}"
FORCE="${FORCE:-0}"

mkdir -p "$OUT/admin" "$OUT/api"
CA_KEY="$OUT/ca.key"
CA_CRT="$OUT/ca.crt"
CA_SRL="$OUT/ca.srl"
DAYS_CA=3650        # CA 10 年
DAYS_LEAF=825       # 叶子 ≈ 2.25 年
SAN="IP:${PUBLIC_IP},IP:127.0.0.1"

if [ "$FORCE" = "1" ]; then
  rm -f "$CA_KEY" "$CA_CRT" "$CA_SRL"
  rm -f "$OUT/admin"/* "$OUT/api"/*
fi

# —— CA(仅首次)——
if [ ! -f "$CA_KEY" ] || [ ! -f "$CA_CRT" ]; then
  echo ">> 生成私有 CA"
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$CA_KEY" -out "$CA_CRT" -days "$DAYS_CA" \
    -subj "/CN=ai-gateway-self-ca" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1
  chmod 600 "$CA_KEY"
else
  echo ">> 使用已有 CA($CA_CRT)"
fi

# —— 叶子(admin / api 各一,key 独立)——
gen_leaf() {
  local name="$1" cn="$2"
  local d="$OUT/$name"
  if [ -f "$d/fullchain.pem" ] && [ -f "$d/key.pem" ] && [ "$FORCE" != "1" ]; then
    echo ">> $name 已存在,跳过(强制轮换用 FORCE=1)"
    return
  fi
  echo ">> 生成 $name 叶证书($cn)"
  openssl req -newkey rsa:2048 -nodes \
    -keyout "$d/key.pem" -new -out "$d/$name.csr" \
    -subj "/CN=${cn}" >/dev/null 2>&1
  openssl x509 -req -in "$d/$name.csr" \
    -CA "$CA_CRT" -CAkey "$CA_KEY" -CAcreateserial \
    -out "$d/cert.pem" -days "$DAYS_LEAF" -sha256 \
    -extfile <(printf "subjectAltName=%s\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n" "$SAN") >/dev/null 2>&1
  cat "$d/cert.pem" "$CA_CRT" > "$d/fullchain.pem"
  rm -f "$d/$name.csr"
  chmod 600 "$d/key.pem"
  chmod 644 "$d/cert.pem" "$d/fullchain.pem"
}

gen_leaf admin "gateway-admin"
gen_leaf api   "gateway-api"

# —— 供上线后比对的指纹 ——
echo
echo "指纹(上线后 openssl s_client 比对):"
for name in admin api; do
  echo -n "  $name  "
  openssl x509 -in "$OUT/$name/cert.pem" -noout -subject -sha256 -fingerprint
done
echo "  CA     $CA_CRT(导入客户端信任即可两端口通用)"
