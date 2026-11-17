package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// TestTableOwnerTextMarksAnInheritedOwner: Table Properties' Owner names the
// table's own owner as is, and says when the name is the schema's owner the
// table inherits (review plan X2).
func TestTableOwnerTextMarksAnInheritedOwner(t *testing.T) {
	for _, c := range []struct {
		d    gosmo.TableDetail
		want string
	}{
		{gosmo.TableDetail{Owner: "x2_owner"}, "x2_owner"},
		{gosmo.TableDetail{Owner: "dbo", OwnedBySchemaOwner: true}, "dbo (schema owner)"},
	} {
		if got := tableOwnerText(&c.d); got != c.want {
			t.Errorf("tableOwnerText(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// A hypothetical (tuning advisor) index is counted but called out: it has no
// data, so a bare count overstated what the table carries.
func TestIndexCountTextCallsOutHypothetical(t *testing.T) {
	real, hypo := &gosmo.Index{Name: "IX"}, &gosmo.Index{Name: "IX_dta", IsHypothetical: true}
	for _, c := range []struct {
		idxs []*gosmo.Index
		want string
	}{
		{nil, "0"},
		{[]*gosmo.Index{real}, "1"},
		{[]*gosmo.Index{real, hypo, hypo}, "3 (2 hypothetical)"},
	} {
		if got := indexCountText(c.idxs); got != c.want {
			t.Errorf("indexCountText(%d indexes) = %q, want %q", len(c.idxs), got, c.want)
		}
	}

}

func tablePropertiesResponses() []fakeResponse {
	return []fakeResponse{
		dbByNameResp(idxDatabase, 5),
		{match: "t.lock_escalation_desc", db: idxDatabase, cols: 11, rows: [][]driver.Value{{
			"dbo", true, "TABLE", true, false, false, "NON_TEMPORAL_TABLE",
			"SCHEMA_AND_DATA", "NON_LEDGER_TABLE", "PK_Orders", "PRIMARY",
		}}},
		idxTableResp(),
		{match: "SELECT SUM(p.rows)", db: idxDatabase, cols: 1, rows: [][]driver.Value{{int64(19)}}},
		{match: "AS reserved_kb", db: idxDatabase, cols: 6, rows: [][]driver.Value{{
			int64(80), int64(32), int64(16), int64(8), int64(24), "PRIMARY",
		}}},
		{match: "SELECT p.partition_number", db: idxDatabase, cols: 3, rows: [][]driver.Value{{
			int64(1), int64(19), "PAGE",
		}}},
		{match: "FROM   sys.foreign_keys fk", db: idxDatabase},
		{match: "FROM   sys.indexes i", db: idxDatabase},
		{match: "FROM   sys.triggers tr", db: idxDatabase},
	}
}

func TestTablePropertiesFabricRowCountIsOptional(t *testing.T) {
	for _, page := range []string{"General", "Storage"} {
		for _, fabric := range []bool{false, true} {
			for _, unavailable := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/Fabric=%v/Unavailable=%v", page, fabric, unavailable), func(t *testing.T) {
					responses := tablePropertiesResponses()
					if unavailable {
						for i := range responses {
							if responses[i].match == "SELECT SUM(p.rows)" {
								responses[i].rows = [][]driver.Value{{nil}}
							}
						}
					}
					sc, inst := newGConn(t, responses...)
					sc.IsFabricDW, sc.Server.IsFabricDW = fabric, fabric
					p := pageTableGeneral(sc, idxDatabase, idxSchema, idxTable)
					if page == "Storage" {
						p = pageTableStorage(sc, idxDatabase, idxSchema, idxTable)
					}
					form, _, err := p.load(context.Background())
					if unavailable && !fabric {
						if err == nil || !strings.Contains(err.Error(), "row count") {
							t.Fatalf("load error = %v, want row count error", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					want := "19"
					if unavailable {
						want = "n/a"
					}
					if got := staticValue(t, form, "Row count"); got != want {
						t.Errorf("Row count = %q, want %q", got, want)
					}
					if page == "General" {
						if got := staticValue(t, form, "Name"); got != idxTable {
							t.Errorf("Name = %q, want %q", got, idxTable)
						}
						if got := staticValue(t, form, "Primary key"); got != "PK_Orders" {
							t.Errorf("Primary key = %q, want PK_Orders", got)
						}
					} else {
						if got := staticValue(t, form, "Reserved (KB)"); got != "80" {
							t.Errorf("Reserved (KB) = %q, want 80", got)
						}
						if got := gridRowsOf(t, form); len(got) != 1 || got[0][1] != "19" {
							t.Errorf("Partitions = %v, want partition with 19 rows", got)
						}
					}
					assertNoStatementsIn(t, inst, idxDatabase)
				})
			}
		}
	}
}

func TestTableStorageFabricSpaceAndPartitionsAreOptional(t *testing.T) {
	for _, match := range []string{"AS reserved_kb", "SELECT p.partition_number", "t.lock_escalation_desc", "all optional metadata"} {
		for _, fabric := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/Fabric=%v", match, fabric), func(t *testing.T) {
				wantErr := errors.New("metadata not available")
				responses := tablePropertiesResponses()
				allUnavailable := match == "all optional metadata"
				for i := range responses {
					read := responses[i].match
					if read == match || allUnavailable && (read == "AS reserved_kb" || read == "SELECT SUM(p.rows)" || read == "SELECT p.partition_number") {
						responses[i].err = wantErr
					}
				}
				sc, _ := newGConn(t, responses...)
				sc.IsFabricDW, sc.Server.IsFabricDW = fabric, fabric
				p := pageTableStorage(sc, idxDatabase, idxSchema, idxTable)
				if match == "t.lock_escalation_desc" {
					p = pageTableGeneral(sc, idxDatabase, idxSchema, idxTable)
				}
				form, _, err := p.load(context.Background())
				if !fabric || match == "t.lock_escalation_desc" {
					if !errors.Is(err, wantErr) {
						t.Fatalf("load error = %v, want %v", err, wantErr)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				wantCount := "19"
				if allUnavailable {
					wantCount = "n/a"
				}
				if got := staticValue(t, form, "Row count"); got != wantCount {
					t.Errorf("Row count = %q, want %q", got, wantCount)
				}
				if match == "AS reserved_kb" || allUnavailable {
					for _, label := range []string{"Reserved (KB)", "Data (KB)", "Indexes (KB)", "LOB data (KB)", "Unused (KB)", "Filegroup"} {
						if got := staticValue(t, form, label); got != "n/a" {
							t.Errorf("%s = %q, want n/a", label, got)
						}
					}
				}
				if match == "SELECT p.partition_number" || allUnavailable {
					if got := gridRowsOf(t, form); len(got) != 0 {
						t.Errorf("unavailable partitions = %v, want empty", got)
					}
				}
			})
		}
	}
}

func TestTablePropertiesPropagateMetadataFailures(t *testing.T) {
	for _, page := range []string{"General", "Storage"} {
		matches := []string{"SELECT SUM(p.rows)"}
		if page == "Storage" {
			matches = append(matches, "AS reserved_kb", "SELECT p.partition_number")
		}
		for _, match := range matches {
			for _, cause := range []error{
				context.Canceled, context.DeadlineExceeded, io.EOF, driver.ErrBadConn,
				&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")},
			} {
				t.Run(fmt.Sprintf("%s/%s/%v", page, match, cause), func(t *testing.T) {
					responses := tablePropertiesResponses()
					for i := range responses {
						if responses[i].match == match {
							responses[i].err = fmt.Errorf("nested metadata read: %w", cause)
						}
					}
					sc, _ := newGConn(t, responses...)
					sc.IsFabricDW, sc.Server.IsFabricDW = true, true
					p := pageTableGeneral(sc, idxDatabase, idxSchema, idxTable)
					if page == "Storage" {
						p = pageTableStorage(sc, idxDatabase, idxSchema, idxTable)
					}
					form, apply, err := p.load(context.Background())
					if !errors.Is(err, cause) || form != nil || apply != nil {
						t.Fatalf("load returned form=%v apply=%v error=%v, want only %v", form != nil, apply != nil, err, cause)
					}
				})
			}
		}
	}
}

func TestFabricPropertyFallbackDoesNotAffectAzureSQL(t *testing.T) {
	for _, edition := range []int{5, 8} {
		for _, tc := range []struct {
			page  string
			match string
		}{
			{"Database General", "AS total_mb"},
			{"Database General", "msdb.dbo.backupset"},
			{"Table General", "SELECT SUM(p.rows)"},
			{"Table Storage", "AS reserved_kb"},
			{"Table Storage", "SELECT SUM(p.rows)"},
			{"Table Storage", "SELECT p.partition_number"},
		} {
			t.Run(fmt.Sprintf("Edition=%d/%s/%s", edition, tc.page, tc.match), func(t *testing.T) {
				responses := tablePropertiesResponses()
				if tc.page == "Database General" {
					responses = databaseGeneralResponses("appuser")
				}
				want := errors.New("metadata read failed")
				for i := range responses {
					if responses[i].match == tc.match {
						responses[i].err = want
					}
				}

				sc, _ := newGConnOnAzure(t, edition, responses...)
				var p propPage
				switch tc.page {
				case "Database General":
					p = pageDatabaseGeneral(sc, genDatabase)
				case "Table General":
					p = pageTableGeneral(sc, idxDatabase, idxSchema, idxTable)
				case "Table Storage":
					p = pageTableStorage(sc, idxDatabase, idxSchema, idxTable)
				}
				form, apply, err := p.load(context.Background())
				if !errors.Is(err, want) || form != nil || apply != nil {
					t.Fatalf("load returned form=%v apply=%v error=%v, want only %v", form != nil, apply != nil, err, want)
				}
			})
		}
	}
}

func TestFabricTableZeroMetadataRemainsVisible(t *testing.T) {
	for _, page := range []string{"General", "Storage"} {
		t.Run(page, func(t *testing.T) {
			responses := tablePropertiesResponses()
			for i := range responses {
				switch responses[i].match {
				case "SELECT SUM(p.rows)":
					responses[i].rows = [][]driver.Value{{int64(0)}}
				case "AS reserved_kb":
					responses[i].rows = [][]driver.Value{{int64(0), int64(0), int64(0), int64(0), int64(0), "PRIMARY"}}
				case "SELECT p.partition_number":
					responses[i].rows = [][]driver.Value{{int64(1), int64(0), "NONE"}}
				}
			}
			sc, inst := newGConn(t, responses...)
			sc.IsFabricDW, sc.Server.IsFabricDW = true, true
			p := pageTableGeneral(sc, idxDatabase, idxSchema, idxTable)
			if page == "Storage" {
				p = pageTableStorage(sc, idxDatabase, idxSchema, idxTable)
			}
			form, _ := loadPageG(t, p, inst)
			if got := staticValue(t, form, "Row count"); got != "0" {
				t.Errorf("Row count = %q, want 0", got)
			}
			if page == "Storage" {
				for _, label := range []string{"Reserved (KB)", "Data (KB)", "Indexes (KB)", "LOB data (KB)", "Unused (KB)"} {
					if got := staticValue(t, form, label); got != "0" {
						t.Errorf("%s = %q, want 0", label, got)
					}
				}
				if got := gridRowsOf(t, form); len(got) != 1 || got[0][0] != "1" || got[0][1] != "0" || got[0][2] != "NONE" {
					t.Errorf("partitions = %v, want one empty partition", got)
				}
			}
		})
	}
}
