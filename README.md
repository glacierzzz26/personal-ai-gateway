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
- `internal/router` 模型匹配 + 候选排序 + 熔断
- `internal/store` SQLite 请求日志
- `internal/proxy`  转发内核(透传 + SSE 流式回传)
- `internal/server` HTTP 路由 + 统一 key 鉴权

> 当前阶段:P1(同协议透传链路)+ P2(用量采集与成本入库 + `/api` 用量查询)。跨协议(Anthropic↔OpenAI)翻译在计划中。

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
