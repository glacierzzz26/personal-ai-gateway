// Package domain 定义 v2 网关注册的所有业务实体(兼 API JSON body)。
//
// 字段命名/形状与 web-v2/src/types/index.ts 一一对应(读接口直接喂给管理端渲染),
// 存储结构(含密文/整数状态等内部字段)以 *_Row / *_Input 收尾,避免污染展示 DTO。
// 展示型字段(latencyMs/successRate/todayCostUsd/status 等)由读接口现算填充,不入库。
package domain

import (
	"time"
)

// ---------- 枚举 ----------

// Provider 渠道供应商。字符串即前端 ProviderMark 展示名,勿改。
type Provider string

const (
	ProviderOpenAI     Provider = "OpenAI"
	ProviderAzure      Provider = "Azure"
	ProviderAnthropic  Provider = "Anthropic"
	ProviderDeepSeek   Provider = "DeepSeek"
	ProviderQwen       Provider = "通义千问"
	ProviderZhipu      Provider = "智谱"
	ProviderMoonshot   Provider = "Moonshot"
	ProviderOpenRouter Provider = "聚合中转"
)

// Providers 前端「新建渠道」下拉的可选集合(与 web-v2 mock providers 一致)。
var Providers = []Provider{
	ProviderOpenAI, ProviderAnthropic, ProviderAzure, ProviderDeepSeek,
	ProviderQwen, ProviderZhipu, ProviderMoonshot, ProviderOpenRouter,
}

// HealthStatus 渠道/供给源健康态(读接口计算)。
type HealthStatus string

const (
	StatusHealthy  HealthStatus = "healthy"  // 近期成功率达标且未熔断
	StatusDegraded HealthStatus = "degraded" // 成功率偏低
	StatusDown     HealthStatus = "down"     // 连续失败进入熔断/冷却,或超时
	StatusDisabled HealthStatus = "disabled" // enabled=false 时强制显示
	StatusUnknown  HealthStatus = "unknown"  // 尚无流量
)

// Capability 模型能力标签。
type Capability string

const (
	CapVision    Capability = "vision"
	CapFunction  Capability = "function"
	CapStream    Capability = "stream"
	CapReasoning Capability = "reasoning"
)

var Capabilities = []Capability{CapVision, CapFunction, CapStream, CapReasoning}

// MatchMode 路由规则匹配方式。
type MatchMode string

const (
	ModePrefix   MatchMode = "prefix"
	ModeWildcard MatchMode = "wildcard"
	ModeRegex    MatchMode = "regex"
)

// Strategy 命中规则后的渠道选择策略。
type Strategy string

const (
	StrategyPriority Strategy = "priority" // channel.priority 升序(小者优先)
	StrategyWeight   Strategy = "weight"   // 按 weights/channel.weight 加权随机
	StrategyLatency  Strategy = "latency"  // 按 EWMA 延迟升序
)

// TokenStatus 令牌状态。
type TokenStatus string

const (
	TokenActive   TokenStatus = "active"
	TokenDisabled TokenStatus = "disabled"
	TokenExpired  TokenStatus = "expired"
)

// ---------- 渠道 ----------

// ChannelInput 创建/更新渠道的请求体。apiKey 留空表示不改/不设置。
type ChannelInput struct {
	Name        string   `json:"name"`
	Provider    Provider `json:"provider"`
	BaseURL     string   `json:"baseUrl"`
	APIKey      string   `json:"apiKey,omitempty"`
	Priority    int      `json:"priority"`
	Weight      int      `json:"weight"`
	TimeoutMs   int      `json:"timeoutMs"`
	Tags        []string `json:"tags,omitempty"`
	Enabled     *bool    `json:"enabled"` // nil=默认 true
	MaxFailures int      `json:"maxFailures"`
	CooldownSec int      `json:"cooldownSec"`
	Note        string   `json:"note,omitempty"`
}

// Defaults 填充请求未显式给出的零值默认,供 handler 调用后写库。
func (c *ChannelInput) Defaults() {
	if c.Provider == "" {
		c.Provider = ProviderOpenAI
	}
	if c.TimeoutMs == 0 {
		c.TimeoutMs = 60000
	}
	if c.MaxFailures == 0 {
		c.MaxFailures = 3
	}
	if c.CooldownSec == 0 {
		c.CooldownSec = 60
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	enabled := true
	if c.Enabled == nil {
		c.Enabled = &enabled
	}
}

// ChannelRead GET /channels 返回给管理端的展示结构。
type ChannelRead struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Provider      Provider     `json:"provider"`
	BaseURL       string       `json:"baseUrl"`
	Priority      int          `json:"priority"`
	Weight        int          `json:"weight"`
	Status        HealthStatus `json:"status"`
	LatencyMs     int64        `json:"latencyMs"`
	SuccessRate   float64      `json:"successRate"`
	TodayTokens   int64        `json:"todayTokens"`
	TodayCostUsd  float64      `json:"todayCostUsd"`
	KeyMasked     string       `json:"keyMasked"`
	ModelCount    int          `json:"modelCount"`
	TimeoutMs     int          `json:"timeoutMs"`
	Proxy         string       `json:"proxy,omitempty"`
	Tags          []string     `json:"tags"`
	Enabled       bool         `json:"enabled"`
	Note          string       `json:"note,omitempty"`
	MaxFailures   int          `json:"maxFailures"`
	CooldownSec   int          `json:"cooldownSec"`
	CircuitOpen   bool         `json:"circuitOpen,omitempty"`
	AvailableFrom time.Time    `json:"availableFrom,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// ChannelRow 是渠道表的一行(含密文与内部控制字段,仅 store/auth/engine 可见)。
type ChannelRow struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Provider     Provider `json:"provider"`
	BaseURL      string   `json:"baseUrl"`
	APIKeyCipher string   `json:"-"`
	KeyMasked    string   `json:"keyMasked"`
	Priority     int      `json:"priority"`
	Weight       int      `json:"weight"`
	TimeoutMs    int      `json:"timeoutMs"`
	Tags         []string `json:"tags"`
	Enabled      bool     `json:"enabled"`
	MaxFailures  int      `json:"maxFailures"`
	CooldownSec  int      `json:"cooldownSec"`
	Note         string   `json:"note"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ---------- 模型与供给源 ----------

// ModelInput 创建/更新模型的请求体。
type ModelInput struct {
	Name          string       `json:"name"`
	ContextWindow int          `json:"contextWindow"`
	Capabilities  []Capability `json:"capabilities"`
	Enabled       *bool        `json:"enabled"`
}

func (m *ModelInput) Defaults() {
	if m.Capabilities == nil {
		m.Capabilities = []Capability{}
	}
	enabled := true
	if m.Enabled == nil {
		m.Enabled = &enabled
	}
}

// ModelRow 是 models 表的一行。
type ModelRow struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	ContextWindow int          `json:"contextWindow"`
	Capabilities  []Capability `json:"capabilities"`
	Enabled       bool         `json:"enabled"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// OfferInput 添加/更新供给源。
type OfferInput struct {
	ChannelID         int64   `json:"channelId"`
	InputPriceUsd     float64 `json:"inputPriceUsd"`
	OutputPriceUsd    float64 `json:"outputPriceUsd"`
	CacheReadPriceUsd float64 `json:"cacheReadPriceUsd"`
	OverridePrice     bool    `json:"overridePrice"`
	RateLimitRpm      int     `json:"rateLimitRpm"`
	TimeoutMs         *int    `json:"timeoutMs"`
	Enabled           *bool   `json:"enabled"`
	Priority          *int    `json:"priority"`
	Note              string  `json:"note"`
}

func (o *OfferInput) Defaults() {
	if o.Enabled == nil {
		enabled := true
		o.Enabled = &enabled
	}
	if o.RateLimitRpm == 0 {
		o.RateLimitRpm = 60
	}
}

// OfferRead 供给源读结构(展示字段由读接口填充)。
type OfferRead struct {
	ID                int64        `json:"id"`
	ModelID           int64        `json:"modelId"`
	ChannelID         int64        `json:"channelId"`
	ChannelName       string       `json:"channelName"`
	Provider          Provider     `json:"provider"`
	InputPriceUsd     float64      `json:"inputPriceUsd"`
	OutputPriceUsd    float64      `json:"outputPriceUsd"`
	CacheReadPriceUsd float64      `json:"cacheReadPriceUsd,omitempty"`
	OverridePrice     bool         `json:"overridePrice"`
	LatencyMs         int64        `json:"latencyMs"`
	SuccessRate       float64      `json:"successRate"`
	Priority          int          `json:"priority"`
	Enabled           bool         `json:"enabled"`
	ContextWindow     int          `json:"contextWindow"`
	RateLimitRpm      int          `json:"rateLimitRpm"`
	TimeoutMs         *int         `json:"timeoutMs,omitempty"`
	Status            HealthStatus `json:"status"`
	Note              string       `json:"note,omitempty"`
}

// ModelRead 模型目录条目 = models 行 + 关联 offers + 展示字段。
type ModelRead struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	ContextWindow int          `json:"contextWindow"`
	Capabilities  []Capability `json:"capabilities"`
	Enabled       bool         `json:"enabled"`
	Offers        []OfferRead  `json:"offers"`
	TodayRequests int          `json:"todayRequests"`
	SuccessRate   float64      `json:"successRate"`
}

// ---------- 路由规则 ----------

// RuleInput 创建/更新规则请求体。
type RuleInput struct {
	Name              string        `json:"name"`
	Enabled           *bool         `json:"enabled"`
	MatchMode         MatchMode     `json:"matchMode"`
	Pattern           string        `json:"pattern"`
	Strategy          Strategy      `json:"strategy"`
	ChannelIDs        []int64       `json:"channelIds"`
	Weights           map[int64]int `json:"weights,omitempty"`
	FallbackChannelID *int64        `json:"fallbackChannelId"`
	Retry             int           `json:"retry"`
	TimeoutMs         int           `json:"timeoutMs"`
}

func (r *RuleInput) Defaults() {
	enabled := true
	if r.Enabled == nil {
		r.Enabled = &enabled
	}
	if r.MatchMode == "" {
		r.MatchMode = ModePrefix
	}
	if r.Strategy == "" {
		r.Strategy = StrategyPriority
	}
	if r.ChannelIDs == nil {
		r.ChannelIDs = []int64{}
	}
	if r.Retry == 0 {
		r.Retry = 1
	}
	if r.TimeoutMs == 0 {
		r.TimeoutMs = 120000
	}
}

// RuleRead 规则展示结构。
type RuleRead struct {
	ID                int64         `json:"id"`
	Name              string        `json:"name"`
	Enabled           bool          `json:"enabled"`
	MatchMode         MatchMode     `json:"matchMode"`
	Pattern           string        `json:"pattern"`
	Strategy          Strategy      `json:"strategy"`
	ChannelIDs        []int64       `json:"channelIds"`
	Weights           map[int64]int `json:"weights,omitempty"`
	FallbackChannelID *int64        `json:"fallbackChannelId"`
	Retry             int           `json:"retry"`
	TimeoutMs         int           `json:"timeoutMs"`
	Sort              int           `json:"sort"`
	Hit               int           `json:"hit"`
}

// ---------- 访问令牌 ----------

// TokenInput 创建/更新令牌。allowedModels "*" 或模型名单。
type TokenInput struct {
	Name          string       `json:"name"`
	AllowedModels []string     `json:"allowedModels"`
	QuotaUsd      float64      `json:"quotaUsd"`
	RpmLimit      int          `json:"rpmLimit"`
	ExpiresAt     *string      `json:"expiresAt"`
	Status        *TokenStatus `json:"status"`
}

func (t *TokenInput) Defaults() {
	if t.RpmLimit == 0 {
		t.RpmLimit = 60
	}
}

// TokenRead 令牌展示结构。KeyMasked 为前缀+尾号掩码,明文只在创建响应出现一次。
type TokenRead struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	KeyMasked     string      `json:"keyMasked"`
	AllowedModels []string    `json:"allowedModels"`
	QuotaUsd      float64     `json:"quotaUsd"`
	UsedUsd       float64     `json:"usedUsd"`
	RpmLimit      int         `json:"rpmLimit"`
	ExpiresAt     *string     `json:"expiresAt"`
	LastUsedAt    *string     `json:"lastUsedAt"`
	Status        TokenStatus `json:"status"`
	CreatedAt     time.Time   `json:"createdAt"`
}

// TokenRow 令牌存储行(内部)。
type TokenRow struct {
	ID            int64
	Name          string
	SHA256        string
	KeyMasked     string
	AllowedModels []string
	QuotaUsd      float64
	UsedUsd       float64
	RpmLimit      int
	ExpiresAt     *string
	LastUsedAt    *string
	Status        TokenStatus
}

// ---------- 管理员与会话 ----------

// AdminUser 管理账号。
type AdminUser struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
}

// LoginReq 登录 / bootstrap 请求体。
type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// MeResp 当前会话管理员信息。
type MeResp struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
}

// ---------- 日志与用量 ----------

// LogItem GET /logs 的单条日志(展示用;ts 已换算成本地时区字符串)。
type LogItem struct {
	ID           int64   `json:"id"`
	TS           string  `json:"ts"`
	Model        string  `json:"model"`
	ChannelName  string  `json:"channelName"`
	TokenName    string  `json:"tokenName"`
	InTokens     int     `json:"inTokens"`
	OutTokens    int     `json:"outTokens"`
	CacheRead    int     `json:"cacheReadTokens,omitempty"`
	CostUsd      float64 `json:"costUsd"`
	FirstTokenMs int     `json:"firstTokenMs"`
	TotalMs      int     `json:"totalMs"`
	StatusCode   int     `json:"statusCode"`
	IP           string  `json:"ip,omitempty"`
	Error        *string `json:"error,omitempty"`
}

// LogRow 日志落库入参(写路径)。
type LogRow struct {
	TS           time.Time
	Model        string
	ChannelID    int64
	ChannelName  string
	TokenID      int64
	TokenName    string
	ClientTool   string
	Protocol     string
	Stream       bool
	Status       int
	PromptTokens int
	Completion   int
	CacheRead    int
	CostUsd      float64
	FirstTokenMs int
	TotalMs      int
	IP           string
	Err          *string
}

// MetricPoint 时间桶聚合点(hour/day)。TS 形如 "2006-01-02 15" 或 "2006-01-02"。
type MetricPoint struct {
	TS       string  `json:"ts"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	CostUsd  float64 `json:"costUsd"`
}

// UsageRow 按模型/渠道/令牌维度聚合的一行。
type UsageRow struct {
	Name      string  `json:"name"`
	Requests  int     `json:"requests"`
	InTokens  int     `json:"inTokens"`
	OutTokens int     `json:"outTokens"`
	CostUsd   float64 `json:"costUsd"`
	ErrorRate float64 `json:"errorRate"`
}

// OverviewResp Dashboard 首屏。hours 近24小时、days 近7天。
type OverviewResp struct {
	Hours           []MetricPoint `json:"hours"`
	Days            []MetricPoint `json:"days"`
	TotalRequests   int           `json:"totalRequests"`
	TotalErrors     int           `json:"totalErrors"`
	TotalCostUsd    float64       `json:"totalCostUsd"`
	AvgFirstTokenMs int64         `json:"avgFirstTokenMs"`
}

// ModelUsageResp 模型抽屉「用量」Tab。
type ModelUsageResp struct {
	Daily     []MetricPoint       `json:"daily"`
	ByChannel []ModelChannelUsage `json:"byChannel"`
}

type ModelChannelUsage struct {
	ChannelName string  `json:"channelName"`
	Requests    int     `json:"requests"`
	CostUsd     float64 `json:"costUsd"`
}

// LogPage GET /logs 分页返回。
type LogPage struct {
	Items []LogItem `json:"items"`
	Total int       `json:"total"`
}

// ---------- 设置 ----------

// Settings 网关参数(settings 表 key-value 的强类型视图)。
type Settings struct {
	RequestTimeoutMs  int    `json:"requestTimeoutMs"` // 默认 60000
	MaxRetries        int    `json:"maxRetries"`       // 默认 2
	DegradeOnError    bool   `json:"degradeOnError"`   // 失败自动降级
	HTTPProxy         string `json:"httpProxy,omitempty"`
	SkipTLSVerify     bool   `json:"skipTlsVerify"`
	LogRetentionDays  int    `json:"logRetentionDays"` // 0=不清理
	RecordRequestBody bool   `json:"recordRequestBody"`
	SampleRatePct     int    `json:"sampleRatePct"` // 0-100
	TZOffsetMin       int    `json:"tzOffsetMin"`   // 默认 480(Asia/Shanghai)
}

func (s *Settings) Defaults() {
	s.RequestTimeoutMs = 60000
	s.MaxRetries = 2
	s.DegradeOnError = true
	s.LogRetentionDays = 7
	s.RecordRequestBody = true
	s.SampleRatePct = 100
	s.TZOffsetMin = 480
}

// ---------- 其他小类型 ----------

// TestResp 渠道连通测试 / 同步结果。
type TestResp struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Message   string `json:"message,omitempty"`
}

// SyncResp 从渠道同步模型结果。
type SyncResp struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Models  []string `json:"models"`
}
