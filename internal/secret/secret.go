// Package secret 管理「本地主密钥」并基于它对渠道 API key 做对称加密。
//
// 渠道密钥需要回放给上游,故不能像令牌 secret 那样只存 sha256;
// 这里采用 AES-256-GCM:DB 只落密文(base64(nonce||ct)),主密钥不入库。
// 主密钥来源优先级:
//  1. 环境变量 GW_MASTER_KEY(任意长度,经 sha256 展为 32 字节);
//  2. 数据库同目录 gateway.master.key(首次启动自动生成 32 字节随机密钥,0600);
//
// 若 DB 里已存在密文而当前主密钥换过,解密将失败,属预期(提示换回原密钥即可)。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyFile 主密钥落盘文件名(位于 DB 同目录)。
const KeyFile = "gateway.master.key"

// EnvKey 主密钥环境变量名。
const EnvKey = "GW_MASTER_KEY"

var (
	current []byte
	loaded  bool
)

// BootstrapKey 解析并固定主密钥。任意多次调用幂等,首次成功后不再变更。
func BootstrapKey(dbDir string) ([]byte, error) {
	if loaded {
		return current, nil
	}
	key, err := bootstrap(dbDir)
	if err != nil {
		return nil, err
	}
	current = key
	loaded = true
	return current, nil
}

// Current 返回已引导的主密钥;未引导返回 nil。
func Current() []byte {
	return current
}

func bootstrap(dbDir string) ([]byte, error) {
	if s := os.Getenv(EnvKey); s != "" {
		h := sha256.Sum256([]byte(s))
		return h[:], nil
	}
	p := filepath.Join(dbDir, KeyFile)
	raw, err := os.ReadFile(p)
	if err == nil {
		key, e := decodeKey(string(raw))
		if e != nil {
			return nil, fmt.Errorf("parse %s: %w", p, e)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read master key file: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dbDir, err)
	}
	b64 := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(p, []byte(b64+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write master key file: %w", err)
	}
	return key, nil
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil && len(raw) == 32 {
		return raw, nil
	}
	return nil, fmt.Errorf("expected base64 of 32 bytes, got %d chars", len(s))
}

// Encrypt 加密明文,返回 base64(nonce||ciphertext)。空明文返回空串(不落密文)。
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if !loaded || current == nil {
		return "", fmt.Errorf("master key not bootstrapped (set %s or keep %s)", EnvKey, KeyFile)
	}
	block, err := aes.NewCipher(current)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 的产物。空串返回空明文。
func Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if !loaded || current == nil {
		return "", fmt.Errorf("master key not bootstrapped (set %s or keep %s)", EnvKey, KeyFile)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("decode cipher: %w", err)
	}
	block, err := aes.NewCipher(current)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("cipher too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt (master key mismatch?): %w", err)
	}
	return string(plain), nil
}
