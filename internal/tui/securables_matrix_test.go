package tui

import (
	"context"
	"sync/atomic"
	"testing"
)

// TestSecurableSearchSurvivesAPanickingSearch: the Add picker's search
// latches one search in flight. A hand-cleared flag stayed set when the
// search panicked, so every later keystroke was refused and search was dead
// for the rest of the showing.
func TestSecurableSearchSurvivesAPanickingSearch(t *testing.T) {
	a := newTestApp()
	d := &PropDialog{app: a, ctx: context.Background()}

	var calls atomic.Int32
	find := func(ctx context.Context, term string) ([]securable, error) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return []securable{{Type: "TABLE", Schema: "dbo", Name: "Orders"}}, nil
	}
	f, _ := buildSecurablesMatrix(d, nil, nil, nil, nil, nil, find, 8, 12, nil, nil)

	typeInto(t, f, "Search to add", "a")
	drainUntil(t, a, func() bool { return calls.Load() == 1 }, "the first search to run")

	// The repair is posted; keep typing until a search runs again, so the test
	// does not race the repair callback.
	typed := "a"
	drainUntil(t, a, func() bool {
		if calls.Load() >= 2 {
			return true
		}
		typed += "b"
		typeInto(t, f, "Search to add", typed)
		return calls.Load() >= 2
	}, "a search after the panicking one")
}
