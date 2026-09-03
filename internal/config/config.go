// Package config 负责加载 YAML 配置、展开 ${ENV}、应用默认值并做基础校验。
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen    string      `yaml:"listen"`
	DBPath    string      `yaml:"db_path"`
	Keys      []Key       `yaml:"keys"`
	Upstreams []Upstream  `yaml:"upstreams"`
	Pricing   []PriceRule `yaml:"pricing"`
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

type Upstream struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"` // "openai" | "anthropic"
	BaseURL     string   `yaml:"base_url"`
	APIKey      string   `yaml:"api_key"`
	Priority    int      `yaml:"priority"` // 越小越优先
	Models      []string `yaml:"models"`   // "*" 或空 = 全部;支持前缀通配 "claude-*"
	CooldownSec int      `yaml:"cooldown_sec"`
	MaxFailures int      `yaml:"max_failures"`
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
	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		if u.CooldownSec == 0 {
			u.CooldownSec = 10
		}
		if u.MaxFailures == 0 {
			u.MaxFailures = 3
		}
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

	if len(c.Upstreams) == 0 {
		return errors.New("config: at least one upstream required (upstreams[])")
	}
	seenUp := map[string]bool{}
	for i, u := range c.Upstreams {
		if u.Name == "" {
			return fmt.Errorf("config: upstream #%d missing name", i)
		}
		if u.Type != TypeOpenAI && u.Type != TypeAnthropic {
			return fmt.Errorf("config: upstream %q type %q must be %q or %q", u.Name, u.Type, TypeOpenAI, TypeAnthropic)
		}
		if strings.TrimRight(u.BaseURL, "/") == "" {
			return fmt.Errorf("config: upstream %q missing base_url", u.Name)
		}
		if seenUp[u.Name] {
			return fmt.Errorf("config: duplicate upstream name %q", u.Name)
		}
		seenUp[u.Name] = true
	}
	return nil
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
