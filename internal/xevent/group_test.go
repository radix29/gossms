package xevent

import (
	"slices"
	"testing"
)

func groupEvents() []*Event {
	mk := func(name, db, dur string, seq uint64) *Event {
		e := ev(name, seq)
		if dur != "" {
			e.Fields = append(e.Fields, Value{Name: "duration", Value: dur})
		}
		if db != "" {
			e.Actions = append(e.Actions, Value{Name: "database_name", Value: db})
		}
		return &e
	}
	return []*Event{
		mk("rpc_completed", "App", "100", 1),
		mk("sql_batch_completed", "master", "900", 2),
		mk("rpc_completed", "master", "20", 3),
		mk("sql_batch_completed", "App", "", 4),
		mk("rpc_completed", "App", "5", 5),
		mk("attention", "", "", 6),
	}
}

var (
	durationCol = Column{Kind: ColField, Name: "duration"}
	dbCol       = Column{Kind: ColAction, Name: "database_name"}
)

func groupValues(gs []*Group) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Value
		if g.Null {
			out[i] = "<null>"
		}
	}
	return out
}

func TestGroupEventsOneLevelWithAggregates(t *testing.T) {
	aggs := []Aggregate{{AggCount, durationCol}, {AggSum, durationCol}, {AggAvg, durationCol},
		{AggMin, durationCol}, {AggMax, durationCol}}
	gs := GroupEvents(groupEvents(), []Column{NameColumn}, aggs)
	if got := groupValues(gs); !slices.Equal(got, []string{"attention", "rpc_completed", "sql_batch_completed"}) {
		t.Fatalf("groups %v", got)
	}
	rpc := gs[1]
	if rpc.Count != 3 || len(rpc.Events) != 3 || rpc.Children != nil {
		t.Fatalf("rpc_completed: count %d, events %d, children %v", rpc.Count, len(rpc.Events), rpc.Children)
	}
	// MIN and MAX numeric: "100" < "20" as text.
	if want := []string{"3", "125", "41.667", "5", "100"}; !slices.Equal(rpc.Aggregates, want) {
		t.Errorf("rpc_completed aggregates %v, want %v", rpc.Aggregates, want)
	}
	// One of the two batches has no duration: COUNT counts the one that has.
	if want := []string{"1", "900", "900", "900", "900"}; !slices.Equal(gs[2].Aggregates, want) {
		t.Errorf("sql_batch_completed aggregates %v, want %v", gs[2].Aggregates, want)
	}
	if want := []string{"0", "", "", "", ""}; !slices.Equal(gs[0].Aggregates, want) {
		t.Errorf("attention aggregates %v, want %v", gs[0].Aggregates, want)
	}
}

// Nested: the second column's groups under each of the first's, the events
// lacking it last, and keys that differ across parents.
func TestGroupEventsNested(t *testing.T) {
	gs := GroupEvents(groupEvents(), []Column{dbCol, NameColumn}, []Aggregate{{AggSum, durationCol}})
	if got := groupValues(gs); !slices.Equal(got, []string{"App", "master", "<null>"}) {
		t.Fatalf("outer groups %v", got)
	}
	app := gs[0]
	if app.Events != nil || app.Count != 3 || app.Aggregates[0] != "105" {
		t.Errorf("App: events %v, count %d, sum %q", app.Events, app.Count, app.Aggregates[0])
	}
	if got := groupValues(app.Children); !slices.Equal(got, []string{"rpc_completed", "sql_batch_completed"}) {
		t.Fatalf("App's groups %v", got)
	}
	if app.Children[0].Level != 1 || app.Children[0].Count != 2 || len(app.Children[0].Events) != 2 {
		t.Errorf("App/rpc_completed: level %d count %d", app.Children[0].Level, app.Children[0].Count)
	}
	master := gs[1]
	if app.Children[0].Key == master.Children[0].Key {
		t.Error("rpc_completed under App and under master share a key")
	}
	// Regrouping the same events gives the same keys: the viewer's expanded
	// set survives a live update.
	again := GroupEvents(groupEvents(), []Column{dbCol, NameColumn}, nil)
	if again[0].Children[1].Key != app.Children[1].Key {
		t.Error("keys changed across regroupings")
	}
	if gs[2].Key == GroupEvents(groupEvents(), []Column{dbCol}, nil)[0].Key {
		t.Error("the null group's key equals a value's")
	}
}

func TestGroupEventsNumericOrder(t *testing.T) {
	gs := GroupEvents(groupEvents(), []Column{durationCol}, nil)
	if got := groupValues(gs); !slices.Equal(got, []string{"5", "20", "100", "900", "<null>"}) {
		t.Errorf("groups %v, want numeric order, null last", got)
	}
	if GroupEvents(groupEvents(), nil, nil) != nil {
		t.Error("no columns grouped something")
	}
}

func TestAggregateAndColumnKeysRoundTrip(t *testing.T) {
	for _, a := range []Aggregate{{AggAvg, durationCol}, {AggMax, dbCol}, {AggCount, TimestampColumn}} {
		back, ok := ParseAggregateKey(a.Key())
		if !ok || back != a {
			t.Errorf("%q parsed back as %v, %v", a.Key(), back, ok)
		}
	}
	// Pinned by name, so a swapped table can't round-trip.
	if k := (Aggregate{AggSum, durationCol}).Key(); k != "SUM:field:duration" {
		t.Errorf("key %q", k)
	}
	for _, bad := range []string{"", "SUM", "BOGUS:name", "SUM:nope", "SUM:field:"} {
		if _, ok := ParseAggregateKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestContainsText(t *testing.T) {
	e := ev("wait_info", 1)
	e.Fields = []Value{{Name: "wait_type", Value: "66", Text: "PAGEIOLATCH_SH"}}
	for s, want := range map[string]bool{"pageio": true, "WAIT_INFO": true, "66": true, "latch_ex": false} {
		if e.ContainsText(s) != want {
			t.Errorf("ContainsText(%q) = %v", s, !want)
		}
	}
}

// A group's Terms filter to exactly its events, through every enclosing
// group, a "(no value)" one included.
func TestGroupTermsMatchExactlyTheGroup(t *testing.T) {
	events := groupEvents()
	all := []Column{NameColumn, TimestampColumn, durationCol, dbCol}
	var check func(gs []*Group)
	check = func(gs []*Group) {
		for _, g := range gs {
			f := (*Filter)(nil).AndTerms(g.Terms(all)...)
			n := 0
			for _, e := range events {
				if f.Match(e) {
					n++
				}
			}
			if n != g.Count {
				t.Errorf("%q matches %d events, the group holds %d", f.String(), n, g.Count)
			}
			check(g.Children)
		}
	}
	gs := GroupEvents(events, []Column{NameColumn, dbCol, durationCol}, nil)
	check(gs)
	if gs[0].Parent != nil || gs[0].Children[0].Parent != gs[0] {
		t.Error("Parent not set")
	}
	// attention has no database_name: its inner group is `is null`.
	f := (*Filter)(nil).AndTerms(gs[0].Children[0].Terms(all)...)
	if f.String() != "name = attention and action:database_name is null" {
		t.Errorf("null group's filter %q", f.String())
	}
}

// A field sharing its name with an action is named field:x — the bare name
// would reach the action on an event without the field.
func TestGroupTermsTellAFieldFromItsNamesakeAction(t *testing.T) {
	withField := ev("a", 1, "db", "x")
	withAction := ev("b", 2)
	withAction.Actions = []Value{{Name: "db", Value: "x"}}
	events := []*Event{&withField, &withAction}
	field := Column{Kind: ColField, Name: "db"}
	all := []Column{NameColumn, field, {Kind: ColAction, Name: "db"}}
	for _, g := range GroupEvents(events, []Column{field}, nil) {
		f := (*Filter)(nil).AndTerms(g.Terms(all)...)
		n := 0
		for _, e := range events {
			if f.Match(e) {
				n++
			}
		}
		if n != g.Count {
			t.Errorf("%q matches %d events, the group holds %d", f.String(), n, g.Count)
		}
	}
}
