# personal-ai-gateway

个人 AI 网关(Go):一套统一 API key 接入你所有厂商的模型,统一用量记账、故障自动切换、配额感知选路与飞书告警。设计文档见 [`DESIGN.md`](DESIGN.md)。

## 快速开始

```bash
# 1. 准备配置与密钥(密钥走环境变量)
cp config.example.yaml config.yaml
export GATEWAY_KEY_LAPTOP=$(openssl rand -hex 24)
export OPENCODE_GO_KEY=...   # 你的上游 key
export ANTHROPIC_API_KEY=... # 需要 anthropic 型上游时

# 2. 启动
go run ./cmd/gateway -config config.yaml

# 3. 指个工具过来(以 Claude Code 为例)
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
export ANTHROPIC_API_KEY=$GATEWAY_KEY_LAPTOP
```

## 常用命令

```bash
go test ./...      # 单测
go vet ./...       # 静态检查
```

## 目录

- `cmd/gateway` 入口
- `internal/config` 配置加载与校验
- `internal/router` 模型匹配 + 候选排序 + 熔断 + 配额感知选路
- `internal/quota`  配额拉取/缓存/判硬(后台轮询)
- `internal/store` SQLite 请求日志 + 模型面 API key(只存 sha256)
- `internal/proxy`  转发内核(同协议透传 + SSE 流式回传)
- `internal/proxy/translate` 跨协议翻译(a2o:anthropic 入站 → openai 上游,流式 + 工具 + usage)
- `internal/server` HTTP 路由 + 双层 key 鉴权
- `web/`            管理台前端(React + antd v5,前后端分离,吃同一 `/api`)

> 当前阶段:P1–P3 + 管理面(订阅源 CRUD + 连通测试)+ 模型面 Key 管理 + a2o 跨协议翻译 + 管理台 Web(概览/用量/订阅源/配额告警/API Keys)。

## 模型面 Key(API Keys)

`config.yaml` 里的 `keys[]` 是**登录/管理 key**(能打 `/api/*` 管理面,如上面的订阅源 CRUD 与用量查询)。给 Claude Code / 脚本用**另一个运行时生成的模型面 key** —— 一个 key 解锁网关内所有模型与协议翻译,但它只能访问 `/v1/*`,访问管理面一律 401。DB 只存 sha256;明文只在创建响应出现一次,请当场保存。

```bash
KEY=$GATEWAY_KEY_LAPTOP
# 生成:201 响应里有 secret(只此一次)
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"name":"claude-code","note":"CC 用"}' http://127.0.0.1:8787/api/v1/keys
# 列(永不出现 secret/sha256)与吊销
curl -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/keys
curl -X POST -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/keys/claude-code/revoke
# 用模型面 key 打模型端点(吊销后立即 401)
export ANTHROPIC_API_KEY=<上面拿到的 secret>
```

## 跨协议翻译(a2o)

Claude Code 讲 Anthropic 协议(`/v1/messages`);DeepSeek/GLM 等订阅只给 OpenAI 兼容接口。网关在候选循环里对跨协议候选做**a2o 翻译**:Claude Code 直接发 `deepseek-chat` / `glm-4-flash` 这类模型名,网关改写请求、把上游响应(非流式整包 + **流式 SSE 逐 chunk**)转回 anthropic 形状 —— 含**工具调用**(tool_call_id ↔ tool_use id 可逆无状态映射)与 **usage**(流式末块转 `message_delta` 权威计数;`message_start` 的 input_tokens 为本地估算,仅供展示)。失败切换与熔断对翻译腿同样生效;上游 4xx 会被重编码成 anthropic 错误信封,不会把 openai 的原始错误体塞给 Claude Code。

模型名只在 openai 型上游时,`count_tokens` 用本地估算作答(启发式,非计量)。Web 管理台「API Keys」页可完成生成/吊销。

> 使用形态:一个模型若同时有 anthropic 型与 openai 型上游,优先走透传(最准确);只有 openai 上游时自动走翻译,客户端无感知。



## 配额感知自动切换(P3)

给配了 `quota` 块的上游(字段同 `/api/v1/upstreams` 的 JSON body,见下节),网关按 `cache_ttl_sec` 周期轮询其用量接口(`GET {base}/v1/usage`),当所选窗口 `used_pct ≥ hard_used_pct` 或 `status ≠ ok` 时,该上游从首选降为备选(熔断仍是硬排除;全部耗尽则尽力而为放行,避免请求直接失败)。拉取失败保留上次快照,不误伤上游。

```json
{
  "name": "opencode-go",
  "type": "openai",
  "base_url": "https://REPLACE_ME/v1",
  "api_key": "${OPENCODE_GO_KEY}",
  "priority": 1,
  "quota": {
    "enabled": true,
    "window": "monthly",
    "warn_used_pct": 80,
    "hard_used_pct": 95,
    "cache_ttl_sec": 60
  }
}
```

`window` 取 `rolling | weekly | monthly` 之一;`warn_used_pct` 只用于日志/未来告警,不改变选路。

## 管理订阅源(增删改查)

运行期改上游不碰 config、不用重启,走 `/api/v1/upstreams`(与模型端点共用同一把 key)。写库存的是原始形式(`api_key` 可写字面值或 `${ENV}` 引用);`GET` 列表**永不回显密钥明文**(env 引用回显 `${VAR}`,字面密钥只露头尾)。每次增删改即时生效。

```bash
KEY=$GATEWAY_KEY_LAPTOP
# 查
curl -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams
# 增:body = 一条上游的完整配置(JSON,字段见配额示例);api_key 必填
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "name":"second-sub","type":"anthropic",
  "base_url":"https://REPLACE_ME","api_key":"${SUB2_KEY}",
  "priority":2,"models":["*"]
}' http://127.0.0.1:8787/api/v1/upstreams
# 连通测试:GET …/models 探测,不耗模型配额
curl -X POST -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams/second-sub/test
# 改:发完整期望配置;api_key 留空 = 保持旧密钥
curl -X PUT  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "name":"second-sub","type":"anthropic",
  "base_url":"https://REPLACE_ME","priority":1,"models":["*"]
}' http://127.0.0.1:8787/api/v1/upstreams/second-sub
# 删:允许删空 —— 删到 0 条是合法态,此后模型请求返回 404,加源即恢复;删不存在的源 → 404
curl -X DELETE -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams/second-sub
```

> **说明**:订阅源以 `gateway.db` 里的 `upstreams` 表为运行时唯一来源,**`config.yaml` 不再承载上游**(`upstreams` 键会被忽略)。全新部署(空 DB)以空上游启动,模型请求返回 404,加源一律用上面的 API;删空后重启也不会"复活"。`gateway.db` 依旧被 gitignore。

## Web 管理台(React + Ant Design)

`web/` 里是一套**前后端分离**的 React + antd v5 管理台:概览(近 24h 用量卡 + 异常/告警卡片 + 14 天趋势图)、用量明细(服务端分页/排序/过滤)、订阅源运行期 CRUD(含连通测试)、配额与告警、API Keys(生成模型面 key,一次性明文展示,吊销)。它只消费网关现有的 `/api/*` 管理接口,与模型接口共用同一把统一 key;开发期由 Vite 把 `/api` 代理到网关(同源,无 CORS,后端不用动)。

```bash
# 终端 1:起网关
go run ./cmd/gateway -config config.yaml
# 终端 2:起前端(默认代理 http://127.0.0.1:8787,可用 GATEWAY_UPSTREAM 覆盖)
cd web && npm install && npm run dev
# 浏览器打开 http://localhost:5173,输入统一 key 进入(密钥仅存本会话 sessionStorage)
```

生产部署(Caddy 静态托管 dist + 同域反代管理/模型接口):

```bash
cd web && npm run build   # 产物在 web/dist
```

```text
your.domain {
    root * /path/to/personal-ai-gateway/web/dist
    try_files {path} /index.html              # SPA 路由回退
    reverse_proxy /api/*   127.0.0.1:8787
    reverse_proxy /v1/*    127.0.0.1:8787
    reverse_proxy /healthz 127.0.0.1:8787
}
```

> 安全:前端只在浏览器会话(sessionStorage)保存网关 key,不写盘;任何源码/日志不含明文。列表里的 `api_key` 一律由网关掩码后返回(env 引用原样、字面量只露头尾)。

## 用量查询 API

每次模型请求都会把元数据 + 解析到的 token 用量 + 成本写入 SQLite(`request_log`),可经 `/api/v1/usage/*` 查询。与模型端点共用同一把 key。

```bash
# 明细(分页/排序/过滤)
curl -H "Authorization: Bearer $GATEWAY_KEY_LAPTOP" \
  "http://127.0.0.1:8787/api/v1/usage/requests?limit=20&offset=0&sort=ts&order=desc&model=claude-sonnet-4-5&status_bucket=4xx"

# 汇总:默认最近 24h,可按 model/upstream/protocol/client_key 分组,并按 hour/day 拆时间桶
curl -H "Authorization: Bearer $GATEWAY_KEY_LAPTOP" \
  "http://127.0.0.1:8787/api/v1/usage/summary?group_by=model,upstream&bucket=day&from=2026-09-01T00:00:00Z&to=2026-09-03T00:00:00Z"
```

请求接口参数:

| 参数 | 说明 |
|---|---|
| `limit` / `offset` | 分页,limit 1..200(默认 50) |
| `sort` / `order` | `id, ts, status, latency_ms, prompt_tokens, completion_tokens, cache_read_tokens, cost, model, upstream, client_key, protocol`;`asc`/`desc` |
| 过滤 | `from`/`to`(RFC3339)、`protocol`、`model`、`upstream`、`client_key`、`stream`、`status`(精确码)、`status_bucket`(`2xx/3xx/4xx/5xx`) |

汇总接口额外参数:`group_by`(逗号分隔,同上一组白名单里的维度列)、`bucket`(`hour`/`day`)。成本需在配置里配好 `pricing` 单价表;没匹配到规则的模型 `cost` 记为 0。
