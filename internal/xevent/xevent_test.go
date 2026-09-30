package xevent

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func ev(name string, seq uint64, fields ...string) Event {
	e := Event{Name: name, Package: "sqlserver", Seq: seq, Timestamp: time.Date(2026, 9, 29, 10, 0, int(seq), 0, time.UTC)}
	for i := 0; i+1 < len(fields); i += 2 {
		e.Fields = append(e.Fields, Value{Name: fields[i], Value: fields[i+1]})
	}
	return e
}

func names(evs []*Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Name
	}
	return out
}

func TestStoreDropsOldestPastCapacity(t *testing.T) {
	s := NewStore(3)
	s.Add(ev("a", 1), ev("b", 2))
	added, _ := s.Add(ev("c", 3), ev("d", 4), ev("e", 5))
	if s.Len() != 3 || s.Dropped() != 2 {
		t.Fatalf("Len %d Dropped %d, want 3 and 2", s.Len(), s.Dropped())
	}
	var held []string
	for i := range s.Len() {
		held = append(held, s.At(i).Name)
	}
	if !slices.Equal(held, []string{"c", "d", "e"}) {
		t.Errorf("held %v, want [c d e]", held)
	}
	if got := names(added); !slices.Equal(got, []string{"c", "d", "e"}) {
		t.Errorf("added %v", got)
	}
	if s.OldestID() != s.At(0).ID || s.At(0).ID != 3 {
		t.Errorf("OldestID %d, At(0).ID %d, want 3", s.OldestID(), s.At(0).ID)
	}
}

// An Add bigger than the whole store must not hand back events it has
// already pushed out — a view would show rows the store no longer holds.
func TestStoreAddLargerThanCapacityReturnsOnlyHeld(t *testing.T) {
	s := NewStore(2)
	added, _ := s.Add(ev("a", 1), ev("b", 2), ev("c", 3))
	if got := names(added); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("added %v, want [b c]", got)
	}
	if s.Dropped() != 1 {
		t.Errorf("Dropped %d, want 1", s.Dropped())
	}
}

func TestStoreColumnsInFirstSeenOrder(t *testing.T) {
	s := NewStore(10)
	e1 := ev("x", 1, "duration", "5")
	e1.Actions = []Value{{Name: "database_id", Value: "5"}}
	_, grew := s.Add(e1)
	if !grew {
		t.Error("first event did not report new columns")
	}
	if _, grew := s.Add(ev("x", 2, "duration", "6")); grew {
		t.Error("same columns reported as new")
	}
	_, grew = s.Add(ev("y", 3, "database_id", "5", "cpu_time", "1"))
	if !grew {
		t.Error("new field not reported")
	}
	cols := s.Columns()
	var hdr []string
	for _, c := range cols {
		hdr = append(hdr, Header(c, cols))
	}
	want := []string{"name", "timestamp", "duration", "database_id", "cpu_time", "database_id (action)"}
	if !slices.Equal(hdr, want) {
		t.Errorf("headers %v, want %v", hdr, want)
	}
	s.Clear()
	if s.Len() != 0 || len(s.Columns()) != len(cols) {
		t.Errorf("Clear: Len %d, columns %d (want 0 and %d)", s.Len(), len(s.Columns()), len(cols))
	}
}

func TestDeduperBySequence(t *testing.T) {
	var d Deduper
	if got := d.New([]Event{ev("a", 1), ev("b", 2)}); len(got) != 2 {
		t.Fatalf("first read: %d new, want 2", len(got))
	}
	// a aged out, b still there, c and d new.
	got := d.New([]Event{ev("b", 2), ev("c", 3), ev("d", 4)})
	if n := len(got); n != 2 || got[0].Name != "c" || got[1].Name != "d" {
		t.Errorf("second read: %+v, want c and d", got)
	}
	if d.Unsequenced {
		t.Error("Unsequenced set for sequenced events")
	}
}

// Without event_sequence, two identical events are counted, not collapsed: a
// read holding one more copy than the last yields that copy.
func TestDeduperWithoutSequenceCountsDuplicates(t *testing.T) {
	var d Deduper
	same := ev("a", 0, "x", "1")
	d.New([]Event{same})
	got := d.New([]Event{same, same, ev("a", 0, "x", "2")})
	if len(got) != 2 {
		t.Fatalf("got %d new, want 2 (the second copy and the x=2 event)", len(got))
	}
	if !d.Unsequenced {
		t.Error("Unsequenced not set")
	}
	d.Reset()
	if got := d.New([]Event{same}); len(got) != 1 {
		t.Errorf("after Reset: %d new, want 1", len(got))
	}
}

func TestFilterExpressions(t *testing.T) {
	slow := ev("rpc_completed", 1, "duration", "1500000", "object_name", "usp_Load")
	slow.Actions = []Value{{Name: "database_name", Value: "App"}}
	fast := ev("sql_batch_completed", 2, "duration", "900", "batch_text", "select 1")
	fast.Actions = []Value{{Name: "database_name", Value: "master"}}
	wait := ev("wait_info", 3)
	wait.Fields = []Value{{Name: "wait_type", Value: "66", Text: "PAGEIOLATCH_SH"}}

	cases := []struct {
		expr string
		want []string // names of the events that pass
	}{
		{"duration > 1000", []string{"rpc_completed"}},
		// Numeric, not textual: "900" > "1000" as text.
		{"duration < 1000", []string{"sql_batch_completed"}},
		{"database_name = app", []string{"rpc_completed"}},
		{"name = sql_batch_completed or duration >= 1500000", []string{"rpc_completed", "sql_batch_completed"}},
		// AND binds tighter than OR.
		{"name = wait_info or duration > 1 and database_name = master", []string{"sql_batch_completed", "wait_info"}},
		{"batch_text contains 'SELECT'", []string{"sql_batch_completed"}},
		{"batch_text ~ select", []string{"sql_batch_completed"}},
		{"batch_text is null", []string{"rpc_completed", "wait_info"}},
		{"batch_text is not null", []string{"sql_batch_completed"}},
		// A missing field fails every comparison, <> included.
		{"batch_text <> 'x'", []string{"sql_batch_completed"}},
		{"object_name starts with usp_", []string{"rpc_completed"}},
		// A map field matches its text or its key.
		{"wait_type = PAGEIOLATCH_SH", []string{"wait_info"}},
		{"wait_type = 66", []string{"wait_info"}},
		// Free text over every value.
		{"usp_load", []string{"rpc_completed"}},
		{"pageiolatch", []string{"wait_info"}},
		{"", []string{"rpc_completed", "sql_batch_completed", "wait_info"}},
	}
	for _, c := range cases {
		f, err := ParseFilter(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		var got []string
		for _, e := range []*Event{&slow, &fast, &wait} {
			if f.Match(e) {
				got = append(got, e.Name)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q matched %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestFilterErrors(t *testing.T) {
	for _, s := range []string{"duration >", "duration > 5 and", "= 5", "x = 'open", "duration > 5 xor y = 1"} {
		if _, err := ParseFilter(s); err == nil {
			t.Errorf("%q parsed; want an error", s)
		}
	}
	// No symbol: not an expression, so free text rather than an error.
	f, err := ParseFilter("this is slow")
	if err != nil || !f.IsFreeText() {
		t.Errorf("free text: %v, %v", f, err)
	}
}

func TestQuoteRoundTrips(t *testing.T) {
	for _, v := range []string{"plain", "two words", "it's", "a=b", "and", ""} {
		f, err := ParseFilter("x = " + Quote(v))
		if err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		e := ev("n", 1, "x", v)
		if !f.Match(&e) {
			t.Errorf("x = %s does not match %q", Quote(v), v)
		}
		if strings.Contains(v, " ") && !strings.HasPrefix(Quote(v), "'") {
			t.Errorf("Quote(%q) = %s, not quoted", v, Quote(v))
		}
	}
}

// AndEquals distributes over OR, and its text parses back to the same filter.
func TestFilterAndEqualsDistributes(t *testing.T) {
	f, _ := ParseFilter("name = a or name = b")
	g := f.AndEquals("db", "my db")
	if g.String() != "name = a and db = 'my db' or name = b and db = 'my db'" {
		t.Errorf("text %q", g.String())
	}
	back, err := ParseFilter(g.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{ev("a", 1, "db", "my db"), ev("b", 2, "db", "x"), ev("c", 3, "db", "my db")} {
		if g.Match(&e) != back.Match(&e) {
			t.Errorf("%s: AndEquals and its reparsed text disagree", e.Name)
		}
		want := e.Name == "a"
		if g.Match(&e) != want {
			t.Errorf("%s matched %v, want %v", e.Name, g.Match(&e), want)
		}
	}
	if f.String() != "name = a or name = b" {
		t.Error("AndEquals changed its receiver")
	}
}

// A field named like a built-in column is told apart from it: system_health's
// ring_buffer_recorded events carry a field called timestamp.
func TestHeaderTellsAFieldFromTheBuiltInItShadows(t *testing.T) {
	s := NewStore(10)
	e := ev("x", 1, "timestamp", "0", "id", "1")
	e.Actions = []Value{{Name: "name", Value: "a"}}
	s.Add(e)
	cols := s.Columns()
	var hdr []string
	for _, c := range cols {
		hdr = append(hdr, Header(c, cols))
	}
	want := []string{"name", "timestamp", "timestamp (field)", "id", "name (action)"}
	if !slices.Equal(hdr, want) {
		t.Errorf("headers %v, want %v", hdr, want)
	}
}

// field: and action: reach what a bare name resolves elsewhere, and
// FilterName uses them exactly where it has to.
func TestFilterKindPrefixes(t *testing.T) {
	e := ev("x", 1, "timestamp", "42", "database_id", "5")
	e.Actions = []Value{{Name: "database_id", Value: "7"}}
	for expr, want := range map[string]bool{
		"field:timestamp = 42": true, "timestamp = 42": false,
		"database_id = 7": false, "action:database_id = 7": true, "field:database_id = 5": true,
	} {
		f, err := ParseFilter(expr)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		if f.Match(&e) != want {
			t.Errorf("%q matched %v, want %v", expr, f.Match(&e), want)
		}
	}
	for c, want := range map[Column]string{
		{Kind: ColField, Name: "timestamp"}: "field:timestamp", {Kind: ColField, Name: "duration"}: "duration",
		{Kind: ColAction, Name: "database_id"}: "action:database_id", TimestampColumn: "timestamp",
	} {
		if got := c.FilterName(); got != want {
			t.Errorf("FilterName(%v) = %q, want %q", c, got, want)
		}
	}
}

// AndTerms ANDs every term onto each OR branch, replaces free text, and
// writes `is null` so it parses back to the same filter.
func TestFilterAndTerms(t *testing.T) {
	terms := []Term{{Column: "db", Op: OpEq, Value: "x"}, {Column: "cpu", Op: OpIsNull, Value: "ignored"}}
	f, _ := ParseFilter("name = a or name = b")
	g := f.AndTerms(terms...)
	if want := "name = a and db = x and cpu is null or name = b and db = x and cpu is null"; g.String() != want {
		t.Errorf("text %q, want %q", g.String(), want)
	}
	free, _ := ParseFilter("some text")
	if h := free.AndTerms(terms...); h.String() != "db = x and cpu is null" || h.IsFreeText() {
		t.Errorf("over free text: %q", h.String())
	}
	back, err := ParseFilter(g.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{ev("a", 1, "db", "x"), ev("a", 2, "db", "x", "cpu", "1"), ev("b", 3, "db", "y"), ev("c", 4, "db", "x")} {
		want := e.Seq == 1
		if g.Match(&e) != want || back.Match(&e) != want {
			t.Errorf("event %d: matched %v, reparsed %v, want %v", e.Seq, g.Match(&e), back.Match(&e), want)
		}
	}
}
