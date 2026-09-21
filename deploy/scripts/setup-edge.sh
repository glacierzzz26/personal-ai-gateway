#!/usr/bin/env bash
# =====================================================================
# setup-edge.sh — 在生产主机上一次性建立域名边缘(Nginx + 公信证书)。
#
# 干什么:
#   1. 装 Nginx(宿主原生,apt)与证书工具;
#   2. 用 deploy/nginx/ai-gateway-edge.conf 模板渲染出最终 conf;
#   3. 走 DNS-01 签一张通配符证书(默认 *.DOMAIN + DOMAIN),覆盖以后所有子域;
#   4. nginx -t → reload,并装续期(证书到期自动重载)。
#
# 端口分工(详见模板头部注释):
#   443   → 管理台/登录面(公信证书)
#   17080 → 数据面;SNI=域名走公信证书,裸 IP(无 SNI)走自签回退(旧客户端不断)
#   17090 → 不碰(网关自行发布,旧 IP:17090 入口保留)
#
# 证书工具:本脚本 TOOL 支持 acme.sh(默认)/ certbot。
#   ⚠️ 默认用 acme.sh 而非 certbot,原因:腾讯云 DNSPod 的 certbot 插件
#   (certbot-dns-tencentcloud)不在 Ubuntu apt 源里,只能 pip 装,与 apt 版 certbot
#   易版本错配;acme.sh 对 DNSPod(dns_dp)是原生支持、单脚本无依赖,更稳。
#   要用 certbot 就 `TOOL=certbot`(需自行确保插件可用)。
#
# 前置(仓库外,人工):
#   - DNSPod 上把 DOMAIN 与 *.DOMAIN 的 A 记录指向本机公网 IP;
#   - 建一个 DNSPod API Token,写入 $CONF_DIR/dnspod.env(格式见下);
#   - 阿里云安全组放行 443(17080/17090 早已放行)。
#
# 用法:
#   sudo DOMAIN=5home.online setup-edge.sh
#   TOOL=certbot sudo -E setup-edge.sh          # 改用 certbot
#   PUBLIC_IP=1.2.3.4 DOMAIN=example.com ...    # 覆盖默认值
# =====================================================================
set -euo pipefail

DOMAIN="${DOMAIN:-5home.online}"
PUBLIC_IP="${PUBLIC_IP:-47.116.65.140}"
TOOL="${TOOL:-acme.sh}"                       # acme.sh | certbot
CONF_DIR="${CONF_DIR:-/etc/ai-gateway-edge}"  # 凭证/渲染物目录(非仓库)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
TEMPLATE="$REPO/deploy/nginx/ai-gateway-edge.conf"

SSL_DIR="${SSL_DIR:-/etc/nginx/ssl/$DOMAIN}"
PUBLIC_CERT="$SSL_DIR/fullchain.pem"
PUBLIC_KEY="$SSL_DIR/key.pem"
SELF_CERTS_DIR="${SELF_CERTS_DIR:-/opt/ai-gateway-v2/certs}"
SELF_CERT="$SELF_CERTS_DIR/api/fullchain.pem"
SELF_KEY="$SELF_CERTS_DIR/api/key.pem"
API_UPSTREAM="${API_UPSTREAM:-https://127.0.0.1:17080}"
ADMIN_UPSTREAM="${ADMIN_UPSTREAM:-https://127.0.0.1:17090}"
NGINX_CONF="/etc/nginx/conf.d/${DOMAIN}.conf"
CRED_FILE="$CONF_DIR/dnspod.env"

log() { printf '\n==> %s\n' "$*"; }
die() { printf '\n✗ %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "需 root 运行(sudo)。"
[ -f "$TEMPLATE" ] || die "找不到模板:$TEMPLATE"

# ---------- 0. 前置检查 ------------------------------------------------
log "[0/6] 前置检查"
[ -f "$SELF_CERT" ] && [ -f "$SELF_KEY" ] \
  || die "找不到自签回退证书:$SELF_CERT(裸 IP 客户端要用它)。确认 $SELF_CERTS_DIR 存在。"
# 自签证书须含公网 IP,否则裸 IP:17080 回退会验证失败。
if ! openssl x509 -in "$SELF_CERT" -noout -text | grep -q "$PUBLIC_IP"; then
  echo "⚠️  自签证书 SAN 未含 $PUBLIC_IP —— 旧 IP:17080 客户端回退会验真失败。"
  echo "    修:GW_PUBLIC_IP=$PUBLIC_IP RESIGN=1 bash $REPO/deploy/scripts/gen-certs.sh 后重推证书。"
fi

# ---------- 1. 装 Nginx ------------------------------------------------
log "[1/6] 安装 Nginx"
if ! command -v nginx >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq nginx openssl curl
fi
nginx -v 2>&1 | sed 's/^/    /'
mkdir -p "$SSL_DIR" && chmod 700 "$SSL_DIR"

# ---------- 2. 装证书工具 + 校验凭证 -----------------------------------
log "[2/6] 准备证书工具($TOOL)"
mkdir -p "$CONF_DIR" && chmod 700 "$CONF_DIR"

if [ "$TOOL" = "acme.sh" ]; then
  export LE_WORKING_DIR="${LE_WORKING_DIR:-$HOME/.acme.sh}"
  if [ ! -f "$LE_WORKING_DIR/acme.sh" ]; then
    curl -fsS "https://get.acme.sh" | sh -s -- --nocron 2>&1 | sed 's/^/    /'
  fi
  ACME="$LE_WORKING_DIR/acme.sh"
  [ -f "$ACME" ] || die "acme.sh 安装失败($ACME 不存在)。"
elif [ "$TOOL" = "certbot" ]; then
  command -v certbot >/dev/null 2>&1 || {
    export DEBIAN_FRONTEND=noninteractive
    apt-get install -y -qq certbot
  }
  certbot plugins 2>/dev/null | grep -q tencentcloud \
    || die "certbot 缺 dns-tencentcloud 插件(apt 无,需 pip 装)。改用默认的 TOOL=acme.sh 更省事。"
else
  die "未知 TOOL=$TOOL(只支持 acme.sh / certbot)。"
fi

if [ ! -f "$CRED_FILE" ]; then
  cat >"$CONF_DIR/dnspod.env.example" <<'EOF'
# 腾讯云 DNSPod API Token(控制台 → DNSPod → API 密钥)。
# 用法:cp dnspod.env.example dnspod.env && chmod 600 dnspod.env 再填值。
DP_Id=你的TokenID
DP_Key=你的Token
EOF
  die "缺凭证文件 $CRED_FILE。已在 $CONF_DIR 放好示例,填好后重跑。"
fi

# ---------- 3. 渲染 Nginx conf -----------------------------------------
log "[3/6] 渲染 Nginx 配置 → $NGINX_CONF"
# 若已存在同名文件且非本脚本产物,先留个备份,避免误覆盖人工配置。
[ -f "$NGINX_CONF" ] && cp -a "$NGINX_CONF" "$NGINX_CONF.bak.$(date +%s)"
render() { sed -i "s|$1|$2|g" "$NGINX_CONF"; }
cp "$TEMPLATE" "$NGINX_CONF"
render '__DOMAIN__'        "$DOMAIN"
render '__PUBLIC_CERT__'   "$PUBLIC_CERT"
render '__PUBLIC_KEY__'    "$PUBLIC_KEY"
render '__SELF_CERT__'     "$SELF_CERT"
render '__SELF_KEY__'      "$SELF_KEY"
render '__API_UPSTREAM__'  "$API_UPSTREAM"
render '__ADMIN_UPSTREAM__' "$ADMIN_UPSTREAM"
grep -v '^[[:space:]]*#' "$NGINX_CONF" | grep -q '__' && die "模板仍有未替换占位符,检查 $NGINX_CONF。"

# Ubuntu 自带 default 站点会在 80 上抢 default_server,且我们不用 80 —— 摘掉更干净。
[ -e /etc/nginx/sites-enabled/default ] && rm -f /etc/nginx/sites-enabled/default || true

# ---------- 4. 签发通配符证书(DNS-01) ---------------------------------
log "[4/6] 签发证书 *.$DOMAIN(DNS-01)"
if [ -f "$PUBLIC_CERT" ]; then
  echo "    已存在 $PUBLIC_CERT,跳过签发(续期由工具自理)。删掉该文件可强制重签。"
else
  # shellcheck disable=SC1090
  . "$CRED_FILE"
  if [ "$TOOL" = "acme.sh" ]; then
    [ -n "${DP_Id:-}" ] && [ -n "${DP_Key:-}" ] || die "$CRED_FILE 里 DP_Id/DP_Key 为空。"
    # --server letsencrypt:acme.sh 新默认 CA 是 ZeroSSL,会要求注册邮箱;LE 更省事。
    "$ACME" --issue --server letsencrypt --dns dns_dp \
      -d "$DOMAIN" -d "*.$DOMAIN" 2>&1 | sed 's/^/    /'
    "$ACME" --install-cert -d "$DOMAIN" \
      --key-file "$PUBLIC_KEY" \
      --fullchain-file "$PUBLIC_CERT" \
      --reloadcmd "systemctl reload nginx" 2>&1 | sed 's/^/    /'
  else
    cp "$CRED_FILE" "$CONF_DIR/certbot-dnspod.ini"
    certbot certonly --non-interactive --agree-tos --register-unsafely-without-email \
      --dns-tencentcloud --dns-tencentcloud-credentials "$CONF_DIR/certbot-dnspod.ini" \
      -d "$DOMAIN" -d "*.$DOMAIN" 2>&1 | sed 's/^/    /'
    LE_DIR="/etc/letsencrypt/live/$DOMAIN"
    ln -sf "$LE_DIR/fullchain.pem" "$PUBLIC_CERT"
    ln -sf "$LE_DIR/privkey.pem"   "$PUBLIC_KEY"
  fi
fi
[ -s "$PUBLIC_CERT" ] && [ -s "$PUBLIC_KEY" ] || die "证书未就绪:$PUBLIC_CERT / $PUBLIC_KEY"
openssl x509 -in "$PUBLIC_CERT" -noout -subject -dates -ext subjectAltName | sed 's/^/    /'

# ---------- 5. 校验并生效 ----------------------------------------------
log "[5/6] nginx -t 并 reload"
nginx -t
systemctl enable nginx >/dev/null 2>&1 || true
systemctl reload nginx 2>/dev/null || systemctl restart nginx

# ---------- 6. 续期挂钩 ------------------------------------------------
log "[6/6] 续期"
if [ "$TOOL" = "acme.sh" ]; then
  # install-cert 已带 --reloadcmd(上面),acme.sh 自建 cron 续期。
  "$ACME" --cron 2>&1 | sed 's/^/    /' || true
else
  # certbot 自带 systemd timer;deploy-hook 让续期后重载 Nginx。
  mkdir -p /etc/letsencrypt/renewal-hooks/deploy
  printf '#!/bin/sh\nsystemctl reload nginx\n' > /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
  chmod +x /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
  systemctl enable --now certbot.timer >/dev/null 2>&1 || true
fi

cat <<EOF

边缘就绪。验证:
  域名数据面(公信) : curl https://$DOMAIN:17080/healthz
  域名管理台(公信) : 浏览器打开 https://$DOMAIN  (登录,无需导 CA)
  旧 IP 数据面(自签): curl -k https://$PUBLIC_IP:17080/healthz   (旧客户端不受影响)
  旧 IP 管理台       : https://$PUBLIC_IP:17090 (原样)

若上面 curl 域名报「连接超时」:先确认 DNS 已解析到 $PUBLIC_IP、安全组已放行 443/17080。
EOF
