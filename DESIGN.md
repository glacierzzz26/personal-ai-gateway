# personal-ai-gateway 设计文档

一个用 Go 写的**个人** AI 网关:把多厂商模型(官方 API、OpenAI 兼容中转、订阅型)收敛到一个出口,
你用一套统一 API key 就能让 Claude Code / OpenCode / 自写脚本访问"你的所有模型"。
同时提供用量记账、故障自动切换(failover)、配额感知选路、飞书告警,并为后续 Web 管理端预留接口。

> 定位:个人 / 少量上游(几家到十几家)。设计上追求简单、可观测、可演进,不追求企业级。

---

## 1. 架构总览

```
  Claude Code      OpenCode / 脚本        (未来的)Web 管理端
  (Anthropic协议)   (OpenAI协议)             └── /api/*  JSON 管理 API
       └──────┬──────────┘                          │
              ▼                                     ▼
┌────────────────────────────── 网关(单进程,公网,前面 Caddy/TLS) ──────┐
│ HTTP 层:  /v1/messages  /v1/chat/completions  /v1/models  /healthz     │
│   · 统一 key 鉴权(x-api-key 或 Authorization Bearer,常量时间比较)        │
│   · 访问日志( stdout ) + request_log( SQLite )                          │
│ 代理层: 读请求体 → 解析 model/stream → 路由选上游 → 转发/翻译 → 流式回传 │
│   · 同协议:透传(改 header,不改 body)                                     │
│   · 跨协议:a2o 翻译(待实施,T6)                                           │
│ 路由层:  模型注册表(规范模型名→可用上游) + failover + 熔断(冷却)          │
│ 记账层:  每次请求落库;token 用量(非流式+SSE 嗅探)与成本折算(P2 ✅)        │
│ 告警层:  飞书 webhook,状态变化才发(P4)                                   │
└─────────────────────────────────────────────────────────────────────────┘
     │                          │                          │
  上游 openai 型           上游 anthropic 型            (本地 Ollama…)
  (opencode-go / 中转…)     (官方 Anthropic)
```

### 核心决策:内部归一化
两个入站端点各自解析成"路由所需的最小信息"(model、stream),选中上游后
按上游协议类型转发;流式响应按入站协议回传。新增一个上游 = 一份配置,通常不用写代码。

### Web 是一等公民(不是后补)
管理端只依赖网关暴露的 **JSON 管理 API**(`/api/*`),静态页是它的一个客户端。
所以从 P1 起 `/api/*` 命名空间就保留、请求日志落 SQLite(可查询),而不是 stdout 或文件,
后面做用量页/管理页不需要重构数据层。

---

## 2. 目录结构

```
cmd/gateway/main.go        # 组装:config → store → router → proxy → server
internal/
  config/    # YAML 加载、${ENV} 展开、默认值、校验
  sess/      # 鉴权上下文(认证后的 key 名、工具名、Admin 位跨包传递)
  router/    # 模型匹配 + 候选排序 + 熔断
  store/     # SQLite(modernc.org/sqlite,纯 Go 无 cgo):request_log + api_keys
  proxy/     # 数据面:转发、SSE 流式回传、错误归一、日志落库
    translate/ # (a2o)协议翻译:anthropic↔openai 形状改写 + SSE 状态机(见决策 #16)
  server/    # HTTP 路由、双层 key 鉴权、访问日志中间件
web/         # (P2.5+) 管理台前端:React+antd v5,前后端分离(独立构建,吃同一 /api)
config.example.yaml
DESIGN.md
```

---

## 3. 关键设计决策与坑位(实现时对照)

1. **failover 的边界**。一旦向客户端回 `2xx` 并开始吐流式 chunk,就**不能**再换上游。
   - 安全切换窗口 = 首个响应字节之前:连接失败 / 超时 / 上游返回可重试状态码。
   - 可重试:`429`、`5xx`、传输层错误。不可重试(`400/401/403/404` 等)直接透传上游错误体给客户端,便于看清是哪家的问题。
   - 流式中途断开:不静默换,透传错误并记录(否则客户端拿到语义断裂的流)。
2. **客户端断连必须取消上游请求**,否则余额/配额白烧。实现:`select ctx.Done() vs 转发完成`,断连即 `resp.Body.Close()`。
3. **熔断**:每家上游连续失败 `max_failures` 次 → 冷却 `cooldown_sec`,期间不进候选。
   否则一次上游故障会让请求把所有家都打一遍。
4. **协议矩阵**(消费方 × 上游):
   | 消费方\上游 | openai 型 | anthropic 型 |
   |---|---|---|
   | OpenCode / 脚本 (OpenAI) | 透传 ✅(P1) | o2a 翻译(暂未实施) |
   | Claude Code (Anthropic) | **a2o 翻译 ✅(本次;流式+工具+usage)** | 透传 ✅(P1) |
   同协议永远走**逐字节透传 fast path**(copySSE/copyNonStream),翻译只是候选循环里的并行分支,
   只对真正跨协议的候选生效。调度按 `[inProto,outProto]` 查表,给 o2a / 新消费方预留对称位置(见决策 #16)。
5. **上游协议判定**:配置里 `type` 决定出站协议与出站路径。约定——
   - `type: openai` → base_url **含 `/v1`**(如 `https://…/v1`),出站拼 `/chat/completions`;
   - `type: anthropic` → base_url 为**域名根**(如 `https://api.anthropic.com`),出站拼 `/v1/messages`、`/v1/messages/count_tokens`。
6. **模型名即路由键,不做别名映射**(P1)。客户端发的 model 名 = 上游真实模型 id。
   中转站往往用 `claude-*` 同一套 id,所以透传时 body 里的模型名不用改。
   别名/重映射留到需要时再补(上游给自家 id 时)。
7. **记账口径**:每次请求落 `request_log` 元数据(时间/key/工具/协议/model/上游/状态/延迟/错误)。
   P2 起解析 token 用量并乘单价算成本入库——
   - 非流式:整段响应经 TeeReader 缓冲后按协议解析 `usage`;
   - 流式(SSE):按行嗅探——openai 型取 chunk 里的 `usage`;anthropic 型取 `message_start` 的
     input/cache_creation 与 `message_delta` 的 output(**累计口径假设**,需对真实上游校准,见 §8)。
   **默认不记录请求/响应体**,只记元数据。
8. **公网安全**:统一 key 要强随机(例子里走环境变量);模型接口全部要鉴权;
   前置 Caddy 终结 TLS(自动证书)。`/healthz` 放行但只回 ok,不泄露内部信息。
9. **并发与 SQLite**:WAL 模式,读多写少,个人规模完全够;后续 /api 只读查询也顺畅。
10. **modernc 时间过滤的坑**:`strftime('%s', ts)` 返回 TEXT,在 modernc.org/sqlite 里拿它和
    整数参数直接比较时 `<=` 恒假(隐式 TEXT→INT 转换有 bug,`>=` 反而正常),表现为"带 from/to 的
    查询悄悄滤光所有行"。一律先 `CAST(... AS INTEGER)` 再比(见 `queries.go` 的 `where()`)。
    另:聚合列全部包 `COALESCE(...,0)`——否则空窗口的无分组查询会产出单行 `SUM=NULL`,
    Scan 进 int 直接报错(Web 端"查一个无用量的时间段"必然踩中)。
    时间过滤在 `strftime` 上无法走 `idx_reqlog_ts`,个人规模可接受。
11. **配额感知选路(P3)语义**。
    - 配额接口约定:`GET {base}/v1/usage`,只认 `Authorization: Bearer`(发 `x-api-key` 会 401)。
      `type: anthropic` → base 为域名根再拼 `/v1/usage`;`type: openai` → base 已含 `/v1` 再拼 `/usage`(与决策 #5 同款拼接)。
      响应 `{"usage":{"rolling|weekly|monthly":{"status","percent","resetsAt"}}}` —— percent 业界惯例为**已用百分比**。
    - percent 整数粒度很粗(小请求推不动),方向按已用默认;万一某上游 percent 表示"剩余",
      用 `quota.invert_used_pct: true` 换算。`status != "ok"` 视为硬信号(强判耗尽)。
    - **hard 是软降级不是硬排除**:配额耗尽的 upstream 只是排到正常候选之后(尽力而为)。
      只有熔断(决策 #3)是硬排除。若全部候选都 hard,则全部放行避免请求直接失败。
      warn 档只写日志/事件,不影响选路(P4 告警复用)。
    - 拉取失败 fail-open:保留最后一次成功快照,不踢上游。配额轮询是独立 goroutine,
      只往 router 里 set 状态,路由/请求路径不加锁热点(快照存 router 由 RWMutex 保护)。
12. **配额窗口选哪个**:`rolling` 按请求滚动、`weekly/monthly` 按自然周期。订阅页最常看的是月度额度,
    默认 `window: monthly`;挑一个最能代表"还能不能跑"的窗口即可,暂不合并多窗口。
13. **运行期管理订阅源:DB 唯一权威,config.yaml 不承载上游**。
    - 上游增删改走 `/api/v1/upstreams`(变更面提前到管理期做,不再等 P4)。`config.yaml` 没有
      `upstreams` 块;全新部署(DB 表空)以空上游启动,增源只能走 API / 管理台,删空重启不会复活。
    - **空上游是合法态**:删除不设下限(删最后一条也允许,无 409);router 零候选时模型请求返回
      404 `not_found_error`,管理面与 /healthz 照常服务(README 有说明)。
    - **raw / resolved 两层**:DB 存 raw(api_key/base_url 可能还是 `${ENV}` 引用);
      出站前经 `config.ResolveUpstreams` 展开 env + 补默认值成 resolved,router / quota / proxy 只用它。
      数据库永不落展开后的明文;`GET` 列表对 api_key 掩码(env 引用回显 `${VAR}` 本身)。
    - 变更链路:validate → 单事务全量 `ReplaceUpstreams` → `router.Apply`(整表换指针,
      在飞请求持旧指针安全,同名熔断状态携带)→ `quota.Apply` + `RefreshAll`(配置没变的条目
      保留缓存快照,窗口变了才重置重拉)。
    - API key 新老都收:字面值或 `${ENV}` 引用,都落 raw;`PUT` 时 `api_key` 留空 = 保持不变,
      不用每次改配置重贴密钥。
14. **鉴权分两层:config key(登录/管理,全权)与 DB 模型面 key(只放 `/v1/*`)**。
    - config `keys[]` = 管理员钥匙:能打 `/api/*` 管理面与 `/v1/*` 模型面。
    - DB `api_keys` 表存**运行时生成的模型面 key**:secret `sk-gw-`+32 hex,库只存 sha256,明文仅
      `POST /api/v1/keys` 的 201 响应出现一次;命中后仅路径前缀 `/v1/` 放行,访问 `/api/*` 一律 401
      (与无效 key 同表现,不泄露"key 有效"这个事实)。判定顺序:config `FindKey` → DB `LookupActiveKey`
      (`sha256=? AND revoked=0`)。
    - `request_log.client_key` 直接取 `sess.Info.KeyName` → 生成 key 的归属零改动生效。
15. **翻译路径的记账与错误语义**(与透传对齐):
    - a2o 流式:上游带 usage 的末块 → 收尾时放进 `message_delta.usage`(opencode 同款,官方只定义
      output_tokens、Claude Code 实测可容,见决策 #16);`message_start.usage.input_tokens` 是本地估算(展示用)。
    - a2o 非流式:整包缓冲、翻译成功才写首字节;翻译失败回干净 502 且不重试(避免对同一上游重放重复计费)。
    - 上游**不可重试 4xx** 重编码成 anthropic 错误信封(不把 openai 的 `{"error":…}` 原样塞给 Claude Code);
      429/5xx 未写字节,照常交候选循环 failover。
    - `count_tokens`:模型只有 openai 型上游时本地估算回 `{"input_tokens":n}`(启发式,估算值不入 request_log 的 token 列)。
16. **协议翻译(translate 包)的接缝**:
    - 包不得 import `internal/proxy`(防 import 环):协议/操作常量在两侧复刻、自持 `Usage` 口径,
      `tryRelayTranslate` 在边界转成 proxy.usage。
    - 只翻译被调度的 anthropic `messages` op;`count_tokens` 无 openai 出站(决策 #15)。
    - tool_call_id ↔ tool_use id 是**可逆无状态**映射(`toolu_`+base64url(provider call id)),跨轮/failover 天然成立;
      解码失败的 id 原样透传 → 上游正确拒绝错误关联(正确失败而非静默错配)。
    - 流式事件序硬约束:`message_start` 恒最先、恰一次 `message_stop`、所有 `content_block_stop` 先于
      唯一 `message_delta`。`message_delta` 先按住不即时发,等上游 usage 末块到了再收尾。

SQLite 库 `gateway.db`:

```sql
CREATE TABLE IF NOT EXISTS request_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  ts            TEXT    NOT NULL,          -- RFC3339(UTC)
  client_key    TEXT    NOT NULL DEFAULT '',
  client_tool   TEXT    NOT NULL DEFAULT '',  -- User-Agent
  protocol      TEXT    NOT NULL,          -- anthropic | openai(入站)
  model         TEXT    NOT NULL,
  upstream      TEXT    NOT NULL DEFAULT '',
  stream        INTEGER NOT NULL DEFAULT 0,
  status        INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cost          REAL    NOT NULL DEFAULT 0,
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  err           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_reqlog_ts   ON request_log(ts);
CREATE INDEX IF NOT EXISTS idx_reqlog_model ON request_log(model);

CREATE TABLE IF NOT EXISTS upstreams (
  name TEXT PRIMARY KEY,
  doc  TEXT NOT NULL,      -- 整条上游配置的 YAML(raw 形式,${ENV} 原样保留)
  ord  INTEGER NOT NULL    -- 列表顺序
);

CREATE TABLE IF NOT EXISTS api_keys (           -- 运行时生成的模型面 key(决策 #14)
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL UNIQUE,
  prefix     TEXT    NOT NULL,             -- 展示用(secret 前 12 字符)
  sha256     TEXT    NOT NULL UNIQUE,      -- 库只存哈希,明文永不落库/日志/列表
  note       TEXT    NOT NULL DEFAULT '',
  revoked    INTEGER NOT NULL DEFAULT 0,
  created_at TEXT    NOT NULL,             -- RFC3339Nano UTC
  revoked_at TEXT    NOT NULL DEFAULT ''   -- '' = 激活
);
CREATE INDEX IF NOT EXISTS idx_apikeys_sha256 ON api_keys(sha256);
```

P2 起据此做按日/模型/上游聚合查询(这就是 Web 用量页的数据源)。`upstreams` 表是运行期订阅源唯一权威(决策 #13)。

---

## 5. 配置(config.example.yaml)

- `keys[]` 统一 API key(名字+secret+备注)。secret 用 `${ENV}` 从环境注入,避免进仓库。
- 上游**不在 config.yaml**(决策 #13):订阅源存 `gateway.db` 的 `upstreams` 表,经 `/api/v1/upstreams` 维护。一条上游(即 API 的 JSON body)的字段:
  - `name` / `type`(openai|anthropic)/ `base_url` / `api_key`(可用 `${ENV}`,运行期也收字面值)
  - `priority` 越小越优先(选路顺序)
  - `models[]` 该上游能出哪些模型;`"*"` 或空 = 全部;支持前缀通配如 `claude-*`
  - `cooldown_sec` / `max_failures` 熔断参数(默认 10s / 3 次)
  - `quota`(可选,该上游暴露用量接口时):`enabled`、`window`(rolling|weekly|monthly,默认 monthly)、
    `warn_used_pct`(默认 80,仅事件/日志)、`hard_used_pct`(默认 95,≥ 视作耗尽)、
    `cache_ttl_sec`(默认 60,也是轮询周期)、`invert_used_pct`(percent 表剩余时 true)。见决策 #11。
- `pricing[]` 成本单价表(USD/百万 token):`model`(`"*"` 或 `claude-*` 通配)+
  `prompt_per_m` / `completion_per_m` / `cache_read_per_m`。按声明顺序匹配第一条;
  每次请求成本 =(prompt×pm + completion×cm + cache_read×crm)/ 1e6 入库。无匹配则 cost 记 0。

---

## 6. 阶段计划

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P1 ✅** | 双端点 + 统一 key + 上游配置 + 同协议透传 + failover/熔断 + SQLite 请求日志 | `go test ./...` 绿;同协议链路真实跑通;拔掉首选上游自动切备选 |
| **P1.5 ✅** | 上游真实链路联调。发现 opencode-go 是**双协议**上游(`https://opencode.ai/zen/go` 同时给 `/v1/messages` 与 `/v1/chat/completions`):Claude Code 走 anthropic 型、OpenCode 走 openai 型,**全程透传**,无需跨协议翻译 | 真实流量稳定 |
| **P2 ✅(API)** | 用量采集(非流式+SSE 嗅探)+ 成本入库 + `pricing` 单价表 + `/api/v1/usage` 查询。**Web 页缓做**——先把 JSON API 设计稳(分页/排序/过滤/分组/时间桶),Web 只是它的一个客户端 | `go test ./...` 绿;真实流式请求校准 anthropic 用量启发式(§8) |
| P2.5 | 管理台 Web(React+antd)已并到下方「管理台 Web ✅」实现;别名映射仍待需要时再做 | — |
| **P3 ✅** | 配额/订阅型用量窗口 + 主动选路(配额快尽自动切)。轮询 `{base}/v1/usage`,hard(≥`hard_used_pct` 或 status≠ok)降级为备选 | `go test ./...` 绿;真实订阅(两端共用一个 opencode 订阅)→ 需第二个独立订阅才能肉眼验证切换 |
| **管理面 ✅** | 订阅源运行期 CRUD:`/api/v1/upstreams`(增/删/改/查,允许删空)+ `POST …/{name}/test` 连通探测。DB 唯一权威(config.yaml 不承载上游);router/quota 热应用 | `go test ./...` 绿;重启后仍读到 DB 里的订阅源;改完无需重启即生效 |
| **管理台 Web ✅** | `web/`:React+antd v5 管理台(前后端分离,吃同一 `/api` 与统一 key)。概览(近 24h 统计卡 + 异常/告警卡片 + 14 天 ECharts)+ 用量明细(服务端分页/排序/过滤/自动刷新)+ 订阅源 CRUD(掩码 key、二次确认、连通测试结果)+ 配额与告警(进度环分级)+ API Keys(生成/一次性明文/吊销)。密钥只进 sessionStorage,列表密钥由网关掩码 | `npm run build` 绿;Vite 代理 `/api`→网关链路通;页面数据与 API 一致 |
| **T6 ✅** | **a2o 跨协议翻译(完整:流式 + 工具调用 + usage)** + 模型面 key 管理(DB `api_keys`、双层鉴权)。Claude Code 可直连纯 openai 型上游(DeepSeek/GLM);路由 failover/熔断对翻译腿同样生效 | `go test ./...` 绿;假上游集成测试覆盖非流/流式/tool id 往返/4xx 重编码/failover;Web Keys 页可用 |
| P4 | 飞书告警(状态变化聚合)+ o2a / 新消费方 + Docker 部署 + 加固 | 配额/故障告警不刷屏 |

## 7. 管理 API 合约

`/api/*` 是管理数据的一等入口(Web 页/脚本共用),全部走统一 key 鉴权(`x-api-key` 或 `Authorization: Bearer`)。查询面(用量/配额)、订阅源变更面与模型面 key 管理均已实现。管理面只认 config key(决策 #14);DB 生成的模型面 key 访问任何 `/api/*` 一律 401。

### 上游订阅源:GET/POST/PUT/DELETE /api/v1/upstreams
- body 字段 = 一条上游配置(`name,type,base_url,api_key,priority,models,cooldown_sec,max_failures,quota`,字段清单见 §5)。写库的是 raw(api_key/base_url 可含 `${ENV}`),读回/出站由 `ResolveUpstreams` 展开(决策 #13)。
- `GET` → `{"data":[{…}]}`,列表按配置序;**api_key 永不回显明文**:`${VAR}` 原样回显,字面密钥只给头尾 `sk-…abcd`。
- `POST` 新增:201 + 新行;`api_key` 必填;重名 409;校验失败 400。
- `PUT /{name}` 修改:200 + 新行;name 取自路径、不可改;请求给**完整期望配置**,`api_key` 留空 = 保持旧密钥;目标不存在 404。
- `DELETE /{name}`:204;允许删空(空上游合法,模型请求将 404);不存在 404。
- `POST /{name}/test` 连通探测:按上游 type `GET …/v1/models`(anthropic)或 `…/models`(openai),5s 超时,不打模型请求不耗配额。响应 `{reachable,status?,message}`:2xx 可达、401/403=可达但 key 被拒、其它状态=可达(HTTP n)、连接失败=不可达。
- 每次变更即热生效:DB 单事务落盘 → router.Apply(熔断状态跨同名保留)→ quota.Apply+RefreshAll,无需重启。

```bash
KEY=$GATEWAY_KEY_LAPTOP
curl -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "name":"second-sub","type":"anthropic","base_url":"https://…","api_key":"${SUB2_KEY}","priority":2
}' http://127.0.0.1:8787/api/v1/upstreams
curl -X POST -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams/second-sub/test
curl -X DELETE -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams/second-sub
```

### GET /api/v1/usage/requests — 明细列表
- 分页:`limit`(1..200,默认 50)、`offset`(默认 0)。响应 `meta.total` 给总数(用同条件 COUNT),`meta.returned` 给本页条数。
- 排序:`sort` × `order`(asc|desc)。sort 白名单:`id, ts, status, latency_ms, prompt_tokens, completion_tokens, cache_read_tokens, cost, model, upstream, client_key, protocol`。默认 `id desc`。
- 过滤(全可选、可组合):`from`/`to`(RFC3339,UTC)、`protocol`(openai|anthropic)、`model`、`upstream`、`client_key`、`stream`(true|false)、`status`(精确码)、`status_bucket`(`2xx`/`3xx`/`4xx`/`5xx`)。`status` 与 `status_bucket` 二选一。
- 响应信封:`{"meta":{"limit","offset","total","returned"},"data":[{一条请求}]}`。`ts` 为 RFC3339Nano;`cost` 为美元。

### GET /api/v1/usage/summary — 聚合汇总
- 时间窗:默认最近 24h(避免数据多时全表统计),可用 `from`/`to` 覆盖(含两端)。
- 分组:`group_by=model,upstream` 逗号分隔,维度白名单同排序列表里的维度列(`model, upstream, protocol, client_key`)。未选维度不出现。
- 时间桶:`bucket=hour|day`,在分组之上再拆时间行(按 ts 文本截断,UTC)。
- 响应信封:`{"meta":{"from","to","group_by","bucket"},"totals":{…},"data":[{bucket?,维度…,指标…}]}`。
- 指标(每行与 totals 一致):`requests, errors(≥400), prompt_tokens, completion_tokens, cache_read_tokens, cost, avg_latency_ms`。
- 空窗口返回 `totals` 全 0、`data` 空数组(见决策 #10 的 COALESCE)。

```bash
KEY=$GATEWAY_KEY_LAPTOP
curl -H "Authorization: Bearer $KEY" \
  "http://127.0.0.1:8787/api/v1/usage/requests?model=claude-sonnet-4-5&status_bucket=4xx&sort=prompt_tokens&order=desc&limit=20"
curl -H "Authorization: Bearer $KEY" \
  "http://127.0.0.1:8787/api/v1/usage/summary?group_by=model,upstream&bucket=day"
```

> 参数错误统一返回 `400 {"error":{"type":"api_error","message":…}}`;非法 sort/group/bucket/status 均 400(白名单兜底防 SQL 注入)。

### GET /api/v1/quota — 配额感知选路状态
- 返回每个上游当前配额快照(诊断用 / 未来 Web 用量页数据源)。
- 响应信封:`{"data":[{upstream,type,enabled,window?,warn_used_pct?,hard_used_pct?,used_pct?,status?,hard?,resets_at?}]}`。
  `enabled=false`(未配 `quota` 块或 `enabled:false`)的行只有基础字段;启用但从未拉到快照的行
  `used_pct/status/resets_at` 为 `null`、`hard=false`。`resets_at` 为该窗口重置时间(UTC)。
- `hard` 即当前路由判定:为 `true` 时该上游在 `Candidates` 里排到正常候选之后。

```bash
KEY=$GATEWAY_KEY_LAPTOP
curl -H "Authorization: Bearer $KEY" "http://127.0.0.1:8787/api/v1/quota"
```

### 模型面 key:GET/POST /api/v1/keys、POST /api/v1/keys/{name}/revoke
- 目的:运行时生成**给 Claude Code/脚本**用的模型面 key,与登录/管理 key 分离。一个 key 解锁网关内
  所有模型(含跨协议 a2o 腿),但它只能访问 `/v1/*`(决策 #14)。
- `GET` → `{"data":[{id,name,prefix,note,revoked,created_at,revoked_at}]}`。**永不包含 secret/sha256**;
  `prefix` 是 secret 前 12 字符(展示/对账用)。`revoked_at` 激活时为空串。
- `POST` `{name,note?}` → 201 + 上行字段 + **`secret`(一次性明文)**。name 规则 `^[a-zA-Z0-9._-]{1,64}$`;
  与 config key 重名或 DB 重名 409;非法 400。明文只在这次响应出现:DB 只存 sha256,handler 绝不 log。
- `POST /{name}/revoke` → 200 + 新行(`revoked:true` 带 revoked_at);未知名 404;已吊销幂等 200(不改 revoked_at)。
- 吊销后该 key 立即失效;模型面 key 访问 `/api/*` 从创建起就 401(与无效 key 同表现)。

```bash
KEY=$GATEWAY_KEY_LAPTOP
# 生成(拿 secret,只此一次)
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"name":"claude-code","note":"CC 用"}' http://127.0.0.1:8787/api/v1/keys
# 列 / 吊销
curl -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/keys
curl -X POST -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/keys/claude-code/revoke
```

## 8. 联调 / 校准本机(不暴露公网时)

```bash
export GATEWAY_KEY_LAPTOP=sk-xxx  # 自造强随机串
export ANTHROPIC_API_KEY=... OPENCODE_GO_KEY=...
go run ./cmd/gateway -config config.yaml

# Claude Code 指向网关(同协议透传,需上游是 anthropic 型)
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
export ANTHROPIC_API_KEY=<GATEWAY_KEY_LAPTOP>

# OpenCode / 脚本(需上游是 openai 型)
export OPENAI_BASE_URL=http://127.0.0.1:8787/v1
export OPENAI_API_KEY=<GATEWAY_KEY_LAPTOP>
```

### 用量入库校准(尤其 anthropic 流式)

对真实上游发几条(小)请求,再查 `/api/v1/usage/requests` 看解析出的 token 是否可信:

```bash
# 非流式
curl -H "x-api-key: $GATEWAY_KEY_LAPTOP" http://127.0.0.1:8787/v1/messages \
  -d '{"model":"<model>","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'
# 流式
curl -N -H "x-api-key: $GATEWAY_KEY_LAPTOP" http://127.0.0.1:8787/v1/messages \
  -d '{"model":"<model>","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}'

curl -H "x-api-key: $GATEWAY_KEY_LAPTOP" \
  http://127.0.0.1:8787/api/v1/usage/requests?sort=id
```

重点核对 anthropic **流式**的 output_tokens 口径(决策 #7):实现按 `message_delta` **累计值**取最大。
若某上游实际发的是**增量**(每个 delta 只有当段新增量),会把 token 记低——到 `sseAnthropicUsage`
把 max 改成累加即可,一行注释处已标出。非流式(整包 usage)与 openai 型(多为一次性或末 chunk 带 usage)
基本可信,无需校准。
### 冒烟:模型面 key + a2o 翻译(需已配 openai 型上游,如 DeepSeek/GLM)

```bash
KEY=$GATEWAY_KEY_LAPTOP
# 1. 生成模型面 key(拿 201 里的 secret,只此一次)
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"name":"smoke","note":"a2o 冒烟"}' http://127.0.0.1:8787/api/v1/keys
# 2. Claude Code 形状直打 openai 型上游(deepseek-chat 最便宜):非流式
curl -H "x-api-key: $MODEL_KEY" http://127.0.0.1:8787/v1/messages \
  -d '{"model":"deepseek-chat","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'
# 3. 流式(校验事件序:message_start → … → message_delta → message_stop;message_delta 带 input_tokens 为 opencode 同款校准点)
curl -N -H "x-api-key: $MODEL_KEY" http://127.0.0.1:8787/v1/messages \
  -d '{"model":"deepseek-chat","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}'
# 4. 吊销后同 key 再打应 401
curl -X POST -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/keys/smoke/revoke
```
