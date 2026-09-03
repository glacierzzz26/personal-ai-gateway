// upstreams 表:运行时订阅源唯一权威(config.yaml 仅首次播种,见 DESIGN 决策 #13)。
// doc 存整条上游配置的 YAML raw 形式 —— 与 config 文件同标签,row 可直接人读;
// base_url/api_key 里的 ${ENV} 引用原样保存,展开发生在 ResolveUpstreams(运行态)。
package store

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"personal-ai-gateway/internal/config"
)

// LoadUpstreams 按配置序读回全部上游(raw,env 引用未展开)。
func (s *Store) LoadUpstreams() ([]config.Upstream, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: not open")
	}
	rows, err := s.db.Query(`SELECT doc FROM upstreams ORDER BY ord`)
	if err != nil {
		return nil, fmt.Errorf("store: load upstreams: %w", err)
	}
	defer rows.Close()
	var ups []config.Upstream
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, fmt.Errorf("store: scan upstream: %w", err)
		}
		var u config.Upstream
		if err := yaml.Unmarshal([]byte(doc), &u); err != nil {
			return nil, fmt.Errorf("store: parse upstream doc: %w", err)
		}
		ups = append(ups, u)
	}
	return ups, rows.Err()
}

// ReplaceUpstreams 单事务全量替换列表(个人规模,删除/重排也简单)。
func (s *Store) ReplaceUpstreams(ups []config.Upstream) error {
	if s == nil || s.db == nil {
		return errors.New("store: not open")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM upstreams`); err != nil {
		return fmt.Errorf("store: clear upstreams: %w", err)
	}
	for i, u := range ups {
		doc, err := yaml.Marshal(u)
		if err != nil {
			return fmt.Errorf("store: marshal upstream %q: %w", u.Name, err)
		}
		if _, err := tx.Exec(`INSERT INTO upstreams (name, doc, ord) VALUES (?, ?, ?)`,
			u.Name, string(doc), i); err != nil {
			return fmt.Errorf("store: insert upstream %q: %w", u.Name, err)
		}
	}
	return tx.Commit()
}
