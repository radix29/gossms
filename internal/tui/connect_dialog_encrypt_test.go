package tui

import (
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// setEncryptMode's fallback used to be a tail call into itself with
// EncryptMandatory, which terminated only because AllEncryptModes happens to
// contain that mode. The two live in different packages and nothing pinned the
// dependency, so dropping or renaming a mode turned a hand-edited config.json
// into a stack overflow the moment the Connect dialog opened.
//
// Both halves are pinned: that an unknown mode still lands on Mandatory, and
// that it gets there without recursing. A test that only asserted the result
// would go on passing while the recursive form was reintroduced.
func TestSetEncryptModeFallsBackWithoutRecursing(t *testing.T) {
	for _, m := range []config.EncryptMode{"", "yes", "TRUE", "mandatory "} {
		t.Run(string(m), func(t *testing.T) {
			d := NewConnectDialog(newTestApp())
			d.setEncryptMode(m)
			got := config.AllEncryptModes()[d.ddEncrypt.Selected()]
			if got != config.EncryptMandatory {
				t.Errorf("an unknown mode %q selected %q, want %q", m, got, config.EncryptMandatory)
			}
		})
	}
}

// The fallback no longer depends on Mandatory being in the list, but the
// dialog's *meaning* still does: a list without it would silently connect
// under a weaker mode than the one the fallback names. Cheaper to pin than to
// rediscover.
func TestAllEncryptModesContainsMandatory(t *testing.T) {
	modes := config.AllEncryptModes()
	if len(modes) == 0 {
		t.Fatal("AllEncryptModes is empty, so the Connect dialog has no mode to select at all")
	}
	if !slices.Contains(modes, config.EncryptMandatory) {
		t.Errorf("AllEncryptModes %v no longer contains %q, which setEncryptMode falls back to",
			modes, config.EncryptMandatory)
	}
}
