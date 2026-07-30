package propsheet

import "testing"

func TestHintRowStartsBlank(t *testing.T) {
	r := Hint()
	if got := r.Text(); got != "" {
		t.Errorf("Hint().Text() = %q, want \"\"", got)
	}
}

func TestHintRowSetAndClear(t *testing.T) {
	r := Hint()

	r.Set("already listed")
	if got := r.Text(); got != "already listed" {
		t.Errorf("after Set: Text() = %q, want %q", got, "already listed")
	}
	if r.isError {
		t.Error("Set marked the hint as an error; want advisory")
	}

	r.SetError("failed")
	if got := r.Text(); got != "failed" {
		t.Errorf("after SetError: Text() = %q, want %q", got, "failed")
	}
	if !r.isError {
		t.Error("SetError did not mark the hint as an error")
	}

	r.Clear()
	if got := r.Text(); got != "" {
		t.Errorf("after Clear: Text() = %q, want \"\"", got)
	}
	if r.isError {
		t.Error("Clear left the error flag set")
	}
}

// A hint appearing or disappearing must not reflow the rows around it, so its
// height is 1 whether or not it currently has text.
func TestHintRowReservesItsLineWhenBlank(t *testing.T) {
	r := Hint()
	if got := r.Height(40); got != 1 {
		t.Errorf("blank Height(40) = %d, want 1", got)
	}
	r.Set("something")
	if got := r.Height(40); got != 1 {
		t.Errorf("populated Height(40) = %d, want 1", got)
	}
}

// The hint is advisory text, not a control: it must never take a Tab stop,
// or every page carrying one would gain a dead focus position.
func TestHintRowIsNotFocusable(t *testing.T) {
	if Hint().Focusable() {
		t.Error("HintRow.Focusable() = true, want false")
	}
}
