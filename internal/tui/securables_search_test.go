package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// securablesParts pulls the rows a search test drives back out of a built
// Securables page. Rows are identified by their label rather than by
// position, so inserting a row above them does not silently retarget the
// test.
type securablesParts struct {
	search  *propsheet.TextRow
	addPick *propsheet.SelectRow
	hint    *propsheet.HintRow
}

func securablesPartsOf(t *testing.T, f *propsheet.Form) securablesParts {
	t.Helper()
	var p securablesParts
	var texts []*propsheet.TextRow
	var selects []*propsheet.SelectRow
	var hints []*propsheet.HintRow
	for _, r := range f.Rows() {
		switch v := r.(type) {
		case *propsheet.TextRow:
			texts = append(texts, v)
		case *propsheet.SelectRow:
			selects = append(selects, v)
		case *propsheet.HintRow:
			hints = append(hints, v)
		}
	}
	// securableFilterRow, searchRow, permFilterRow; addSelect, colPermSelect;
	// the Add hint, then the column-editor hint.
	if len(texts) != 3 || len(selects) != 2 || len(hints) != 2 {
		t.Fatalf("form has %d text rows, %d selects, %d hints; want 3, 2, 2",
			len(texts), len(selects), len(hints))
	}
	p.search, p.addPick, p.hint = texts[1], selects[0], hints[0]
	return p
}

// searchRecorder is a securableFindFn that records the terms it was asked
// for and can be held open, so a test can type while one is in flight.
type searchRecorder struct {
	mu      sync.Mutex
	terms   []string
	gate    chan struct{} // when non-nil, each call waits on a receive
	results func(term string) []securable
	err     error
}

func (r *searchRecorder) fn() securableFindFn {
	return func(_ context.Context, term string) ([]securable, error) {
		if r.gate != nil {
			<-r.gate
		}
		r.mu.Lock()
		r.terms = append(r.terms, term)
		r.mu.Unlock()
		if r.err != nil {
			return nil, r.err
		}
		if r.results != nil {
			return r.results(term), nil
		}
		return nil, nil
	}
}

// Terms returns the searches run so far. The find function runs on a
// background goroutine, so the slice is only safe to read under the lock.
func (r *searchRecorder) Terms() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.terms)
}

func securablesPage(t *testing.T, rec *searchRecorder, candidates []securable) (*App, securablesParts) {
	t.Helper()
	a := &App{}
	d := &PropDialog{app: a, ctx: context.Background()}
	f, _ := buildSecurablesMatrix(d, nil, nil, nil, nil, candidates, rec.fn(), 8, 12,
		func(context.Context, string, gosmo.PermissionOptions, securable, string) error { return nil },
		func(context.Context, string, gosmo.PermissionOptions, securable, string, string) error { return nil },
	)
	return a, securablesPartsOf(t, f)
}

func pickerItems(p securablesParts) []string { return p.addPick.Items() }

// Typing five characters must not put five searches on the wire. At most one
// is in flight; the rest coalesce, so what runs is the first term and then
// whatever the box holds when that one lands. Queueing them instead lets an
// early result land last and repopulate the picker from a term the user has
// already backspaced away.
func TestSecurablesSearchCoalescesWhileOneIsInFlight(t *testing.T) {
	rec := &searchRecorder{gate: make(chan struct{}, 8)}
	a, p := securablesPage(t, rec, nil)

	// "o", "or", "ord" typed before anything is allowed to complete.
	for _, term := range []string{"o", "or", "ord"} {
		p.search.Paste(term[len(term)-1:])
	}
	if got := rec.Terms(); len(got) != 0 {
		t.Fatalf("a gated search reported terms early: %v", got)
	}

	// Let the first through and drain its callback, which starts the next.
	rec.gate <- struct{}{}
	rec.gate <- struct{}{}
	drainUntil(t, a, func() bool { return len(rec.Terms()) == 2 }, "two searches to run")

	// Nothing more is queued: the second one already used the final term.
	if got := rec.Terms(); !slices.Equal(got, []string{"o", "ord"}) {
		t.Errorf("searched for %v, want [o ord] — one per keystroke was queued", got)
	}
}

// A search returning more than the cap must show exactly the cap and say so.
// Silently showing an arbitrary subset is the failure: the user scrolls a
// list that looks complete and concludes the object does not exist.
func TestSecurablesSearchCapsAndSaysSo(t *testing.T) {
	rec := &searchRecorder{results: func(string) []securable {
		out := make([]securable, securableSearchLimit+1)
		for i := range out {
			out[i] = securable{Type: "TABLE", Schema: "dbo", Name: fmt.Sprintf("T%03d", i)}
		}
		return out
	}}
	_, p := securablesPage(t, rec, rec.results(""))

	items := pickerItems(p)
	// The database securable is offered too when the term matches it, which
	// an empty term does.
	if len(items) != securableSearchLimit+1 {
		t.Fatalf("picker lists %d items, want %d (the cap plus the database itself)",
			len(items), securableSearchLimit+1)
	}
	if items[0] != "(database)" {
		t.Errorf("first item = %q, want the database securable", items[0])
	}
	if !slices.Contains(items, "[dbo].[T000]") || slices.Contains(items, fmt.Sprintf("[dbo].[T%03d]", securableSearchLimit)) {
		t.Error("the cap did not trim the tail of the result")
	}
	want := fmt.Sprintf("More than %d matches — type more to narrow the list.", securableSearchLimit)
	if got := p.hint.Text(); got != want {
		t.Errorf("hint = %q, want %q — a trimmed list that says nothing reads as complete", got, want)
	}
}

// The database itself is a securable no name search returns, so the page has
// to offer it — but only when what was typed actually matches it, or a
// search for a table name lists "(database)" above the match.
func TestSecurablesSearchOffersTheDatabaseOnlyWhenItMatches(t *testing.T) {
	rec := &searchRecorder{results: func(string) []securable {
		return []securable{{Type: "TABLE", Schema: "dbo", Name: "Orders"}}
	}}
	a, p := securablesPage(t, rec, nil)

	if got := pickerItems(p); !slices.Contains(got, "(database)") {
		t.Errorf("picker = %v, want the database offered for an empty search", got)
	}

	p.search.Paste("ord")
	drainUntil(t, a, func() bool { return slices.Contains(pickerItems(p), "[dbo].[Orders]") },
		"the search result to reach the picker")
	got := pickerItems(p)
	if slices.Contains(got, "(database)") {
		t.Errorf("picker = %v, want no database entry for a term that does not match it", got)
	}
	if !slices.Contains(got, "[dbo].[Orders]") {
		t.Errorf("picker = %v, want the searched-for table", got)
	}
}

// A failed search must say so and leave the picker alone rather than
// emptying it, which would read as "no such object".
func TestSecurablesSearchReportsFailure(t *testing.T) {
	rec := &searchRecorder{err: errors.New("boom")}
	seed := []securable{{Type: "TABLE", Schema: "dbo", Name: "Orders"}}
	a, p := securablesPage(t, rec, seed)

	before := slices.Clone(pickerItems(p))
	p.search.Paste("zzz")
	drainUntil(t, a, func() bool { return p.hint.Text() != "" }, "the failure to be reported")

	if got := p.hint.Text(); got != "Search failed: boom" {
		t.Errorf("hint = %q, want it to name the failure", got)
	}
	if got := pickerItems(p); !slices.Equal(got, before) {
		t.Errorf("picker = %v after a failed search, want it left at %v", got, before)
	}
}
