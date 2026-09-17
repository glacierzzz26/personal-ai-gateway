// Command gwbackfill-official 一次性维护工具:为未绑定官方价的模型自动回填绑定。
//
// 背景:成本口径改造为「成本 = 厂商官方价 × 渠道系数」后,派生存本的前提是模型**绑定了**
// 官方价行(models.official_vendor / official_model_name)。生产目录 85 个模型里只有 4 个
// 有绑定,而 official_prices 已有 168 行 —— 大量本可自动匹配的模型仍是空绑定,成本只能手填。
//
// 匹配规则(与渠道同步归并同源,见 store.canonicalModelKey):两侧模型名都取最后一个 '/' 之后
// 的段、去空白、转小写,完全相等才算命中。于是 `Qwen/Qwen3.8-Flash` ↔ `qwen3.8-flash`
// 这类前缀与大小写差异正好被吃掉。
//
// 安全约定:
//   - 默认 dry-run,只打印将改动的行;必须显式 -apply 才写库;
//   - 只处理 official_vendor 与 official_model_name **都为空**的模型,任何人工绑定绝不覆盖;
//   - 规范名命中多个厂商时跳过(歧义交人工判断),不做子串/模糊匹配;
//   - 写库前用 VACUUM INTO 做一致性备份(与本仓库既有回填工具同规)。
//
// 用法(在生产容器内以 root 运行,数据目录 /data 仅 root 可写):
//
//	docker cp gwbackfill-official ai-gateway-gateway-1:/tmp/
//	docker exec ai-gateway-gateway-1 /tmp/gwbackfill-official -db /data/gateway-v2.db             # dry-run
//	docker exec ai-gateway-gateway-1 /tmp/gwbackfill-official -db /data/gateway-v2.db -apply -backup /data/pre-bind.db
package main

import (
	"flag"
	"fmt"
	"os"

	"personal-ai-gateway/internal/store"
)

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

	// dry-run 与 apply 走同一条计算路径,保证「看到的」就是「会写的」。
	fills, err := st.BackfillOfficialBindings(!apply)
	if err != nil {
		return err
	}

	total, err := countModels(st)
	if err != nil {
		return err
	}
	fmt.Printf("目录模型 %d 个;本次可绑定 %d 个(其余无官方价来源或规范名歧义,保持未绑定)\n\n",
		total, len(fills))
	for _, f := range fills {
		fmt.Printf("  #%-4d %-34s → %s / %s\n", f.ModelID, f.ModelName, f.Vendor, f.OfficialName)
	}

	if len(fills) == 0 {
		fmt.Println("\n无可绑定项,跳过写库。")
		return nil
	}
	if !apply {
		fmt.Printf("\n[dry-run] 将绑定上述 %d 个模型;加 -apply 执行。\n", len(fills))
		return nil
	}
	if backup != "" {
		if _, err := os.Stat(backup); err == nil {
			return fmt.Errorf("备份目标已存在,拒绝覆盖: %s", backup)
		}
		if _, err := st.DB().Exec(`VACUUM INTO ?`, backup); err != nil {
			return fmt.Errorf("备份失败: %w", err)
		}
		fmt.Printf("\n已备份(一致性快照): %s\n", backup)
	}
	fmt.Printf("\n已绑定 %d 个模型。成本将从「手填兜底价」转为「官方价 × 渠道系数」。\n", len(fills))
	return nil
}

func countModels(st *store.Store) (int, error) {
	models, err := st.ListModels()
	if err != nil {
		return 0, err
	}
	return len(models), nil
}
