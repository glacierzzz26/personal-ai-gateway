// Command gwbackfill 一次性维护工具:用当前供给源单价回填历史 request_logs.cost。
//
// 背景:2026-09-11~13 期间供给源单价为空,全部请求的 cost 落库为 0。单价补齐后历史
// 花费仍是 0 —— 因为 cost 是请求当时的快照(见 internal/proxy/gateway.go 的 costUsd),
// 读取时只做 SUM(cost),不会按当前价重算。本工具按 (channel_id, model) 找到该请求
// 命中的当前 offer 四价,重算:
//
//	cost = in×prompt + out×completion + cacheRead×cache_read + cacheWrite×cache_write
//	       (单价为每百万 token;cacheWrite 价为 0 = 无依据 → 按 in 价计)
//
// 并同步 tokens.used_usd —— 令牌累计用量是运行期累加值,不补会让它与日志对不上。
//
// 安全约定:
//   - 默认 dry-run,只打印将发生的改动;必须显式 -apply 才写库;
//   - 写库前先把原值归档到 request_logs_cost_backfill / tokens_used_backfill;
//   - 找不到单价的日志行原样保留(宁缺勿假),最后单独列出;
//   - 计算公式与运行期 costUsd 完全一致(float64、不四舍五入)。
//
// 用法(在生产容器内以 root 运行,数据目录 /data 仅 root 可写):
//
//	docker cp gwbackfill ai-gateway-gateway-1:/tmp/gwbackfill
//	docker exec ai-gateway-gateway-1 /tmp/gwbackfill -db /data/gateway-v2.db            # dry-run
//	docker exec ai-gateway-gateway-1 /tmp/gwbackfill -db /data/gateway-v2.db -apply -backup /data/pre-backfill.db
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"personal-ai-gateway/internal/store"
)

// offerKey 日志 → 单价 的查表键。
type offerKey struct {
	channelID int64
	modelID   int64
}

// prices 一条 offer 的四价(每百万 token,当前计价币种)。cacheWrite=0 表示该 offer 没给
// 缓存写价 —— 与运行期 costUsd 一样按 input 价回落(见 costOf)。
type prices struct{ in, out, cacheRead, cacheWrite float64 }

func main() {
	dbPath := flag.String("db", "gateway-v2.db", "SQLite 库路径")
	apply := flag.Bool("apply", false, "真正写库(缺省仅 dry-run)")
	backup := flag.String("backup", "", "写库前用 VACUUM INTO 做一致性备份到该路径")
	flag.Parse()

	if err := run(*dbPath, *apply, *backup); err != nil {
		fmt.Fprintln(os.Stderr, "回填失败:", err)
		os.Exit(1)
	}
}

func run(dbPath string, apply bool, backup string) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	db := st.DB()

	if apply && backup != "" {
		if _, err := os.Stat(backup); err == nil {
			return fmt.Errorf("备份目标已存在,拒绝覆盖: %s", backup)
		}
		if _, err := db.Exec(`VACUUM INTO ?`, backup); err != nil {
			return fmt.Errorf("备份失败: %w", err)
		}
		fmt.Printf("已备份(一致性快照): %s\n\n", backup)
	}

	offers, err := loadOffers(db)
	if err != nil {
		return err
	}
	modelNames, err := loadModelNames(db)
	if err != nil {
		return err
	}

	rows, err := loadLogs(db)
	if err != nil {
		return err
	}

	// 逐行重算。找不到单价的保留原值。
	type plan struct {
		id       int64
		old, new float64
		model    string
		channel  int64
		tokenID  int64
	}
	var plans []plan
	var unresolved int
	byModel := map[string][2]float64{} // model → {old, new}
	byToken := map[int64]float64{}     // tokenID → 重算后累计
	for _, r := range rows {
		mid, ok := modelNames[r.model]
		if !ok {
			unresolved++
			byModel[r.model] = add2(byModel[r.model], r.cost, r.cost)
			byToken[r.tokenID] += r.cost
			continue
		}
		p, ok := offers[offerKey{r.channelID, mid}]
		if !ok {
			unresolved++
			byModel[r.model] = add2(byModel[r.model], r.cost, r.cost)
			byToken[r.tokenID] += r.cost
			continue
		}
		n := costOf(p, r.prompt, r.completion, r.cacheRead, r.cacheWrite)
		plans = append(plans, plan{r.id, r.cost, n, r.model, r.channelID, r.tokenID})
		byModel[r.model] = add2(byModel[r.model], r.cost, n)
		byToken[r.tokenID] += n
	}

	changed := make([]plan, 0, len(plans))
	var totOld, totNew float64
	for _, p := range plans {
		totOld += p.old
		totNew += p.new
		if p.new != p.old {
			changed = append(changed, p)
		}
	}

	// —— 报告 ——
	fmt.Printf("日志行 %d 条;可定价 %d 条;无单价保留原值 %d 条\n", len(rows), len(plans), unresolved)
	fmt.Printf("花费合计:%.6f → %.6f(净增 %.6f)\n\n", totOld, totNew, totNew-totOld)
	fmt.Println("按模型:")
	for _, name := range sortedKeys(byModel) {
		v := byModel[name]
		fmt.Printf("  %-40s %.6f → %.6f\n", name, v[0], v[1])
	}

	tokens, err := loadTokens(db)
	if err != nil {
		return err
	}
	fmt.Println("\n按令牌(used_usd):")
	for _, t := range tokens {
		fmt.Printf("  #%d %-16s %.6f → %.6f\n", t.id, t.name, t.used, byToken[t.id])
	}

	if !apply {
		fmt.Printf("\n[dry-run] 将更新 %d 行日志;加 -apply 执行。\n", len(changed))
		return nil
	}
	if len(changed) == 0 && tokensUnchanged(tokens, byToken) {
		fmt.Println("\n无改动,跳过写库。")
		return nil
	}

	at := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 归档表:原值留档,便于核对与回滚。
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS request_logs_cost_backfill (
		log_id     INTEGER PRIMARY KEY,
		old_cost   REAL    NOT NULL,
		new_cost   REAL    NOT NULL,
		model      TEXT    NOT NULL DEFAULT '',
		channel_id INTEGER NOT NULL DEFAULT 0,
		at         TEXT    NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS tokens_used_backfill (
		token_id INTEGER PRIMARY KEY,
		old_used REAL    NOT NULL,
		new_used REAL    NOT NULL,
		at       TEXT    NOT NULL
	)`); err != nil {
		return err
	}

	for _, p := range changed {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO request_logs_cost_backfill
			(log_id, old_cost, new_cost, model, channel_id, at) VALUES (?,?,?,?,?,?)`,
			p.id, p.old, p.new, p.model, p.channel, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE request_logs SET cost=? WHERE id=?`, p.new, p.id); err != nil {
			return err
		}
	}
	for _, t := range tokens {
		n := byToken[t.id]
		if n == t.used {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO tokens_used_backfill
			(token_id, old_used, new_used, at) VALUES (?,?,?,?)`, t.id, t.used, n, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tokens SET used_usd=? WHERE id=?`, n, t.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Printf("\n已写库:%d 行日志、%d 个令牌累计;原值归档于 request_logs_cost_backfill / tokens_used_backfill。\n",
		len(changed), countTokenChanges(tokens, byToken))
	return nil
}

// —— 数据加载 ——

type logRow struct {
	id                                        int64
	model                                     string
	channelID                                 int64
	tokenID                                   int64
	prompt, completion, cacheRead, cacheWrite int64
	cost                                      float64
}

func loadLogs(db *sql.DB) ([]logRow, error) {
	rows, err := db.Query(`SELECT id, model, channel_id, token_id,
		prompt_tokens, completion_tokens, cache_read_tokens, cache_write_tokens, cost FROM request_logs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []logRow
	for rows.Next() {
		var r logRow
		if err := rows.Scan(&r.id, &r.model, &r.channelID, &r.tokenID,
			&r.prompt, &r.completion, &r.cacheRead, &r.cacheWrite, &r.cost); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadModelNames 目录名 → 模型 id。请求体里的名字可能是模型名(name)或对外统一名
// (display_name),两者都登记;重名时以 name 为准(与运行期解析同序)。
func loadModelNames(db *sql.DB) (map[string]int64, error) {
	rows, err := db.Query(`SELECT id, name, display_name FROM models`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	type pair struct {
		id   int64
		name string
		disp string
	}
	var all []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.name, &p.disp); err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range all {
		if p.disp != "" {
			out[p.disp] = p.id
		}
	}
	for _, p := range all { // name 后写,优先级更高
		if p.name != "" {
			out[p.name] = p.id
		}
	}
	return out, nil
}

func loadOffers(db *sql.DB) (map[offerKey]prices, error) {
	rows, err := db.Query(`SELECT channel_id, model_id,
		input_price_usd, output_price_usd, cache_read_price_usd, cache_write_price_usd FROM model_offers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[offerKey]prices{}
	for rows.Next() {
		var k offerKey
		var p prices
		if err := rows.Scan(&k.channelID, &k.modelID, &p.in, &p.out, &p.cacheRead, &p.cacheWrite); err != nil {
			return nil, err
		}
		out[k] = p
	}
	return out, rows.Err()
}

type tokenRow struct {
	id   int64
	name string
	used float64
}

func loadTokens(db *sql.DB) ([]tokenRow, error) {
	rows, err := db.Query(`SELECT id, name, used_usd FROM tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tokenRow
	for rows.Next() {
		var t tokenRow
		if err := rows.Scan(&t.id, &t.name, &t.used); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// —— 小工具 ——

// costOf 与 internal/proxy/gateway.go 的 costUsd 同式:单价 × token / 1e6 四项相加。
// 缓存写价缺失(0)时按 input 价计 —— 与「cache_creation 折进 prompt」的旧账面逐位一致。
func costOf(p prices, prompt, completion, cacheRead, cacheWrite int64) float64 {
	pm := func(price float64, n int64) float64 { return price * float64(n) / 1e6 }
	cw := p.cacheWrite
	if cw == 0 {
		cw = p.in
	}
	return pm(p.in, prompt) + pm(p.out, completion) + pm(p.cacheRead, cacheRead) + pm(cw, cacheWrite)
}

func add2(v [2]float64, old, new float64) [2]float64 { return [2]float64{v[0] + old, v[1] + new} }

func sortedKeys(m map[string][2]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func tokensUnchanged(tokens []tokenRow, byToken map[int64]float64) bool {
	return countTokenChanges(tokens, byToken) == 0
}

func countTokenChanges(tokens []tokenRow, byToken map[int64]float64) int {
	n := 0
	for _, t := range tokens {
		if byToken[t.id] != t.used {
			n++
		}
	}
	return n
}
