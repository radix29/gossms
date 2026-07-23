package dialogs

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestTypedConfirmDialog(t *testing.T) *TypedConfirmDialog {
	t.Helper()
	return NewTypedConfirmDialog(nil)
}

func TestTypedConfirmDialogMismatchRefusesAndStaysOpen(t *testing.T) {
	d := newTestTypedConfirmDialog(t)
	var got *bool
	d.ShowTypedConfirm("Confirm Overwrite", `Database "HealthClinic" already exists.`, "Heal",
		func(confirmed bool) { got = &confirmed })

	for _, r := range "nope" {
		d.HandleKey(rn(r))
	}
	d.HandleKey(key(tcell.KeyEnter))

	if !d.Visible() {
		t.Fatal("dialog closed on a mismatched confirm — should stay open")
	}
	if got != nil {
		t.Fatalf("OnConfirm called with %v on a mismatch — should not fire yet", *got)
	}
	if d.status == "" {
		t.Error("expected a status message explaining the mismatch")
	}
}

func TestTypedConfirmDialogMatchConfirms(t *testing.T) {
	d := newTestTypedConfirmDialog(t)
	var got *bool
	d.ShowTypedConfirm("Confirm Overwrite", `Database "HealthClinic" already exists.`, "Heal",
		func(confirmed bool) { got = &confirmed })

	for _, r := range "Heal" {
		d.HandleKey(rn(r))
	}
	d.HandleKey(key(tcell.KeyEnter))

	if d.Visible() {
		t.Fatal("dialog stayed open after a matching confirm")
	}
	if got == nil || !*got {
		t.Fatal("OnConfirm(true) was not called after a matching confirm")
	}
}

func TestTypedConfirmDialogMatchIsCaseInsensitiveAndTrimsSpace(t *testing.T) {
	d := newTestTypedConfirmDialog(t)
	d.ShowTypedConfirm("Confirm", "msg", "Heal", func(bool) {})

	for _, r := range "  HEAL  " {
		d.HandleKey(rn(r))
	}
	if !d.matched() {
		t.Errorf("expected %q to match required %q case-insensitively, trimmed", d.input.Value(), d.required)
	}
}

func TestTypedConfirmDialogEscapeCancelsWithoutRequiringMatch(t *testing.T) {
	d := newTestTypedConfirmDialog(t)
	var got *bool
	d.ShowTypedConfirm("Confirm", "msg", "Heal", func(confirmed bool) { got = &confirmed })

	d.HandleKey(key(tcell.KeyEscape))

	if d.Visible() {
		t.Fatal("dialog stayed open after Escape")
	}
	if got == nil || *got {
		t.Fatal("OnConfirm(false) was not called after Escape")
	}
}
