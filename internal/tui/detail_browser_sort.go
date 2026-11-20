package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	dbconn "github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
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
//
// The first rows a Refresh of the node on screen shows keep the cursor on the
// row it was on (see detailReload), and the scroll and dragged column widths
// with it, when the columns are the same ones; everything else starts at the
// top.
func (db *DetailBrowser) showRows(cols []string, rows [][]string, objs []nodeData) {
	reload := db.reload
	db.reload = detailReload{}
	db.base = detailRows{cols: cols, rows: rows, objs: objs}
	sorted, sortedObjs, perm := db.sortedRows()
	if reload.node == nil || reload.node != db.currentNode || !slices.Equal(reload.cols, cols) {
		db.grid.SetData(cols, sorted)
	} else {
		db.grid.SetDataPreservingView(cols, sorted)
		// On a miss (the row is gone) the cursor stays on its index.
		for i := range sorted {
			if detailRowIDOf(sorted, sortedObjs, i) == reload.id {
				db.grid.SetSelectedCell(i, reload.col)
				break
			}
		}
	}
	db.perm = perm
	db.setRowObjects(sorted, sortedObjs)
	db.grid.SetSortIndicator(db.sortColumn())
}

// detailReload is the cursor of a list view whose node is being refetched: the
// node, the columns shown, the cursor's column, and id, what its row stands
// for. Index alone is not enough: the reload may add or drop rows above it, or
// reorder a sorted view (its figures changed, or a progressive loader's read
// "…" until they land and sort last), so the cursor's index names another row
// by then.
type detailReload struct {
	node *explorerNode
	cols []string
	col  int
	id   detailRowID
}

// detailRowID is what a list-view row stands for across a reload: its object,
// for a view whose rows are objects, otherwise its first cell (a name, or a
// Property/Value view's property).
type detailRowID struct {
	obj    nodeKey
	hasObj bool
	text   string
}

// detailRowIDOf is row i's detailRowID, objs being parallel to rows or nil.
func detailRowIDOf(rows [][]string, objs []nodeData, i int) detailRowID {
	if len(objs) == len(rows) {
		return detailRowID{obj: objs[i].key(), hasObj: true}
	}
	if len(rows[i]) == 0 {
		return detailRowID{}
	}
	return detailRowID{text: rows[i][0]}
}

// cursorForReload captures where the cursor is in the list view on screen, for
// the refetch of the same node to restore; the zero value when nothing is
// shown or the cursor is on no row.
func (db *DetailBrowser) cursorForReload() detailReload {
	row, col := db.grid.SelectedCell()
	if db.base.cols == nil || row < 0 || row >= len(db.perm) {
		return detailReload{}
	}
	p := db.perm[row]
	return detailReload{
		node: db.currentNode,
		cols: db.base.cols,
		col:  col,
		id:   detailRowIDOf(db.base.rows, db.base.objs, p),
	}
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
	return controls.SortMenuItems(
		func(desc bool) bool { return db.sort == detailSort{col: name, desc: desc} },
		func(desc bool) { db.setSort(detailSort{col: name, desc: desc}) },
	)
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
	sorted, sortedObjs, perm := db.sortedRows()
	db.grid.SetSourceReordered(db.base.cols, controls.SliceRowSource(sorted), db.perm, perm)
	db.perm = perm
	db.setRowObjects(sorted, sortedObjs)
	db.grid.SetSortIndicator(db.sortColumn())
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
// formatBytes' "4.0 KB", FormatThousands' "1,234", a percentage. Anything else
// (a name, a date, a version such as 16.0.4135.4) is not a number, so it sorts
// as text.
//
// A byte unit scales the value to bytes: formatBytes picks its unit per cell,
// so the SQL Server Logs Size column mixes "512 B", "4.0 KB" and "1.2 MB", and
// comparing the bare mantissas put 4.0 KB before 512 B. Other units (ms, rows)
// are only stripped.
func parseDetailNumber(s string) (float64, bool) {
	scale := 1.0
	if i := strings.LastIndexByte(s, ' '); i > 0 && isUnitSuffix(s[i+1:]) {
		if f, ok := byteUnitScale[s[i+1:]]; ok {
			scale = f
		}
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
	return n * scale, err == nil
}

// byteUnitScale is the factor to bytes of each binary unit formatBytes writes.
var byteUnitScale = map[string]float64{
	"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30,
	"TB": 1 << 40, "PB": 1 << 50, "EB": 1 << 60,
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
