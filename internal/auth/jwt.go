// jwt.go 管理台会话的 JWT 签发/校验(HS256,密钥由主密钥派生)。
//
// 会话是无状态 JWT:登出=清 cookie,不做服务端吊销。为让「改密码 / 角色变更 / 删号」
// 立即生效,除验签外每次请求还会用 AdminByID 复核角色与密码版本(pv),见 server/middleware.go。
//
// 过期是**两重**的(见 auth.go 常量):
//   - exp  = 空闲窗(SessionIdleTTL)。滑动续期:每次通过鉴权的请求都重签、重算 exp;
//     连续无请求超过空闲窗即失效。
//   - abs  = 绝对上限(SessionMaxTTL)。自首次登录起算,续签时原样保留、不重置;到期强制重登。
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

// ErrSessionExpired 会话已过绝对上限(SessionMaxTTL),须重新登录。
var ErrSessionExpired = errors.New("session expired")

// SessionClaims 会话 JWT 载荷。
type SessionClaims struct {
	Username string      `json:"uname"`
	Role     domain.Role `json:"role"`
	PV       string      `json:"pv"` // 密码版本:密码变更即失效
	// AbsoluteExpiresAt 绝对上限(自首次登录起算)。滑动续期时原样保留、不重置,
	// 到期即 fail —— 给被盗会话封顶。见 SessionMaxTTL / RefreshSession。
	AbsoluteExpiresAt *jwt.NumericDate `json:"abs,omitempty"`
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

// issueSessionAt 以给定时刻签发会话:空闲窗从 now 起算,绝对上限为 abs(exp 不晚于它)。
func issueSessionAt(now time.Time, id int64, username string, role domain.Role, pv string, abs time.Time) (string, error) {
	key, err := signingKey()
	if err != nil {
		return "", err
	}
	exp := now.Add(SessionIdleTTL)
	if exp.After(abs) {
		exp = abs
	}
	claims := SessionClaims{
		Username:          username,
		Role:              role,
		PV:                pv,
		AbsoluteExpiresAt: jwt.NewNumericDate(abs),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(id, 10),
			Issuer:    jwtIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

// IssueSession 首次登录签发 HS256 会话 JWT。bcryptHash 用于生成 pv(见 PasswordVersion)。
// 绝对上限自此刻起算(now + SessionMaxTTL);空闲窗同样自此刻起算。
func IssueSession(id int64, username string, role domain.Role, bcryptHash string) (string, error) {
	now := time.Now()
	return issueSessionAt(now, id, username, role, PasswordVersion(bcryptHash), now.Add(SessionMaxTTL))
}

// RefreshSession 以既有会话 claims **滑动续签**:空闲窗从此刻重算,绝对上限原样保留。
// 已过绝对上限 → ErrSessionExpired(调用方应视作需重新登录)。
func RefreshSession(claims *SessionClaims) (string, error) {
	now := time.Now()
	abs := time.Time{}
	if claims.AbsoluteExpiresAt != nil {
		abs = claims.AbsoluteExpiresAt.Time
	}
	if abs.IsZero() {
		// 兼容本次变更前签发的旧会话(无 abs):以原签发时刻 + 上限兜底。
		base := now
		if claims.IssuedAt != nil {
			base = claims.IssuedAt.Time
		}
		abs = base.Add(SessionMaxTTL)
	}
	if !now.Before(abs) {
		return "", ErrSessionExpired
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id <= 0 {
		return "", fmt.Errorf("invalid subject %q", claims.Subject)
	}
	return issueSessionAt(now, id, claims.Username, claims.Role, claims.PV, abs)
}

// ParseSession 校验签名/算法/过期(exp 空闲窗 + abs 绝对上限),返回 claims。失败返回错误。
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
	// 绝对上限的显式兜底:签发时已保证 exp ≤ abs,这里再核一遍防意外。
	if claims.AbsoluteExpiresAt != nil && !time.Now().Before(claims.AbsoluteExpiresAt.Time) {
		return nil, ErrSessionExpired
	}
	if id, err := strconv.ParseInt(claims.Subject, 10, 64); err != nil || id <= 0 {
		return nil, fmt.Errorf("invalid subject %q", claims.Subject)
	}
	return claims, nil
}
