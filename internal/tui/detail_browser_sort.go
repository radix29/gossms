package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// detail_browser_sort.go is the Object Explorer Details pane's column sort: a
// click on a list view's column header orders its rows by that column, a second
// click on the same header reverses it. The rows are sorted here, in the pane,
// never by the query: the loaders fill cells in place after the first paint
// (the Tables and Databases folders' sizes), and only the pane holds the
// row-to-object mapping the order has to carry along (rowObjs, which Delete
// indexes by grid row).

// detailSort is the order the user picked for the node on screen: a column, by
// name, and a direction. The zero value is the loader's own order.
//
// By name rather than index: a Refresh re-installs the node's columns from a
// new result, and the name is what still means the same column there.
type detailSort struct {
	col  string
	desc bool
}

// detailRows is a list view as its loader handed it over, in the loader's
// order: what every sort reorders. rows' inner []string are the loader's own,
// so a backfill writing cells in place reaches the sorted view as well.
type detailRows struct {
	cols []string
	rows [][]string
	objs []nodeData
}

// showRows installs a loader's rows, sorted by the order in force. Every path
// that puts a result on screen goes through it, so a Refresh or a progressive
// loader's first stage keeps the sort the user picked.
func (db *DetailBrowser) showRows(cols []string, rows [][]string, objs []nodeData) {
	db.base = detailRows{cols: cols, rows: rows, objs: objs}
	sorted, sortedObjs, perm := db.sortedRows()
	db.grid.SetData(cols, sorted)
	db.perm = perm
	db.setRowObjects(sorted, sortedObjs)
	db.grid.SetSortIndicator(db.sortColumn())
}

// clearRows forgets the list view on screen, for every path that replaces it
// with something no header click may reorder (an error, the loading state).
func (db *DetailBrowser) clearRows() {
	db.base, db.perm = detailRows{}, nil
}

// sortByColumn is the grid's OnHeaderClick: the clicked column ascending, or the
// order reversed when it is the column already sorted on. A Property/Value view
// is a record, not a list, and keeps the order its fields were given in.
func (db *DetailBrowser) sortByColumn(col int) {
	if col < 0 || col >= len(db.base.cols) || isPropertyValueColumns(db.base.cols) {
		return
	}
	name := db.base.cols[col]
	if db.sort.col == name {
		db.setSort(detailSort{col: name, desc: !db.sort.desc})
	} else {
		db.setSort(detailSort{col: name})
	}
}

// setSort makes s the order of the node on screen, remembered for it (see
// DetailBrowser.sorts), and applies it.
func (db *DetailBrowser) setSort(s detailSort) {
	db.sort = s
	if db.currentNode != nil {
		db.sorts[sortKeyOf(db.currentNode)] = s
	}
	db.resort()
}

// nodeSortKey names a node for DetailBrowser.sorts: the same server object or
// folder on the same connection, whichever *explorerNode stands for it now.
type nodeSortKey struct {
	conn *dbconn.ServerConn
	node nodeKey
}

// sortKeyOf is n's nodeSortKey; the zero key for nil, which nothing is
// stored under.
func sortKeyOf(n *explorerNode) nodeSortKey {
	if n == nil {
		return nodeSortKey{}
	}
	return nodeSortKey{conn: resolveConn(n), node: n.data.key()}
}

// sortMenuItems are the cell menu's keyboard route to the header click (the
// menu opens on the cursor's cell with Ctrl+Space): the cursor's column in
// either direction, the one it is already sorted in withheld.
func (db *DetailBrowser) sortMenuItems() []controls.MenuItem {
	_, col := db.grid.SelectedCell()
	if col < 0 || col >= len(db.base.cols) || isPropertyValueColumns(db.base.cols) {
		return nil
	}
	name := db.base.cols[col]
	item := func(label string, desc bool) controls.MenuItem {
		return controls.MenuItem{
			Label:   label,
			Enabled: func() bool { return db.sort != detailSort{col: name, desc: desc} },
			Action:  func() { db.setSort(detailSort{col: name, desc: desc}) },
		}
	}
	return []controls.MenuItem{item("Sort Ascending", false), item("Sort Descending", true)}
}

// resort re-applies the order in force to the rows on screen, keeping the
// cursor on the row it was on (not on its old index, which now holds another
// object), the scroll and the dragged column widths. Run after a header click,
// and after a progressive loader's last cells land: rows sorted while their
// figures still read "…" are otherwise left in the order the placeholders had.
func (db *DetailBrowser) resort() {
	if db.base.cols == nil {
		return
	}
	row, col := db.grid.SelectedCell()
	selected := -1
	if row >= 0 && row < len(db.perm) {
		selected = db.perm[row]
	}
	sorted, sortedObjs, perm := db.sortedRows()
	db.grid.SetDataPreservingView(db.base.cols, sorted)
	db.perm = perm
	db.setRowObjects(sorted, sortedObjs)
	db.grid.SetSortIndicator(db.sortColumn())
	if i := slices.Index(perm, selected); i >= 0 {
		db.grid.SetSelectedCell(i, col)
	}
}

// sortColumn is the index of the sorted column in the rows on screen and its
// direction, -1 when nothing is sorted or the view lacks the column.
func (db *DetailBrowser) sortColumn() (int, bool) {
	if db.sort.col == "" || isPropertyValueColumns(db.base.cols) {
		return -1, false
	}
	return slices.Index(db.base.cols, db.sort.col), db.sort.desc
}

// sortedRows is db.base in the order in force: the rows, the row objects
// permuted alongside them (nil when they do not line up with the rows, which
// setRowObjects refuses anyway), and perm, where perm[i] is the base index of
// the i-th row shown.
func (db *DetailBrowser) sortedRows() ([][]string, []nodeData, []int) {
	b := db.base
	perm := make([]int, len(b.rows))
	for i := range perm {
		perm[i] = i
	}
	if c, desc := db.sortColumn(); c >= 0 {
		keys := make([]detailSortKey, len(b.rows))
		for i, r := range b.rows {
			if c < len(r) {
				keys[i] = newDetailSortKey(r[c])
			} else {
				keys[i] = detailSortKey{blank: true}
			}
		}
		// Stable, so rows tied on the column keep the loader's order between them.
		slices.SortStableFunc(perm, func(i, j int) int {
			return compareDetailSortKeys(keys[i], keys[j], desc)
		})
	}
	rows := make([][]string, len(perm))
	for i, p := range perm {
		rows[i] = b.rows[p]
	}
	var objs []nodeData
	if len(b.objs) == len(b.rows) {
		objs = make([]nodeData, len(perm))
		for i, p := range perm {
			objs[i] = b.objs[p]
		}
	}
	return rows, objs, perm
}

// detailSortKey is one cell read for sorting: a number when the whole cell is
// one (thousands separators and a trailing unit allowed: "1,234", "12 MB",
// "45%"), its case-folded text otherwise, or blank for a cell that holds no
// value yet or none at all.
type detailSortKey struct {
	blank bool
	isNum bool
	num   float64
	text  string
}

// detailPlaceholders are the cells that stand for a missing value: a backfill
// not landed yet, one that failed, an empty cell or a dash.
var detailPlaceholders = map[string]bool{
	"": true, "…": true, "...": true, "N/A": true, "-": true, "—": true,
}

func newDetailSortKey(cell string) detailSortKey {
	s := strings.TrimSpace(cell)
	if detailPlaceholders[s] {
		return detailSortKey{blank: true}
	}
	if n, ok := parseDetailNumber(s); ok {
		return detailSortKey{isNum: true, num: n}
	}
	return detailSortKey{text: strings.ToLower(s)}
}

// parseDetailNumber reads a cell rendered from a number: formatMB's "12,345 MB",
// FormatThousands' "1,234", a percentage. Anything else (a name, a date, a
// version such as 16.0.4135.4) is not a number, so it sorts as text.
func parseDetailNumber(s string) (float64, bool) {
	if i := strings.LastIndexByte(s, ' '); i > 0 && isUnitSuffix(s[i+1:]) {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, "%")
	s = strings.ReplaceAll(s, ",", "")
	// ParseFloat also takes "Inf", "NaN" and hex floats, which here are names.
	if s == "" || !strings.ContainsAny(s[:1], "0123456789+-.") ||
		strings.ContainsAny(s, "xXpP_") {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil
}

// isUnitSuffix reports whether s reads as a short unit after a number (MB, KB,
// ms, rows).
func isUnitSuffix(s string) bool {
	if s == "" || len(s) > 4 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// compareDetailSortKeys orders a before b ascending, or the reverse for desc:
// numbers by value ahead of text, text case-insensitively. Blanks sort last
// either way, so reversing a half-loaded column doesn't bring every "…" to the
// top.
func compareDetailSortKeys(a, b detailSortKey, desc bool) int {
	if a.blank || b.blank {
		switch {
		case a.blank && b.blank:
			return 0
		case a.blank:
			return 1
		default:
			return -1
		}
	}
	var c int
	switch {
	case a.isNum && b.isNum:
		c = cmp.Compare(a.num, b.num)
	case a.isNum:
		c = -1
	case b.isNum:
		c = 1
	default:
		c = strings.Compare(a.text, b.text)
	}
	if desc {
		return -c
	}
	return c
}
