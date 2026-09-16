package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// A contract's message usages are two bits, not one column:
// is_sent_by_initiator and is_sent_by_target, with both set meaning SENT BY
// ANY. Reading them as one column loses that case, and a contract scripted
// from the wrong reading does not parse.

func TestContractPageShowsEachSenderIncludingBoth(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(),
		brokerRowByArg(contractResp(), "//claims/Contract", 1), contractMessageResp())
	pages := contractPropPages(sc, brokerDB, "//claims/Contract")
	form, apply := loadPage(t, pages[0], inst)

	if apply != nil {
		t.Error("Contract Properties is read-only; its page returned an apply")
	}
	want := map[string]string{
		"//claims/Ack":    "SENT BY TARGET",
		"//claims/Both":   "SENT BY ANY",
		"//claims/Submit": "SENT BY INITIATOR",
	}
	got := map[string]string{}
	for _, r := range form.Rows() {
		sr, ok := r.(*propsheet.StaticRow)
		if !ok || sr.Label() != sheetLabel("Message type") {
			continue
		}
		name, sender, found := strings.Cut(sr.Value(), " (")
		if !found {
			t.Fatalf("message row %q does not name its sender", sr.Value())
		}
		got[name] = strings.TrimSuffix(sender, ")")
	}
	for name, sender := range want {
		if got[name] != sender {
			t.Errorf("%s reads %q, want %q", name, got[name], sender)
		}
	}
}

// TestContractPageKeepsLongMessageTypeNamesInTheValue. A propsheet label is
// padded to LabelWidth and hard-clipped with no ellipsis, and a message type's
// name is a URI — put in the label, every one of them would render as the same
// truncated string. See CLAUDE.md § Coding conventions.
func TestContractPageKeepsLongMessageTypeNamesInTheValue(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(),
		brokerRowByArg(contractResp(), "//claims/Contract", 1), contractMessageResp())
	pages := contractPropPages(sc, brokerDB, "//claims/Contract")
	form, _ := loadPage(t, pages[0], inst)

	for _, r := range form.Rows() {
		sr, ok := r.(*propsheet.StaticRow)
		if !ok {
			continue
		}
		if len(sr.Label()) > propsheet.LabelWidth {
			t.Errorf("row label %q is longer than LabelWidth and will render clipped", sr.Label())
		}
	}
	if !strings.Contains(staticValuesJoined(form), "//claims/Submit") {
		t.Error("no row carries the message type's full name")
	}
}

// staticValuesJoined is every static row's value on one line, for a test that
// cares that a string is somewhere on the page rather than in which row.
func staticValuesJoined(f *propsheet.Form) string {
	var b strings.Builder
	for _, r := range f.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok {
			b.WriteString(sr.Value())
			b.WriteString("\n")
		}
	}
	return b.String()
}
