package tui

import (
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/tuikit/propsheet"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// TestPendingNameIndex: newPendingEdits compares names under the scope's
// collation; Taken also sees a row being removed, Index only what the grid
// lists.
func TestPendingNameIndex(t *testing.T) {
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
		p := newPendingTestEdits(tt.collation, "PRIMARY", "Sales", "sales_archive")
		if got := p.Index(tt.name); got != tt.want {
			t.Errorf("%s: %q = %d, want %d", tt.collation, tt.name, got, tt.want)
		}
		if got := p.Taken(tt.name); got != (tt.want >= 0) {
			t.Errorf("%s: %q taken = %v, want %v", tt.collation, tt.name, got, tt.want >= 0)
		}
	}
	p := newPendingTestEdits("SQL_Latin1_General_CP1_CI_AS", "Ops")
	p.Remove(p.All()[0])
	if p.Listed("ops") || !p.Taken("ops") {
		t.Error("a row being removed: listed, or not taken")
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
		sc, inst := newGConn(t, responses...)
		form, _ := loadPageG(t, pageDatabaseFilegroups(sc, "appdb"), inst)

		textRow(t, form, "New filegroup name").Edit("staging")
		clickButton(t, form, "Add")

		hint := hintText(t, form)
		if got := strings.Contains(hint, "already listed"); got != tt.refused {
			t.Errorf("%s: \"staging\" beside STAGING refused = %v (hint %q), want %v", tt.collation, got, hint, tt.refused)
		}
	}
}

// msdbOpposite is the server collation that disagrees with msdb's: each
// msdb-scoped page test runs its server under it, so a page comparing under
// the server's collation instead of msdb's gets the wrong answer in both runs.
func msdbOpposite(msdb string) string {
	if msdb == pendingAddCollations[0].collation {
		return pendingAddCollations[1].collation
	}
	return pendingAddCollations[0].collation
}

// msdbResponse answers DatabaseByName("msdb") with collation, ahead of any
// broader sys.databases read in the script.
func msdbResponse(collation string) fakeResponse {
	r := dbByNameResp("msdb", 4)
	r.rows[0][5], r.rows[0][6] = collation, collation
	return r
}

// TestMailProfileAndAccountAddFollowTheMsdbCollation: Database Mail profiles
// and accounts live in msdb, so its collation decides a duplicate, not the
// server's (C1 of the 2026-10-26 review).
func TestMailProfileAndAccountAddFollowTheMsdbCollation(t *testing.T) {
	for _, tt := range pendingAddCollations {
		for _, c := range []struct {
			page       func(*db.ServerConn) propPage
			row, typed string
			beside     string
		}{
			{func(sc *db.ServerConn) propPage { return pageMailProfiles(sc, &mailModel{}) }, "New profile name", "OPS", "ops"},
			{func(sc *db.ServerConn) propPage { return pageMailAccounts(sc, &mailModel{}) }, "New account name", "RELAY1", "relay1"},
		} {
			sc, inst := newGConnWithMsdb(t, tt.collation, mailPageReads()...)
			form, _ := loadPageG(t, c.page(sc), inst)

			textRow(t, form, c.row).Edit(c.typed)
			clickButton(t, form, "Add")

			hint := hintText(t, form)
			if got := strings.Contains(hint, "already listed"); got != tt.refused {
				t.Errorf("msdb %s: %q beside %s refused = %v (hint %q), want %v", tt.collation, c.typed, c.beside, got, hint, tt.refused)
			}
		}
	}
}

// TestRGGroupAddFollowsTheServerCollation: workload group names are unique
// server-wide, under the server's collation.
func TestRGGroupAddFollowsTheServerCollation(t *testing.T) {
	for _, tt := range pendingAddCollations {
		sc, inst := newGConnWith(t, func(i *gosmotest.Info) { i.Collation = tt.collation }, rgReads(rgConfig(true))...)
		form, _ := loadPageG(t, pageRGGroups(sc, &rgModel{}, rgFocus{}), inst)

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

// pendingTestRow is a page row for the newPendingEdits tests: a name and one
// edited value (propsheet's own tests cover the list's lifecycle).
type pendingTestRow struct {
	propsheet.PendingState
	name, origName string
	value, orig    int
}

func newPendingTestEdits(collation string, names ...string) *propsheet.PendingEdits[*pendingTestRow] {
	loaded := make([]*pendingTestRow, len(names))
	for i, n := range names {
		loaded[i] = &pendingTestRow{name: n, origName: n}
	}
	return newPendingEdits(collation, loaded,
		func(e *pendingTestRow) string { return e.name },
		func(e *pendingTestRow) bool { return e.name != e.origName || e.value != e.orig },
		func(e *pendingTestRow) { e.name, e.value = e.origName, e.orig })
}

// TestPendingEditsRefusal: a new row has no stored name, whatever stored
// says of it, so it cannot be mistaken for the row it was seeded like.
func TestPendingEditsRefusal(t *testing.T) {
	const ci = "SQL_Latin1_General_CP1_CI_AS"
	stored := func(e *pendingTestRow) string { return e.origName }
	p := newPendingTestEdits(ci, "Ops")
	p.Remove(p.All()[0])
	p.Add(&pendingTestRow{name: "ops", origName: "ops"})
	if err := pendingRefusal(ci, p, "profile", stored); err == nil || !strings.Contains(err.Error(), "still in use until its removal") {
		t.Errorf("re-adding a removed name: err = %v, want the removal refusal", err)
	}
	p.Revert()
	p.Add(&pendingTestRow{name: "B", origName: "Ops"})
	if err := pendingRefusal(ci, p, "profile", stored); err != nil {
		t.Errorf("a new row seeded with a stored name: refused: %v", err)
	}
}
