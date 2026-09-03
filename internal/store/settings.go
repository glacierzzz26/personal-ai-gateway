package store

import (
	"strconv"

	"personal-ai-gateway/internal/domain"
)

// settings 键名(下划线常量,与 domain.Settings JSON 字段一一对应)。
const (
	keyRequestTimeout = "request_timeout_ms"
	keyMaxRetries     = "max_retries"
	keyDegradeOnError = "degrade_on_error"
	keyHTTPProxy      = "http_proxy"
	keySkipTLSVerify  = "skip_tls_verify"
	keyLogRetention   = "log_retention_days"
	keyRecordBody     = "record_request_body"
	keySampleRate     = "sample_rate_pct"
	keyTZOffsetMin    = "tz_offset_min"
)

var settingsKeys = []string{
	keyRequestTimeout, keyMaxRetries, keyDegradeOnError, keyHTTPProxy, keySkipTLSVerify,
	keyLogRetention, keyRecordBody, keySampleRate, keyTZOffsetMin,
}

// GetSettings 读取全部设置;表为空返回默认值(不落库)。
func (s *Store) GetSettings() (domain.Settings, error) {
	rows, err := s.db.Query(`SELECT k, v FROM settings`)
	if err != nil {
		return domain.Settings{}, err
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return domain.Settings{}, err
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return domain.Settings{}, err
	}

	cfg := domain.Settings{}
	cfg.Defaults()
	cfg.RequestTimeoutMs = intOr(cfg.RequestTimeoutMs, raw[keyRequestTimeout])
	cfg.MaxRetries = intOr(cfg.MaxRetries, raw[keyMaxRetries])
	cfg.DegradeOnError = boolOr(cfg.DegradeOnError, raw[keyDegradeOnError])
	cfg.HTTPProxy = strOr(raw[keyHTTPProxy])
	cfg.SkipTLSVerify = boolOr(cfg.SkipTLSVerify, raw[keySkipTLSVerify])
	cfg.LogRetentionDays = intOr(cfg.LogRetentionDays, raw[keyLogRetention])
	cfg.RecordRequestBody = boolOr(cfg.RecordRequestBody, raw[keyRecordBody])
	cfg.SampleRatePct = intOr(cfg.SampleRatePct, raw[keySampleRate])
	cfg.TZOffsetMin = intOr(cfg.TZOffsetMin, raw[keyTZOffsetMin])
	return cfg, nil
}

// SaveSettings 整体写入(调用方先把未改字段从 GetSettings 合并)。
func (s *Store) SaveSettings(cfg domain.Settings) error {
	kv := map[string]string{
		keyRequestTimeout: strconv.Itoa(cfg.RequestTimeoutMs),
		keyMaxRetries:     strconv.Itoa(cfg.MaxRetries),
		keyDegradeOnError: boolStr(cfg.DegradeOnError),
		keyHTTPProxy:      cfg.HTTPProxy,
		keySkipTLSVerify:  boolStr(cfg.SkipTLSVerify),
		keyLogRetention:   strconv.Itoa(cfg.LogRetentionDays),
		keyRecordBody:     boolStr(cfg.RecordRequestBody),
		keySampleRate:     strconv.Itoa(cfg.SampleRatePct),
		keyTZOffsetMin:    strconv.Itoa(cfg.TZOffsetMin),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range kv {
		if _, err := tx.Exec(`INSERT INTO settings (k, v) VALUES (?, ?)
			ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func intOr(def int, raw string) int {
	if raw == "" {
		return def
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return def
}

func boolOr(def bool, raw string) bool {
	if raw == "" {
		return def
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	return def
}

func strOr(raw string) string { return raw }

func boolStr(b bool) string { return strconv.FormatBool(b) }
