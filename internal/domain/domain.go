// Package domain 定义 v2 网关注册的所有业务实体(兼 API JSON body)。
//
// 字段命名/形状与 web-v2/src/types/index.ts 一一对应(读接口直接喂给管理端渲染),
// 存储结构(含密文/整数状态等内部字段)以 *_Row / *_Input 收尾,避免污染展示 DTO。
// 展示型字段(latencyMs/successRate/todayCostUsd/status 等)由读接口现算填充,不入库。
package domain

import (
	"encoding/json"
	"time"
)

// OptionalFloat 三分态可空 float64:区分「未传」(键缺席 → 保持原值)、
// 「显式 null」(清空覆盖)与「设值」。仅用于「null 与未传语义不同」的字段
// (如 ModelInput.RateOverride);其余字段沿用 *T(nil 与未传同义)。
type OptionalFloat struct {
	Set   bool
	Clear bool
	Value float64
}

// UnmarshalJSON 记录键是否出现:出现即 Set,值为 null 则 Clear。
func (o *OptionalFloat) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Clear = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// SetFloat 构造「设值」态。
func SetFloat(v float64) OptionalFloat { return OptionalFloat{Set: true, Value: v} }

// ClearFloat 构造「显式清空」态。
func ClearFloat() OptionalFloat { return OptionalFloat{Set: true, Clear: true} }

// Ptr 转为存储用的 *float64:未设或清空 → nil(SQL NULL),否则指向值。
func (o OptionalFloat) Ptr() *float64 {
	if !o.Set || o.Clear {
		return nil
	}
	v := o.Value
	return &v
}

// Apply 在「保持原值」基础上套用本三态:未传 → cur 原样;清空 → nil;设值 → 新值。
func (o OptionalFloat) Apply(cur *float64) *float64 {
	if !o.Set {
		return cur
	}
	return o.Ptr()
}

// ---------- 枚举 ----------

// Provider 渠道供应商 —— **只留真厂商**(卖的是谁的模型)。字符串即前端 ProviderMark 展示名,勿改。
// 「怎么连上去」由 EgressProto 承载;「上游是哪家、怎么查额度」由 ChannelType 承载。
// 非厂商渠道(聚合/中转,如 command code)该字段为空,徽标回落显示渠道类型。
type Provider string

const (
	ProviderOpenAI    Provider = "OpenAI"
	ProviderAnthropic Provider = "Anthropic"
	ProviderDeepSeek  Provider = "DeepSeek"
	ProviderQwen      Provider = "通义千问"
	ProviderZhipu     Provider = "智谱"
	ProviderMoonshot  Provider = "Moonshot"
)

// ProviderNone 「不是单一厂商」(多厂家中转/区域部署)。空串即此语义 —— 用常量而非裸 ""
// 是为了让 Go 代码与测试有明确的书写对象,不代表它是 Providers 里的一个可选值。
const ProviderNone Provider = ""

// Providers 前端「新建渠道」下拉的可选集合(与 web-v2 constants.providers 一致)。
var Providers = []Provider{
	ProviderOpenAI, ProviderAnthropic, ProviderDeepSeek,
	ProviderQwen, ProviderZhipu, ProviderMoonshot,
}

// ChannelType 渠道类型 —— 决定**上游额度怎么查**(各家问法完全不同),与 Provider 正交:
// 同一类型可卖多家厂商的模型,同一厂商的模型也可来自多种类型。
type ChannelType string

const (
	ChannelTypeDeepSeek    ChannelType = "deepseek"    // DeepSeek 官方直连
	ChannelTypeCommandCode ChannelType = "commandcode" // command code 订阅
	ChannelTypeOpenCode    ChannelType = "opencode"    // opencode zen
	ChannelTypeThirdParty  ChannelType = "thirdparty"  // 其它中转站(额度路径手工配置)
)

// ChannelTypes 前端「渠道类型」下拉的可选集合,与 web-v2 constants.channelTypes 一致。
var ChannelTypes = []ChannelType{
	ChannelTypeDeepSeek, ChannelTypeCommandCode, ChannelTypeOpenCode, ChannelTypeThirdParty,
}

// Valid 是否为受支持的渠道类型。
func (t ChannelType) Valid() bool {
	switch t {
	case ChannelTypeDeepSeek, ChannelTypeCommandCode, ChannelTypeOpenCode, ChannelTypeThirdParty:
		return true
	}
	return false
}

// EgressProto 出站协议 —— 「怎么把请求发上去」。原先由 provider 反推(OutProto),
// 但聚合渠道卖别家模型却仍走 OpenAI 协议,两者必须解耦。
type EgressProto string

const (
	EgressAnthropic EgressProto = "anthropic"
	EgressOpenAI    EgressProto = "openai"
	EgressAzure     EgressProto = "azure" // OpenAI 兼容 + api-version 查询参数
)

// EgressProtos 前端「出站协议」下拉的可选集合。
var EgressProtos = []EgressProto{EgressOpenAI, EgressAnthropic, EgressAzure}

// Valid 是否为受支持的出站协议。
func (p EgressProto) Valid() bool {
	switch p {
	case EgressAnthropic, EgressOpenAI, EgressAzure:
		return true
	}
	return false
}

// QuotaShape 第三方渠道额度接口的响应形状(路径手工填,形状从这里选)。
type QuotaShape string

const (
	// ShapeUsage 通用额度信封:路径返回 {usage:{rolling,weekly,monthly:{status,percent,resetsAt}}}。
	// 网关原生的 /v1/usage 协议,opencode zen 与部分中转站同形 —— 默认形状。
	ShapeUsage QuotaShape = "usage"
	// ShapeOneAPI one-api / new-api / veloera 的计费接口:
	// /v1/dashboard/billing/subscription → {hard_limit_usd},配 /v1/dashboard/billing/usage → {total_usage}(美分)。
	ShapeOneAPI QuotaShape = "oneapi"
	// ShapeNewAPIUser new-api 的 /api/user/self → {data:{quota(剩余),used_quota(已用)}}(额度单位制,无币种)。
	ShapeNewAPIUser QuotaShape = "newapi_user"
)

// QuotaShapes 前端「额度形状」下拉的可选集合(与 web-v2 constants.quotaShapes 一致)。
var QuotaShapes = []QuotaShape{ShapeUsage, ShapeOneAPI, ShapeNewAPIUser}

// Valid 是否为受支持的额度形状。
func (s QuotaShape) Valid() bool {
	switch s {
	case ShapeUsage, ShapeOneAPI, ShapeNewAPIUser:
		return true
	}
	return false
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

// StatusClientClosed 客户端主动断开的日志状态码(non-standard 499)。
// 不是任何一方的故障:不计入错误率、不触发渠道熔断,日志页显示为「中断」。
const StatusClientClosed = 499

// TokenStatus 令牌状态。
type TokenStatus string

const (
	TokenActive   TokenStatus = "active"
	TokenDisabled TokenStatus = "disabled"
	TokenExpired  TokenStatus = "expired"
)

// Role 账号角色。admin 可见/可改全部;user 只能自助管理自己的访问令牌。
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Valid 是否为受支持的角色值。
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// AnnouncementLevel 公告级别(仅影响展示语义色,不改变可见范围)。
type AnnouncementLevel string

const (
	LevelInfo   AnnouncementLevel = "info"   // 常规通知
	LevelWarn   AnnouncementLevel = "warn"   // 注意(如短时降级)
	LevelDanger AnnouncementLevel = "danger" // 重要(如停机维护)
)

// Valid 是否为受支持的级别值。
func (l AnnouncementLevel) Valid() bool {
	return l == LevelInfo || l == LevelWarn || l == LevelDanger
}

// ---------- 渠道 ----------

// ChannelInput 创建/更新渠道的请求体。apiKey 留空表示不改/不设置。
type ChannelInput struct {
	Name        string      `json:"name"`
	Provider    Provider    `json:"provider"`    // 真厂商;非厂商渠道留空
	ChannelType ChannelType `json:"channelType"` // 决定额度协议
	EgressProto EgressProto `json:"egressProto"` // 决定出站协议
	BaseURL     string      `json:"baseUrl"`
	APIKey      string      `json:"apiKey,omitempty"`
	// QuotaPath/QuotaShape 仅 thirdparty 用:上游额度查询路径 + 响应形状(手工配置)。
	QuotaPath   string   `json:"quotaPath,omitempty"`
	QuotaShape  string   `json:"quotaShape,omitempty"`
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
	// Provider 不设默认:留空即「非单一厂商」(聚合渠道),徽标回落显示渠道类型。
	if c.ChannelType == "" {
		c.ChannelType = ChannelTypeThirdParty
	}
	if c.EgressProto == "" {
		// 旧客户端只传 provider:按老口径(Anthropic 之外皆 OpenAI 兼容)推。
		if c.Provider == ProviderAnthropic {
			c.EgressProto = EgressAnthropic
		} else {
			c.EgressProto = EgressOpenAI
		}
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
	ChannelType   ChannelType  `json:"channelType"`
	EgressProto   EgressProto  `json:"egressProto"`
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
	QuotaPath     string       `json:"quotaPath,omitempty"`
	QuotaShape    string       `json:"quotaShape,omitempty"`
	MaxFailures   int          `json:"maxFailures"`
	CooldownSec   int          `json:"cooldownSec"`
	CircuitOpen   bool         `json:"circuitOpen,omitempty"`
	AvailableFrom time.Time    `json:"availableFrom,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// ChannelRow 是渠道表的一行(含密文与内部控制字段,仅 store/auth/engine 可见)。
type ChannelRow struct {
	ID           int64       `json:"id"`
	Name         string      `json:"name"`
	Provider     Provider    `json:"provider"`
	ChannelType  ChannelType `json:"channelType"`
	EgressProto  EgressProto `json:"egressProto"`
	BaseURL      string      `json:"baseUrl"`
	APIKeyCipher string      `json:"-"`
	KeyMasked    string      `json:"keyMasked"`
	Priority     int         `json:"priority"`
	Weight       int         `json:"weight"`
	TimeoutMs    int         `json:"timeoutMs"`
	Tags         []string    `json:"tags"`
	Enabled      bool        `json:"enabled"`
	QuotaPath    string      `json:"quotaPath"`
	QuotaShape   string      `json:"quotaShape"`
	MaxFailures  int         `json:"maxFailures"`
	CooldownSec  int         `json:"cooldownSec"`
	Note         string      `json:"note"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ---------- 模型与供给源 ----------

// ModelInput 创建/更新模型的请求体。
type ModelInput struct {
	Name string `json:"name"`
	// DisplayName 网关侧统一名称(对外展示/模型面调用名);空 = 用 name。
	// 更新时非 nil 才改动(指针区分「未传」与「清空」)。
	DisplayName   *string      `json:"displayName"`
	ContextWindow int          `json:"contextWindow"`
	Capabilities  []Capability `json:"capabilities"`
	Enabled       *bool        `json:"enabled"`
	// OfficialVendor/OfficialModelName 模型级官方价绑定(指向某厂商 official_prices 的一行);
	// 用于聚合中转等 provider 非厂商的渠道显示厂商官方价。更新时非 nil 才改动(nil = 不动)。
	OfficialVendor    *string `json:"officialVendor"`
	OfficialModelName *string `json:"officialModelName"`
	// RateOverride 该模型的售价倍率(本站价 = 官方价 × 倍率)。未传 = 保持原值;显式 null = 清空
	// (回落全局 settings.price_multiplier);数值 = 覆盖。三态(OptionalFloat)区分「未传」与「清空」,
	// 普通 *float64 无法区分,会让清空退化成 no-op。
	RateOverride OptionalFloat `json:"rateOverride"`
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
	DisplayName   string       `json:"displayName"`
	ContextWindow int          `json:"contextWindow"`
	Capabilities  []Capability `json:"capabilities"`
	Enabled       bool         `json:"enabled"`
	// 模型级官方价绑定(空 = 未绑定,走自动匹配)。
	OfficialVendor    Provider `json:"officialVendor"`
	OfficialModelName string   `json:"officialModelName"`
	// RateOverride 该模型的售价倍率;nil = 回落全局 settings.price_multiplier。
	// 定价按模型(全站同模型同价),不再有用户级倍率(见迁移 v9)。
	RateOverride *float64  `json:"rateOverride,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// PublicName 网关对外统一名:重命名后为 display_name,否则回落真实模型名。
func (m ModelRow) PublicName() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Name
}

// OfferInput 添加/更新供给源。
//
// PriceSourceURL/PriceFetchedAt/PriceCurrency/PriceNativeText 为「官方价来源留证」,
// 由应用官方定价时写入;管理端编辑报价时必须原样回传,否则会被清空(全量替换语义)。
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
	PriceSourceURL    string  `json:"priceSourceUrl,omitempty"`
	PriceFetchedAt    string  `json:"priceFetchedAt,omitempty"`
	PriceCurrency     string  `json:"priceCurrency,omitempty"`
	PriceNativeText   string  `json:"priceNativeText,omitempty"`
	// UpstreamModel 本渠道侧真实模型名:非空 = 出站发往本渠道时改写请求体 model 为该值;
	// 空 = 回落模型级 name。管理端编辑报价时须原样回传(全量替换语义)。
	UpstreamModel string `json:"upstreamModel,omitempty"`
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
	ID          int64    `json:"id"`
	ModelID     int64    `json:"modelId"`
	ChannelID   int64    `json:"channelId"`
	ChannelName string   `json:"channelName"`
	Provider    Provider `json:"provider"`
	// ChannelType 所属渠道的类型(provider 为空时前端用它的标签代替供应商展示)。
	ChannelType       ChannelType  `json:"channelType,omitempty"`
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
	// 官方价来源留证(空 = 未从官方来源应用过)。仅作核对,不参与计费。
	PriceSourceURL  string `json:"priceSourceUrl,omitempty"`
	PriceFetchedAt  string `json:"priceFetchedAt,omitempty"`
	PriceCurrency   string `json:"priceCurrency,omitempty"`
	PriceNativeText string `json:"priceNativeText,omitempty"`
	// UpstreamModel 本渠道侧真实模型名(非空 = 发往本渠道时改写请求体 model;空 = 用模型级 name)。
	UpstreamModel string `json:"upstreamModel,omitempty"`
	// InferredVendor 由上游名/模型名推断出的厂商(空 = 判不出)。供前端做官方价「推断厂商」匹配。
	InferredVendor Provider `json:"inferredVendor,omitempty"`
}

// ModelRead 模型目录条目 = models 行 + 关联 offers + 展示字段。
// Name 为对外统一名(PublicName);OriginalName 始终是渠道侧真实模型名,便于对照。
type ModelRead struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	DisplayName   string       `json:"displayName,omitempty"`
	OriginalName  string       `json:"originalName"`
	ContextWindow int          `json:"contextWindow"`
	Capabilities  []Capability `json:"capabilities"`
	Enabled       bool         `json:"enabled"`
	Offers        []OfferRead  `json:"offers"`
	TodayRequests int          `json:"todayRequests"`
	SuccessRate   float64      `json:"successRate"`
	// OfficialVendor/OfficialModelName 模型级官方价绑定(空 = 未绑定)。
	OfficialVendor    Provider `json:"officialVendor,omitempty"`
	OfficialModelName string   `json:"officialModelName,omitempty"`
	// InferredVendor 由模型名(取首个 '/' 前的段)推断出的厂商;空 = 判不出。
	// 前端据此在「provider 直连」之外追加一次「推断厂商」官方价匹配(聚合渠道场景)。
	InferredVendor Provider `json:"inferredVendor,omitempty"`
	// RateOverride 模型级售价倍率;缺省/nil = 跟随全局 settings.price_multiplier。模型编辑器回显用。
	RateOverride *float64 `json:"rateOverride,omitempty"`
}

// ---------- 官方定价(厂商官网) ----------

// BillingShape 官方计费形态。决定「不能把分时/阶梯价当单一价静默落库」如何表达。
type BillingShape string

const (
	ShapeFlat     BillingShape = "flat"         // 单一价
	ShapePeakOff  BillingShape = "peak_offpeak" // 峰谷分时(DeepSeek)
	ShapeTiered   BillingShape = "tiered"       // 按单次请求输入 token 区间(通义)
	ShapeDiscount BillingShape = "discount"     // 限时折扣(智谱)
)

// Currency 官方源币种。网关内部报价恒为 USD,官方价按原币种留存。
type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyCNY Currency = "CNY"
)

// Valid 是否为受支持的币种。
func (c Currency) Valid() bool { return c == CurrencyUSD || c == CurrencyCNY }

// OfficialPriceInput 手工录入官方参考价(智谱等页面不可抓的厂商)。
type OfficialPriceInput struct {
	Provider       Provider `json:"provider"`
	ModelName      string   `json:"modelName"`
	SourceURL      string   `json:"sourceUrl"`
	Currency       Currency `json:"currency"`
	InputPrice     float64  `json:"inputPrice"`
	OutputPrice    float64  `json:"outputPrice"`
	CacheReadPrice float64  `json:"cacheReadPrice"`
	NativeText     string   `json:"nativeText,omitempty"`
	Note           string   `json:"note,omitempty"`
}

// OfficialPriceRow 官方参考价一行(原币种 / 百万 token)。
// 分时类(peak_offpeak)的 InPrice/OutPrice 取空闲价作「生效默认」,明细在 Detail。
type OfficialPriceRow struct {
	ID             int64          `json:"id"`
	Provider       Provider       `json:"provider"`
	ModelName      string         `json:"modelName"`
	SourceURL      string         `json:"sourceUrl"`
	FetchedAt      time.Time      `json:"fetchedAt"`
	Currency       Currency       `json:"currency"`
	BillingShape   BillingShape   `json:"billingShape"`
	InputPrice     float64        `json:"inputPrice"`
	OutputPrice    float64        `json:"outputPrice"`
	CacheReadPrice float64        `json:"cacheReadPrice"`
	CacheDerived   bool           `json:"cacheDerived"` // 缓存价由官方规则推导,非官方列
	NativeText     string         `json:"nativeText,omitempty"`
	Detail         map[string]any `json:"detail,omitempty"`
	ContentSHA256  string         `json:"contentSha256,omitempty"`
	Note           string         `json:"note,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

// CostRatioRow 一条「渠道 × 厂商」成本系数:该渠道消耗该厂商模型时,成本 = 官方价 × Ratio。
//
// 按 (渠道, 厂商) 而非按模型:credit 型套餐($10 买 $60 额度)对所有模型同倍率,
// 按模型是 O(渠道×模型) 个格子,按厂商是 O(渠道×厂商)。见迁移 m0012。
type CostRatioRow struct {
	ChannelID int64    `json:"channelId"`
	Vendor    Provider `json:"vendor"` // 取值同 official_prices.provider(即 models.official_vendor)
	Ratio     float64  `json:"ratio"`  // 成本 = 官方价 × ratio;1.0 = 不折扣
	Note      string   `json:"note,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

// CostRatioInput 写入一条系数(PUT 全量替换该渠道的系数行)。
type CostRatioInput struct {
	Vendor Provider `json:"vendor"`
	Ratio  float64  `json:"ratio"`
	Note   string   `json:"note,omitempty"`
}

// OfficialBindingFill 回填结果的一行(模型 → 官方价绑定)。
type OfficialBindingFill struct {
	ModelID      int64    `json:"modelId"`
	ModelName    string   `json:"modelName"`
	Vendor       Provider `json:"vendor"`
	OfficialName string   `json:"officialName"`
}

// OfficialPriceView 官方价 + 与现有 offer 的比对(读接口填充)。
type OfficialPriceView struct {
	OfficialPriceRow
	// 按 settings.displayCurrency 换算后的计价金额(每百万 token)。
	// 字段名保留 *Usd 是历史命名(内部口径原为美元);计价币种为 CNY 时这里就是人民币金额。
	// 原币种与计价币种一致时等同原价。
	InputPriceUsd     float64 `json:"inputPriceUsd"`
	OutputPriceUsd    float64 `json:"outputPriceUsd"`
	CacheReadPriceUsd float64 `json:"cacheReadPriceUsd"`
	// RateSet 金额可用:原币种与计价币种一致,或已按汇率折算成功。
	// false = 币种不一致且未设汇率,前端应提示补汇率(此三价均为 0,不可应用)。
	RateSet bool `json:"rateSet"`
	// AppliedOfferIDs 已应用该官方价(来源 URL + 抓取时间均匹配)的 offer。
	AppliedOfferIDs []int64 `json:"appliedOfferIds"`
}

// FetchPricingResult POST /channels/{id}/fetch-pricing 返回。
// 抓取失败即失败:Failed 非空且 Upserted=0,原报价与旧官方价保持不变。
type FetchPricingResult struct {
	Provider   Provider `json:"provider"`
	SourceURL  string   `json:"sourceUrl"`
	Upserted   int      `json:"upserted"`
	Models     []string `json:"models"`
	Failed     []string `json:"failed,omitempty"`
	ContentSHA string   `json:"contentSha256,omitempty"`
	// Removed 本次对账删掉的陈旧行数(该厂商来源页已不再列出的模型)。
	Removed int64 `json:"removed,omitempty"`
}

// ApplyPriceReq POST /official-prices/{id}/apply 请求体。
type ApplyPriceReq struct {
	OfferID         int64 `json:"offerId"`
	ConfirmOverride bool  `json:"confirmOverride"` // offer.override_price=true 时须显式确认
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
// OwnerID 仅创建时生效(admin 可指定归属;user 由服务端强制为自己)。
type TokenInput struct {
	Name          string       `json:"name"`
	AllowedModels []string     `json:"allowedModels"`
	QuotaUsd      float64      `json:"quotaUsd"`
	RpmLimit      int          `json:"rpmLimit"`
	ExpiresAt     *string      `json:"expiresAt"`
	Status        *TokenStatus `json:"status"`
	OwnerID       *int64       `json:"ownerId,omitempty"`
}

func (t *TokenInput) Defaults() {
	if t.RpmLimit == 0 {
		t.RpmLimit = 60
	}
}

// TokenRead 令牌展示结构。KeyMasked 为前缀+尾号掩码,明文只在创建响应出现一次。
// KeyRetrievable=key_cipher 非空(本特性上线后创建的 key 才可回显/生成配置)。
type TokenRead struct {
	ID             int64       `json:"id"`
	Name           string      `json:"name"`
	KeyMasked      string      `json:"keyMasked"`
	AllowedModels  []string    `json:"allowedModels"`
	QuotaUsd       float64     `json:"quotaUsd"`
	UsedUsd        float64     `json:"usedUsd"`
	RpmLimit       int         `json:"rpmLimit"`
	ExpiresAt      *string     `json:"expiresAt"`
	LastUsedAt     *string     `json:"lastUsedAt"`
	Status         TokenStatus `json:"status"`
	CreatedAt      time.Time   `json:"createdAt"`
	OwnerID        *int64      `json:"ownerId"`
	OwnerName      string      `json:"ownerName,omitempty"`
	KeyRetrievable bool        `json:"keyRetrievable"`
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
	OwnerID       *int64
	// 归属账号的钱包视图(随鉴权一次查出,免数据面每请求再查一次):
	// 全局 key(OwnerID=nil)时 OwnerRole 为空、余额为 0。
	OwnerRole    Role
	OwnerBalance float64
}

// ---------- 管理员 / 用户与会话 ----------

// AdminUser 管理账号(兼 /auth/me 响应)。Role 决定管理台可见范围。
type AdminUser struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// LoginReq 登录 / bootstrap 请求体。
type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// MeResp 当前会话账号信息(与 AdminUser 同形)。
type MeResp struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// UserRead GET /users 行(管理员视角;KeyCount 为该用户名下的访问令牌数)。
type UserRead struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	KeyCount  int       `json:"keyCount"`
	CreatedAt time.Time `json:"createdAt"`
	// BalanceUsd 钱包余额(计价币种金额)。售价倍率按模型存(见 ModelRow.RateOverride),
	// 不再有用户级倍率(迁移 v9 起)。
	BalanceUsd float64 `json:"balanceUsd"`
	// TokenQuotaCeiling 该用户名下令牌的额度上限(0 = 不限);TokenRpmCeiling 同理。
	// 只约束 role=user 的自助建令牌,管理员不受限。
	TokenQuotaCeiling float64 `json:"tokenQuotaCeiling"`
	TokenRpmCeiling   int     `json:"tokenRpmCeiling"`
}

// CeilingInput PATCH /users/{id}/ceiling 请求体:该用户名下令牌的额度/RPM 上限。
// 两者 0 = 不限。普通用户建/改令牌时不得超过此值。
type CeilingInput struct {
	QuotaUsd float64 `json:"quotaUsd"`
	RpmLimit int     `json:"rpmLimit"`
}

// BalanceLogItem GET /users/{id}/balance-logs 与 /me/balance 流水行。
type BalanceLogItem struct {
	ID           int64     `json:"id"`
	Delta        float64   `json:"delta"`
	BalanceAfter float64   `json:"balanceAfter"`
	Reason       string    `json:"reason"` // charge | topup | adjust
	LogID        int64     `json:"logId,omitempty"`
	Note         string    `json:"note,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// TopupReq POST /users/{id}/topup 请求体:充值金额(正=充值,负=扣减调整)。
type TopupReq struct {
	Amount float64 `json:"amount"`
	Note   string  `json:"note,omitempty"`
}

// BalanceResp GET /me/balance:余额 + 近期流水(用户自助视角)。
type BalanceResp struct {
	BalanceUsd float64          `json:"balanceUsd"`
	Logs       []BalanceLogItem `json:"logs"`
	// Currency 计价币种。用户读不到 /settings,前端据此决定余额符号(¥/$)。
	Currency Currency `json:"currency"`
	// TokenQuotaCeiling 该账号名下令牌的额度上限(0 = 不限);TokenRpmCeiling 同理。
	// 供用户建令牌时前端预校验,避免提交后才被拒。
	TokenQuotaCeiling float64 `json:"tokenQuotaCeiling"`
	TokenRpmCeiling   int     `json:"tokenRpmCeiling"`
}

// TokenProbeResp POST /tokens/{id}/probe 返回:该令牌对某模型「能不能用」的静态判定。
// 只做本地校验(不访问上游、不计费、不消耗 RPM),用于客户自检 —— 今天只能真发一次请求去猜。
type TokenProbeResp struct {
	Model string `json:"model"`
	// Ok 全部检查通过。
	Ok bool `json:"ok"`
	// Checks 逐项判定(名称/是否通过/说明),便于前端逐条展示未通过的原因。
	Checks []ProbeCheck `json:"checks"`
}

// ProbeCheck 一项自检结果。
type ProbeCheck struct {
	Name   string `json:"name"`
	Ok     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// UserCreateReq POST /users 请求体(管理员建号,设初始密码)。
type UserCreateReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     Role   `json:"role"`
}

// PasswordChangeReq 改自己密码(需验旧密码)。
type PasswordChangeReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// PasswordResetReq 管理员重置他人密码(无需旧密码)。
type PasswordResetReq struct {
	NewPassword string `json:"newPassword"`
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
	CostUsd      float64 `json:"costUsd"`   // 成本(你付上游)
	ChargeUsd    float64 `json:"chargeUsd"` // 售价(客户付你);admin 视角下差额即毛利
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
	OwnerID      int64 // 归属账号(0 = 无归属/全局 key)
	ClientTool   string
	Protocol     string
	Stream       bool
	Status       int
	PromptTokens int
	Completion   int
	CacheRead    int
	CostUsd      float64
	ChargeUsd    float64
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
	// ChargeUsd 该桶实际向客户收的钱(售价口径,用户面曲线用);全站口径下为全部请求的售价合计。
	ChargeUsd float64 `json:"chargeUsd"`
}

// UsageRow 按模型/渠道/令牌维度聚合的一行。
type UsageRow struct {
	Name      string  `json:"name"`
	Requests  int     `json:"requests"`
	InTokens  int     `json:"inTokens"`
	OutTokens int     `json:"outTokens"`
	CostUsd   float64 `json:"costUsd"`
	// ChargeUsd 该维度实际向客户收的钱(= 售价口径,用户面上的「花费」)。
	// 与 CostUsd(你付上游的成本)不是一回事:定价模型见 PLAN.md §2。
	ChargeUsd float64 `json:"chargeUsd"`
	ErrorRate float64 `json:"errorRate"`
}

// OverviewResp Dashboard 首屏。窗口由前端筛选器决定(1/7/30 天或自定义区间):
// Points 为窗口内曲线(≤3 天按小时,>3 天按天),汇总同窗口。
//
// 金额有两个口径,别混:
//   - TotalCostUsd 全站成本 —— 你付上游的钱,含站主自用与无归属流量;
//   - Totals 客户归属的经营口径(营收/成本/毛利并列出现,由同一批行算出,自洽)。
//
// 全站成本不进 Totals:它与营收不同源(一个含站主自用、一个只算客户),
// 相减出来的「毛利」是假的。要看的「今天赚了多少」在 Totals。
type OverviewResp struct {
	Points          []MetricPoint `json:"points"`
	TotalRequests   int           `json:"totalRequests"`
	TotalErrors     int           `json:"totalErrors"`
	TotalCostUsd    float64       `json:"totalCostUsd"`
	AvgFirstTokenMs int64         `json:"avgFirstTokenMs"`
	Days            int           `json:"days"`
	Bucket          string        `json:"bucket"`
	// CustomerPoints 客户归属口径的同窗口曲线(营收/成本),与 Totals 同源同桶,
	// 供「营收 vs 成本」图用 —— 拿全站 Points 画会把站主自用算进来,与营收合计对不上。
	CustomerPoints []MetricPoint `json:"customerPoints"`
	// Totals 客户归属的营收/成本/毛利(经营口径),见 WindowTotalsCustomers。
	Totals MarginTotals `json:"totals"`
	// Prev 上一等长自然日窗口的同口径合计(环比基准)。仅预设窗口回填;
	// 自定义区间没有自然对齐的「上一区间」,为 null。
	Prev *MarginTotals `json:"prev"`
}

// MarginTotals 经营口径的一窗口合计:营收(客户付你)、成本(你付上游)、毛利与毛利率。
// RevenueUsd 与 CostUsd 恒同源(同一批客户归属的请求行),故 MarginUsd 可直接相减。
type MarginTotals struct {
	Requests   int     `json:"requests"`
	RevenueUsd float64 `json:"revenueUsd"`
	CostUsd    float64 `json:"costUsd"`
	MarginUsd  float64 `json:"marginUsd"`
	// MarginRate 毛利率 = 毛利 / 营收。营收为 0 时返回 0(不是 100%——没有分母就没有比率)。
	MarginRate float64 `json:"marginRate"`
}

// CustomerRow 客户关注区的一行:近 1 天消耗 + 钱包余额 + 风险判定。
type CustomerRow struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	// BalanceUsd 钱包余额(计价币种)。可为负(透支至多一笔)。
	BalanceUsd float64 `json:"balanceUsd"`
	// SpendUsd 窗口内该客户的营收消耗(charge_usd 合计);无消耗为 0。
	SpendUsd float64 `json:"spendUsd"`
	Requests int     `json:"requests"`
	// Risk 风险等级:depleted 余额 ≤0(已被拒,客户在流失)/ low 余额撑不过一天 / ok。
	Risk string `json:"risk"`
	// Note 面向站主的一句话说明(为什么被标红)。
	Note string `json:"note"`
}

// CustomerFocusResp 「客户关注区」:欠费/低余额名单 + 消耗排行(仅 admin 可见)。
type CustomerFocusResp struct {
	// Window 本区各指标的时间窗说明,与 /overview 的经营窗口一致(供前端副标题直显)。
	Window string `json:"window"`
	// AtRisk 余额告警客户:已欠费(≤0)+ 撑不过一天的,按余额升序(最危险在前)。
	AtRisk []CustomerRow `json:"atRisk"`
	// Top 消耗 Top 客户(窗口内营收降序),含所有有消耗的客户。
	Top []CustomerRow `json:"top"`
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
	// DisplayCurrency 计价币种:网关所有价格的展示币种,也是「应用官方价」的目标币种。
	// 供给源报价与官方价换算结果都以该币种为金额单位 —— offers.*_usd / input_price_usd 是历史
	// 命名,语义已是「当前计价币种的金额」,故不随币种改动而迁移。
	// 官方价原币种与之一致时可直接应用,无需汇率;不一致才需要 USDPerCNY 折算。
	DisplayCurrency Currency `json:"displayCurrency"`
	// USDPerCNY 手工维护的美元→人民币换算率(如 0.139 = 1 元折 0.139 美元)。
	// 仅当官方价原币种与 DisplayCurrency 不一致时用于折算;0 = 未设置(此时拒绝折算,不臆造汇率)。
	// 汇率非厂商官方数据,故不自动抓取。
	USDPerCNY float64 `json:"usdPerCny"`
	// PriceMultiplier 全局售价倍率(默认基线):本站卖给客户的价格 = 官方价 × 该倍率(见 PLAN.md §2)。
	// 模型级 ModelRow.RateOverride 非空时覆盖它 —— 倍率按模型定,全站同模型同价。
	// 默认 1.0(= 不加价)。<=0 视为 1.0。
	PriceMultiplier float64 `json:"priceMultiplier"`
	// PublicBaseURL 生成 Claude 配置时对外可见的网关基址(如 https://ai-gateway.lan)。
	// 留空则按请求的 scheme+host 推断(X-Forwarded-Proto/Host 优先)。
	PublicBaseURL string `json:"publicBaseUrl,omitempty"`
}

func (s *Settings) Defaults() {
	s.RequestTimeoutMs = 60000
	s.MaxRetries = 2
	s.DegradeOnError = true
	s.LogRetentionDays = 7
	s.RecordRequestBody = true
	s.SampleRatePct = 100
	s.TZOffsetMin = 480
	s.DisplayCurrency = CurrencyCNY
	s.PriceMultiplier = 1.0
}

// ---------- 通知/公告 ----------

// AnnouncementInput 创建/更新公告请求体。
// PublishAt/ExpiresAt 为 RFC3339 字符串(RFC3339Nano);nil = 立即发布 / 永不过期。
type AnnouncementInput struct {
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Level     AnnouncementLevel `json:"level"`
	Enabled   *bool             `json:"enabled"`
	PublishAt *string           `json:"publishAt"`
	ExpiresAt *string           `json:"expiresAt"`
}

func (a *AnnouncementInput) Defaults() {
	enabled := true
	if a.Enabled == nil {
		a.Enabled = &enabled
	}
	if a.Level == "" {
		a.Level = LevelInfo
	}
}

// AnnouncementRow 公告存储结构(用户面弹窗直接渲染它)。
// ReadCount/UserTotal 不在此结构:仅管理员列表现算,用户面不需要。
type AnnouncementRow struct {
	ID        int64             `json:"id"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Level     AnnouncementLevel `json:"level"`
	Enabled   bool              `json:"enabled"`
	PublishAt *time.Time        `json:"publishAt"`
	ExpiresAt *time.Time        `json:"expiresAt"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// AnnouncementRead 管理员列表展示结构:公告 + 已读计数。
// UserTotal = 站点普通用户总数;ReadCount = 已确认该公告的人数,供站主评估触达。
type AnnouncementRead struct {
	AnnouncementRow
	ReadCount int `json:"readCount"`
	UserTotal int `json:"userTotal"`
}

// ---------- 其他小类型 ----------

// TestResp 渠道连通测试 / 同步结果。
type TestResp struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Message   string `json:"message,omitempty"`
}

// SyncResp 从渠道同步模型结果。ModelCount 为该渠道同步后总关联供给源数
// (口径与渠道列表 modelCount 一致,供弹窗展示权威总数)。
type SyncResp struct {
	Added      int      `json:"added"`
	Updated    int      `json:"updated"`
	Models     []string `json:"models"`
	ModelCount int      `json:"modelCount"`
}

// ClaudeConfigResp GET /tokens/{id}/claude-config 的返回:一段可逐字复制进
// ~/.claude/settings.json 的配置(含真实 key),及其实用提示。
type ClaudeConfigResp struct {
	TokenID      int64             `json:"tokenId"`
	BaseURL      string            `json:"baseUrl"`
	SettingsJSON string            `json:"settingsJson"` // 缩进版 JSON 文本,前端原样展示/复制
	ModelAliases map[string]string `json:"modelAliases"` // opus/sonnet/haiku → 实际模型名
	Warnings     []string          `json:"warnings,omitempty"`
}

// QuotaWindow 渠道额度单个窗口(rolling≈近5h/weekly/monthly)。
// Status=="ok" 时 Percent 为该窗口已用百分比。
// Used/Cap/ResetAt 为可选的原始信息:上游给了就带上(不同渠道类型给的不一样),
// 前端据此在 tooltip 里显示「已用 12/14」与重置时间。
type QuotaWindow struct {
	Status  string  `json:"status"`
	Percent float64 `json:"percent"`
	Used    float64 `json:"used,omitempty"`    // 上游原始已用量(如 1.24 美元)
	Cap     float64 `json:"cap,omitempty"`     // 上游原始上限(如 14 美元)
	ResetAt string  `json:"resetAt,omitempty"` // 窗口重置时间,统一 RFC3339(上游 ms/ISO 都归一)
}

// QuotaBalance 绝对余额型额度(DeepSeek /user/balance、one-api 等):
// 这类上游只报「还剩多少钱」,没有窗口百分比。Amount 按**上游原币种**原样展示,不做折算。
type QuotaBalance struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"` // 上游原币种(CNY/USD),原样展示
}

// ChannelQuotaResp GET /channels/{id}/quota 返回。windows 仅含 status=ok 的窗口
// (缺失/非 ok = 该窗口/该渠道不提供额度)。Available=false 时 error 给出原因。
// Balance 与 Windows 互斥:窗口型上游(rolling/weekly/monthly)用 Windows,
// 余额型上游(DeepSeek / one-api)用 Balance。
type ChannelQuotaResp struct {
	Available bool                   `json:"available"`
	PlanName  string                 `json:"planName,omitempty"`
	Windows   map[string]QuotaWindow `json:"windows"`
	Balance   *QuotaBalance          `json:"balance,omitempty"`
	LatencyMs int64                  `json:"latencyMs"`
	Error     string                 `json:"error,omitempty"`
}
