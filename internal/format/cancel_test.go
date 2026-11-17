package format

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// countdownCtx is a context whose Err turns context.Canceled on its (n+1)th
// call: a cancel landing at each of the formatter's check points in turn,
// without a timing race.
type countdownCtx struct {
	context.Context
	left atomic.Int64
}

func newCountdown(n int64) *countdownCtx {
	c := &countdownCtx{Context: context.Background()}
	c.left.Store(n)
	return c
}

func (c *countdownCtx) Err() error {
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// cancelCorpus is one document per formatter, large enough that each long
// phase passes several of its own check points.
func cancelCorpus() map[Language]string {
	return map[Language]string{
		LangSQL:  strings.Repeat("select a, b as c from t where x = 1 and y in (select 1)\ngo\n", 200),
		LangXML:  "<r>" + strings.Repeat(`<a x="1"><b/><!-- c --><d>t</d></a>`, 600) + "</r>",
		LangJSON: "[" + strings.Repeat(`{"a":[1,2],"b":"x"},`, 600) + "0]",
	}
}

// A cancel at any of Format's check points ends it with ctx's error — never a
// guard refusal (a lexing cut short "no longer tokenizes"), a syntax error or
// partial output — and a long input is checked often enough to stop early.
func TestFormatStopsAtEveryCheckPoint(t *testing.T) {
	for l, src := range cancelCorpus() {
		t.Run(l.String(), func(t *testing.T) {
			// Count the checks a full run makes. XML's well-formedness pass
			// runs beside the layout, so its count is a ceiling, not exact.
			full := newCountdown(1 << 40)
			want, err := Format(full, l, "", src, Options{})
			if err != nil {
				t.Fatalf("uncancelled Format: %v", err)
			}
			checks := 1<<40 - full.left.Load()
			// JSON is checked between phases only (encoding/json takes no
			// context); the others inside their loops too.
			if minChecks := int64(20); l == LangJSON && checks < 3 || l != LangJSON && checks < minChecks {
				t.Fatalf("only %d ctx checks over %d bytes: too coarse to stop a large format", checks, len(src))
			}
			// Every check point is too slow for SQL (one per line, thrice);
			// a stride that still lands in every phase, plus both ends.
			step := max(1, checks/200)
			for n := int64(0); n < checks; n += min(step, max(1, checks-1-n)) {
				out, err := Format(newCountdown(n), l, "", src, Options{})
				switch {
				case errors.Is(err, context.Canceled):
					if out != "" {
						t.Fatalf("cancel after %d checks returned output", n)
					}
				case err != nil:
					t.Fatalf("cancel after %d checks: err %v, want context.Canceled", n, err)
				case out != want:
					t.Fatalf("cancel after %d checks: output differs from the full run", n)
				}
			}
			if _, err := Format(newCountdown(0), l, "", src, Options{}); !errors.Is(err, context.Canceled) {
				t.Errorf("already-cancelled ctx: err %v, want context.Canceled", err)
			}
		})
	}
}

// Verify, which Format Document runs on the whole document after formatting a
// selection, reports a cancel as ctx's error, not ErrGuard.
func TestVerifyReportsCancelNotGuard(t *testing.T) {
	for l, src := range cancelCorpus() {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := Verify(ctx, l, src, src); !errors.Is(err, context.Canceled) || errors.Is(err, ErrGuard) {
			t.Errorf("%s: Verify on a cancelled ctx = %v, want context.Canceled", l, err)
		}
		if err := Verify(t.Context(), l, src, src); err != nil {
			t.Errorf("%s: Verify = %v", l, err)
		}
	}
}
