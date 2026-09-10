package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

func initAuthKey(t *testing.T) {
	t.Helper()
	if _, err := secret.BootstrapKey(t.TempDir()); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	initAuthKey(t)
	tok, err := IssueSession(42, "alice", domain.RoleUser, "$2a$10$abcdefghijklmnopqrstuv")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := ParseSession(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "42" {
		t.Errorf("subject = %q, want 42", claims.Subject)
	}
	if claims.Username != "alice" || claims.Role != domain.RoleUser {
		t.Errorf("claims = %+v", claims)
	}
	if claims.PV == "" {
		t.Error("pv empty")
	}
}

func TestParseSessionRejectsTampered(t *testing.T) {
	initAuthKey(t)
	tok, _ := IssueSession(1, "root", domain.RoleAdmin, "hash")
	// 改签名末位
	bad := tok[:len(tok)-1] + string(flip(tok[len(tok)-1]))
	if _, err := ParseSession(bad); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestParseSessionRejectsExpired(t *testing.T) {
	initAuthKey(t)
	key, err := signingKey()
	if err != nil {
		t.Fatal(err)
	}
	claims := SessionClaims{
		Username: "bob",
		Role:     domain.RoleUser,
		PV:       "x",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "7",
			Issuer:    jwtIssuer,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSession(tok); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestParseSessionRejectsNoneAlg(t *testing.T) {
	initAuthKey(t)
	claims := SessionClaims{
		Username: "eve",
		Role:     domain.RoleAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    jwtIssuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSession(tok); err == nil {
		t.Fatal("alg=none token accepted")
	}
}

func TestPasswordVersionTracksHash(t *testing.T) {
	a := PasswordVersion("$2a$10$hash-one")
	b := PasswordVersion("$2a$10$hash-two")
	if a == b {
		t.Fatal("pv must differ for different hashes")
	}
	if a != PasswordVersion("$2a$10$hash-one") {
		t.Fatal("pv not deterministic")
	}
}

func flip(b byte) byte {
	if b == 'A' {
		return 'B'
	}
	return 'A'
}
