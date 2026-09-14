package pricing

import (
	"strings"

	"golang.org/x/net/html"
)

// table 展开后的二维网格。合并单元格(rowspan/colspan)已按占位填充,
// 故 grid[r][c] 恒为一个单元格文本(被合并区域为其宿主格的文本副本)。
type table struct {
	grid [][]string
}

// cols 网格列数(取各行最大)。
func (t table) cols() int {
	n := 0
	for _, r := range t.grid {
		if len(r) > n {
			n = len(r)
		}
	}
	return n
}

// parseTables 抽出文档里全部 <table> 并各自展开为网格。
func parseTables(doc *html.Node) ([]table, error) {
	var out []table
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			out = append(out, expandTable(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// sectionTable 一张 <table> 及其所处的最近一个 <h2> 小节标题。
type sectionTable struct {
	heading string
	table   table
}

// parseSectionTables 按文档顺序抽出全部 <table>,并记录每张表所属的最近 <h2> 小节标题。
//
// 为什么需要:阿里云百炼等页面既是厂商自营清单、又转售第三方模型,两者同页不同小节
// (「文本生成-千问」vs「文本生成-第三方模型」)。只按表头匹配会把转售的他厂模型
// 一并算成本厂商(github: glm-4.5 被记到通义千问名下)。带出小节标题即可按归属过滤。
func parseSectionTables(doc *html.Node) []sectionTable {
	var out []sectionTable
	heading := ""
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "h2" {
			heading = normalizeText(nodeText(n))
		}
		if n.Type == html.ElementNode && n.Data == "table" {
			out = append(out, sectionTable{heading: heading, table: expandTable(n)})
			return // 不再递归进表体
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// rawCell 物理单元格(含 span)。
type rawCell struct {
	text    string
	rowspan int
	colspan int
}

// expandTable 把一个 <table> 展开为矩形网格,处理 rowspan/colspan。
// 不区分 thead/tbody(按出现顺序);单元格文本为全部后代文本拼接。
// 被合并覆盖的列在后续行填入宿主格文本(便于按列取值)。
func expandTable(t *html.Node) table {
	var rows []*html.Node
	var findRows func(*html.Node)
	findRows = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			rows = append(rows, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findRows(c)
		}
	}
	findRows(t)

	raw := make([][]rawCell, 0, len(rows))
	for _, tr := range rows {
		var cs []rawCell
		for c := tr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
				continue
			}
			rs, cspan := intAttr(c, "rowspan"), intAttr(c, "colspan")
			cs = append(cs, rawCell{
				text:    normalizeText(nodeText(c)),
				rowspan: rs,
				colspan: cspan,
			})
		}
		raw = append(raw, cs)
	}

	// carry[col] = 上方 rowspan 落在本列的剩余行数与文本。
	type carry struct {
		text      string
		remaining int
	}
	var carries []carry
	grid := make([][]string, len(raw))
	for r, cs := range raw {
		row := []string{}
		col, ci := 0, 0
		for ci < len(cs) || (col < len(carries) && carries[col].remaining > 0) {
			if col < len(carries) && carries[col].remaining > 0 {
				row = append(row, carries[col].text)
				carries[col].remaining--
				col++
				continue
			}
			c := cs[ci]
			ci++
			for k := 0; k < c.colspan; k++ {
				for len(carries) <= col {
					carries = append(carries, carry{})
				}
				carries[col] = carry{text: c.text, remaining: c.rowspan - 1}
				row = append(row, c.text)
				col++
			}
		}
		grid[r] = row
	}
	return table{grid: grid}
}

// nodeText 递归取节点全部文本。
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// normalizeText 折叠空白并去掉 NBSP,便于文本比对。
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "​", "")
	return strings.Join(strings.Fields(s), " ")
}

// intAttr 读取 rowspan/colspan 数值属性,缺失或非法返回 1。
func intAttr(n *html.Node, key string) int {
	for _, a := range n.Attr {
		if a.Key == key {
			v := 0
			for _, ch := range a.Val {
				if ch < '0' || ch > '9' {
					break
				}
				v = v*10 + int(ch-'0')
			}
			if v > 0 {
				return v
			}
		}
	}
	return 1
}
