package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// TestAgentJobsIconIsStopwatchNotFolder checks NodeAgentJobs ("SQL Server
// Agent") gets its own distinct glyph rather than the generic folder icon
// every other container node type falls back to.
func TestAgentJobsIconIsStopwatchNotFolder(t *testing.T) {
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		d := nodeData{Type: NodeAgentJobs}
		if got := nodeIcon(d, style, false); got != '⏱' {
			t.Errorf("nodeIcon(NodeAgentJobs, %v, false) = %q, want stopwatch ⏱", style, got)
		}
		if got := nodeIcon(d, style, true); got != '⏱' {
			t.Errorf("nodeIcon(NodeAgentJobs, %v, true) = %q, want stopwatch ⏱ regardless of expanded state", style, got)
		}
	}
	if got := nodeIcon(nodeData{Type: NodeAgentJobs}, config.IconStyleNone, false); got != 0 {
		t.Errorf("nodeIcon(NodeAgentJobs, IconStyleNone, false) = %q, want 0 (no icon)", got)
	}
}
