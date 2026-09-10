package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

func TestAdminAccountsAndRoles(t *testing.T) {
	st := newTestStore(t)

	n, err := st.CountAdmins()
	mustNoErr(t, err, "count admins")
	mustEqual(t, n, 0, "empty store has no admin (首启 bootstrap)")

	a, err := st.CreateAdmin("admin", "bcrypt-hash", domain.RoleAdmin)
	mustNoErr(t, err, "create admin")
	mustEqual(t, string(a.Role), "admin", "admin role")
	_, err = st.CreateAdmin("admin", "x", domain.RoleUser)
	mustErrIs(t, err, ErrConflict, "dup admin")

	found, hash, err := st.AdminByUsername("admin")
	mustNoErr(t, err, "find admin")
	if found.ID != a.ID || hash != "bcrypt-hash" || found.Role != domain.RoleAdmin {
		t.Errorf("admin lookup mismatch: %+v hash=%s", found, hash)
	}

	byID, hash2, err := st.AdminByID(a.ID)
	mustNoErr(t, err, "find admin by id")
	if byID.Username != "admin" || hash2 != "bcrypt-hash" || byID.Role != domain.RoleAdmin {
		t.Errorf("admin by id mismatch: %+v", byID)
	}

	// 角色计数
	na, err := st.CountAdminsByRole(domain.RoleAdmin)
	mustNoErr(t, err, "count admins by role")
	mustEqual(t, na, 1, "one admin")
	nu, _ := st.CountAdminsByRole(domain.RoleUser)
	mustEqual(t, nu, 0, "no user yet")

	// 建普通用户
	u, err := st.CreateAdmin("alice", "hash2", domain.RoleUser)
	mustNoErr(t, err, "create user")
	mustEqual(t, string(u.Role), "user", "user role")
}

func TestUserPasswordAndDelete(t *testing.T) {
	st := newTestStore(t)
	admin, _ := st.CreateAdmin("root", "h-admin", domain.RoleAdmin)
	user, _ := st.CreateAdmin("alice", "h-old", domain.RoleUser)

	// 改密码
	mustNoErr(t, st.UpdateAdminPassword(user.ID, "h-new"), "update password")
	_, hash, _ := st.AdminByID(user.ID)
	mustEqual(t, hash, "h-new", "password updated")
	err := st.UpdateAdminPassword(9999, "x")
	mustErrIs(t, err, ErrNotFound, "update unknown user")

	// ListUsers + KeyCount
	_, _ = st.CreateToken("k1", &user.ID, "cipher", []string{"*"}, 0, 60, nil, "sha-k1", "sk-gw-••••k1")
	_, _ = st.CreateToken("k2", &user.ID, "cipher", []string{"*"}, 0, 60, nil, "sha-k2", "sk-gw-••••k2")
	users, err := st.ListUsers()
	mustNoErr(t, err, "list users")
	byName := map[string]domain.UserRead{}
	for _, uu := range users {
		byName[uu.Username] = uu
	}
	mustEqual(t, byName["alice"].KeyCount, 2, "alice key count")
	mustEqual(t, byName["root"].KeyCount, 0, "root key count")

	// 删用户 → 其令牌级联删除
	mustNoErr(t, st.DeleteUser(user.ID), "delete user")
	var cnt int
	mustNoErr(t, st.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE owner_id=?`, user.ID).Scan(&cnt), "count orphan tokens")
	mustEqual(t, cnt, 0, "user tokens cascade-deleted")
	err = st.DeleteUser(9999)
	mustErrIs(t, err, ErrNotFound, "delete unknown user")

	// 管理员仍在
	if _, _, err := st.AdminByID(admin.ID); err != nil {
		t.Errorf("admin should survive: %v", err)
	}
}
