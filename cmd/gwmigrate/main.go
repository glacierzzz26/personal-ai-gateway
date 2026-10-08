// gwmigrate 把旧的 SQLite 库(gateway-v2.db)整库搬运到 PostgreSQL。
//
// 一次性工具:绿地切换时,把源库逐表原样搬进目标 PG(逐列拷贝,不做任何值变换),
// 搬运完逐表推进 identity 序列,并复核逐表行数。源 SQLite 文件只读打开、不修改;
// 目标 PG 通过 store.Open 建出基线(空表 + schema_migrations=14)。
//
// 为什么逐列原样拷贝而不是 pgloader:类型强转/identity/setval 不可控,而本库
// 刻意保持「时间戳 text、布尔 bigint 0/1、JSON text、金额 double precision」,
// 源值语义与目标列一一对应,直搬即正确(见 internal/store/schema.go 头注)。
//
// 用法:
//
//	go run ./cmd/gwmigrate \
//	    --from /path/to/gateway-v2.db \
//	    --to   'postgres://gw:gw@127.0.0.1:5432/gateway?sslmode=disable'
//
// 目标非空则拒绝(防重复导入);--force 跳过该检查(仅在确认要覆盖时用)。
// 导入后自动校验逐表 COUNT(*) 一致,不一致即报错退出(非零)。
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"personal-ai-gateway/internal/store"

	_ "modernc.org/sqlite"
)

// tables 搬运顺序:外键依赖序(被引用者在前)。hasID 标记是否有 identity 主键
// (settings / announcement_dismissals 无 id,不需 setval)。
var tables = []struct {
	name  string
	hasID bool
}{
	{"admins", true},
	{"channels", true},
	{"models", true},
	{"model_offers", true},
	{"rules", true},
	{"tokens", true},
	{"request_logs", true},
	{"settings", false},
	{"official_prices", true},
	{"channel_vendor_costs", true},
	{"announcements", true},
	{"announcement_dismissals", false},
	{"balance_logs", true},
}

func main() {
	from := flag.String("from", "", "源 SQLite 库路径(只读)")
	to := flag.String("to", "", "目标 PostgreSQL DSN")
	force := flag.Bool("force", false, "目标非空时仍继续(默认拒绝,防重复导入)")
	flag.Parse()
	if *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "用法: gwmigrate --from <sqlite> --to <pg-dsn>")
		os.Exit(2)
	}
	if err := run(*from, *to, *force); err != nil {
		fmt.Fprintln(os.Stderr, "gwmigrate 失败:", err)
		os.Exit(1)
	}
}

func run(from, to string, force bool) error {
	ctx := context.Background()

	if _, err := os.Stat(from); err != nil {
		return fmt.Errorf("源库不可读: %w", err)
	}
	src, err := sql.Open("sqlite", "file:"+from+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return fmt.Errorf("打开源库: %w", err)
	}
	defer src.Close()
	if err := src.Ping(); err != nil {
		return fmt.Errorf("连源库: %w", err)
	}

	// 建目标基线(空表 + schema_migrations=14);幂等。
	st, err := store.Open(to)
	if err != nil {
		return fmt.Errorf("建目标基线: %w", err)
	}
	st.Close()

	conn, err := pgx.Connect(ctx, to)
	if err != nil {
		return fmt.Errorf("连目标库: %w", err)
	}
	defer conn.Close(ctx)

	if !force {
		for _, t := range tables {
			var n int64
			if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM `+t.name).Scan(&n); err != nil {
				return fmt.Errorf("统计目标 %s: %w", t.name, err)
			}
			if n > 0 {
				return fmt.Errorf("目标表 %s 已有 %d 行;如需覆盖请加 --force", t.name, n)
			}
		}
	}

	for _, t := range tables {
		n, err := copyTable(ctx, src, conn, t.name)
		if err != nil {
			return fmt.Errorf("搬运 %s: %w", t.name, err)
		}
		if t.hasID {
			if err := setval(ctx, conn, t.name); err != nil {
				return fmt.Errorf("推进 %s 序列: %w", t.name, err)
			}
		}
		fmt.Printf("  %-24s %d 行\n", t.name, n)
	}

	fmt.Println("校验:逐表 COUNT(*) 对比(SQLite vs PG)")
	if err := verify(ctx, src, conn); err != nil {
		return err
	}
	fmt.Println("完成。")
	return nil
}

// copyTable 把 src 的某表逐列原样 COPY 进 PG;返回搬运行数。
// 列清单取自目标 PG 的 information_schema(按序号),据此构造源端 SELECT ——
// 源库若停在 m0014,列与基线一致;缺列会在源端 SELECT 处立即报错(不静默丢列)。
func copyTable(ctx context.Context, src *sql.DB, conn *pgx.Conn, table string) (int64, error) {
	cols, err := pgColumns(ctx, conn, table)
	if err != nil {
		return 0, err
	}
	if len(cols) == 0 {
		return 0, fmt.Errorf("目标表 %s 无列(基线未建?)", table)
	}

	sel := "SELECT " + joinIdent(cols) + " FROM " + table
	rows, err := src.Query(sel)
	if err != nil {
		return 0, fmt.Errorf("读源端: %w", err)
	}
	defer rows.Close()

	source := &copySource{rows: rows, vals: make([]any, len(cols))}
	for i := range source.vals {
		source.dest = append(source.dest, &source.vals[i])
	}

	n, err := conn.CopyFrom(ctx, pgx.Identifier{table}, cols, source)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// copySource 把 database/sql 的行迭代适配成 pgx.CopyFromSource。
type copySource struct {
	rows *sql.Rows
	vals []any
	dest []any
	err  error
}

func (s *copySource) Next() bool {
	if !s.rows.Next() {
		return false
	}
	// 复用同一 vals 底层,pgx 在 Values() 返回后立即编码,故无需再分配。
	if err := s.rows.Scan(s.dest...); err != nil {
		s.err = err
		return false
	}
	return true
}

func (s *copySource) Values() ([]any, error) { return s.vals, nil }
func (s *copySource) Err() error             { return s.err }

// setval 把某表 id 序列推到 MAX(id):下一 nextval 得 max+1(空表则从 1 起)。
// 漏做会让导入后的首次插入撞主键 —— 这是整库搬运最易踩的坑。
func setval(ctx context.Context, conn *pgx.Conn, table string) error {
	_, err := conn.Exec(ctx, fmt.Sprintf(
		`SELECT setval(pg_get_serial_sequence('%[1]s','id'),
			GREATEST((SELECT COALESCE(MAX(id),0) FROM %[1]s),1),
			(SELECT COUNT(*) > 0 FROM %[1]s))`, table))
	return err
}

// verify 逐表比对行数(主闸);任一不等即报错。
func verify(ctx context.Context, src *sql.DB, conn *pgx.Conn) error {
	bad := 0
	for _, t := range tables {
		var a, b int64
		if err := src.QueryRow(`SELECT COUNT(*) FROM ` + t.name).Scan(&a); err != nil {
			return fmt.Errorf("统计源 %s: %w", t.name, err)
		}
		if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM `+t.name).Scan(&b); err != nil {
			return fmt.Errorf("统计目标 %s: %w", t.name, err)
		}
		mark := "ok"
		if a != b {
			mark = "不一致!"
			bad++
		}
		fmt.Printf("  %-24s sqlite=%-8d pg=%-8d %s\n", t.name, a, b, mark)
	}
	if bad > 0 {
		return fmt.Errorf("%d 张表行数不一致", bad)
	}
	return nil
}

// pgColumns 取目标表列名(按 ordinal_position)。
func pgColumns(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// joinIdent 用逗号连接标识符(列名由 information_schema 给出,非用户输入)。
func joinIdent(cols []string) string {
	s := ""
	for i, c := range cols {
		if i > 0 {
			s += ","
		}
		s += c
	}
	return s
}
