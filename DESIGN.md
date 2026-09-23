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
        │候选逐个尝试:渠道按 egress_proto 出站(anthropic-native / openai 兼容 + Azure api-version)
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
- `channels(id, name, provider, channel_type, egress_proto, base_url, api_key_cipher, key_masked,
   priority, weight, timeout_ms, tags(json), enabled, max_failures, cooldown_sec,
   quota_path, quota_shape, note, created_at, updated_at)`。
  **三个正交字段勿混用**:`provider` = 卖的是谁的模型(真厂商,可空=聚合渠道,前端回落显示渠道类型);
  `channel_type` = 上游归属(`deepseek`/`commandcode`/`opencode`/`thirdparty`),决定**额度怎么查**;
  `egress_proto` = 出站线上协议(`openai`/`anthropic`/`azure`),决定**请求怎么发**。
  `quota_path`/`quota_shape` 仅 `thirdparty` 有意义 —— 这类中转的额度接口没有统一约定,手工配「相对路径 + 响应形状」;
- `models(id, name UNIQUE, display_name, context_window, capabilities(json), enabled, …)` — `name` 为渠道侧真实模型名;
  `display_name` 为网关统一名称(空=未重命名,对外回落 `name`),非空时唯一(部分索引 `WHERE display_name <> ''`)。
  统一名只作用于网关侧(管理台展示/路由规则匹配/`/v1/models`/日志归因),出站转发仍改回 `name`。
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
   模型名解析支持**统一名**(`display_name`)与真实名:`GetModelByPublicName` 先命中统一名,再回落真实名。
   令牌 `allowed_models` 同样对请求名与统一名各比对一次,重命名后按统一名配置的规则继续生效。
2. 选路候选 = `offers(m).enabled ∧ offer.channel.enabled ∧ 渠道未熔断`(熔断判定见 §5.4)。
   - 无命中规则 → 按 offer.priority 升序(= 抽屉拖拽序)逐个尝试。
   - 命中规则 → 候选收缩到 `rule.channel_ids ∩ offers`;策略:priority=渠道 priority 再 offer.priority;
     weight=按 `rule.weights`(缺省 channel.weight)加权;latency=按 EWMA 延迟升序。
     用规则的 retry/timeout_ms;全败后如有 `fallback_channel_id` 追加一轮。
   - 每次失败记录(RecordFailure/冷却),成功 RecordSuccess;渠道/供给源的 status/latency/successRate
     由近窗口 EWMA 驱动。
3. 转发:出站协议按 `egress_proto`(anthropic → anthropic 原生;openai → openai 兼容;azure → openai 兼容
   补 api-version)。**与 `provider` 无关** —— 同一家的模型可以走不同协议的上游。
   跨协议 → translate(a2o / o2a),非流式整包 + 流式 SSE 逐块翻译。
   **一旦开始回 2xx 流即不可换上游**(failover 窗口 = 首字节前)。
   出站 client 按 (proxy, skipTLS, 请求超时) 三元组缓存复用 Transport(连接池不再每请求重建);
   该超时只作**响应头阶段**硬上限(`ResponseHeaderTimeout`),流式拿到响应头后交给看门狗。
4. **流式超时口径**(见 §5.1)。
5. 记账:cost = 命中 offer 单价 × token;流式以结束块权威计数(流被中断时用已嗅探到的部分 + 输入估算兜底);
   写 request_logs;`token.used_usd` 事务累加;今天/曲线统计由日志实时 GROUP BY(个人规模不建 rollup 表)。
6. `/v1/models` = enabled 且有启用 offer 的模型(anthropic/openai 双形状),`id`/`display_name` 用统一名。
7. **统一名称(重命名)**:模型级(不按渠道),同一模型多渠道共用一个对外名。客户端用统一名请求即可选路;
   出站前把请求体 `model` 改回渠道侧真实名(同协议透传与跨协议翻译两条路径都改),不改动渠道侧真实模型名。
   路由规则按统一名匹配;日志/用量以统一名归因;管理台列表回显 `originalName` 供对照。

### 5.1 流式看门狗、断连与错误率口径

流式成功(200)后整条流**不再设全局 ctx 超时**——实测有 169s 的长流,一刀切会误杀。受约束的只有两种故障形态,
由 `stallGuard` 的两级看门狗分别盯住,任一触发即关掉底层连接并交回可归因的错误(而非裸的
"use of closed network connection"):

| 窗口 | 起点 / 重置 | 触发 |
|---|---|---|
| 首字节 `first` | 拿到响应头起算,**永不重置** | `errFirstByteTimeout` → 504,此时尚未向客户端写字节 |
| 中途静默 `idle` | 每读到一块数据重置 | `errStreamIdle` → 504(客户端已收到 200,仅落账) |

`first` = 本次候选超时;`idle = max(first, minStreamIdle=120s)`——流一旦开始再掐断无法换渠道,判定必须宽松,
否则上游一次正常的长思考停顿就被记成故障。

**候选超时怎么来的**(`engine.BuildPlan`):默认取 settings 的 `request_timeout_ms`;命中路由规则则取 `rule.timeout_ms`;
再逐候选用 `offer.timeout_ms`(非空且 >0)覆盖。**渠道的 `timeout_ms` 不参与这个取值**——它只在规则策略里当排序
权重用(见 §5 第 2 条),不要指望在渠道上填超时能约束请求。

> ⚠️ **这个值必须显著小于客户端自己的耐心**,否则故障会以「客户端先跑」的形态出现,而不是可归因的 504:
> 线上把 `request_timeout_ms` 设成了 **360000(6 分钟)**,于是首字节窗口也是 6 分钟,而 Claude Code 自身约
> **2 分钟**就放弃并报 "check your network"。实测 2 条 499(`total_ms` ≈ 110~118s、`first_token_ms=0`):
> 客户端断开时网关还在首字节窗口内,于是**既没 504、也没触发 failover**(换上游的窗口 = 首字节之前),
> 白白放过了一次换渠道的机会。把 `request_timeout_ms` 收到 **90000**(或 60000)即可:首字节窗口先于客户端到期,
> 网关先回可重试的 504 并换到下一个候选;中途静默仍有 120s 下限兜底,不会误杀正常长流。

归因与落账:

- **客户端断连**(`r.Context()` 取消):记 `499 StatusClientClosed`,**不写 err 字段**、不 `RecordFailure`、不熔断渠道。
  上游请求同步取消,避免额度白烧。
- **上游停滞**:504,err 文案区分首字节(`no data…`)与中途静默(`mid-stream…`)。
- **部分 usage**:流被中断时上游往往还没下发 usage 末块。此时用已嗅探到的部分;若一个 token 都没观测到,
  兜底填入输入侧估算(`EstimateOpenAIChatInput`),不整条丢账。

**错误率口径**(SQL 常量 `store.errCond`):错误 = `(status >= 400 OR err IS NOT NULL) AND status <> 499`。
「用户按 Esc」既非网关也非上游故障,计入会把交互行为变成渠道健康问题。日志列表按 ok / error / canceled(499)三桶互斥筛选。

### 5.2 a2o 推理回填(thinking 模式多轮不再 400)

**问题**:Anthropic 协议没有 `reasoning_content` 这个字段,而 DeepSeek 的 thinking 模式要求**多轮工具循环时把上一轮的
`reasoning_content` 原样带回**,否则第 2 轮直接 400
(`The reasoning_content in the thinking mode must be passed back to the API.`)。
同协议(openai→openai)逐字节透传不受影响,故障只在 **a2o 跨协议**路径:客户端(Claude Code)根本无从回传这个字段。

**方案:网关侧缓存回填**,对客户端完全透明——响应的形状一个字节都不变(reasoning **绝不**发成 anthropic 事件)。

- 响应侧把上游的 `reasoning_content`(非流整段 / 流式逐块累加)**捕获**下来(`translate.Capture`),流式只进缓冲区不 emit;
- 下一轮重建 assistant 历史时按需**回填**(`translate.ReasoningLookup`),于是上游看到的是完整的多轮上下文。

键(同一 entry 两把,均带 **token id** 前缀,避免跨用户串味):
`t|<tokenID>|<anthropic 侧 tool_use id>`(主键,精确;id 是本网关 mint 的 `toolu_gw_<b64>`,与 `AnthropicToOpenAIToolID` 互逆)
与 `x|<tokenID>|<sha256(assistant 文本)[:16]>`(兜底,覆盖无 tool_use 的纯文本轮)。TTL 30min、上限 1024 条,超限丢最旧。

**关键设计点——只在命中缓存时才回填**。命中本身就证明「这个上游确实在用 `reasoning_content`」,
所以不需要 provider 白名单,也不会把该字段塞给不认识它的上游(OpenAI 官方 / Azure)。

**局限**:缓存是进程内内存,丢了就不回填——进程重启、超过 TTL、或网关没见过的历史(如切到别的网关),
对应那一轮仍会 400。这是有意的取舍:宁可那一轮失败,也不猜上游要不要这个字段。

**为什么是「时不时」而不是稳定复现**(2026-09-11 定位):拿到的 5 条 400,响应体里带着
`providerMetadata.gateway.routing` / `AI_APICallError` / `canonicalSlug` —— 说明渠道背后是一个**聚合网关**
(它自己还会重试:`providerAttemptCount=2`,第 1 次 429/502、第 2 次才报这个 400)。是否进入 thinking 模式
取决于它这次把 `deepseek/deepseek-v4.1-flash` 落到哪个后端,**不由客户端决定**——所以无法按需复现。
另外 a2o 请求侧本就**不转发 `thinking` 参数**(静默丢弃,与 `top_k`/`metadata` 同),客户端也左右不了它。
这也反过来印证了「只在缓存命中时回填」是对的:上游给过才回填,与它这次是不是 thinking 后端天然对齐。

> 复现只能靠测试而非线上:见 `TestE2EThinkingReasoningBackfill`(假上游稳定吐 reasoning)。把
> `backfillReasoning` 里的注入注掉,该测试立刻红,失败现场就是线上那条——assistant 消息只剩 `tool_calls`、
> 没有 `reasoning_content`。

### 5.3 上游额度获取(`GET /channels/{id}/quota`)

用**该渠道自己的 Key** 只读问上游「还剩多少额度」,按 `channel_type` 分发到四套完全不同的协议
(`internal/proxy/quota*.go`)。全部走 6s 超时,失败不影响转发,前端灰色占位 + tooltip 给原因。

| `channel_type` | 请求 | 形状 |
|---|---|---|
| `commandcode` | `GET https://api.commandcode.ai/alpha/billing/credits`(host 固定,不看 base_url) | `credits.*` 之和 = **剩余**;`windowLimits.{fiveHour,weekly}` 给 `used/cap/resetAt`,resetAt 是 epoch **毫秒** |
| `opencode` | `GET {apiRoot(base_url)}/v1/usage` | `{usage:{rolling,weekly,monthly:{status,percent,resetsAt}}}`,resetsAt 是 ISO-8601 |
| `deepseek` | `GET {apiRoot(base_url)}/user/balance`(**非 `/v1`**) | `{is_available, balance_infos:[{currency,total_balance,…}]}`,金额是**字符串**、无百分比 |
| `thirdparty` | 手工配 `quota_path` + `quota_shape` | 见下 |

第三方中转没有统一约定,故手工配置。形状三选一(`quotaShapes`):`usage`(通用信封,同 opencode)、
`oneapi`(`/v1/dashboard/billing/subscription` 取 `hard_limit_usd`,再 `/v1/dashboard/billing/usage` 取
`total_usage` 美分,剩余 = 前者 − 后者/100)、`newapi_user`(`/api/user/self` 取 `{data:{quota,used_quota}}`)。

两条必须当**失败**处理的回包,否则前端会显示「额度 0%」这种假好消息:
- 这类中转查不到 key 时常回 **HTTP 200 带 `{"error":{…}}`**(`relayErrorBody`);
- 路由不存在时可能回 **200 的 SPA HTML**(`ensureJSON` 要求 body 以 `{` 开头)。

`quota_path` 是用户输入、且请求会带上明文 Key,**故当 SSRF 面处理**(`ValidateQuotaPath`):只接受相对路径,
拒绝 `://`、反斜杠、`//` 开头、空白/控制字符,长度 ≤512,且 `url.Parse` 不得解析出 Host/Scheme ——
即 Key 只可能发往该渠道自己的 base_url 主机。

窗口百分比 `percent` 是**已用**;`QuotaWindow` 另带可选的 `used`/`cap`/`resetAt` 原始信息
(`resetAt` 上游给 ms 或 ISO 都归一成 RFC3339)。余额型上游(deepseek/one-api)填 `QuotaBalance`,
**按上游原币种原样展示,不折算** —— 汇率是官方价用的、手工维护的,不该拿去当余额前提。
`channel_type` 为空按 `thirdparty` 处理(老行/未回填);第三方没配路径时明确回「未配置额度查询路径」,
前端 `useQueries` 的 `enabled` 也据此不发起请求。

### 关键坑位(实现时对照)
- 管理端 PATCH 是**全量替换**(Update* 仓库方法会清零未传字段)。前端启停类操作用「先取全量快照再整包提交」
  (services/api.ts 的 toggle*/offerDraft 帮助器),勿发部分 body。
- 成功率口径:后端 successRate 用 0..100 百分数;前端统一除 100 还原 0..1 再 `×100` 展示(api.ts `frac`)。
- modernc.org/sqlite:`strftime` 返回 TEXT,与整型参数比较 `<=` 恒假 —— 一律 `CAST(... AS INTEGER)` 再比;
  聚合列包 `COALESCE(...,0)`(空窗口 SUM=NULL 会 Scan 报错)。
- 客户端断连必须取消上游请求(`ctx` / `resp.Body.Close()`),否则额度白烧;并归因成 499 而非渠道失败(§5.1)。
- 流式看门狗**不得在每次 Read 时重置首字节定时器**:那样「首字节窗口」会退化成「任意两次数据间隔」窗口,
  把上游的正常停顿全记成 502(生产实测 18 条误报)。首字节只盯一次,中途停顿时长另用宽松的 idle 窗口。
- 错误率统计一律走 `errCond`,勿再手写 `status >= 400`——否则 499 会污染渠道健康度。
- 首字节窗口 / `ResponseHeaderTimeout` 取自候选超时,**渠道上填的 `timeout_ms` 不参与**(§5.1)。要让上游慢时能
  及时 504 并 failover,得调 settings 的 `request_timeout_ms`;别在两处各填一个值然后奇怪哪个生效。
- a2o 回填的 `reasoning_content` **只在缓存命中时注入**(§5.2)。改动谓词时务必保留这个前提,
  否则会把该字段塞给不认识它的上游(OpenAI 官方/Azure)而新增 400。
- SQLite WAL,个人读多写少足够;管理端写操作集中在事务内(额度扣减等)。

### 5.4 熔断状态机与健康度口径(issue #17)

渠道熔断是引擎里的**显式状态**(`circuit.openUntil`),三个态由 `engine.Claim` / `engine.CircuitState`
统一表达 —— 选路的「能否选中」与读接口的「显示什么态」**共用同一个原语**,不再各判各的:

| 态 | 条件 | 选路 | 列表展示 |
|---|---|---|---|
| `closed` 未熔断 | `openUntil` 零值;或冷却后有近期成功证据 | 放行 | `healthy`(有流量）/ `degraded`(成功率 < 80%) |
| `down` 冷却中 | `now < openUntil` | **剔除** | `down` + `circuitOpen=true` + `availableFrom` |
| `probing` 待复检 | 曾熔断、冷却已过、尚无近成功证据 | **只放行一次探测** | `unknown`(前端「待观察」) |

要点与踩过的坑:

- **「无流量 ≠ 健康」。** 旧实现里近 15 分钟统计为空就直接 `healthy`/100%,而冷却窗口(`cooldown_sec`)
  常配得远大于 15 分钟 —— 一条刚熔断又恰好静默的渠道会显示回健康,引擎却仍在剔除它(issue #17)。
  现在无流量一律 `unknown`,除非近期确有一次成功。
- **半开只放行一次。** `Claim` 判定「冷却已过」时会把 `openUntil` 往后推一个冷却(原子占位),并发的
  第二个请求随即被拒 —— 否则一条渠道下多个供给源会同时涌入刚恢复的上游。同渠道多个 offer 共用一次机会
  (`Evaluate` 内按 `ChannelID` 去重)。
- **管理台「测试」成功即算复检证据。** 探测走 `RecordSuccess`(与真实转发同一原语),引擎记
  `circuit.lastOK`;于是无流量的渠道凭一次真实成功即可判 `healthy`,不必永远 `unknown`。
  证据有效期:`failures==0` 取 15 分钟(对齐展示窗口),否则取 30s(对齐最小冷却)。
- **失败的探测(499/中断)不写成功也不写失败**,`probing` 会持续到证据过期 —— 最多再等一个冷却,不漏判。
- 前端 `HealthStatus` 增加 `unknown`(文案「待观察」,灰色),渠道页状态筛选与 latency 占位同步更新;
  `unknown`/`down`/`disabled` 均无有效成功率与延迟,展示 `—`。

## 6. 管理 REST 契约(v2;会话鉴权)

| 方法与路径 | 作用 |
|---|---|
| `POST /api/v1/auth/bootstrap` / `login` / `logout` · `GET /me` | 首启建管理员(无则 409)/ 登录 / 登出 / 当前账号(含 role) |
| `POST /api/v1/auth/password` | 改自己密码(验旧密码,成功后重签 cookie) |
| `GET /api/v1/auth/state` | 匿名:回 `{adminExists}` 驱动首启引导 |
| `GET/POST /users` · `PATCH /users/{id}/password` · `DELETE /users/{id}` | 用户管理(仅 admin):建号/列号/重置密码/删号 |
| `GET/POST /channels` · `GET/PATCH/DELETE /channels/{id}` | 渠道 CRUD(改时 apiKey 留空=保持) |
| `POST /channels/{id}/test` · `/sync-models` | 连通探测 `{ok,latencyMs}`;拉 `/v1/models` 补目录+停用 offer |
| `GET /channels/{id}/quota` | 用该渠道自己的 Key 问上游额度(按 `channel_type` 分发,见 §5.3) |
| `GET/POST /models` · `PATCH/DELETE /models/{id}` | 目录(`name`=统一名、`originalName`=真实名)/新增/改(全量,`displayName` 非传=不变)/删;GET 全站可读 |
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
- 展示词表(providers/channelTypes/egressProtos/quotaShapes/capabilities 标签)属前端常量,与后端枚举一致;
  不作为运行时数据。渠道的展示名/徽标统一走 `utils/channel.ts` 的 `channelLabel`/`channelMark`
  (有厂商显示厂商,聚合渠道回落渠道类型),勿在页面里直接渲染 `ch.provider`(为空会显示空白)。

## 8. 运行与联调

- 后端静态托管:`web_dir`(默认 web-v2)的 `dist/` 于 `/`,非文件路径回退 `index.html`(SPA);生产只需一个 Go 进程。
- 开发期:`cd web-v2 && npm run dev`(Vite :5178,`/api`、`/v1` 已代理到 :8787),改前端热更。
- 冒烟路径:起网关(空库)→ 浏览器创建管理员 → 建渠道(指向假/真上游)+ 测试 → 同步或手工建模型 + offer 定价 →
  建令牌 → 用令牌打 `/v1/chat/completions`(或 `/v1/messages`)→ 刷新日志/用量/概览/渠道统计应实时变化。
- **失败率排障**:日志页状态筛选有 ok / error / **中断**(499)。概览的失败率只统计 error,
  「中断」占比高说明是客户端在取消(如 Claude Code 按 Esc),不指向渠道问题;按 err 文案可区分
  首字节超时与 `mid-stream` 静默(§5.1)。

### 8.1 生产部署(Go 自终止 TLS 双口 + 域名边缘 Nginx)

生产主机 = 云主机 `47.116.65.140`。Go 网关**自身终止 TLS** 于数据面 17080 / 管理台 17090
(容器 bridge + 端口发布);宿主**原生 Nginx** 作域名边缘,用**公信证书**终结 TLS:

```
[公网客户端]   --https--> gateway.5home.online(管理台) / gatewayapi.5home.online(数据面)   ← 公信证书,均 443
                                     │
                                     └─ Nginx ─回源 https─→ 127.0.0.1:17081/17090 (网关容器)
```

- **TLS 双口**:`config.tls` 配 `api_listen/api_cert/api_key` 与 `admin_listen/admin_cert/admin_key` 两组,
  齐全时 main 额外起两个 `http.Server`(分别挂 `HandlerAPI`/`HandlerAdmin`);缺任一项则只起明文 `listen`(dev/测试)。
  明文 `:8787` 仍起(容器 healthcheck `http://127.0.0.1:8787/healthz` 用),但 compose 不发布该端口。
- **自签证书**:`deploy/scripts/gen-certs.sh` 生成一个私有 CA + admin/api 两张独立叶子(各挂一个口,不共用)。
  SAN 覆盖 `ai-gateway.lan / localhost / 127.0.0.1 / <局域网 IP> / <公网 IP>`;域名上线后自签降级为
  **仅回源**(Nginx → 网关容器,`proxy_ssl_verify off`),不再给任何客户端用(旧 IP 客户端回退已随域名稳定退役)。
  `RESIGN=1` 只重签叶子保留 CA(客户端信任不失效),`FORCE=1` 连 CA 轮换。
- **域名边缘(Nginx)**:配置在**独立仓库 `host-infra`**(宿主级**多服务**边缘:公网入口/vhost/通配符证书
  集中管理,不散落在各业务仓库),本仓库只声明"网关占哪些端口、回源到哪"。端口与主机名分工:
  **443 上按 SNI 主机名分两面**——`gateway.5home.online` → 管理台/登录面、`gatewayapi.5home.online` → 数据面;
  两面共用同一张通配符证书(加面不加证书),443 的 `default_server`(裸 IP/未知主机名)返 444。
  **只从这两个主机名可达**:Nginx **不监听 17080/17090**,公网亦无对应发布口——「裸 IP + 非标端口」
  时代的口子全部关掉(容器发布口一律只绑 `127.0.0.1`,见下)。
  回源 `https://127.0.0.1:17081/17090`(`proxy_ssl_verify off`)以保留两面隔离;`proxy_buffering off`
  保 SSE 流式首字节不被攒住(对应旧 Caddy 的 `flush_interval -1`)。Nginx **覆写** `X-Forwarded-Proto`,
  因网关无条件信任该头(`admin_claude_config.go` / `admin_auth.go`)决定 Secure Cookie 与对外基址推断。
- **公信证书**:DNS-01 签通配符 `*.5home.online`(腾讯云 DNSPod),一张覆盖所有子域,不需开 80 口;
  工具默认 `acme.sh`(`dns_dp` / `dns_tencent` 原生支持,按凭证形态自动选),续期后自动 `systemctl reload nginx`。
- **对外基址**:设置项 `public_base_url`(管理台「系统设置 → 对外基址」)填 `https://gatewayapi.5home.online`;
  留空则 `admin_claude_config.go` 按 `X-Forwarded-Proto`/`Host` 推断。
- **容器端口发布**:`deploy/docker-compose.yml` 把两个口都收窄为 `127.0.0.1`——数据面
  `127.0.0.1:17081:17080`、管理台 `127.0.0.1:17090:17090`,**公网一律经 Nginx 的 443 + 域名**。
  容器内监听口不变(`config.prod.yaml` 仍 `:17080`/`:17090`),改绑定只需 `docker compose up -d`,
  **不必重建镜像**;发布口用 17081(非 17080)是必需的:与(曾经的)Nginx 抢同一端口会 bind 冲突。
- **部署流**:`deploy/scripts/deploy.sh [GW_HOST]`(默认 `rguo@192.168.0.202`)→ 本地 `build.sh` 构建镜像
  (前端 + 交叉编译 + docker build,版本由 `git describe` 注入 `-ldflags -X main.version`)→
  `docker save | ssh docker load` 推到目标主机 → 同步 compose/证书 → 远端 `compose up -d`。
  目标主机只需 docker,不需 Go/Node/Docker Hub。远端 `.env`(`GW_MASTER_KEY`/`GW_IMAGE_TAG`)与 `data/` 首次生成后保留。
- **版本可见**:`/healthz` 回 `{ok,store,version}`;`build.sh` 打 `ai-gateway:$VER` 与 `:latest` 便于回滚。

## 9. 迁移与留档

- v1 老库 `gateway.db`(upstreams/api_keys/request_log 等 v1 表)**整文件原样留档、不做迁移**;v2 用默认新库
  `gateway-v2.db`。二者混用同一文件会产生语义错乱的旧表残留,务必分开。
- v1 概念(统一 key 兼管、`upstreams`/`keys`/`pricing`/`quota`、旧 `/api/v1/upstreams` 面、旧 `web/` 前端)已在演进中退役删除。
- m0011 把渠道的「厂商 / 渠道类型 / 出站协议」拆成三列并回填老行:`provider` 收窄为真厂商
  (`Azure`→`OpenAI` + `egress_proto='azure'`,`聚合中转`→`''`);`channel_type` 按 base_url/provider 推断
  (`commandcode.ai`→`commandcode`、`opencode.ai`→`opencode`、`provider='DeepSeek'`→`deepseek`,其余 `thirdparty`)。
  迁移是追加式的,老库直接起新版本即可,无需手工干预。
- 渠道 api_key 密文依赖主密钥;换主密钥会解不开旧密文 → 保留原密钥即可回放。
