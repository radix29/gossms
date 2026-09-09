package tui

import (
	"slices"
	"testing"
)

// TestResourceGovernanceIsAnAzureOnlyPage. The page reads
// sys.dm_user_db_resource_governance and sys.dm_db_resource_stats, neither of
// which exists off an Azure engine edition — offering it elsewhere would put a
// page in the dialog that can only ever load an "invalid object name".
func TestResourceGovernanceIsAnAzureOnlyPage(t *testing.T) {
	const title = "Resource Governance"

	azure := probedAzureConn(t, "appdb")
	if got := propPageTitles(databasePropPages(azure, "appdb")); !slices.Contains(got, title) {
		t.Errorf("Database Properties on a Managed Instance has no %q page: %v", title, got)
	}

	onPrem := probedConn(t, "appdb",
		[]string{"ALTER ANY DATABASE", "CONTROL SERVER"}, nil,
		[]string{"ALTER", "CONTROL"}, nil)
	got := propPageTitles(databasePropPages(onPrem, "appdb"))
	if slices.Contains(got, title) {
		t.Errorf("%q is offered on an on-premises instance, which has neither view: %v", title, got)
	}
	// The edition must add a page, never reorder or drop one.
	for _, want := range []string{"General", "Files", "Filegroups", "Options",
		"Change Tracking", "Query Store", "Permissions", "Extended Properties",
		"Database Scoped Configurations"} {
		if !slices.Contains(got, want) {
			t.Errorf("on-premises Database Properties lost its %q page: %v", want, got)
		}
	}
	azureTitles := propPageTitles(databasePropPages(azure, "appdb"))
	if len(azureTitles) != len(got)+1 {
		t.Errorf("Azure page set is %v, on-premises %v — want exactly one more page", azureTitles, got)
	}
	if azureTitles[len(azureTitles)-1] != title {
		t.Errorf("%q is not last: %v", title, azureTitles)
	}
}

// propPageTitles is the page titles in dialog order.
func propPageTitles(pages []propPage) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.title
	}
	return out
}
