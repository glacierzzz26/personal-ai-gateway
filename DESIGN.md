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
  sess/      # 鉴权上下文(认证后的 key 名、工具名跨包传递)
  router/    # 模型匹配 + 候选排序 + 熔断
  store/     # SQLite(modernc.org/sqlite,纯 Go 无 cgo):request_log
  proxy/     # 数据面:转发、SSE 流式回传、错误归一、日志落库
  server/    # HTTP 路由、统一 key 鉴权、访问日志中间件
  web/       # (P2+) 静态管理页 + /api 实现
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
   | Claude Code (Anthropic) | **a2o 翻译(T6,待实施)** | 透传 ✅(P1) |
   P1 先保证透传链路真实可用;跨协议翻译单独迭代(T6),避免一次吃下过多不稳定代码。
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

---

## 4. 数据模型

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
```

P2 起据此做按日/模型/上游聚合查询(这就是 Web 用量页的数据源)。

---

## 5. 配置(config.example.yaml)

- `keys[]` 统一 API key(名字+secret+备注)。secret 用 `${ENV}` 从环境注入,避免进仓库。
- `upstreams[]`:
  - `name` / `type`(openai|anthropic)/ `base_url` / `api_key`(可用 `${ENV}`)
  - `priority` 越小越优先(选路顺序)
  - `models[]` 该上游能出哪些模型;`"*"` 或空 = 全部;支持前缀通配如 `claude-*`
  - `cooldown_sec` / `max_failures` 熔断参数(默认 10s / 3 次)
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
| P2.5 | 简单 Web 用量页(读同一 `/api`)+ 模型别名映射(如需要) | 页面上能按天/模型/上游看 token 与成本 |
| P3 | 配额/订阅型用量窗口 + 主动选路(配额快尽自动切) | 阈值触发自动切,日志可查 |
| P4 | 飞书告警(状态变化聚合)+ key 管理入库 + Docker 部署 + 加固 | 配额/故障告警不刷屏 |
| T6 | **a2o / o2a 跨协议翻译**。目前无需求(P1.5 证实 opencode-go 双协议);仅当要接"纯 openai 型上游 + Claude Code 直连"时再做 | Claude Code 直连 openai 型上游走通 |

## 7. 管理 API(用量)合约

`/api/*` 是管理数据的一等入口(Web 页/脚本共用),全部走统一 key 鉴权(`x-api-key` 或 `Authorization: Bearer`)。当前实现的是查询面;变更面(改上游/建 key)随 P4 再做。

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