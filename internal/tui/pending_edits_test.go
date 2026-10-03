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
