// Package store 提供 v2 网关的全部持久化:版本化 schema 迁移 + 各业务仓库。
//
// 存储 = PostgreSQL(pgx 的 database/sql 驱动)。时间口径:request_logs.ts / 各 *_at
// 一律存 UTC RFC3339Nano 文本;展示与聚合按 settings.tz_offset_min(默认 480)换算,
// 见 logs.go 桶查询。渠道 api_key 只存密文(secret 包 AES-GCM),明文不落库。
//
// 占位符:本包全部 SQL 沿用 SQLite 风格的 `?`,在 pdb / ptx 入口统一重绑为 PG 的 `$n`
// (见 rebind)。之所以不在源码里直接写 `$n`:多处 SQL 在运行时拼装(占位符数量不定),
// 静态编号不可行;集中一层重绑既让调用点零改动,也杜绝漏改。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrNotFound 记录不存在(查询/更新/删除目标缺失)。
var ErrNotFound = errors.New("record not found")

// ErrConflict 唯一性冲突(名称/模型-渠道 组合已存在)。
var ErrConflict = errors.New("record conflict")

// Store 网关数据访问门面。并发安全(sql.DB 自带池)。
type Store struct {
	db  pdb
	now func() time.Time
}

// Open 连接 PostgreSQL 并执行版本化迁移。
// dsn 形如 postgres://user:pass@host:5432/dbname?sslmode=disable。
func Open(dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("store: empty DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open pg: %w", err)
	}
	// 连接池上限须与 PG 的 max_connections 协调(多实例时 N×池 ≤ 该值)。
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}
	if err := migrate(pdb{db}); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: pdb{db}, now: time.Now}, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// SchemaVersion 返回本库已应用的最大迁移号(0 = 空库/未迁移)。
// 供 /healthz 回显与升级脚本判「降级是否会越过 DB 迁移」——迁移单向,旧版本读不了新库。
func (s *Store) SchemaVersion() (int, error) {
	var v int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func (s *Store) nowUTC() time.Time { return s.now().UTC() }

// rebind 把 SQL 中第 k 个 `?` 占位符改写为 PostgreSQL 的 `$k`。
// 假定 `?` 不作为数据出现(本包全部 SQL 满足;LIKE 通配由参数值携带)。
func rebind(q string) string {
	if !strings.ContainsRune(q, '?') {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

// pdb 包裹 *sql.DB,在 Exec/Query/QueryRow/Begin 入口重绑 `?`→`$n`,其余方法透传。
type pdb struct{ *sql.DB }

func (d pdb) Exec(q string, a ...any) (sql.Result, error) { return d.DB.Exec(rebind(q), a...) }
func (d pdb) Query(q string, a ...any) (*sql.Rows, error) { return d.DB.Query(rebind(q), a...) }
func (d pdb) QueryRow(q string, a ...any) *sql.Row        { return d.DB.QueryRow(rebind(q), a...) }
func (d pdb) Begin() (ptx, error) {
	tx, err := d.DB.Begin()
	return ptx{tx}, err
}

// ptx 包裹 *sql.Tx,同样在入口重绑;Commit/Rollback 由内嵌 *sql.Tx 透传。
type ptx struct{ *sql.Tx }

func (t ptx) Exec(q string, a ...any) (sql.Result, error) { return t.Tx.Exec(rebind(q), a...) }
func (t ptx) Query(q string, a ...any) (*sql.Rows, error) { return t.Tx.Query(rebind(q), a...) }
func (t ptx) QueryRow(q string, a ...any) *sql.Row        { return t.Tx.QueryRow(rebind(q), a...) }

// execer 是 *sql.Tx / ptx 的公共子集,供事务内辅助函数(must be passed a ptx)收参,
// 保证辅助函数里的 `?` 也经 ptx 入口重绑。
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// parseTime 解析库内时间文本(UTC RFC3339Nano)。
func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
