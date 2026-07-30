package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

func testMembershipConfig(added, removed *[]string) membershipConfig {
	return membershipConfig{
		members: []*gosmo.RoleMember{
			{Name: "alice", Type: "SQL_USER"},
			{Name: "bob", Type: "SQL_USER"},
		},
		candidates:    []string{"carol", "dave"},
		principalType: map[string]string{"carol": "SQL_USER", "dave": "DATABASE_ROLE"},
		note:          "note",
		add: func(_ context.Context, name string) error {
			*added = append(*added, name)
			return nil
		},
		remove: func(_ context.Context, name string) error {
			*removed = append(*removed, name)
			return nil
		},
	}
}

// membershipHint finds the page's single HintRow.
func membershipHint(t *testing.T, f *propsheet.Form) *propsheet.HintRow {
	t.Helper()
	for _, r := range f.Rows() {
		if h, ok := r.(*propsheet.HintRow); ok {
			return h
		}
	}
	t.Fatal("the membership form has no HintRow — a handler that declines has nowhere to say why")
	return nil
}

// clickFormButton fires the named button's OnClick, the same thing a press on
// it does (widgets.Button.HandleMouse/HandleKey both just call OnClick).
func clickFormButton(t *testing.T, f *propsheet.Form, label string) {
	t.Helper()
	for _, r := range f.Rows() {
		br, ok := r.(*propsheet.ButtonsRow)
		if !ok {
			continue
		}
		for _, b := range br.Buttons() {
			if b.Label() == label {
				b.OnClick()
				return
			}
		}
	}
	t.Fatalf("no %q button on this form", label)
}

// The whole point of the shared page: an Add that can't do anything says why
// rather than silently returning, which left the button looking broken.
func TestMembershipAddReportsAnExistingMember(t *testing.T) {
	var added, removed []string
	cfg := testMembershipConfig(&added, &removed)
	// "alice" is already a member, and reaching her through the dropdown means
	// a candidate list that wasn't filtered — exactly what the guard is for.
	cfg.candidates = []string{"alice", "carol"}

	f, apply := buildMembershipForm(cfg)
	hint := membershipHint(t, f)

	clickFormButton(t, f, "Add") // dropdown sits on "alice"
	if hint.Text() == "" {
		t.Fatal("Add on an existing member left the hint blank — the click looked like a no-op")
	}
	if !strings.Contains(hint.Text(), "alice") {
		t.Errorf("hint = %q, want it to name alice", hint.Text())
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("apply added %v, want nothing — the duplicate must not be queued", added)
	}
}

// Remove with nothing selected is the other half: report it instead of
// returning silently.
func TestMembershipRemoveWithNoSelectionReportsIt(t *testing.T) {
	var added, removed []string
	cfg := testMembershipConfig(&added, &removed)
	cfg.members = nil // empty grid, so no row can be selected

	f, apply := buildMembershipForm(cfg)
	hint := membershipHint(t, f)

	clickFormButton(t, f, "Remove")
	if hint.Text() == "" {
		t.Fatal("Remove with no selection left the hint blank — the click looked like a no-op")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("apply removed %v, want nothing", removed)
	}
}

// A successful Add clears any stale complaint and queues the real change.
func TestMembershipAddQueuesAndClearsTheHint(t *testing.T) {
	var added, removed []string
	f, apply := buildMembershipForm(testMembershipConfig(&added, &removed))
	hint := membershipHint(t, f)

	hint.Set("stale complaint")
	clickFormButton(t, f, "Add") // dropdown sits on "carol"
	if hint.Text() != "" {
		t.Errorf("hint = %q after a successful Add, want it cleared", hint.Text())
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(added) != 1 || added[0] != "carol" {
		t.Errorf("apply added %v, want [carol]", added)
	}
	if len(removed) != 0 {
		t.Errorf("apply removed %v, want nothing", removed)
	}
}

// An existing member marked for removal applies as a real remove.
func TestMembershipRemoveAppliesToAnExistingMember(t *testing.T) {
	var added, removed []string
	f, apply := buildMembershipForm(testMembershipConfig(&added, &removed))

	clickFormButton(t, f, "Remove") // row 0 == alice, an existing member
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(removed) != 1 || removed[0] != "alice" {
		t.Errorf("apply removed %v, want [alice]", removed)
	}
	if len(added) != 0 {
		t.Errorf("apply added %v, want nothing", added)
	}
}

// A member added and then removed before Apply cancels out — neither gosmo
// call should fire for a principal the server never saw.
func TestMembershipAddThenRemoveCancelsOut(t *testing.T) {
	var added, removed []string
	cfg := testMembershipConfig(&added, &removed)
	cfg.members = nil // start empty so the added row is the only one

	f, apply := buildMembershipForm(cfg)
	clickFormButton(t, f, "Add")    // queues carol as isNew
	clickFormButton(t, f, "Remove") // row 0 is carol; marks her pendingRemove

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("apply added %v and removed %v, want neither — the pair cancels", added, removed)
	}
}

// Revert drops pending additions, un-marks pending removals, and clears a
// stale hint, so a page reverted mid-edit reports itself clean.
func TestMembershipRevertRestoresTheOriginalMembers(t *testing.T) {
	var added, removed []string
	f, apply := buildMembershipForm(testMembershipConfig(&added, &removed))
	hint := membershipHint(t, f)

	clickFormButton(t, f, "Add")    // queue carol
	clickFormButton(t, f, "Remove") // and drop a row
	if !f.Dirty() {
		t.Fatal("form is not Dirty after an Add and a Remove")
	}

	f.Revert()
	if f.Dirty() {
		t.Error("form is still Dirty after Revert")
	}
	if hint.Text() != "" {
		t.Errorf("hint = %q after Revert, want it cleared", hint.Text())
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("apply after Revert added %v and removed %v, want neither", added, removed)
	}
}
