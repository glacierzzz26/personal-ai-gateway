# personal-ai-gateway 设计文档

一个 Go 写的**个人** AI 网关:多厂商模型(官方 API、OpenAI 兼容中转、聚合订阅)收敛到一个出口。
一个「模型」可挂多家「渠道」供给源;请求按模型目录 + 报价 + 路由规则 + 渠道健康**真实选路转发**;
统一记账、故障自动降级、令牌(额度/RPM/有效期/允许模型)管控;带账号登录的 Web 管理台(React 18 + antd v5)
对网关做全 CRUD。数据全在 SQLite(`gateway-v2.db`)。

> 定位:个人 / 少量渠道。设计追求简单、可观测、可演进,不追求企业级。

## 1. v2 演进总览(2026-09,web-v2 契约驱动同仓重写)

按 web-v2 前端契约(渠道/供给源/模型目录/路由规则/访问令牌/日志/用量/设置)重写了 v1 的
`upstreams + api_keys + config keys + 配额轮询`心智,替换 v1 管理面与旧 `web/` 前端。演进记录:
| 里程碑 | 内容 | 提交 |
|---|---|---|
| M1 | 数据层与新库:config 瘦身、domain、schema 版本化 + 全仓库、`gateway-v2.db`(旧库留档) | 119625a |
| M2+M3 | 管理 REST + 账号登录;全真转发引擎(engine 选路 + billing + 跨协议翻译 + 熔断) | 2aa8a70 |
| M4 | 前端接线:http 层、登录/首启引导、守卫;八页全绑真;静态托管 dist | 9993bce |
| M5 | 收尾:退役旧 `web/` 与 v1 配置概念;README/本文档全文重写 | 本次 |

## 2. 架构

```
 Claude Code / claude (anthropic)      OpenCode / curl (openai)       浏览器 → Web 管理台(静态 dist,同源)
        └──────────────┬──────────────────────┘                            │
                       ▼                                                   ▼
┌──── 网关(单进程;生产:Go 自终止 TLS 双口 17080 数据面 / 17090 管理台;明文 :8787 仅容器内 healthcheck) ────┐
│ 管理面 /api/v1/*       会话 cookie 鉴权(gw_session,HS256 JWT)                                │
│   认证:bootstrap/login/logout/me、auth/state(匿名判定首启)                                   │
│   业务:channels·models·offers·rules·tokens·logs·usage·overview·settings·users 全 CRUD        │
│ 模型面 /v1/*           访问令牌鉴权(Bearer / x-api-key → sha256)                              │
│   POST /v1/chat/completions · POST /v1/messages · count_tokens · GET /v1/models             │
│ 中间层:engine 选路(offer/rule/channel health)→ proxy 转发/翻译 → billing 记账 → request_logs │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
        │候选逐个尝试:渠道按 provider 出站(anthropic-native / openai 兼容 + Azure api-version)
        ▼
   上游渠道(官方 API / OpenAI 兼容中转 / 聚合订阅),密钥 AES-GCM 加密落库
```

两面**物理隔离**在两个监听口(见 §8 生产部署):数据面口只认 `/healthz` 与 `/v1/*`,管理台口只认
`/healthz`、`/api/v1/*` 与静态 SPA;错面访问一律 404(如管理台口打 `/v1` 不会落到 SPA 回退成假 200)。

分层(目录 = 当前实现):

```
cmd/gateway     组装 config → 主密钥(secret)→ store(迁移)→ server
internal/config listen/db_path/web_dir/tls;业务数据不进配置
internal/domain v2 实体 DTO(JSON tag,兼 API body 与展示字段)
internal/secret AES-GCM(渠道 api_key);主密钥 GW_MASTER_KEY 或 gateway.master.key(0600)
internal/store  schema 版本化;channels/models/model_offers/rules/tokens/admins/users/
                request_logs/settings 仓库;时区聚合(ts 存 UTC,桶/本地化按 tz_offset_min 换算)
internal/auth   账号(bcrypt)+ 会话 JWT(HS256,密钥由主密钥派生;httpOnly SameSite=Lax cookie)
internal/engine 把目录+offers+规则+渠道健康编译为一次转发决策(候选/策略/重试/兜底)
internal/proxy  relay 转发 + translate(anthropic↔openai 双向,流式状态机 + usage 权威计数)+ probe/ping
internal/server 管理面 CRUD handler(会话)+ 模型面(令牌)+ 静态托管 web-v2/dist(SPA 回退)
web-v2/         管理台前端源码(React18+antd5+react-query+echarts);dist 产物 gitignore
```

## 3. 数据模型(新库 `gateway-v2.db`,schema_migrations 版本化)

- `admins(id, username UNIQUE, password_bcrypt, role TEXT DEFAULT 'admin', created_at)` — config 不再承载账号;
  role 分 `admin`(全权)/`user`(仅能管理自己的令牌)。
- `channels(id, name, provider, base_url, api_key_cipher, key_masked, priority, weight, timeout_ms,
   tags(json), enabled, max_failures, cooldown_sec, note, created_at, updated_at)`。
- `models(id, name UNIQUE, context_window, capabilities(json), enabled, …)`。
- `model_offers(id, model_id FK, channel_id FK, in/out/cache_read 单价, override_price,
   priority, enabled, rate_limit_rpm, note, UNIQUE(model_id, channel_id))` — 渠道/模型删除级联。
- `rules(id, name, enabled, match_mode(prefix|wildcard|regex), pattern, strategy(priority|weight|latency),
   channel_ids(json), weights(json), fallback_channel_id, retry, timeout_ms, sort, hit)`。
- `tokens(id, name, sha256 UNIQUE, key_cipher, key_masked, allowed_models(json "*"|数组), quota_usd, used_usd,
   rpm_limit, expires_at, status, last_used_at, owner_id FK→admins NULL, …)` — sha256 供鉴权,key_cipher
   (AES-GCM)供回显/生成配置;owner_id=NULL 为全局 key;删用户级联删其令牌。m0002 之前建的旧 key 无密文。
- `request_logs(id, ts UTC, model, channel_id/name, token_id/name, protocol, stream, status,
   in/out/cache tokens, cost, first_token_ms, total_ms, ip, err)` — idx ts/model/channel/token。
- `settings(k PK, v)` — 网关参数 + `tz_offset_min`(默认 +480 Asia/Shanghai)+ `public_base_url`(生成配置用)。

**时间口径**:`ts/*_at` UTC RFC3339Nano 落库;小时/天桶、today、日志展示全部按 `settings.tz_offset_min`
换算后再截串聚合(store logs.go tzMod / LocalDayWindowUTC),避免 UTC 桶与本地图表错位一天。

## 4. 鉴权模型

- **管理面**(`/api/*`):会话 **JWT**(HS256,签名密钥由主密钥经 `secret.DeriveSubkey` 派生),写 httpOnly
  cookie `gw_session`。无状态:登出=清 cookie;为让改密码/改角色/删号立即生效,中间件除验签外还用
  `AdminByID` 复核角色与密码版本(`pv` claim)。`GET /auth/state` 匿名放行(仅回 `adminExists`,驱动首启引导)。
- **角色授权**:`requireAdmin` 闸门保护 channels/models 写/rules/logs/settings/overview/usage/users;
  `GET /models` 与 tokens 面为「已登录即可」。令牌端点按 owner 收束:user 只见/操作自己名下,越权回 404。
- **模型面**(`/v1/*`):访问令牌。`extractSecret`(x-api-key | Authorization Bearer)→ sha256 查 tokens;
  校验 `status=active`、未过期、`used_usd < quota_usd`、RPM 滑动窗口,失败按协议回
  `authentication_error / quota_exceeded / rate_limit_exceeded`(403/429)。**cookie 永不接受于此,key 永不接受于管理面**。
- 入口分流一律**闭死**(fail closed):无凭据、错凭据都拒绝,不泄漏内部结构。
- **生成 Claude 配置**:`GET /tokens/{id}/claude-config` 解出 key 明文,按该令牌可用模型(∩ 目录启用且可路由)
  自动挑 opus/sonnet/haiku,输出可逐字粘进 `~/.claude/settings.json` 的 `env` 块;基址取 `public_base_url`
  或按请求 scheme+host(X-Forwarded-* 优先)推断。

## 5. 转发引擎语义(M3,M4 验证)

1. 入站:模型名必在 `models` 目录且 enabled;令牌 `allowed_models!='*'` 需匹配(精确或前缀通配),否则 404/403。
2. 选路候选 = `offers(m).enabled ∧ offer.channel.enabled ∧ 渠道未熔断`。
   - 无命中规则 → 按 offer.priority 升序(= 抽屉拖拽序)逐个尝试。
   - 命中规则 → 候选收缩到 `rule.channel_ids ∩ offers`;策略:priority=渠道 priority 再 offer.priority;
     weight=按 `rule.weights`(缺省 channel.weight)加权;latency=按 EWMA 延迟升序。
     用规则的 retry/timeout_ms;全败后如有 `fallback_channel_id` 追加一轮。
   - 每次失败记录(RecordFailure/冷却),成功 RecordSuccess;渠道/供给源的 status/latency/successRate
     由近窗口 EWMA 驱动。
3. 转发:出站协议按 provider(Anthropic → anthropic 原生;OpenAI/Azure/DeepSeek/通义/智谱/Moonshot/聚合
   中转 → openai 兼容;Azure 补 api-version)。跨协议 → translate(a2o / o2a),非流式整包 + 流式 SSE 逐块翻译。
   **一旦开始回 2xx 流即不可换上游**(failover 窗口 = 首字节前)。
4. 记账:cost = 命中 offer 单价 × token;流式以结束块权威计数;写 request_logs;`token.used_usd` 事务累加;
   今天/曲线统计由日志实时 GROUP BY(个人规模不建 rollup 表)。
5. `/v1/models` = enabled 且有启用 offer 的模型(anthropic/openai 双形状)。

### 关键坑位(实现时对照)
- 管理端 PATCH 是**全量替换**(Update* 仓库方法会清零未传字段)。前端启停类操作用「先取全量快照再整包提交」
  (services/api.ts 的 toggle*/offerDraft 帮助器),勿发部分 body。
- 成功率口径:后端 successRate 用 0..100 百分数;前端统一除 100 还原 0..1 再 `×100` 展示(api.ts `frac`)。
- modernc.org/sqlite:`strftime` 返回 TEXT,与整型参数比较 `<=` 恒假 —— 一律 `CAST(... AS INTEGER)` 再比;
  聚合列包 `COALESCE(...,0)`(空窗口 SUM=NULL 会 Scan 报错)。
- 客户端断连必须取消上游请求(`ctx` / `resp.Body.Close()`),否则额度白烧。
- SQLite WAL,个人读多写少足够;管理端写操作集中在事务内(额度扣减等)。

## 6. 管理 REST 契约(v2;会话鉴权)

| 方法与路径 | 作用 |
|---|---|
| `POST /api/v1/auth/bootstrap` / `login` / `logout` · `GET /me` | 首启建管理员(无则 409)/ 登录 / 登出 / 当前账号(含 role) |
| `POST /api/v1/auth/password` | 改自己密码(验旧密码,成功后重签 cookie) |
| `GET /api/v1/auth/state` | 匿名:回 `{adminExists}` 驱动首启引导 |
| `GET/POST /users` · `PATCH /users/{id}/password` · `DELETE /users/{id}` | 用户管理(仅 admin):建号/列号/重置密码/删号 |
| `GET/POST /channels` · `GET/PATCH/DELETE /channels/{id}` | 渠道 CRUD(改时 apiKey 留空=保持) |
| `POST /channels/{id}/test` · `/sync-models` | 连通探测 `{ok,latencyMs}`;拉 `/v1/models` 补目录+停用 offer |
| `GET/POST /models` · `PATCH/DELETE /models/{id}` | 目录(聚合 offers 与展示字段)/新增/改(全量)/删;GET 全站可读 |
| `POST /models/{id}/offers` · `PATCH/DELETE /offers/{oid}` | 加供给源 / 改价·启停 / 删 |
| `PUT /models/{id}/offers/order` `{from,insertAt}` | 供给源拖拽重排 → priority 1..N |
| `GET /models/{id}/usage?days=7` | `{daily:[MetricPoint], byChannel:[{channelName,requests,costUsd}]}` |
| `GET/POST /tokens` · `GET/PATCH/DELETE /tokens/{id}` | 令牌 CRUD;新建响应一次性返回明文 key;user 只见/操作自己名下 |
| `GET /tokens/{id}/claude-config` | 生成可直接粘的 `~/.claude/settings.json` 片段(含真实 key;旧 key 无密文回 409) |
| `GET/POST /rules` · `PATCH/DELETE /rules/{id}` · `PUT /rules/order` | 路由规则 CRUD + 重排 |
| `GET /logs?model&channel&token&status(ok|error)&kw&page&size` | →`{items,total}` 服务端分页;ts 已本地化 |
| `DELETE /logs` | 清空日志(设置页弹确认) |
| `GET /usage?dim=model\|channel\|token&days=7\|30` | →`{rows:UsageRow[](errorRate 0..1), days}` |
| `GET /overview` | `{hours[24], days[7], totalRequests, totalErrors, totalCostUsd, avgFirstTokenMs}`(近 7 天窗口) |
| `GET/PATCH /settings` | 网关参数(超时/重试/降级/代理/TLS/日志保留/采样/记录请求体/时区/对外基址) |
| `GET /healthz` | 健康检查 |

标注「仅 admin」者经 `requireAdmin` 闸门;其余为「已登录即可」。令牌越权访问一律回 404(不泄露他人 key 存在)。

展示字段(channel/offer/model 的 status·latencyMs·successRate·today·costUsd 等)由后端现算,前端只消费。

## 7. 前端接线约定(web-v2/)

- `services/http.ts`:baseURL `/api/v1`,同源会话 cookie;非 2xx 抛 `HttpError`;401 触发已注册的守卫回调。
- `services/api.ts`:全方法映射真实 REST;负责单位换算(0..100→0..1)与「全量快照」帮助器;页面不直接 import 任何 mock。
- 会话守卫:`App.tsx` 启动 `GET /auth/me` 恢复会话;未登录渲染 `Login`(内含首启「创建管理员」态)。
- 数据层:react-query(重试 0);mutation 成功后 invalidate 对应 queryKey(`['channels']/['models']/['rules']/
  ['tokens']/['logs',filters,page]/['usage',dim,days]/['overview']/['settings']/['model-usage',id]`)。
- 展示词表(providers/capabilities 标签)属前端常量,与后端枚举一致;不作为运行时数据。

## 8. 运行与联调

- 后端静态托管:`web_dir`(默认 web-v2)的 `dist/` 于 `/`,非文件路径回退 `index.html`(SPA);生产只需一个 Go 进程。
- 开发期:`cd web-v2 && npm run dev`(Vite :5178,`/api`、`/v1` 已代理到 :8787),改前端热更。
- 冒烟路径:起网关(空库)→ 浏览器创建管理员 → 建渠道(指向假/真上游)+ 测试 → 同步或手工建模型 + offer 定价 →
  建令牌 → 用令牌打 `/v1/chat/completions`(或 `/v1/messages`)→ 刷新日志/用量/概览/渠道统计应实时变化。

### 8.1 生产部署(Go 自终止 TLS 双口 + frp 隧道;无 Caddy)

```
[局域网客户端]  --https--> 192.168.0.202:17080(数据面)/17090(管理台)   ← Go 网关自身终止 TLS
[公网客户端]    --https--> 47.116.65.140:17080/17090 --frps--> frpc(202) --> 127.0.0.1:17080/17090
```

- **TLS 双口**:`config.tls` 配 `api_listen/api_cert/api_key` 与 `admin_listen/admin_cert/admin_key` 两组,
  齐全时 main 额外起两个 `http.Server`(分别挂 `HandlerAPI`/`HandlerAdmin`);缺任一项则只起明文 `listen`(dev/测试)。
  明文 `:8787` 仍起(容器 healthcheck `http://127.0.0.1:8787/healthz` 用),但 compose 不发布该端口。
- **证书**:`deploy/scripts/gen-certs.sh` 生成一个私有 CA + admin/api 两张独立叶子(各挂一个口,不共用)。
  SAN 覆盖 `ai-gateway.lan / localhost / 127.0.0.1 / <局域网 IP> / <公网 IP>`,两条访问路径都能验真;
  `RESIGN=1` 只重签叶子保留 CA(客户端信任不失效),`FORCE=1` 连 CA 轮换。
- **隧道**:`deploy/scripts/setup-frp.sh` 把云 frps 的 17080/17090 反向映射到目标主机 127.0.0.1 同名端口。
  因目标主机用户无 sudo,frpc 以 `--network host` 的 docker 容器常驻(`restart unless-stopped`),免系统服务。
  token 取自本地 `~/frp/frpc.toml`(单一来源,不入仓库)。
- **部署流**:`deploy/scripts/deploy.sh [GW_HOST]`(默认 `rguo@192.168.0.202`)→ 本地 `build.sh` 构建镜像
  (前端 + 交叉编译 + docker build,版本由 `git describe` 注入 `-ldflags -X main.version`)→
  `docker save | ssh docker load` 推到目标主机 → 同步 compose/证书 → 远端 `compose up -d`。
  目标主机只需 docker,不需 Go/Node/Docker Hub。远端 `.env`(`GW_MASTER_KEY`/`GW_IMAGE_TAG`)与 `data/` 首次生成后保留。
- **版本可见**:`/healthz` 回 `{ok,store,version}`;`build.sh` 打 `ai-gateway:$VER` 与 `:latest` 便于回滚。

## 9. 迁移与留档

- v1 老库 `gateway.db`(upstreams/api_keys/request_log 等 v1 表)**整文件原样留档、不做迁移**;v2 用默认新库
  `gateway-v2.db`。二者混用同一文件会产生语义错乱的旧表残留,务必分开。
- v1 概念(统一 key 兼管、`upstreams`/`keys`/`pricing`/`quota`、旧 `/api/v1/upstreams` 面、旧 `web/` 前端)已在演进中退役删除。
- 渠道 api_key 密文依赖主密钥;换主密钥会解不开旧密文 → 保留原密钥即可回放。
