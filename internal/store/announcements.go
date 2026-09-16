package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"personal-ai-gateway/internal/domain"
)

// announcementCols 公告读列(管理员列表与单条回读共用)。
// 两个计数字段:已读人数 / 站点普通用户总数(触达评估用)。
const announcementCols = `SELECT a.id, a.title, a.body, a.level, a.enabled,
	a.publish_at, a.expires_at, a.created_at, a.updated_at,
	(SELECT COUNT(*) FROM announcement_dismissals d WHERE d.announcement_id = a.id),
	(SELECT COUNT(*) FROM admins WHERE role = 'user')
	FROM announcements a`

// announcementRowCols 用户面弹窗读列(无计数)。
const announcementRowCols = `SELECT id, title, body, level, enabled, publish_at, expires_at, created_at, updated_at
	FROM announcements`

// ListAnnouncements 管理员列表,新建在前。
func (s *Store) ListAnnouncements() ([]domain.AnnouncementRead, error) {
	rows, err := s.db.Query(announcementCols + ` ORDER BY a.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AnnouncementRead
	for rows.Next() {
		a, err := scanAnnouncementRead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAnnouncement 取单条(管理员回读/更新后返回)。
func (s *Store) GetAnnouncement(id int64) (domain.AnnouncementRead, error) {
	row := s.db.QueryRow(announcementCols+` WHERE a.id = ?`, id)
	a, err := scanAnnouncementRead(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AnnouncementRead{}, ErrNotFound
	}
	return a, err
}

// CreateAnnouncement 新建公告。enabled/level 由输入 Defaults() 兜底。
func (s *Store) CreateAnnouncement(in domain.AnnouncementInput) (domain.AnnouncementRead, error) {
	in.Defaults()
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`INSERT INTO announcements (
		title, body, level, enabled, publish_at, expires_at, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?)`,
		in.Title, in.Body, in.Level, b2i(*in.Enabled),
		nullStrPtr(in.PublishAt), nullStrPtr(in.ExpiresAt), now, now)
	if err != nil {
		return domain.AnnouncementRead{}, fmt.Errorf("insert announcement: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetAnnouncement(id)
}

// UpdateAnnouncement 更新公告体(整字段覆盖;调用方提交完整对象)。
func (s *Store) UpdateAnnouncement(id int64, in domain.AnnouncementInput) (domain.AnnouncementRead, error) {
	in.Defaults()
	res, err := s.db.Exec(`UPDATE announcements SET
		title=?, body=?, level=?, enabled=?, publish_at=?, expires_at=?, updated_at=?
		WHERE id=?`,
		in.Title, in.Body, in.Level, b2i(*in.Enabled),
		nullStrPtr(in.PublishAt), nullStrPtr(in.ExpiresAt),
		formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return domain.AnnouncementRead{}, fmt.Errorf("update announcement %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.AnnouncementRead{}, ErrNotFound
	}
	return s.GetAnnouncement(id)
}

// SetAnnouncementEnabled 启停公告(停用后立即不再对任何用户弹出)。
func (s *Store) SetAnnouncementEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE announcements SET enabled=?, updated_at=? WHERE id=?`,
		b2i(enabled), formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAnnouncement 删除公告,已读记录随外键级联清除(store.go 已开启 foreign_keys)。
func (s *Store) DeleteAnnouncement(id int64) error {
	res, err := s.db.Exec(`DELETE FROM announcements WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingAnnouncement 取「对该用户生效且尚未确认」的最新一条公告;无则返回 (nil, nil)。
//
// 生效 = 启用 ∩ 已到发布时刻(publish_at 空即立即) ∩ 未过期(expires_at 空即永久)。
// 「最新」按 id 降序 —— 发布序即 id 序,新公告永远优先弹出;用户确认后再轮到次新一条。
// 作用域锁死传入账号:确认记录按 (公告, 账号) 去重,互不影响。
func (s *Store) PendingAnnouncement(adminID int64) (*domain.AnnouncementRow, error) {
	now := formatRFC3339(s.nowUTC())
	// publish_at/expires_at 存 UTC RFC3339Nano,字典序即时间序,故可直接字符串比较。
	row := s.db.QueryRow(announcementRowCols+`
		WHERE enabled = 1
		  AND (publish_at IS NULL OR publish_at <= ?)
		  AND (expires_at IS NULL OR expires_at  > ?)
		  AND NOT EXISTS (SELECT 1 FROM announcement_dismissals d
		                  WHERE d.announcement_id = announcements.id AND d.admin_id = ?)
		ORDER BY id DESC LIMIT 1`, now, now, adminID)
	a, err := scanAnnouncementRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// DismissAnnouncement 记「我已知晓」(幂等:重复确认不报错)。
// 公告须存在,否则 ErrNotFound —— 避免写入指向不存在公告的悬挂记录。
func (s *Store) DismissAnnouncement(adminID, announcementID int64) error {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM announcements WHERE id = ?`, announcementID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO announcement_dismissals
		(announcement_id, admin_id, dismissed_at) VALUES (?,?,?)`,
		announcementID, adminID, formatRFC3339(s.nowUTC()))
	return err
}

// nullStrPtr 可空时间串 → 驱动值:nil → SQL NULL(「立即发布」/「永不过期」)。
func nullStrPtr(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

// parseNullTime 可空时间列 → *time.Time(nil 表示未设)。
func parseNullTime(n sql.NullString) *time.Time {
	if !n.Valid || n.String == "" {
		return nil
	}
	if t, ok := parseExpires(n.String); ok {
		return &t
	}
	return nil
}

func scanAnnouncementRow(row scanner) (domain.AnnouncementRow, error) {
	var a domain.AnnouncementRow
	var enabled int
	var publish, expires, created, updated sql.NullString
	if err := row.Scan(&a.ID, &a.Title, &a.Body, &a.Level, &enabled,
		&publish, &expires, &created, &updated); err != nil {
		return domain.AnnouncementRow{}, err
	}
	a.Enabled = enabled == 1
	a.PublishAt = parseNullTime(publish)
	a.ExpiresAt = parseNullTime(expires)
	a.CreatedAt, _ = parseTime(created.String)
	a.UpdatedAt, _ = parseTime(updated.String)
	return a, nil
}

func scanAnnouncementRead(row scanner) (domain.AnnouncementRead, error) {
	var a domain.AnnouncementRead
	var enabled int
	var publish, expires, created, updated sql.NullString
	if err := row.Scan(&a.ID, &a.Title, &a.Body, &a.Level, &enabled,
		&publish, &expires, &created, &updated, &a.ReadCount, &a.UserTotal); err != nil {
		return domain.AnnouncementRead{}, err
	}
	a.Enabled = enabled == 1
	a.PublishAt = parseNullTime(publish)
	a.ExpiresAt = parseNullTime(expires)
	a.CreatedAt, _ = parseTime(created.String)
	a.UpdatedAt, _ = parseTime(updated.String)
	return a, nil
}
