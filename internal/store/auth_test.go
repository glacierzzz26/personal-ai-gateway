package store

import (
	"testing"
	"time"
)

func TestAdminAndSessions(t *testing.T) {
	st := newTestStore(t)

	n, err := st.CountAdmins()
	mustNoErr(t, err, "count admins")
	mustEqual(t, n, 0, "empty store has no admin (首启 bootstrap)")

	a, err := st.CreateAdmin("admin", "bcrypt-hash")
	mustNoErr(t, err, "create admin")
	_, err = st.CreateAdmin("admin", "x")
	mustErrIs(t, err, ErrConflict, "dup admin")

	found, hash, err := st.AdminByUsername("admin")
	mustNoErr(t, err, "find admin")
	if found.ID != a.ID || hash != "bcrypt-hash" {
		t.Errorf("admin lookup mismatch: %+v hash=%s", found, hash)
	}

	// 会话:建 → 查 → 删
	expires, err := st.CreateSession(a.ID, "tokhash1", time.Hour)
	mustNoErr(t, err, "create session")
	if !expires.After(time.Now()) {
		t.Error("session should expire in future")
	}
	me, err := st.LookupSession("tokhash1")
	mustNoErr(t, err, "lookup session")
	if me.Username != "admin" {
		t.Errorf("session admin = %s", me.Username)
	}

	mustNoErr(t, st.DeleteSession("tokhash1"), "delete session")
	_, err = st.LookupSession("tokhash1")
	mustErrIs(t, err, ErrUnauthorized, "session gone")

	// 过期会话不可用,且可被清理
	_, err = st.CreateSession(a.ID, "tokhash-exp", -time.Second)
	mustNoErr(t, err, "create expired session")
	_, err = st.LookupSession("tokhash-exp")
	mustErrIs(t, err, ErrUnauthorized, "expired session rejected")
	mustNoErr(t, st.PruneExpiredSessions(), "prune")
	var c int
	mustNoErr(t, st.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token='tokhash-exp'`).Scan(&c), "count expired session")
	mustEqual(t, c, 0, "expired session pruned")
}
