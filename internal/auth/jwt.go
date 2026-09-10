// jwt.go 管理台会话的 JWT 签发/校验(HS256,密钥由主密钥派生)。
//
// 会话是无状态 JWT:登出=清 cookie,不做服务端吊销。为让「改密码 / 角色变更 / 删号」
// 立即生效,除验签外每次请求还会用 AdminByID 复核角色与密码版本(pv),见 server/middleware.go。
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

// jwtSubkeyLabel 派生 JWT 签名密钥的域分隔标签。改动它 = 全部现存会话失效。
const jwtSubkeyLabel = "personal-ai-gateway/session-jwt/v1"

// jwtIssuer 签发者标识。
const jwtIssuer = "personal-ai-gateway"

// SessionClaims 会话 JWT 载荷。
type SessionClaims struct {
	Username string      `json:"uname"`
	Role     domain.Role `json:"role"`
	PV       string      `json:"pv"` // 密码版本:密码变更即失效
	jwt.RegisteredClaims
}

var (
	jwtKeyOnce sync.Once
	jwtKey     []byte
	jwtKeyErr  error
)

// signingKey 懒派生并缓存 JWT 签名密钥(主密钥须已由 main/测试 BootstrapKey 引导)。
func signingKey() ([]byte, error) {
	jwtKeyOnce.Do(func() {
		jwtKey, jwtKeyErr = secret.DeriveSubkey(jwtSubkeyLabel)
	})
	return jwtKey, jwtKeyErr
}

// PasswordVersion 由 bcrypt 哈希派生短版本号:密码变 → pv 变 → 旧 JWT 全部失效。
// 用哈希(非明文)派生的,哈希不出库。
func PasswordVersion(bcryptHash string) string {
	sum := sha256.Sum256([]byte(bcryptHash))
	return hex.EncodeToString(sum[:])[:16]
}

// IssueSession 签发 HS256 会话 JWT。bcryptHash 用于生成 pv(见 PasswordVersion)。
func IssueSession(id int64, username string, role domain.Role, bcryptHash string) (string, error) {
	key, err := signingKey()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := SessionClaims{
		Username: username,
		Role:     role,
		PV:       PasswordVersion(bcryptHash),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(id, 10),
			Issuer:    jwtIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(SessionTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

// ParseSession 校验签名/算法/过期,返回 claims。失败返回错误。
func ParseSession(token string) (*SessionClaims, error) {
	key, err := signingKey()
	if err != nil {
		return nil, err
	}
	claims := &SessionClaims{}
	tok, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid session token")
	}
	if id, err := strconv.ParseInt(claims.Subject, 10, 64); err != nil || id <= 0 {
		return nil, fmt.Errorf("invalid subject %q", claims.Subject)
	}
	return claims, nil
}
