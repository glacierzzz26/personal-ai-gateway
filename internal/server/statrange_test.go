package server

import (
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

/*
StatRange 覆盖首页统计窗口(issue: 首页需支持 1/7/30 天 + 自定义区间)。

关键不变量:
  - days=N 时窗口 = 最近 N 个自然日(含今天),汇总只算窗口内的日志;
  - from&to 时窗口 = [起日零点, 止日次日零点),含首尾;
  - ≤3 天按小时分桶,>3 天按日;
  - 超 90 天 / from>to / 只给一半 → 400。
*/
func TestOverviewStatRange(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	now := time.Now().UTC()
	// 三条日志:今天、5 天前、40 天前。用 days=7 只应命中前两条。
	seedLog(t, st, "m-today", now.Add(-1*time.Hour))
	seedLog(t, st, "m-5d", now.AddDate(0, 0, -5))
	seedLog(t, st, "m-40d", now.AddDate(0, 0, -40))

	// --- days=7:命中今天 + 5 天前 ---
	ov := decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/overview?days=7"))
	if got := int(ov["totalRequests"].(float64)); got != 2 {
		t.Fatalf("days=7 totalRequests = %d, want 2 (40 天前那条应被排除)", got)
	}
	if got := ov["days"].(float64); got != 7 {
		t.Fatalf("days=7 days field = %v, want 7", got)
	}
	if got := ov["bucket"].(string); got != "day" {
		t.Fatalf("days=7 bucket = %q, want day", got)
	}
	if pts := ov["points"].([]any); len(pts) != 7 {
		t.Fatalf("days=7 points = %d, want 7 个连续日桶", len(pts))
	}

	// --- days=1:只有今天,按小时分桶 ---
	// 窗口 = [今天本地零点, now),故桶数 = 今天已过的小时数(1..24),而非固定 24 ——
	// 曲线只画到当下,不拖一串未来空桶。
	ov = decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/overview?days=1"))
	if got := int(ov["totalRequests"].(float64)); got != 1 {
		t.Fatalf("days=1 totalRequests = %d, want 1", got)
	}
	if got := ov["bucket"].(string); got != "hour" {
		t.Fatalf("days=1 bucket = %q, want hour", got)
	}
	if pts := ov["points"].([]any); len(pts) < 1 || len(pts) > 24 {
		t.Fatalf("days=1 points = %d, want 1..24(今天已过小时数)", len(pts))
	}

	// --- days=90:命中全部三条 ---
	ov = decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/overview?days=90"))
	if got := int(ov["totalRequests"].(float64)); got != 3 {
		t.Fatalf("days=90 totalRequests = %d, want 3", got)
	}

	// --- 自定义区间,只盖住 40 天前那条 ---
	from := now.AddDate(0, 0, -41).Format("2006-01-02")
	to := now.AddDate(0, 0, -39).Format("2006-01-02")
	ov = decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/overview?from="+from+"&to="+to))
	if got := int(ov["totalRequests"].(float64)); got != 1 {
		t.Fatalf("custom range totalRequests = %d, want 1", got)
	}

	// --- 参数校验 ---
	// 预设天数超上限走钳制(数据只留 90 天,返回 90 天更有用),不报错。
	code, _ := doJSON(t, admin, http.MethodGet, base+"/api/v1/overview?days=120", nil)
	if code != http.StatusOK {
		t.Fatalf("days=120: status %d, want 200(预设天数超界应钳制而非报错)", code)
	}
	for _, q := range []string{
		"?from=2026-01-01",               // 只给一半
		"?from=2026-03-01&to=2026-01-01", // 起 > 止
		"?from=" + now.AddDate(0, 0, -100).Format("2006-01-02") + "&to=" + now.Format("2006-01-02"), // >90 天
		"?from=bad-date&to=" + now.Format("2006-01-02"),                                             // 日期格式错
	} {
		code, _ := doJSON(t, admin, http.MethodGet, base+"/api/v1/overview"+q, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", q, code)
		}
	}
}

// TestUsageStatRange dim 聚合与曲线共用同一窗口,且 rows 也受窗口约束。
func TestUsageStatRange(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	now := time.Now().UTC()
	seedLog(t, st, "m-new", now.Add(-1*time.Hour))
	seedLog(t, st, "m-old", now.AddDate(0, 0, -20))

	d := decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/usage?dim=model&days=7"))
	if rows := d["rows"].([]any); len(rows) != 1 {
		t.Fatalf("days=7 usage rows = %d, want 1", len(rows))
	}
	d = decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/usage?dim=model&days=30"))
	if rows := d["rows"].([]any); len(rows) != 2 {
		t.Fatalf("days=30 usage rows = %d, want 2", len(rows))
	}
	if pts := d["days"].([]any); len(pts) != 30 {
		t.Fatalf("days=30 series = %d, want 30", len(pts))
	}
}

// TestMeUsageStatRange 用户面窗口:同样吃 days/from&to,且只算本人名下令牌(owner 锁死)。
func TestMeUsageStatRange(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	code, _ := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "cust", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user")
	users := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/users"))
	var custID int64
	for _, u := range users {
		if u["username"] == "cust" {
			custID = int64(u["id"].(float64))
		}
	}
	if custID == 0 {
		t.Fatalf("cust not found")
	}

	now := time.Now().UTC()
	seedLogOwner(t, st, "mine-new", now.Add(-1*time.Hour), custID)
	seedLogOwner(t, st, "mine-old", now.AddDate(0, 0, -20), custID)
	seedLogOwner(t, st, "other-new", now.Add(-2*time.Hour), 0) // 无归属(全局 key),不该进客户视角

	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	code, body := doJSON(t, cust, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "cust", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "cust login: "+string(body))

	d := decode[map[string]any](t, mustGet(t, cust, base+"/api/v1/me/usage?dim=model&days=7"))
	rows := d["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("me days=7 rows = %d, want 1(仅本人 + 窗口内)", len(rows))
	}
	if name := rows[0].(map[string]any)["name"].(string); name != "mine-new" {
		t.Fatalf("me days=7 row = %q, want mine-new", name)
	}

	d = decode[map[string]any](t, mustGet(t, cust, base+"/api/v1/me/usage?dim=model&days=30"))
	if rows := d["rows"].([]any); len(rows) != 2 {
		t.Fatalf("me days=30 rows = %d, want 2", len(rows))
	}
	// 自定义窗口同样生效。
	from := now.AddDate(0, 0, -21).Format("2006-01-02")
	to := now.AddDate(0, 0, -19).Format("2006-01-02")
	d = decode[map[string]any](t, mustGet(t, cust, base+"/api/v1/me/usage?dim=model&from="+from+"&to="+to))
	if rows := d["rows"].([]any); len(rows) != 1 {
		t.Fatalf("me custom rows = %d, want 1", len(rows))
	}
	// 参数校验与管理员面同一套。
	code, _ = doJSON(t, cust, http.MethodGet, base+"/api/v1/me/usage?dim=model&from=2026-01-01", nil)
	mustStatus(t, code, http.StatusBadRequest, "me/usage half range")
}

// seedLog 直接落一条成功日志(绕过转发链路,只求聚合有数据)。
func seedLog(t *testing.T, st *store.Store, model string, at time.Time) {
	t.Helper()
	seedLogOwner(t, st, model, at, 0)
}

func seedLogOwner(t *testing.T, st *store.Store, model string, at time.Time, ownerID int64) {
	t.Helper()
	if err := st.InsertLog(domain.LogRow{
		TS: at, Model: model, ChannelName: "ch", TokenName: "tk", OwnerID: ownerID,
		Status: 200, PromptTokens: 10, Completion: 5, CostUsd: 0.01, ChargeUsd: 0.02,
		FirstTokenMs: 120, TotalMs: 400, IP: "127.0.0.1",
	}); err != nil {
		t.Fatalf("seed log: %v", err)
	}
}
