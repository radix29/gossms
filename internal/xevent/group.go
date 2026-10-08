package xevent

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// Grouping and Aggregation: SSMS's viewer toolbar pair. Events are grouped by
// one or more columns, nested in the order given, and each group can carry
// aggregates — COUNT, SUM, AVG, MIN, MAX of a column — over the events under
// it. Evaluated over whatever event list the caller hands in (the viewer's
// filtered events), so a filter narrows the groups too.

// AggFunc is an aggregate function.
type AggFunc int

const (
	AggCount AggFunc = iota
	AggSum
	AggAvg
	AggMin
	AggMax
)

// AggFuncs is every function, in the order a menu lists them.
var AggFuncs = []AggFunc{AggCount, AggSum, AggAvg, AggMin, AggMax}

func (f AggFunc) String() string {
	switch f {
	case AggCount:
		return "COUNT"
	case AggSum:
		return "SUM"
	case AggAvg:
		return "AVG"
	case AggMin:
		return "MIN"
	case AggMax:
		return "MAX"
	}
	return "?"
}

// ParseAggFunc is String's inverse, case-insensitive.
func ParseAggFunc(s string) (AggFunc, bool) {
	for _, f := range AggFuncs {
		if strings.EqualFold(f.String(), s) {
			return f, true
		}
	}
	return 0, false
}

// Aggregate is one function over one column.
type Aggregate struct {
	Func   AggFunc
	Column Column
}

// Group is one group of events: the events sharing Value in Column, under the
// groups of the columns before it.
type Group struct {
	Column Column
	// Value is the group's value as shown (a map field's text) — the first
	// spelling seen, since the group holds every value the filter's = finds
	// equal to it (equalityKey). Null is set for the events without the
	// column, whose Value is "".
	Value string
	Null  bool
	// Level is the depth, 0 for the outermost column.
	Level int
	// Key names the group across regroupings — its column and value and every
	// enclosing group's — for the caller's expanded set.
	Key string
	// Parent is the enclosing group, nil at level 0.
	Parent *Group

	// Children are the next column's groups; Events the events of an
	// innermost group, in the order handed in. Exactly one is set.
	Children []*Group
	Events   []*Event
	// Count is how many events the group holds, at every depth.
	Count int

	// Aggregates are the results, one per Aggregate asked for, in order.
	Aggregates []string
}

// Terms is the filter matching exactly g's events: one term per group from
// the outermost down to g — `column = value`, or `column is null` for the
// events without it. all is every column the events carry: a field that
// shares its name with an action is named field:x, since the bare name
// would reach the action on an event lacking the field, and so match (or
// fail to be NULL) where grouping didn't.
func (g *Group) Terms(all []Column) []Term {
	var out []Term
	for ; g != nil; g = g.Parent {
		name := g.Column.FilterName()
		if g.Column.Kind == ColField && name == g.Column.Name &&
			slices.Contains(all, Column{Kind: ColAction, Name: g.Column.Name}) {
			name = g.Column.Key()
		}
		t := Term{Column: name, Op: OpEq, Value: g.Value}
		if g.Null {
			t = Term{Column: name, Op: OpIsNull}
		}
		out = append(out, t)
	}
	slices.Reverse(out)
	return out
}

// GroupEvents groups events by the columns in by, nested in that order, and
// evaluates aggs over each group. Groups are ordered by value — numerically
// where both values are numbers, else as case-insensitive text — with the
// events lacking the column last. An empty by returns nil.
func GroupEvents(events []*Event, by []Column, aggs []Aggregate) []*Group {
	if len(by) == 0 {
		return nil
	}
	return groupLevel(events, by, aggs, 0, nil)
}

func groupLevel(events []*Event, by []Column, aggs []Aggregate, level int, parent *Group) []*Group {
	c := by[level]
	index := map[groupID]*Group{}
	var groups []*Group
	for _, e := range events {
		v, ok := e.Value(c)
		id := groupID{null: !ok}
		if ok {
			id.value = equalityKey(v.Display())
		}
		g := index[id]
		if g == nil {
			g = &Group{Column: c, Null: id.null, Level: level, Parent: parent}
			if ok {
				g.Value = v.Display()
			}
			if parent != nil {
				g.Key = parent.Key
			}
			g.Key += "\x00" + c.Key() + "\x01" + id.value
			if id.null {
				g.Key += "\x02"
			}
			index[id] = g
			groups = append(groups, g)
		}
		g.Events = append(g.Events, e)
	}
	slices.SortStableFunc(groups, func(a, b *Group) int {
		if a.Null != b.Null {
			if a.Null {
				return 1
			}
			return -1
		}
		return compareValues(a.Value, b.Value)
	})
	for _, g := range groups {
		g.Count = len(g.Events)
		g.Aggregates = make([]string, len(aggs))
		for i, a := range aggs {
			g.Aggregates[i] = evaluate(g.Events, a)
		}
		if level+1 < len(by) {
			g.Children = groupLevel(g.Events, by, aggs, level+1, g)
			g.Events = nil
		}
	}
	return groups
}

type groupID struct {
	value string // equalityKey of the value
	null  bool
}

// equalityKey is the one key per class of values the filter's = treats as
// equal, so a group and its Terms agree: a number (1, 1.0, " 1") keys by its
// value, anything else by its lowered text (App, app). Grouping by the exact
// text made App and app two groups whose `= App` filters each matched both.
// The group shows the first value seen; its Key uses this, so a group keeps
// its expanded state whichever spelling arrived first.
func equalityKey(s string) string {
	if f, ok := parseNumber(s); ok {
		if f == 0 {
			f = 0 // -0 = 0, as compare finds
		}
		return "n" + strconv.FormatFloat(f, 'g', -1, 64)
	}
	return "t" + strings.ToLower(s)
}

// compareValues orders two values numerically when both are numbers, else as
// case-insensitive text.
func compareValues(a, b string) int {
	if na, err := strconv.ParseFloat(strings.TrimSpace(a), 64); err == nil {
		if nb, err := strconv.ParseFloat(strings.TrimSpace(b), 64); err == nil {
			return cmp.Compare(na, nb)
		}
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// evaluate computes a over events. COUNT counts the events carrying the
// column; SUM and AVG add the values that are numbers and are empty when none
// is; MIN and MAX compare two numbers as numbers, anything else as text.
func evaluate(events []*Event, a Aggregate) string {
	var vals []string
	for _, e := range events {
		if v, ok := e.Value(a.Column); ok {
			vals = append(vals, v.Display())
		}
	}
	switch a.Func {
	case AggCount:
		return strconv.Itoa(len(vals))
	case AggSum, AggAvg:
		sum, n := 0.0, 0
		for _, s := range vals {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				sum += f
				n++
			}
		}
		if n == 0 {
			return ""
		}
		if a.Func == AggAvg {
			return formatNumber(sum / float64(n))
		}
		return formatNumber(sum)
	case AggMin, AggMax:
		if len(vals) == 0 {
			return ""
		}
		best := vals[0]
		for _, s := range vals[1:] {
			c := compareValues(s, best)
			if (a.Func == AggMin && c < 0) || (a.Func == AggMax && c > 0) {
				best = s
			}
		}
		return best
	}
	return ""
}

// formatNumber renders a sum or an average: whole numbers without a decimal
// point, others to at most three places.
func formatNumber(f float64) string {
	if f == float64(int64(f)) && f < 1e18 && f > -1e18 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Label names a for a menu or a group row: SUM(duration), the column headed
// as Header heads it among all.
func (a Aggregate) Label(all []Column) string {
	return a.Func.String() + "(" + Header(a.Column, all) + ")"
}

// Key names a in saved settings: the function, a colon, the column's Key.
func (a Aggregate) Key() string { return a.Func.String() + ":" + a.Column.Key() }

// ParseAggregateKey is Key's inverse.
func ParseAggregateKey(k string) (Aggregate, bool) {
	fn, col, ok := strings.Cut(k, ":")
	if !ok {
		return Aggregate{}, false
	}
	f, ok := ParseAggFunc(fn)
	if !ok {
		return Aggregate{}, false
	}
	c, ok := ParseColumnKey(col)
	if !ok {
		return Aggregate{}, false
	}
	return Aggregate{Func: f, Column: c}, true
}
