// Package config 加载 YAML 配置并应用默认值。
//
// v2 起 config.yaml 只承载网关自身参数(listen/db_path)。
// 账号、渠道、模型、令牌、路由规则等业务数据一律存 DB(gateway-v2.db),
// config 不再承载 keys/pricing/upstreams —— 见 DESIGN 决策(账号体系/渠道/令牌)。
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen string `yaml:"listen"`
	DBPath string `yaml:"db_path"`
}

// Load 读取并解析配置文件。整个文件先做一次 os.ExpandEnv,
// 因此 db_path/listen 里可用 ${NAME} 从环境注入。
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
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8787"
	}
	if c.DBPath == "" {
		// v2 换新库文件,旧 gateway.db(含 v1 表)原样留档,不迁移。
		c.DBPath = "gateway-v2.db"
	}
}
