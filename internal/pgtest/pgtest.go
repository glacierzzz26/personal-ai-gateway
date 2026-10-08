// Package pgtest 提供「一次一 schema」的 PostgreSQL 测试库脚手架。
//
// 不 import store(故 store 包自身的测试也能用,不构成 import cycle)。库由 TEST_PG_DSN
// 指定;未设置时 DSN 会 t.Skip。隔离:每测试建独立 schema,连接 search_path 钉过去,
// 测试结束 DROP SCHEMA CASCADE。
package pgtest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DSN 返回测试库 DSN;TEST_PG_DSN 未设置时跳过当前测试。
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN 未设置:跳过需数据库的测试(本地:deploy/scripts/test-pg.sh 输出即为该值)")
	}
	return dsn
}

// SchemaDSN 建一个独立 schema 并返回把连接 search_path 钉到它的 DSN。
// 测试结束自动 DROP schema。调用方据此 Open 出的库互不干扰。
func SchemaDSN(t *testing.T) string {
	t.Helper()
	dsn := DSN(t)
	schema := "t_" + randSuffix()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin conn: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		admin.Close()
	})
	return withSearchPath(t, dsn, schema)
}

// withSearchPath 把连接 search_path 钉到 schema(经 libpq options 启动参数下发)。
func withSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_PG_DSN %q: %v", dsn, err)
	}
	q := u.Query()
	q.Set("options", "-csearch_path="+schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func randSuffix() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand 失败 = 环境异常,直接炸而非静默退化
	}
	return hex.EncodeToString(b[:])
}
