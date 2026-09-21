# personal-ai-gateway

个人 AI 网关(Go):把多厂商模型收敛到一个出口。一个模型可挂多家「渠道」供给源(官方 API / OpenAI 兼容中转),
请求按「模型目录 + 供给源报价 + 路由规则 + 渠道健康」真实选路转发,统一记账、故障降级、令牌额度/RPM/有效期管控。
管理端是带账号登录的 Web 管理台(React 18 + antd v5),所有业务数据存 SQLite。设计决策见 [`DESIGN.md`](DESIGN.md)。

## 快速开始

```bash
# 0) 构建前端产物(管理台静态页;dist 已被 gitignore)
cd web-v2 && npm ci && npm run build && cd ..

# 1) 准备本地主密钥(渠道 API key 加密落库用;不设则自动生成 gateway.master.key)
export GW_MASTER_KEY=$(openssl rand -hex 24)

# 2) 准备配置(仓库内 config.yaml 已被 gitignore,内容见 config.example.yaml)
cp config.example.yaml config.yaml

# 3) 启动
go run ./cmd/gateway -config config.yaml
#    浏览器打开 http://127.0.0.1:8787
```

**首启引导**:库(`gateway-v2.db`,默认 `db_path`)里没有管理员账号时,登录页自动出现「创建管理员」
(或 `POST /api/v1/auth/bootstrap`)。之后的登录走会话 Cookie,`config.yaml` 不含任何凭据。

## 常用命令

```bash
go build ./... && go vet ./... && go test ./...   # 后端全门禁
cd web-v2 && npm run typecheck && npm run build    # 前端类型检查 + 构建
```

## 概念:用户 / 渠道 / 供给源 / 模型 / 规则 / 令牌

| 概念 | 说明 |
|---|---|
| 用户 | 管理台账号,分 `admin`(全部权限)与 `user`(仅能管理自己的访问令牌)。管理员在「用户管理」建号设初始密码,用户登录后可改自己密码。 |
| 渠道 | 一个上游 API 端点 + 凭据(供应商、Base URL、API key、优先级/权重、超时、熔断参数)。API key AES-GCM 加密落库。渠道上的「超时」只在路由规则里当排序权重用,**不约束请求本身的等待时长**——单次请求的超时取「系统设置」的 `request_timeout_ms`(或规则/供给源上的覆盖值),详见 DESIGN §5.1。 |
| 供给源(offer) | 某模型挂在某渠道上的报价(输入/输出/缓存读价,$/1M tokens)、RPM、启用与否;顺序 = 请求尝试次序。 |
| 模型目录 | 网关对客户端暴露的模型集合(contextWindow / capabilities / 启停)。可由渠道 `/v1/models` 同步而来,也可手工新增。每个模型可设**统一名称**(`displayName`):模型级生效、多渠道共用,只作用于网关侧展示/选路/日志,不改渠道侧真实模型名;留空=用真实名,原始名始终可在管理台查看。 |
| 路由规则 | 可选,按模型名(前缀/通配/正则)把请求限定到某些渠道并定策略(优先级/权重/低延迟),支持兜底渠道与重试。无命中规则时按供给源顺序。 |
| 访问令牌 | 给 Claude Code / 脚本用的模型面 Key(`sk-gw-` 前缀)。可设允许模型、额度上限(USD)、RPM、有效期;可归属某个用户。库内存 sha256(鉴权)与 AES-GCM 密文(回显/生成配置),明文在创建时返回一次。列表每行可「生成配置」导出可直接粘的 `~/.claude/settings.json` 片段。 |

## 模型面(数据面)用法

```bash
# 在管理台「访问令牌」建一个令牌,只此一次拿到完整 Key:
KEY=sk-gw-xxxxx

# OpenAI 形状
curl -X POST http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}'

# Anthropic 形状(Claude Code / claude 可直接把 base_url 指到网关)
curl -X POST http://127.0.0.1:8787/v1/messages \
  -H "x-api-key: $KEY" -H 'Content-Type: application/json' \
  -d '{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}'

curl -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/v1/models   # 目录(含启用供给源的模型)
```

令牌鉴权 + 记账一体:网关校验令牌(`allowed_models` / `quota_usd` / `rpm_limit` / `expires_at`),
选路转发(失败自动尝试下一个可用供给源,熔断冷却),落账(`request_logs`,成本按命中供给源单价),
并在额度/RPM/过期不满足时返回对应错误(`quota_exceeded` / `rate_limit_exceeded` / 过期 `authentication_error`)。

## Web 管理台

- 后端直接静态托管 `web_dir`(默认 `web-v2`)下的 `dist/`,SPA 路由回退到 `index.html`;`/` 打开即管理台。
- 开发期可 `cd web-v2 && npm run dev`(Vite :5178,`/api`、`/v1` 已代理到 :8787),改前端热更。
- 页面覆盖:运行总览(默认**近 1 天**,以「经营」为中心 —— 营收/成本/毛利 + 客户余额风险 + 渠道健康 + 明细;预设可切 7/30 天或自定义区间)/ 渠道管理(CRUD + 连通测试 + 同步模型)/ 模型广场(供给源管理、定价、拖拽排序、设为首选、用量)/ 路由规则 / 访问令牌(归属与筛选、生成 Claude 配置)/ 请求日志(服务端分页/筛选/详情)/ 用户管理(建号/角色/充值/余额)/ 系统设置(超时、重试、自动降级、代理、TLS、日志保留/采样、时区、对外基址、清空日志)。
- 角色:普通用户登录后仅见「访问令牌」页,只能管理自己的 Key;其余页面与接口对其实admin-only(前端隐藏 + 服务端 403 双重约束)。

> 页面读到的数值口径:成功率等一律 0..100 百分数经服务层换算成 0..1 小数;日志/曲线时间戳按设置里的
> 时区(`tz_offset_min`,默认 +480 Asia/Shanghai)显示,底层 UTC 落库。

## 目录

- `cmd/gateway` 入口(组装 config → 主密钥 → store → server;版本经 `-ldflags -X main.version` 注入,按 `tls` 配置叠加双口)
- `internal/config` 只读 `listen/db_path/web_dir/tls`(业务数据全部在 DB)
- `internal/domain` v2 实体 DTO(兼 API body)
- `internal/store` SQLite(schema 版本化 + channels/models/offers/rules/tokens/admins/users/request_logs/settings 仓库 + 时区聚合)
- `internal/secret` AES-GCM 渠道密钥(主密钥 `GW_MASTER_KEY` 或 DB 同目录 `gateway.master.key` 0600)
- `internal/engine` 选路决策:候选(启用供给源 ∩ 未熔断渠道 ∩ 命中规则)→ 策略排序 → 重试/兜底
- `internal/proxy` 转发内核 + 跨协议翻译(anthropic ↔ openai,流式 + usage 记账)
- `internal/auth` 管理会话(bcrypt 账号 + HS256 JWT,签名密钥由主密钥派生)
- `internal/server` v2 管理 REST(会话鉴权)+ 模型面 /v1(令牌鉴权)+ 静态托管
- `web-v2/` 管理台前端源码(React 18 + antd v5 + react-query + echarts)

## 配置与密钥(全部见 config.example.yaml)

```yaml
listen: ":8787"           # 明文合并面(dev/测试/容器 healthcheck);生产 compose 不发布此端口
db_path: "gateway-v2.db"   # v2 新库(默认)。旧 gateway.db(v1 表)原样留档,不做迁移
web_dir: "web-v2"          # 管理台源码目录(托管其 dist/);空串 = 关闭静态托管
# 生产 TLS 双口(齐全才起;两组证书必须独立,不得共用同一套)
tls:
  api_listen: ":17080"                  # 数据面(仅 /healthz + /v1/*)
  api_cert: "/certs/api/fullchain.pem"
  api_key: "/certs/api/key.pem"
  admin_listen: ":17090"                # 管理台(仅 /healthz + /api/v1/* + SPA)
  admin_cert: "/certs/admin/fullchain.pem"
  admin_key: "/certs/admin/key.pem"
```

- 渠道 API key:环境变量 `GW_MASTER_KEY`(任意长度,sha256 展平)加密;缺失自动生成 DB 同目录 `gateway.master.key`(0600)。主密钥换过会让旧密文解不开——保留原密钥即可。
- 管理账号与访问令牌:**不走配置**。账号靠首启「创建管理员」;令牌在管理台创建,明文只现一次。
- 存量 v1(`upstreams`/`keys`/`pricing`/`quota` 概念、旧 `/api/v1/upstreams` 管理面、旧 `web/` 前端)已在 v2 演进中退役;旧 `gateway.db` 仅作历史留档。

## 生产部署(Go 自终止 TLS 双口 + 域名边缘 Nginx)

生产在云主机 `47.116.65.140`(别名 `aliyun`)上跑 Go 网关自身终止 TLS(数据面 17080 / 管理台 17090,
容器 bridge + 端口发布);宿主原生 Nginx 作域名边缘,用**公信证书**终结 TLS,客户端不再需要导入自签 CA:

```
[域名]  https://gateway.5home.online(管理台) / https://gatewayapi.5home.online(数据面)  ← 公信证书,均 443
[旧IP]  https://47.116.65.140:17080(数据面,自签回退) / :17090(管理台)                  ← 过渡期保留,零改动
[过渡]  https://5home.online:17080(数据面,公信证书)                                    ← 兼容写死 :17080 的老客户端
```

- **Nginx 边缘**(宿主 apt 原生,非容器):配置在**独立仓库 `host-infra`**(宿主级多服务边缘,不是本仓库),
  本仓库只声明自己的端口与上文拓扑;网关为其中一个 vhost `ai-gateway.conf`。
  **两面各占一个子域、都在 443**,靠 SNI 主机名分流:管理台面 → 回源 17090,数据面 → 回源 17081;
  两个子域共用一张通配符证书,**加面不加证书**。公网 17080 是**过渡口**,用 **SNI 双证书**——域名连接走公信证书、
  裸 IP(无 SNI)落到 `default_server` 自签回退,所以**旧 IP 客户端在切换后不断**。17090 不碰,旧入口原样。
- **证书**:DNS-01 签一张通配符 `*.5home.online`(腾讯云 DNSPod),覆盖包括上面两个子域在内的所有子域,
  不需开 80 口。工具默认 `acme.sh`(`dns_dp`/`dns_tencent` 原生支持;certbot 的 DNSPod 插件不在 apt)。
- **回源**:Nginx → `https://127.0.0.1:17081/17090`(网关自签 TLS,`proxy_ssl_verify off`)。
  回源仍用 https 是为了保留**两面物理隔离**(数据面口只认 `/healthz`+`/v1/*`,管理台口只认
  `/healthz`+`/api/v1/*`+SPA;明文 `:8787` 是合并面,绝不发布);错面访问 404。流式必须 `proxy_buffering off`。
- **对外基址**:管理台「系统设置 → 对外基址」填 `https://gatewayapi.5home.online`,让「生成 Claude 配置」
  吐出的 `ANTHROPIC_BASE_URL` 指向数据面子域(留空则按请求头推断)。

```bash
deploy/scripts/gen-certs.sh          # 生成自签 CA + admin/api 叶子(私钥不落仓库);重签叶子用 RESIGN=1
deploy/scripts/deploy.sh [GW_HOST]   # 本地构建镜像 → docker save 经 ssh 推目标主机 → compose up(默认 rguo@192.168.0.202)
deploy/scripts/backup.sh             # SQLite 在线快照(REMOTE_DIR=~/ai-gateway)
# 域名边缘(公网入口)在独立仓库 host-infra:cd ../host-infra && sudo DOMAIN=5home.online bash scripts/deploy.sh
```

- 目标主机只需 docker + compose(不需 Go/Node/Docker Hub);镜像本地构建,版本由 `git describe` 注入 `/healthz`。
- 自签证书 SAN 含各主机 IP,`deploy/certs/` 保留作**回源 + 旧 IP 客户端回退**;新客户端走公信证书,无需导 CA。
- **灾备(家主机断电 / 云入口故障):** 方案与分阶段落地见 [`deploy/DR.md`](deploy/DR.md)(异地加密快照 + 云冷备同 IP 接管,RPO ≤15min / RTO ≤2min,客户端零改动)。
- **改造方向(个人网关 → 中转站):** 角色/定价/钱包/可见面的方案见 [`PLAN.md`](PLAN.md)(admin=自己、user=客户;售价 = 官方价 × 倍率、成本仅自己可见;用户级钱包)。**尚未实施。**
