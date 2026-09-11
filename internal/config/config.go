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
	// WebDir 管理台前端源码目录(静态托管其 dist/ 构建产物,路径按进程 cwd 解析)。
	// 缺省 "web-v2";空串关闭静态托管(纯 API 模式)。
	WebDir string `yaml:"web_dir"`

	// Version 由 main 经 -ldflags -X 注入(仅回显在 /healthz),不来自 YAML。
	Version string `yaml:"-"`
	// TLS 双口自终止监听(数据面 api / 管理台 admin);未配全时保持纯明文 Listen。
	TLS TLSConfig `yaml:"tls"`
}

// TLSConfig 描述 Go 网关自身终止 TLS 的两个监听口,取代此前的 Caddy 边缘。
// 两组 listen+cert+key 全部齐全时 Enabled 才为真;否则只起明文 cfg.Listen(dev/测试)。
type TLSConfig struct {
	APIListen   string `yaml:"api_listen"` // 数据面(仅 /healthz + /v1/*)
	APICert     string `yaml:"api_cert"`
	APIKey      string `yaml:"api_key"`
	AdminListen string `yaml:"admin_listen"` // 管理台(仅 /healthz + /api/v1/* + 静态 SPA)
	AdminCert   string `yaml:"admin_cert"`
	AdminKey    string `yaml:"admin_key"`
}

// Enabled 报告 TLS 双口是否配置齐备。
func (t TLSConfig) Enabled() bool {
	return t.APIListen != "" && t.APICert != "" && t.APIKey != "" &&
		t.AdminListen != "" && t.AdminCert != "" && t.AdminKey != ""
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
	if c.WebDir == "" {
		c.WebDir = "web-v2"
	}
}
