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

> 当前阶段:P1(同协议透传链路)。跨协议(Anthropic↔OpenAI)翻译在计划中。
