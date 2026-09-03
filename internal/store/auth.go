package store

import (
	"database/sql"
	"errors"
	"time"

	"personal-ai-gateway/internal/domain"
)

// ErrUnauthorized 会话无效/过期(与不存在同等返回,不区分以免信息泄露)。
var ErrUnauthorized = errors.New("invalid or expired session")

// CountAdmins 管理员数量(0 = 首启,走 bootstrap)。
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

// CreateAdmin 建管理员。username 唯一;password 传入 bcrypt 哈希(明文不入库)。
func (s *Store) CreateAdmin(username, passwordBcrypt string) (domain.AdminUser, error) {
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`INSERT INTO admins (username, password_bcrypt, created_at) VALUES (?,?,?)`,
		username, passwordBcrypt, now)
	if err != nil {
		if isUniqueErr(err) {
			return domain.AdminUser{}, ErrConflict
		}
		return domain.AdminUser{}, err
	}
	id, _ := res.LastInsertId()
	return domain.AdminUser{ID: id, Username: username, CreatedAt: s.nowUTC()}, nil
}

// AdminByUsername 登录校验用:返回管理员与其 bcrypt 哈希。
func (s *Store) AdminByUsername(username string) (admin domain.AdminUser, passwordBcrypt string, err error) {
	var created string
	err = s.db.QueryRow(`SELECT id, username, password_bcrypt, created_at FROM admins WHERE username=?`,
		username).Scan(&admin.ID, &admin.Username, &passwordBcrypt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AdminUser{}, "", ErrNotFound
	}
	if err != nil {
		return domain.AdminUser{}, "", err
	}
	admin.CreatedAt, _ = parseTime(created)
	return admin, passwordBcrypt, nil
}

// CreateSession 为管理员建会话,返回过期时刻(server 据此设 cookie MaxAge)。
func (s *Store) CreateSession(adminID int64, tokenHash string, ttl time.Duration) (time.Time, error) {
	now := s.nowUTC()
	expires := now.Add(ttl)
	_, err := s.db.Exec(`INSERT INTO sessions (token, admin_id, created_at, expires_at) VALUES (?,?,?,?)`,
		tokenHash, adminID, formatRFC3339(now), formatRFC3339(expires))
	if err != nil {
		return time.Time{}, err
	}
	return expires, nil
}

// LookupSession 按 token 哈希查会话;缺失或已过期一律 ErrUnauthorized。
func (s *Store) LookupSession(tokenHash string) (domain.AdminUser, error) {
	var a domain.AdminUser
	var created, expires string
	err := s.db.QueryRow(`SELECT a.id, a.username, a.created_at, s.expires_at
		FROM sessions s JOIN admins a ON a.id = s.admin_id WHERE s.token=?`, tokenHash).
		Scan(&a.ID, &a.Username, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AdminUser{}, ErrUnauthorized
	}
	if err != nil {
		return domain.AdminUser{}, err
	}
	exp, perr := parseTime(expires)
	if perr != nil || !exp.After(s.nowUTC()) {
		return domain.AdminUser{}, ErrUnauthorized
	}
	a.CreatedAt, _ = parseTime(created)
	return a, nil
}

// DeleteSession 登出:删除该 token 会话。幂等。
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, tokenHash)
	return err
}

// PruneExpiredSessions 启动时清一次过期会话。
func (s *Store) PruneExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, formatRFC3339(s.nowUTC()))
	return err
}
