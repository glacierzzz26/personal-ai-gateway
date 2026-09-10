package store

import (
	"database/sql"
	"errors"

	"personal-ai-gateway/internal/domain"
)

// ErrUnauthorized 凭据无效(与不存在同等返回,不区分以免信息泄露)。
var ErrUnauthorized = errors.New("invalid or expired credentials")

// CountAdmins 账号总数(0 = 首启,走 bootstrap)。
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

// CountAdminsByRole 按角色计数(守卫「不能删最后一个管理员」)。
func (s *Store) CountAdminsByRole(role domain.Role) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE role=?`, string(role)).Scan(&n)
	return n, err
}

// CreateAdmin 建账号。username 唯一;password 传入 bcrypt 哈希(明文不入库)。
func (s *Store) CreateAdmin(username, passwordBcrypt string, role domain.Role) (domain.AdminUser, error) {
	if !role.Valid() {
		role = domain.RoleUser
	}
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`INSERT INTO admins (username, password_bcrypt, role, created_at) VALUES (?,?,?,?)`,
		username, passwordBcrypt, string(role), now)
	if err != nil {
		if isUniqueErr(err) {
			return domain.AdminUser{}, ErrConflict
		}
		return domain.AdminUser{}, err
	}
	id, _ := res.LastInsertId()
	return domain.AdminUser{ID: id, Username: username, Role: role, CreatedAt: s.nowUTC()}, nil
}

// AdminByUsername 登录校验用:返回账号与其 bcrypt 哈希。
func (s *Store) AdminByUsername(username string) (admin domain.AdminUser, passwordBcrypt string, err error) {
	row := s.db.QueryRow(`SELECT id, username, role, password_bcrypt, created_at FROM admins WHERE username=?`, username)
	admin, passwordBcrypt, err = scanAdmin(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AdminUser{}, "", ErrNotFound
	}
	return admin, passwordBcrypt, err
}

// AdminByID 按主键取账号与哈希(JWT 复核角色/密码版本用)。
func (s *Store) AdminByID(id int64) (admin domain.AdminUser, passwordBcrypt string, err error) {
	row := s.db.QueryRow(`SELECT id, username, role, password_bcrypt, created_at FROM admins WHERE id=?`, id)
	admin, passwordBcrypt, err = scanAdmin(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AdminUser{}, "", ErrNotFound
	}
	return admin, passwordBcrypt, err
}

// ListUsers 全部账号(附各自名下令牌数),新建在前。
func (s *Store) ListUsers() ([]domain.UserRead, error) {
	rows, err := s.db.Query(`SELECT a.id, a.username, a.role, a.created_at,
		(SELECT COUNT(*) FROM tokens t WHERE t.owner_id = a.id)
		FROM admins a ORDER BY a.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UserRead
	for rows.Next() {
		var u domain.UserRead
		var role, created string
		if err := rows.Scan(&u.ID, &u.Username, &role, &created, &u.KeyCount); err != nil {
			return nil, err
		}
		u.Role = domain.Role(role)
		u.CreatedAt, _ = parseTime(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateAdminPassword 重置/修改密码(调用方传 bcrypt 哈希)。
func (s *Store) UpdateAdminPassword(id int64, passwordBcrypt string) error {
	res, err := s.db.Exec(`UPDATE admins SET password_bcrypt=? WHERE id=?`, passwordBcrypt, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser 删除账号(其名下令牌经 FK 级联删除)。
func (s *Store) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM admins WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanAdmin(row scanner) (domain.AdminUser, string, error) {
	var a domain.AdminUser
	var role, created, hash string
	if err := row.Scan(&a.ID, &a.Username, &role, &hash, &created); err != nil {
		return domain.AdminUser{}, "", err
	}
	a.Role = domain.Role(role)
	a.CreatedAt, _ = parseTime(created)
	return a, hash, nil
}
