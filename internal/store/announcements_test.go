package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

// mkAnnouncement 造一条立即发布、永久有效、启用的公告。
func mkAnnouncement(t *testing.T, st *Store, title string) domain.AnnouncementRead {
	t.Helper()
	a, err := st.CreateAnnouncement(domain.AnnouncementInput{Title: title, Body: title + " 正文"})
	mustNoErr(t, err, "create announcement "+title)
	return a
}

func TestAnnouncementCRUD(t *testing.T) {
	st := newTestStore(t)
	a := mkAnnouncement(t, st, "维护通知")
	if !a.Enabled || a.Level != domain.LevelInfo {
		t.Fatalf("defaults not applied: enabled=%v level=%q", a.Enabled, a.Level)
	}
	if a.PublishAt != nil || a.ExpiresAt != nil {
		t.Fatalf("empty schedule should be nil: %+v %+v", a.PublishAt, a.ExpiresAt)
	}

	// 更新:级别 + 定时 + 过期
	pub := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	exp := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339Nano)
	up, err := st.UpdateAnnouncement(a.ID, domain.AnnouncementInput{
		Title: "维护通知 v2", Body: "改期", Level: domain.LevelDanger,
		PublishAt: &pub, ExpiresAt: &exp,
	})
	mustNoErr(t, err, "update announcement")
	if up.Title != "维护通知 v2" || up.Level != domain.LevelDanger {
		t.Fatalf("update not applied: %+v", up)
	}
	if up.PublishAt == nil || up.ExpiresAt == nil {
		t.Fatalf("schedule not persisted: %+v %+v", up.PublishAt, up.ExpiresAt)
	}

	// 启停
	mustNoErr(t, st.SetAnnouncementEnabled(a.ID, false), "disable")
	got, _ := st.GetAnnouncement(a.ID)
	if got.Enabled {
		t.Error("announcement should be disabled")
	}

	// 列表按 id 降序
	mkAnnouncement(t, st, "第二条")
	list, err := st.ListAnnouncements()
	mustNoErr(t, err, "list")
	if len(list) != 2 || list[0].Title != "第二条" {
		t.Fatalf("list order wrong: %+v", list)
	}

	// 删除
	mustNoErr(t, st.DeleteAnnouncement(a.ID), "delete")
	_, err = st.GetAnnouncement(a.ID)
	mustErrIs(t, err, ErrNotFound, "get deleted")
	mustErrIs(t, st.DeleteAnnouncement(a.ID), ErrNotFound, "delete again")
}

// TestPendingAnnouncement 生效判定:启用 ∩ 已发布 ∩ 未过期 ∩ 未确认,多条取最新。
func TestPendingAnnouncement(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateAdmin("alice", "h", domain.RoleUser)

	// 无公告 → nil
	p, err := st.PendingAnnouncement(u.ID)
	mustNoErr(t, err, "pending empty")
	if p != nil {
		t.Fatalf("empty store should yield nil, got %+v", p)
	}

	// 立即发布 → 可见
	a1 := mkAnnouncement(t, st, "第一条")
	p, _ = st.PendingAnnouncement(u.ID)
	if p == nil || p.ID != a1.ID {
		t.Fatalf("live announcement should be pending, got %+v", p)
	}

	// 定时未到 → 不可见(仅 a1 仍在)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	futureA, err := st.CreateAnnouncement(domain.AnnouncementInput{Title: "未来", Body: "x", PublishAt: &future})
	mustNoErr(t, err, "create future")
	p, _ = st.PendingAnnouncement(u.ID)
	if p == nil || p.ID != a1.ID {
		t.Fatalf("future announcement must not surface; want a1, got %+v", p)
	}

	// 定时已到(过去时刻)→ 可见,且 id 更新故优先
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	_, err = st.UpdateAnnouncement(futureA.ID, domain.AnnouncementInput{Title: "未来", Body: "x", PublishAt: &past})
	mustNoErr(t, err, "publish future now")
	p, _ = st.PendingAnnouncement(u.ID)
	if p == nil || p.ID != futureA.ID {
		t.Fatalf("newest live should win, want %d got %+v", futureA.ID, p)
	}

	// 已过期 → 不可见,回落 a1
	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	_, err = st.UpdateAnnouncement(futureA.ID, domain.AnnouncementInput{Title: "未来", Body: "x", ExpiresAt: &expired})
	mustNoErr(t, err, "expire")
	p, _ = st.PendingAnnouncement(u.ID)
	if p == nil || p.ID != a1.ID {
		t.Fatalf("expired must not surface; want a1 got %+v", p)
	}

	// 停用 → 不可见,全无
	mustNoErr(t, st.SetAnnouncementEnabled(a1.ID, false), "disable a1")
	p, _ = st.PendingAnnouncement(u.ID)
	if p != nil {
		t.Fatalf("disabled must not surface, got %+v", p)
	}
}

// TestDismissAndScope 确认后不再对该用户弹出,且不影响他人;重复确认幂等。
func TestDismissAndScope(t *testing.T) {
	st := newTestStore(t)
	alice, _ := st.CreateAdmin("alice", "h", domain.RoleUser)
	bob, _ := st.CreateAdmin("bob", "h", domain.RoleUser)
	a := mkAnnouncement(t, st, "公告")

	// 确认前 alice 可见
	if p, _ := st.PendingAnnouncement(alice.ID); p == nil || p.ID != a.ID {
		t.Fatalf("alice should see it before ack, got %+v", p)
	}
	mustNoErr(t, st.DismissAnnouncement(alice.ID, a.ID), "ack")
	if p, _ := st.PendingAnnouncement(alice.ID); p != nil {
		t.Fatalf("alice should not see it after ack, got %+v", p)
	}
	// 幂等
	mustNoErr(t, st.DismissAnnouncement(alice.ID, a.ID), "ack again idempotent")
	// 作用域:bob 不受影响
	if p, _ := st.PendingAnnouncement(bob.ID); p == nil || p.ID != a.ID {
		t.Fatalf("bob should still see it, got %+v", p)
	}

	// 已读计数
	got, _ := st.GetAnnouncement(a.ID)
	if got.ReadCount != 1 || got.UserTotal != 2 {
		t.Fatalf("read count = %d/%d, want 1/2", got.ReadCount, got.UserTotal)
	}

	// 确认不存在的公告 → ErrNotFound(不写悬挂记录)
	mustErrIs(t, st.DismissAnnouncement(alice.ID, 99999), ErrNotFound, "ack missing")
}

// TestAnnouncementDeleteCascadesDismissals 删公告级联清除已读记录(外键 ON DELETE CASCADE)。
func TestAnnouncementDeleteCascadesDismissals(t *testing.T) {
	st := newTestStore(t)
	alice, _ := st.CreateAdmin("alice", "h", domain.RoleUser)
	a := mkAnnouncement(t, st, "公告")
	mustNoErr(t, st.DismissAnnouncement(alice.ID, a.ID), "ack")

	var n int
	mustNoErr(t, st.db.QueryRow(`SELECT COUNT(*) FROM announcement_dismissals`).Scan(&n), "count")
	mustEqual(t, n, 1, "one dismissal before delete")

	mustNoErr(t, st.DeleteAnnouncement(a.ID), "delete")
	mustNoErr(t, st.db.QueryRow(`SELECT COUNT(*) FROM announcement_dismissals`).Scan(&n), "count after")
	mustEqual(t, n, 0, "dismissals cascade-deleted")
}
