// Package auth 承载管理面与模型面的凭据原语:
// 管理员密码 bcrypt 哈希;会话 token 随机生成、库里只存 sha256;
// 模型面令牌明文仅创建时返回一次,库里只存 sha256(范式沿用 store/keys.go)。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionCookie 会话 cookie 名。
const SessionCookie = "gw_session"

// SessionTTL 会话有效期。
const SessionTTL = 7 * 24 * time.Hour

// HashPassword bcrypt 哈希管理员密码(明文不入库)。
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码与 bcrypt 哈希。
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// HashSecret sha256 十六进制(令牌落库形态)。
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// NewSessionToken 生成会话 token;返回明文(写 cookie)与 sha256(落库)。
func NewSessionToken() (raw, hashed string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	return raw, HashSecret(raw), nil
}

// NewModelKey 生成模型面令牌明文 "sk-gw-"+32字节hex 与落库哈希。
func NewModelKey() (plain, hashed string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "sk-gw-" + hex.EncodeToString(b)
	return plain, HashSecret(plain), nil
}
