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
- `internal/store` SQLite 请求日志
- `internal/proxy`  转发内核(透传 + SSE 流式回传)
- `internal/server` HTTP 路由 + 统一 key 鉴权
- `web/`            管理台前端(React + antd v5,前后端分离,吃同一 `/api`)

> 当前阶段:P1 + P2(用量与成本入库 + 查询)+ P3(配额感知自动切换)+ 管理面(订阅源 CRUD + 连通测试)+ 管理台 Web(概览/用量/订阅源/配额告警)。跨协议(Anthropic↔OpenAI)翻译在计划中。

## 配额感知自动切换(P3)

给配了 `quota` 块的上游,网关按 `cache_ttl_sec` 周期轮询其用量接口(`GET {base}/v1/usage`),当所选窗口 `used_pct ≥ hard_used_pct` 或 `status ≠ ok` 时,该上游从首选降为备选(熔断仍是硬排除;全部耗尽则尽力而为放行,避免请求直接失败)。拉取失败保留上次快照,不误伤上游。

```yaml
upstreams:
  - name: opencode-go
    type: openai
    base_url: https://REPLACE_ME/v1
    api_key: ${OPENCODE_GO_KEY}
    priority: 1
    quota:
      enabled: true
      window: monthly     # rolling | weekly | monthly
      warn_used_pct: 80   # 仅日志/未来告警
      hard_used_pct: 95
      cache_ttl_sec: 60
```

## 管理订阅源(增删改查)

运行期改上游不碰 config、不用重启,走 `/api/v1/upstreams`(与模型端点共用同一把 key)。写库存的是原始形式(`api_key` 可写字面值或 `${ENV}` 引用);`GET` 列表**永不回显密钥明文**(env 引用回显 `${VAR}`,字面密钥只露头尾)。每次增删改即时生效。

```bash
KEY=$GATEWAY_KEY_LAPTOP
# 查
curl -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams
# 增:body 字段与 config.yaml 里一条 upstreams 相同(JSON);api_key 必填
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
# 删:最后一条上游不允许删(409)
curl -X DELETE -H "Authorization: Bearer $KEY" http://127.0.0.1:8787/api/v1/upstreams/second-sub
```

> **迁移说明**:订阅源以 `gateway.db` 里的 `upstreams` 表为运行时唯一来源。首次用本版本启动时表为空 → 自动把 `config.yaml` 的 `upstreams` 原样播种一次(保持 `${ENV}` 引用)。**此后手改 `config.yaml` 的上游不再生效**,请一律用上面的 API 管理;`gateway.db` 依旧被 gitignore。清空该表即可回到 config.yaml 重新播种。

## Web 管理台(React + Ant Design)

`web/` 里是一套**前后端分离**的 React + antd v5 管理台:概览(近 24h 用量卡 + 异常/告警卡片 + 14 天趋势图)、用量明细(服务端分页/排序/过滤)、订阅源运行期 CRUD(含连通测试)、配额与告警。它只消费网关现有的 `/api/*` 管理接口,与模型接口共用同一把统一 key;开发期由 Vite 把 `/api` 代理到网关(同源,无 CORS,后端不用动)。

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
