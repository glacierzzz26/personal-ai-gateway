// Package config 负责加载 YAML 配置、展开 ${ENV}、应用默认值并做基础校验。
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 只承载网关自身配置(listen/db/管理 key/计费表)。
// 订阅源(Upstream)不在此列:运行期上游以 DB upstreams 表为唯一权威,
// 经 /api/v1/upstreams 维护(见 DESIGN 决策 #13)。
type Config struct {
	Listen  string      `yaml:"listen"`
	DBPath  string      `yaml:"db_path"`
	Keys    []Key       `yaml:"keys"`
	Pricing []PriceRule `yaml:"pricing"`
}

// PriceRule 按模型(支持 "*" 与 "claude-*" 前缀通配)给定每百万 token 的美元单价。
// 规则按声明顺序匹配,命中第一条即停;想给未收录模型兜底,把 model: "*" 放最后。
type PriceRule struct {
	Model          string  `yaml:"model"`
	PromptPerM     float64 `yaml:"prompt_per_m"`
	CompletionPerM float64 `yaml:"completion_per_m"`
	CacheReadPerM  float64 `yaml:"cache_read_per_m"`
}

type Key struct {
	Name   string `yaml:"name"`
	Secret string `yaml:"secret"`
	Note   string `yaml:"note,omitempty"`
}

// Upstream 一个订阅源/厂商。yaml 标签用于 store 落盘(raw 形式);
// json 标签让同一结构体直接充当 /api/v1/upstreams 的请求/响应 DTO。
// 上游不写在 config.yaml 里 —— 运行期以 DB upstreams 表为唯一权威。
type Upstream struct {
	Name        string       `yaml:"name" json:"name"`
	Type        string       `yaml:"type" json:"type"` // "openai" | "anthropic"
	BaseURL     string       `yaml:"base_url" json:"base_url"`
	APIKey      string       `yaml:"api_key" json:"api_key"`
	Priority    int          `yaml:"priority" json:"priority"` // 越小越优先
	Models      []string     `yaml:"models" json:"models"`     // "*" 或空 = 全部;支持前缀通配 "claude-*"
	CooldownSec int          `yaml:"cooldown_sec" json:"cooldown_sec"`
	MaxFailures int          `yaml:"max_failures" json:"max_failures"`
	Quota       *QuotaConfig `yaml:"quota" json:"quota"` // 可选:配额感知选路
}

// QuotaConfig 声明该上游的订阅配额如何拉取与判等。
// Window 指定以哪层窗口为准(rolling|weekly|monthly,默认 monthly)。
// percent 按上游惯例=已用比例;若你的上游实际返回的是"剩余",把 invert_used_pct 置 true。
type QuotaConfig struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Window        string `yaml:"window" json:"window"`
	WarnUsedPct   int    `yaml:"warn_used_pct" json:"warn_used_pct"`     // 仅用于事件/日志(P4 告警复用),不改变选路
	HardUsedPct   int    `yaml:"hard_used_pct" json:"hard_used_pct"`     // ≥ 此值视作"配额耗尽",选路降级到备选
	CacheTTLSec   int    `yaml:"cache_ttl_sec" json:"cache_ttl_sec"`     // 配额缓存秒数,也是轮询间隔
	InvertUsedPct bool   `yaml:"invert_used_pct" json:"invert_used_pct"` // true = percent 表示剩余,换算成已用
}

const (
	TypeOpenAI    = "openai"
	TypeAnthropic = "anthropic"
)

// Load 读取并解析配置文件。整个文件先做一次 os.ExpandEnv,
// 因此密钥/base_url 里可用 ${NAME} 从环境注入。
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8787"
	}
	if c.DBPath == "" {
		c.DBPath = "gateway.db"
	}
}

func (c *Config) validate() error {
	if len(c.Keys) == 0 {
		return errors.New("config: at least one unified key required (keys[])")
	}
	seenKey := map[string]bool{}
	for _, k := range c.Keys {
		if k.Name == "" || k.Secret == "" {
			return fmt.Errorf("config: key entry needs both name and secret (got name=%q)", k.Name)
		}
		if seenKey[k.Name] {
			return fmt.Errorf("config: duplicate key name %q", k.Name)
		}
		seenKey[k.Name] = true
	}
	// 上游不属 config:运行期上游以 DB upstreams 表为唯一权威,
	// 正确性统一在 ValidateUpstreams 边界(/api CRUD commitUpstreams)把关,见 DESIGN 决策 #13。
	return nil
}

// ApplyUpstreamDefaults 给上游列表就地补齐默认值(原地修改,不拷贝)。
func ApplyUpstreamDefaults(ups []Upstream) {
	for i := range ups {
		u := &ups[i]
		if u.CooldownSec == 0 {
			u.CooldownSec = 10
		}
		if u.MaxFailures == 0 {
			u.MaxFailures = 3
		}
		if q := u.Quota; q != nil && q.Enabled {
			if q.Window == "" {
				q.Window = "monthly"
			}
			if q.WarnUsedPct == 0 {
				q.WarnUsedPct = 80
			}
			if q.HardUsedPct == 0 {
				q.HardUsedPct = 95
			}
			if q.CacheTTLSec == 0 {
				q.CacheTTLSec = 60
			}
		}
	}
}

// ValidateUpstreams 校验整份上游列表:逐条形状 + name 全局唯一。
// 唯一冲突由调用方映射成 409,其余为 400。
func ValidateUpstreams(ups []Upstream) error {
	seenUp := map[string]bool{}
	for i, u := range ups {
		if u.Name == "" {
			return fmt.Errorf("upstream #%d missing name", i)
		}
		if seenUp[u.Name] {
			return fmt.Errorf("duplicate upstream name %q", u.Name)
		}
		seenUp[u.Name] = true
		if u.Type != TypeOpenAI && u.Type != TypeAnthropic {
			return fmt.Errorf("upstream %q type %q must be %q or %q", u.Name, u.Type, TypeOpenAI, TypeAnthropic)
		}
		if strings.TrimRight(u.BaseURL, "/") == "" {
			return fmt.Errorf("upstream %q missing base_url", u.Name)
		}
		if u.APIKey == "" {
			return fmt.Errorf("upstream %q missing api_key", u.Name)
		}
		if q := u.Quota; q != nil && q.Enabled {
			switch q.Window {
			case "rolling", "weekly", "monthly":
			default:
				return fmt.Errorf("upstream %q quota.window %q must be rolling|weekly|monthly", u.Name, q.Window)
			}
			if q.WarnUsedPct < 0 || q.HardUsedPct <= q.WarnUsedPct || q.HardUsedPct > 100 {
				return fmt.Errorf("upstream %q quota needs 0 <= warn_used_pct < hard_used_pct <= 100", u.Name)
			}
			if q.CacheTTLSec <= 0 {
				return fmt.Errorf("upstream %q quota.cache_ttl_sec must be > 0", u.Name)
			}
		}
	}
	return nil
}

// ResolveUpstreams 把 raw(持久化态,可能含 ${ENV})变成 resolved(运行态):
// 拷贝 + 默认值 + os.ExpandEnv。router / quota manager / proxy 只使用 resolved;
// 数据库永远只存 raw,避免回写时把密钥展开成明文。返回的新切片与入参无共享可变状态。
func ResolveUpstreams(raw []Upstream) []Upstream {
	out := make([]Upstream, len(raw))
	copy(out, raw)
	ApplyUpstreamDefaults(out)
	for i := range out {
		out[i].BaseURL = os.ExpandEnv(out[i].BaseURL)
		out[i].APIKey = os.ExpandEnv(out[i].APIKey)
	}
	return out
}

// FindKey 按 secret(不区分它来自 x-api-key 还是 Authorization)返回 key 名。
// 供鉴权中间件使用;为防时序攻击请用 constant-time 比较。
func (c *Config) FindKey(secret string) (string, bool) {
	for _, k := range c.Keys {
		if constantTimeEqual(k.Secret, secret) {
			return k.Name, true
		}
	}
	return "", false
}
