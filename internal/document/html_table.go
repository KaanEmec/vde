package document

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Data tables. Under visible_text a table that holds data (a fare breakdown, an
// itinerary) is written as a Markdown table whose header row keeps each value tied to its
// column. Most email templates are built from layout tables; those stay flattened, cells
// separated by tabs, exactly as before. The classifier errs towards layout: a table it
// cannot read with confidence is flattened.

// tableVersion names the table output in service_version, with PreparationVersion.
const tableVersion = "tables/1"

// Bounds of a data table: a wider table, or a wider colspan, is a layout table.
const maxTableColumns = 50

// tableCell is a cell of a data table.
type tableCell struct {
	node *html.Node
	span int // colspan, at least 1
}

// dataTable is a table classified as holding data.
type dataTable struct {
	caption *html.Node // may be nil
	rows    [][]tableCell
	columns int // cells per row, colspan counted
}

// asDataTable classifies table and returns its rows when it holds data: it has no nested
// table, no rowspan and at most maxTableColumns columns, and either its first row has
// header cells (th), or it has at least two rows and two columns and every row has the
// same number of cells, colspan counted. Hidden rows are left out.
func asDataTable(table *html.Node) (*dataTable, bool) {
	t := &dataTable{}
	var (
		stack   []*html.Node
		hasTH   bool
		firstTH = -1 // index of the first row with a th cell
		equal   = true
	)
	push := func(n *html.Node) {
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	push(table)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type != html.ElementNode {
			continue
		}
		switch n.DataAtom {
		case atom.Table:
			return nil, false // a nested table: this one is layout
		case atom.Caption:
			if t.caption == nil && !hidden(n) {
				t.caption = n
			}
			continue
		case atom.Tr:
			if hidden(n) {
				continue
			}
			row, th, ok := tableRow(n)
			if !ok {
				return nil, false
			}
			cells := 0
			for _, c := range row {
				cells += c.span
			}
			if cells > maxTableColumns {
				return nil, false
			}
			if len(t.rows) > 0 && cells != t.columns {
				equal = false
			}
			if th {
				hasTH = true
				if firstTH < 0 {
					firstTH = len(t.rows)
				}
			}
			t.columns = max(t.columns, cells)
			t.rows = append(t.rows, row)
		default:
			if !hidden(n) {
				push(n)
			}
		}
	}
	switch {
	case len(t.rows) == 0 || t.columns == 0:
		return nil, false
	case hasTH:
		if firstTH != 0 { // the header row must come first
			return nil, false
		}
	case len(t.rows) < 2 || t.columns < 2 || !equal:
		return nil, false
	}
	return t, true
}

// tableRow returns the cells of a tr, and whether any is a th. It fails when a cell has
// a rowspan, a colspan beyond the column bound, or a table inside it.
func tableRow(tr *html.Node) (row []tableCell, th, ok bool) {
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.DataAtom != atom.Td && c.DataAtom != atom.Th) {
			continue
		}
		if r, err := strconv.Atoi(strings.TrimSpace(attr(c, "rowspan"))); err == nil && r > 1 {
			return nil, false, false
		}
		span := 1
		if v := strings.TrimSpace(attr(c, "colspan")); v != "" {
			if s, err := strconv.Atoi(v); err == nil && s > 1 {
				span = s
			}
		}
		if span > maxTableColumns || containsTable(c) {
			return nil, false, false
		}
		th = th || c.DataAtom == atom.Th
		row = append(row, tableCell{node: c, span: span})
	}
	return row, th, true
}

// containsTable reports whether a table lies under n.
func containsTable(n *html.Node) bool {
	stack := []*html.Node{n}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			if k.Type != html.ElementNode {
				continue
			}
			if k.DataAtom == atom.Table {
				return true
			}
			stack = append(stack, k)
		}
	}
	return false
}

// markdown writes t as a Markdown table: the first row as the header, a rule row, then
// the other rows, one line each. A cell's text is on one line with | escaped, and a
// colspan adds empty cells. A row that does not fit under the limit is not written at
// all: the output ends at a row boundary with the truncation marker.
func (w *textWriter) markdown(t *dataTable) {
	if t.caption != nil {
		if c := cellText(t.caption, w.room()); c != "" {
			w.block(1)
			w.write(c)
		}
	}
	for i, row := range t.rows {
		if w.full {
			return
		}
		cells := make([]string, 0, t.columns)
		for _, c := range row {
			cells = append(cells, cellText(c.node, w.room()))
			for range c.span - 1 {
				cells = append(cells, "")
			}
		}
		for len(cells) < t.columns {
			cells = append(cells, "")
		}
		line := markdownRow(cells)
		if i == 0 {
			rule := make([]string, t.columns)
			for j := range rule {
				rule[j] = "---"
			}
			line += "\n" + markdownRow(rule)
		}
		w.block(1)
		w.whole = true
		w.write(line)
		w.whole = false
	}
}

func markdownRow(cells []string) string {
	return "| " + strings.Join(cells, " | ") + " |"
}

// cellText is the visible text under a cell on one line, | escaped, at most limit bytes
// (a cell cut by the limit ends with the truncation marker, and its row is then dropped).
func cellText(n *html.Node, limit int) string {
	if limit <= 0 {
		return ""
	}
	text := strings.Join(strings.Fields(renderVisible(n, limit, nil, false)), " ")
	return strings.ReplaceAll(text, "|", `\|`)
}

// room is how many bytes the output may still take.
func (w *textWriter) room() int { return max(w.max-w.b.Len(), 0) }
