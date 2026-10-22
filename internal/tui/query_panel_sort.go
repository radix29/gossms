package tui

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// query_panel_sort.go is the results grid's column sort, the Object Explorer
// Details pane's (detail_browser_sort.go) for a query's result sets: a header
// click orders the set on screen by that column, a second click on the same
// header reverses it, and the cell menu offers the same as Sort Ascending / Sort
// Descending. The rows are reordered for display only: the set keeps the order
// the server returned it in, which Results to Text and a re-run still show.
//
// Unlike the Details pane's cells, a result set's carry their declared type, so a
// column is compared by it: numeric types by value, NULL last in either
// direction (as the Details pane's blanks, not as ORDER BY), everything
// else as case-insensitive text (the default collations' order; the formats the
// date/time types render in sort as text).

// resultSort is one result set's order: the column, its direction, and perm,
// where perm[i] is the set's index of the i-th row shown.
type resultSort struct {
	col  int
	desc bool
	perm []int
}

// resultSorts is the sort of each set of one result, by tab index. Kept with the
// result it was made for, so a new run (or a plan replacing the result) drops
// every sort without each of those paths having to.
type resultSorts struct {
	result *query.Result
	byTab  map[int]resultSort
}

// sortedResultSet shows a result set in perm's order. It is a controls.NullSource
// like the set itself, so the reordered NULLs still draw dimmed.
type sortedResultSet struct {
	set  query.ResultSet
	perm []int
}

func (s sortedResultSet) Len() int                 { return len(s.perm) }
func (s sortedResultSet) Row(i int) []string       { return s.set.Rows[s.perm[i]] }
func (s sortedResultSet) IsNull(row, col int) bool { return s.set.IsNull(s.perm[row], col) }

// activeSort is the active tab's sort, ok false when it has none.
func (p *QueryPanel) activeSort() (resultSort, bool) {
	if p.sorts.result != p.result || p.result == nil {
		return resultSort{}, false
	}
	s, ok := p.sorts.byTab[p.activeTab]
	return s, ok
}

// showResultSet puts set, the active tab's, into the results grid in the order
// picked for it.
func (p *QueryPanel) showResultSet(set query.ResultSet) {
	s, ok := p.activeSort()
	if !ok {
		// The set itself, not its Rows: it is the grid's NullSource.
		p.results.SetSource(set.Columns, set)
		return
	}
	p.results.SetSource(set.Columns, sortedResultSet{set: set, perm: s.perm})
	p.results.SetSortIndicator(s.col, s.desc)
}

// sortResultsByColumn is the results grid's OnHeaderClick: the clicked column
// ascending, or the order reversed when it is the column already sorted on (or
// being sorted on, so a second click while a large set sorts still reverses).
func (p *QueryPanel) sortResultsByColumn(col int) {
	cur, ok := p.activeSort()
	if k := p.sortRunKey; p.sorting() {
		cur, ok = resultSort{col: k.col, desc: k.desc}, true
	}
	p.setResultSort(col, ok && cur.col == col && !cur.desc)
}

// sortSyncRows is the largest set sortResultSet orders on the UI goroutine. A
// million rows took 1.1 s by an int column and 1.8 s by a text one, with input
// frozen; below this it is tens of milliseconds, and a "Sorting..." flash would
// cost more than it saves.
const sortSyncRows = 100_000

// sortRunKey is what a sort off the UI goroutine is producing: the order of one
// tab of one result.
type sortRunKey struct {
	result   *query.Result
	tab, col int
	desc     bool
}

// sorting reports whether a sort of the active tab is running off the UI
// goroutine right now.
func (p *QueryPanel) sorting() bool {
	k := p.sortRunKey
	return !p.sortRun.Idle() && k.result == p.result && k.tab == p.activeTab
}

// setResultSort orders the active set by col, remembered for its tab: in place
// when small, otherwise off the UI goroutine, applied when it lands if the result
// is still the one on screen. A newer sort supersedes one in flight. Only a grid
// tab in Results to Grid is sortable: the others have no header to click.
func (p *QueryPanel) setResultSort(col int, desc bool) {
	set, ok := p.activeResultSet()
	if !ok || p.resultsMode == ResultsModeText || col < 0 || col >= len(set.Columns) {
		return
	}
	key := sortRunKey{result: p.result, tab: p.activeTab, col: col, desc: desc}
	if len(set.Rows) <= sortSyncRows {
		p.sortRun.Abandon()
		perm, _ := sortResultSet(context.Background(), set, col, desc)
		p.applyResultSort(key, perm)
		return
	}
	// Rooted at Background for the reason showResultsText's is: the rows are in
	// memory, and a disconnect leaves them worth sorting.
	ctx, token := p.sortRun.Begin(context.Background())
	p.sortRunKey = key
	repair := func() { p.sortRun.Done(token) }
	p.app.safegoRepair("sorting results", repair, func() {
		perm, err := sortResultSet(ctx, set, col, desc)
		p.app.postAndWake(func() {
			if !p.sortRun.Done(token) || err != nil || p.result != key.result {
				return
			}
			p.applyResultSort(key, perm)
		})
	})
}

// applyResultSort remembers perm as key's tab's order and, when that tab is the
// one on screen in Results to Grid, shows it, keeping the cursor on the row it
// was on (not on its old index, which now holds another row), the scroll and
// the dragged column widths.
func (p *QueryPanel) applyResultSort(key sortRunKey, perm []int) {
	shown := key.tab == p.activeTab && p.resultsMode != ResultsModeText
	selected, cursorCol := -1, 0
	if shown {
		row, col := p.results.SelectedCell()
		selected, cursorCol = row, col
		if prev, ok := p.activeSort(); ok && row >= 0 && row < len(prev.perm) {
			selected = prev.perm[row]
		}
	}

	if p.sorts.result != key.result {
		p.sorts = resultSorts{result: key.result, byTab: make(map[int]resultSort)}
	}
	p.sorts.byTab[key.tab] = resultSort{col: key.col, desc: key.desc, perm: perm}
	if !shown {
		return
	}
	set := key.result.Sets[key.tab]
	p.results.SetSourcePreservingView(set.Columns, sortedResultSet{set: set, perm: perm})
	p.results.SetSortIndicator(key.col, key.desc)
	if i := slices.Index(perm, selected); i >= 0 {
		p.results.SetSelectedCell(i, cursorCol)
	}
}

// resultSortMenuItems are the cell menu's keyboard route to the header click
// (the menu opens on the cursor's cell with Ctrl+Space): the cursor's column in
// either direction, the one it is already sorted in withheld.
func (p *QueryPanel) resultSortMenuItems() []controls.MenuItem {
	set, ok := p.activeResultSet()
	_, col := p.results.SelectedCell()
	if !ok || col < 0 || col >= len(set.Columns) {
		return nil
	}
	item := func(label string, desc bool) controls.MenuItem {
		return controls.MenuItem{
			Label: label,
			Enabled: func() bool {
				s, ok := p.activeSort()
				return !ok || s.col != col || s.desc != desc
			},
			Action: func() { p.setResultSort(col, desc) },
		}
	}
	return []controls.MenuItem{item("Sort Ascending", false), item("Sort Descending", true)}
}

// resultSortKind is how a column's cells compare, from its declared type.
type resultSortKind int

const (
	sortAsText resultSortKind = iota
	sortAsInt
	sortAsFloat
)

// resultSortKindOf reads a declared type as columnTypeName renders it
// ("decimal(18,2)", "bigint") into how its cells compare. sql_variant and the
// rest compare as text.
func resultSortKindOf(typ string) resultSortKind {
	name, _, _ := strings.Cut(typ, "(")
	switch name {
	case "bigint", "int", "smallint", "tinyint", "bit":
		return sortAsInt
	case "decimal", "numeric", "money", "smallmoney", "float", "real":
		return sortAsFloat
	}
	return sortAsText
}

// resultSortKey is one cell read for sorting, and row the set's index of its
// row. A cell of a numeric column that does not parse (none should) falls back
// to text, after every number.
type resultSortKey struct {
	null  bool
	isNum bool
	row   int
	i     int64
	f     float64
	text  string
}

// sortResultSet returns the permutation that orders set by column col, stable so
// rows tied on it keep the server's order between them. ctx is checked while the
// keys are read and once the sort is done; the sort itself runs to the end.
//
// The keys are sorted themselves, each carrying its row, rather than a
// permutation indexing into them: a million-row set sorted 2.3 s the indirect
// way, the random reads into keys missing the cache on nearly every compare.
func sortResultSet(ctx context.Context, set query.ResultSet, col int, desc bool) ([]int, error) {
	kind := sortAsText
	if col < len(set.ColumnTypes) {
		kind = resultSortKindOf(set.ColumnTypes[col])
	}
	keys := make([]resultSortKey, len(set.Rows))
	for i, r := range set.Rows {
		if i%textCancelEvery == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if set.IsNull(i, col) || col >= len(r) {
			keys[i] = resultSortKey{null: true, row: i}
			continue
		}
		keys[i] = newResultSortKey(r[col], kind)
		keys[i].row = i
	}
	// Unstable, the row breaking ties: stable at a fraction of SortStableFunc's cost.
	slices.SortFunc(keys, func(a, b resultSortKey) int {
		if c := compareResultSortKeys(a, b, desc); c != 0 {
			return c
		}
		return cmp.Compare(a.row, b.row)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	perm := make([]int, len(keys))
	for i, k := range keys {
		perm[i] = k.row
	}
	return perm, nil
}

func newResultSortKey(cell string, kind resultSortKind) resultSortKey {
	switch kind {
	case sortAsInt:
		if n, err := strconv.ParseInt(cell, 10, 64); err == nil {
			return resultSortKey{isNum: true, i: n, f: float64(n)}
		}
	case sortAsFloat:
		if n, err := strconv.ParseFloat(cell, 64); err == nil {
			return resultSortKey{isNum: true, f: n}
		}
	}
	// ToLower returns an already-lower ASCII cell as is, without allocating.
	return resultSortKey{text: strings.ToLower(cell)}
}

// compareResultSortKeys orders a before b ascending, or the reverse for desc:
// numbers by value (integers exactly: a bigint past 2^53 is not exact as a
// float64), then text. NULL sorts last either way, as the Details pane's blanks
// do (compareDetailSortKeys), so reversing a column doesn't bring every NULL to
// the top.
func compareResultSortKeys(a, b resultSortKey, desc bool) int {
	if a.null || b.null {
		return cmp.Compare(boolRank(a.null), boolRank(b.null))
	}
	c := compareResultValues(a, b)
	if desc {
		return -c
	}
	return c
}

// compareResultValues orders two non-NULL keys ascending.
func compareResultValues(a, b resultSortKey) int {
	switch {
	case a.isNum && b.isNum:
		// i is 0 for both outside an integer column.
		if c := cmp.Compare(a.f, b.f); c != 0 {
			return c
		}
		return cmp.Compare(a.i, b.i)
	case a.isNum != b.isNum:
		return cmp.Compare(boolRank(!a.isNum), boolRank(!b.isNum))
	}
	return strings.Compare(a.text, b.text)
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}
