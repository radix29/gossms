package tui

import (
	"context"
	"fmt"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The W8 measurement (review plan 2026-09-24): a million rows of eight
// columns in Results to Text. BenchmarkResultsTextFormat is the part that runs
// off the UI goroutine; BenchmarkResultsTextInstall is all that is left on it.
// On an i5-2500K: 3.2 s and 1.8 ms. Before, all of 2.1 s formatting, 1.1 s of
// SetText and 370 ms of the first Draw ran on the UI goroutine.

func resultsTextBenchSet(rows int) query.ResultSet {
	set := query.ResultSet{Columns: []string{"id", "name", "created", "amount", "status", "code", "note", "flag"}}
	set.Rows = make([][]string, rows)
	for i := range set.Rows {
		set.Rows[i] = []string{
			fmt.Sprint(i), fmt.Sprintf("customer %d", i), "2026-09-25 10:11:12.123", fmt.Sprintf("%d.%02d", i%100000, i%100),
			"ACTIVE", fmt.Sprintf("C%07d", i), "some free text note here", "1",
		}
	}
	return set
}

func BenchmarkResultsTextFormat(b *testing.B) {
	set := resultsTextBenchSet(1_000_000)
	b.ResetTimer()
	for b.Loop() {
		_, _ = formatResultsAsText(context.Background(), set, config.DefaultMaxTextColumnLength, 4)
	}
}

func BenchmarkResultsTextInstall(b *testing.B) {
	lines, _ := formatResultsAsText(context.Background(), resultsTextBenchSet(1_000_000), config.DefaultMaxTextColumnLength, 4)
	e := controls.NewEditor(nil)
	b.ResetTimer()
	for b.Loop() {
		e.SetLineBuffer(lines)
	}
}
