// Package storetest 为跨包测试提供「一次一 schema」的 store 实例。
//
// 仅被各包的 _test.go 引入 —— 不会进入任何发布产物。底层隔离逻辑见 internal/pgtest。
// 目标库由 TEST_PG_DSN 指定;未设置时 Open 会 t.Skip。
package storetest

import (
	"testing"

	"personal-ai-gateway/internal/pgtest"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// Open 在独立 schema 中打开一个已迁移完成的 store,并引导主密钥(渠道密钥加解密)。
// 测试结束自动关库并 DROP schema。
func Open(t *testing.T) *store.Store {
	t.Helper()
	// 主密钥引导:同进程幂等,任意目录皆可,后续调用直接复用。
	if _, err := secret.BootstrapKey(t.TempDir()); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := store.Open(pgtest.SchemaDSN(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
