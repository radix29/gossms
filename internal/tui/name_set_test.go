package tui

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"
)

func TestNameSetFollowsCollation(t *testing.T) {
	tests := []struct {
		collation string
		fold      bool
	}{
		{"", true}, // not read: the install default
		{"SQL_Latin1_General_CP1_CI_AS", true},
		{"Latin1_General_100_CI_AS_KS_WS_SC_UTF8", true},
		{"Latin1_General_CS_AS", false},
		{"SQL_Latin1_General_CP1_CS_AS", false},
		{"Japanese_Bushu_Kakusu_140_CS_AS_KS_WS", false},
		{"Latin1_General_BIN", false},
		{"Latin1_General_100_BIN2_UTF8", false},
		{"Czech_CI_AS", true},
	}
	for _, tt := range tests {
		s := newNameSet(tt.collation, "Sales")
		if !s.Has("Sales") {
			t.Errorf("%q: the exact name is not found", tt.collation)
		}
		if got := s.Has("sales"); got != tt.fold {
			t.Errorf("%q: Has(\"sales\") beside \"Sales\" = %v, want %v", tt.collation, got, tt.fold)
		}
	}
	var none *nameSet
	if none.Has("x") {
		t.Error("a nil set claims a name")
	}
}

// sameName and nameMap are nameSet's one-off and keyed forms, and must fold
// exactly when it does.
func TestSameNameAndNameMapFollowCollation(t *testing.T) {
	for _, tt := range []struct {
		collation string
		fold      bool
	}{{"", true}, {"SQL_Latin1_General_CP1_CI_AS", true}, {"Latin1_General_CS_AS", false}, {"Latin1_General_BIN2", false}} {
		if got := sameName(tt.collation, "Sales", "sales"); got != tt.fold {
			t.Errorf("%q: sameName(Sales, sales) = %v, want %v", tt.collation, got, tt.fold)
		}
		if !sameName(tt.collation, "Sales", "Sales") {
			t.Errorf("%q: sameName of identical names is false", tt.collation)
		}
		m := newNameMap[int](tt.collation)
		m.Set("Sales", 1)
		m.Set("sales", 2)
		if want := map[bool]int{true: 1, false: 2}[tt.fold]; m.Len() != want {
			t.Errorf("%q: Sales and sales make %d keys, want %d", tt.collation, m.Len(), want)
		}
		if v, ok := m.Get("Sales"); !ok || v != map[bool]int{true: 2, false: 1}[tt.fold] {
			t.Errorf("%q: Get(Sales) = %d, %v", tt.collation, v, ok)
		}
	}
	var none *nameMap[int]
	if _, ok := none.Get("x"); ok || none.Len() != 0 {
		t.Error("a nil map claims a name")
	}
}

// On a case-sensitive instance, a database differing only in case from one
// already in a group is a different database, and is offered.
func TestAGEligibleDatabasesFollowsServerCollation(t *testing.T) {
	dbs := []agDBCandidate{{Name: "Sales", RecoveryModel: "FULL", State: "ONLINE", LogChainStarted: true}}
	for _, tt := range []struct {
		collation string
		eligible  bool
	}{{"SQL_Latin1_General_CP1_CI_AS", false}, {"Latin1_General_CS_AS", true}} {
		eligible, _ := agEligibleDatabases(dbs, newNameSet(tt.collation, "sales"))
		if got := len(eligible) == 1; got != tt.eligible {
			t.Errorf("%q: Sales beside grouped sales eligible = %v, want %v", tt.collation, got, tt.eligible)
		}
	}
}

// csDatabaseRow answers DatabaseByName(name) with a case-sensitive collation.
func csDatabaseRow(name string) fakeResponse {
	return fakeResponse{match: "compatibility_level, collation_name", arg: name, cols: 9, rows: [][]driver.Value{{
		name, int64(7), "ONLINE", "FULL", int64(160), "Latin1_General_CS_AS", false,
		time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC), int64(0)}}}
}

// A server object's duplicate check follows the *server's* collation: on a
// CS instance, a credential differing only in case is a new one.
func TestNewCredentialPrefetchFollowsServerCollation(t *testing.T) {
	now := time.Now()
	creds := fakeResponse{match: "FROM   sys.credentials", cols: 7, rows: [][]driver.Value{
		{int64(1), "Sales", `DOMAIN\svc`, now, now, nil, nil},
	}}
	for _, tt := range []struct {
		collation string
		taken     bool
	}{{"SQL_Latin1_General_CP1_CI_AS", true}, {"Latin1_General_CS_AS", false}} {
		info := serverInfoResponse()
		info.rows[0][4] = tt.collation
		sc, _ := newFakeConnFrom(t, []fakeResponse{info, sysInfoResponse(), creds})
		pf, err := fetchNewCredentialPrefetch(context.Background(), sc)
		if err != nil {
			t.Fatalf("%s: %v", tt.collation, err)
		}
		if got := pf.existingNames.Has("sales"); got != tt.taken {
			t.Errorf("%s: \"sales\" beside \"Sales\" taken = %v, want %v", tt.collation, got, tt.taken)
		}
	}
}

// A database object's check follows the *database's* collation, which a CI
// server does not override: a CS database takes "sales" beside "Sales".
func TestNewDBScopedCredPrefetchFollowsDatabaseCollation(t *testing.T) {
	now := time.Now()
	sc, _ := newFakeConn(t, csDatabaseRow("appdb"),
		fakeResponse{match: "FROM   sys.database_scoped_credentials", cols: 5, rows: [][]driver.Value{
			{int64(1), "Sales", "identity", now, now},
		}})
	pf, err := fetchNewDBScopedCredPrefetch(context.Background(), sc, "appdb")
	if err != nil {
		t.Fatal(err)
	}
	if !pf.existingNames.Has("Sales") || pf.existingNames.Has("sales") {
		t.Errorf("CS database on a CI server: Has(Sales)=%v Has(sales)=%v, want true/false",
			pf.existingNames.Has("Sales"), pf.existingNames.Has("sales"))
	}
}

// An Agent object's check follows msdb's collation, read by name.
func TestNewOperatorPrefetchFollowsMsdbCollation(t *testing.T) {
	responses := append(agentOperatorResponses(), agentCategoryResponse(), csDatabaseRow("msdb"))
	sc, _ := newFakeConn(t, responses...)
	pf, err := fetchNewOperatorPrefetch(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.existingNames.Has(agentOperatorName) || pf.existingNames.Has("REPORTING") {
		t.Errorf("CS msdb on a CI server: Has(%s)=%v Has(REPORTING)=%v, want true/false",
			agentOperatorName, pf.existingNames.Has(agentOperatorName), pf.existingNames.Has("REPORTING"))
	}
}
