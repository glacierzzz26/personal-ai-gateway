// Package sess 承载一次请求的鉴权结果(key 名、来源工具),
// 由 server 的鉴权中间件写入,proxy 记日志时读取,避免包间循环依赖。
package sess

import "context"

type ctxKey int

const key ctxKey = 0

type Info struct {
	KeyName string // 认证通过的统一 key 的名字
	Tool    string // 来源工具(截断后的 User-Agent)
	Admin   bool   // true = config 登录/管理 key(全权);false = DB 生成的模型面 key(仅 /v1/*)
}

func With(ctx context.Context, i Info) context.Context {
	return context.WithValue(ctx, key, i)
}

func From(ctx context.Context) (Info, bool) {
	i, ok := ctx.Value(key).(Info)
	return i, ok
}
