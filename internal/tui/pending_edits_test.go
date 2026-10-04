package tui

import (
	"strings"
	"testing"
)

func TestPendingNameIndex(t *testing.T) {
	names := []string{"PRIMARY", "Sales", "sales_archive"}
	self := func(s string) string { return s }
	for _, tt := range []struct {
		collation, name string
		want            int
	}{
		{"SQL_Latin1_General_CP1_CI_AS", "sales", 1},
		{"SQL_Latin1_General_CP1_CI_AS", "SALES_ARCHIVE", 2},
		{"SQL_Latin1_General_CP1_CI_AS", "Sale", -1},
		{"", "primary", 0}, // unread: the case-insensitive default
		{"Latin1_General_CS_AS", "sales", -1},
		{"Latin1_General_CS_AS", "Sales", 1},
		{"Latin1_General_BIN2", "primary", -1},
	} {
		if got := pendingNameIndex(tt.collation, names, self, tt.name); got != tt.want {
			t.Errorf("%s: %q = %d, want %d", tt.collation, tt.name, got, tt.want)
		}
		if got := pendingNameTaken(tt.collation, names, self, tt.name); got != (tt.want >= 0) {
			t.Errorf("%s: %q taken = %v, want %v", tt.collation, tt.name, got, tt.want >= 0)
		}
	}
}

// pendingAddCollations is each page test's two runs: a case-insensitive
// collation, on which a name differing only in case is refused before Apply
// can fail on the server's "already exists", and a case-sensitive one, on
// which it is a new object and accepted.
var pendingAddCollations = []struct {
	collation string
	refused   bool
}{{"SQL_Latin1_General_CP1_CI_AS", true}, {"Latin1_General_CS_AS", false}}

// TestFilegroupAddFollowsTheDatabaseCollation: the filegroup scope is the
// database's, read with the database itself.
func TestFilegroupAddFollowsTheDatabaseCollation(t *testing.T) {
	for _, tt := range pendingAddCollations {
		responses := filegroupsPageResponses()
		responses[0].rows[0][5] = tt.collation
		responses[0].rows[0][6] = tt.collation // uncontained: names compare under it too
		sc, inst := newFakeConn(t, responses...)
		form, _ := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

		textRow(t, form, "New filegroup name").Edit("staging")
		clickButton(t, form, "Add")

		hint := hintText(t, form)
		if got := strings.Contains(hint, "already listed"); got != tt.refused {
			t.Errorf("%s: \"staging\" beside STAGING refused = %v (hint %q), want %v", tt.collation, got, hint, tt.refused)
		}
	}
}

// TestMailProfileAddFollowsTheServerCollation: Database Mail lives in msdb,
// whose collation the server's stands in for.
func TestMailProfileAddFollowsTheServerCollation(t *testing.T) {
	for _, tt := range pendingAddCollations {
		info := serverInfoResponse()
		info.rows[0][4] = tt.collation
		sc, inst := newFakeConnFrom(t, append([]fakeResponse{info, sysInfoResponse()}, mailPageReads()...))
		form, _ := loadPage(t, pageMailProfiles(sc, &mailModel{}), inst)

		textRow(t, form, "New profile name").Edit("OPS")
		clickButton(t, form, "Add")

		hint := hintText(t, form)
		if got := strings.Contains(hint, "already listed"); got != tt.refused {
			t.Errorf("%s: \"OPS\" beside ops refused = %v (hint %q), want %v", tt.collation, got, hint, tt.refused)
		}
	}
}

// TestRGGroupAddFollowsTheServerCollation: workload group names are unique
// server-wide, under the server's collation.
func TestRGGroupAddFollowsTheServerCollation(t *testing.T) {
	for _, tt := range pendingAddCollations {
		info := serverInfoResponse()
		info.rows[0][4] = tt.collation
		sc, inst := newFakeConnFrom(t, append([]fakeResponse{info, sysInfoResponse()}, rgReads(rgConfig(true))...))
		form, _ := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{}), inst)

		textRow(t, form, "New workload group").Edit("AdHoc")
		clickButton(t, form, "Add")

		hint := hintText(t, form)
		if got := strings.Contains(hint, "already exists"); got != tt.refused {
			t.Errorf("%s: \"AdHoc\" beside adhoc refused = %v (hint %q), want %v", tt.collation, got, hint, tt.refused)
		}
	}
}

// TestPendingNamesRefusal: a name freed by a removal or a rename in the same
// Apply is still taken when the create or rename runs — Database Mail drops
// last, Job Properties deletes after it updates — so the page refuses it
// rather than the server refusing the Apply.
func TestPendingNamesRefusal(t *testing.T) {
	const ci, cs = "SQL_Latin1_General_CP1_CI_AS", "Latin1_General_CS_AS"
	for _, tt := range []struct {
		name, collation string
		rows            []pendingName
		want            string // "" accepts
	}{
		{"unchanged", ci, []pendingName{{"Ops", "Ops", false}, {"B", "B", false}}, ""},
		{"removed, then added again", ci,
			[]pendingName{{"Ops", "Ops", true}, {"", "Ops", false}}, "Ops is still in use until its removal"},
		{"renamed away, then added", ci,
			[]pendingName{{"Ops", "Ops-old", false}, {"", "ops", false}}, "Ops is still in use until its rename to Ops-old"},
		{"renamed onto a removed row's", ci,
			[]pendingName{{"A", "A", true}, {"B", "a", false}}, "A is still in use until its removal"},
		{"two names swapped", ci,
			[]pendingName{{"A", "B", false}, {"B", "A", false}}, "B is still in use until its rename to A"},
		{"Ops and ops, case-insensitive", ci,
			[]pendingName{{"A", "Ops", false}, {"B", "ops", false}}, "two profiles are named ops"},
		{"Ops and ops, case-sensitive", cs, []pendingName{{"A", "Ops", false}, {"B", "ops", false}}, ""},
		{"removed then added, case-sensitive other case", cs,
			[]pendingName{{"Ops", "Ops", true}, {"", "ops", false}}, ""},
		{"a row recased in place", ci, []pendingName{{"ops", "OPS", false}, {"B", "B", false}}, ""},
		{"a removed row's name, freed by nothing else", ci,
			[]pendingName{{"A", "A", true}, {"", "A2", false}}, ""},
		{"a name blanked", ci, []pendingName{{"A", "", false}}, "every profile needs a name"},
		{"a blank row removed", ci, []pendingName{{"", "", true}}, ""},
	} {
		err := pendingNamesRefusal(tt.collation, "profile", tt.rows)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: refused: %v", tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

// pendingTestRow is a page row for the pendingEdits tests: a name and one
// edited value.
type pendingTestRow struct {
	pendingState
	name, origName string
	value, orig    int
}

func newPendingTestEdits(collation string, names ...string) *pendingEdits[*pendingTestRow] {
	loaded := make([]*pendingTestRow, len(names))
	for i, n := range names {
		loaded[i] = &pendingTestRow{name: n, origName: n}
	}
	return newPendingEdits(collation, loaded,
		func(e *pendingTestRow) string { return e.name },
		func(e *pendingTestRow) bool { return e.name != e.origName || e.value != e.orig },
		func(e *pendingTestRow) { e.name, e.value = e.origName, e.orig })
}

func pendingTestNames(rows []*pendingTestRow) string {
	out := make([]string, len(rows))
	for i, e := range rows {
		out[i] = e.name
	}
	return strings.Join(out, ",")
}

// TestPendingEditsLifecycle: what every page used to write for itself — an
// Add is new, a Remove forgets a new row and marks a loaded one, the grid
// lists what is not being removed, Revert puts the list back as loaded.
func TestPendingEditsLifecycle(t *testing.T) {
	p := newPendingTestEdits("", "a", "b", "c")
	if p.dirty() {
		t.Fatal("dirty as loaded")
	}
	b := p.all()[1]
	p.remove(b)
	p.add(&pendingTestRow{name: "d"})
	d := p.all()[3]
	if !d.isNew || !b.removing || !p.dirty() {
		t.Fatalf("after Remove b and Add d: d.isNew=%v b.removing=%v dirty=%v", d.isNew, b.removing, p.dirty())
	}
	if got := pendingTestNames(p.visible()); got != "a,c,d" {
		t.Errorf("visible = %s, want a,c,d", got)
	}
	if got := pendingTestNames(p.all()); got != "a,b,c,d" {
		t.Errorf("all = %s, want a,b,c,d", got)
	}

	// A new row removed is forgotten: nothing for Apply to write.
	p.remove(d)
	if got := pendingTestNames(p.all()); got != "a,b,c" {
		t.Errorf("all after removing new d = %s, want a,b,c", got)
	}
	p.restore(b)
	if p.dirty() {
		t.Error("dirty after the only changes were undone")
	}

	p.all()[0].value = 7
	p.all()[2].name = "c2"
	p.remove(p.all()[1])
	p.add(&pendingTestRow{name: "e"})
	p.swap(p.all()[0], p.all()[2])
	p.revert()
	if got := pendingTestNames(p.all()); got != "a,b,c" {
		t.Errorf("all after Revert = %s, want a,b,c in loaded order", got)
	}
	if p.dirty() || p.all()[0].value != 0 {
		t.Errorf("after Revert: dirty=%v a.value=%d, want clean", p.dirty(), p.all()[0].value)
	}
}

// TestPendingEditsNameChecks: index and listed see only the rows the grid
// lists; taken sees a row being removed too, for a page that creates before
// it drops.
func TestPendingEditsNameChecks(t *testing.T) {
	p := newPendingTestEdits("SQL_Latin1_General_CP1_CI_AS", "Ops", "Sales", "Archive")
	p.remove(p.all()[1])
	for _, tt := range []struct {
		name         string
		index        int
		listed, take bool
	}{
		{"ops", 0, true, true},
		{"ARCHIVE", 1, true, true},
		{"sales", -1, false, true},
		{"other", -1, false, false},
	} {
		if got := p.index(tt.name); got != tt.index {
			t.Errorf("index(%q) = %d, want %d", tt.name, got, tt.index)
		}
		if got := p.listed(tt.name); got != tt.listed {
			t.Errorf("listed(%q) = %v, want %v", tt.name, got, tt.listed)
		}
		if got := p.taken(tt.name); got != tt.take {
			t.Errorf("taken(%q) = %v, want %v", tt.name, got, tt.take)
		}
	}
	if cs := newPendingTestEdits("Latin1_General_CS_AS", "Ops"); cs.listed("ops") {
		t.Error("ops listed beside Ops under a case-sensitive collation")
	}
}

// TestPendingEditsRefusal: a new row has no stored name, whatever stored
// says of it, so it cannot be mistaken for the row it was seeded like.
func TestPendingEditsRefusal(t *testing.T) {
	stored := func(e *pendingTestRow) string { return e.origName }
	p := newPendingTestEdits("SQL_Latin1_General_CP1_CI_AS", "Ops")
	p.remove(p.all()[0])
	p.add(&pendingTestRow{name: "ops", origName: "ops"})
	if err := p.refusal("profile", stored); err == nil || !strings.Contains(err.Error(), "still in use until its removal") {
		t.Errorf("re-adding a removed name: err = %v, want the removal refusal", err)
	}
	p.revert()
	p.add(&pendingTestRow{name: "B", origName: "Ops"})
	if err := p.refusal("profile", stored); err != nil {
		t.Errorf("a new row seeded with a stored name: refused: %v", err)
	}
}
