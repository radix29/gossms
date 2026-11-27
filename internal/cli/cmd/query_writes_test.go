package cmd

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
)

// writeTargetTable is one dbo table of writeTargetServer's, with the AFTER
// UPDATE trigger it has ("" for none).
type writeTargetTable struct {
	id            int
	name, trigger string
}

// writeTargetServer is a server whose catalog compares every name exactly —
// a case-sensitive one — holding dbs' tables, run's the connection's database.
func writeTargetServer(t *testing.T, collation, run string, dbs map[string][]writeTargetTable) *gosmo.Server {
	f := gosmotest.New(t)
	f.Info.Collation = collation
	table := func(q gosmotest.Query, name string) *writeTargetTable {
		for i, tb := range dbs[q.Database] {
			if tb.name == name {
				return &dbs[q.Database][i]
			}
		}
		return nil
	}
	rows := func(fn func(gosmotest.Query) [][]any) gosmotest.Responder {
		return func(q gosmotest.Query) gosmotest.Result {
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: fn(q)}}}
		}
	}
	f.On("SELECT DB_NAME()", gosmotest.Rows([]any{run}))
	f.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM sys.databases", "WHERE name = @p1") },
		rows(func(q gosmotest.Query) [][]any {
			if _, ok := dbs[argString(q, 0)]; !ok {
				return nil
			}
			return [][]any{clitest.DatabaseRow(argString(q, 0), 5, catalogAt)}
		}))
	f.OnFunc(func(q gosmotest.Query) bool {
		return has(q, "OBJECT_SCHEMA_NAME(o.parent_object_id)", "FROM   sys.all_objects o")
	},
		rows(func(q gosmotest.Query) [][]any {
			name := argString(q, 0)
			if has(q, "@p2") {
				if argString(q, 0) != "dbo" {
					return nil
				}
				name = argString(q, 1)
			}
			tb := table(q, name)
			if tb == nil {
				return nil
			}
			return [][]any{clitest.ObjectInfoRow(&gosmo.ObjectInfo{ObjectID: tb.id, Schema: "dbo", Name: tb.name, Type: "U", TypeDesc: "USER_TABLE"})}
		}))
	f.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.tables t", "AND  t.name") },
		rows(func(q gosmotest.Query) [][]any {
			tb := table(q, argString(q, 1))
			if tb == nil {
				return nil
			}
			return [][]any{clitest.TableRow(&gosmo.Table{ObjectID: tb.id, Schema: "dbo", Name: tb.name}, catalogAt)}
		}))
	f.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.triggers tr") },
		rows(func(q gosmotest.Query) [][]any {
			id, _ := q.Arg(0).(int64)
			for _, tb := range dbs[q.Database] {
				if tb.id == int(id) && tb.trigger != "" {
					return [][]any{{tb.trigger, tb.name, "dbo", false, `[{"v":"UPDATE"}]`}}
				}
			}
			return nil
		}))
	f.On("FROM   sys.foreign_keys fk", gosmotest.Rows())
	srv, err := f.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// checkWrites runs --max-rows-affected's server-side check of script.
func checkWrites(t *testing.T, srv *gosmo.Server, script string) *safety.Blocked {
	t.Helper()
	p := safety.Policy{Allow: spec.ClassWrite, Transaction: true, MaxRowsAffected: true}
	sc := safety.Classify(script)
	if b := safety.Check(sc, p); b != nil {
		t.Fatalf("refused offline: %s", b.Message)
	}
	b, err := checkWriteTargets(context.Background(), srv, sc, script, p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWriteTargetsCaseSensitiveDatabase: in a _CS_ database t and T are two
// tables, so T's trigger refuses its write although t, written first under
// the other spelling, passed.
func TestWriteTargetsCaseSensitiveDatabase(t *testing.T) {
	srv := writeTargetServer(t, "Latin1_General_CI_AS", "cs", map[string][]writeTargetTable{
		"cs": {{id: 1, name: "t"}, {id: 2, name: "T", trigger: "tr_T"}},
	})
	b := checkWrites(t, srv, "UPDATE t SET a = 1; UPDATE T SET a = 1")
	if b == nil || !strings.Contains(b.Describe(), "has trigger [dbo].[tr_T]") || strings.Contains(b.Describe(), "UPDATE t ") {
		t.Errorf("refusal %v, want T's write refused for tr_T and t's not", b)
	}
}

// TestWriteTargetsCaseSensitiveServer: on a case-sensitive server the run's
// database sales and the script's USE Sales are two databases, both checked.
func TestWriteTargetsCaseSensitiveServer(t *testing.T) {
	dbs := map[string][]writeTargetTable{
		"sales": {{id: 1, name: "x"}},
		"Sales": {{id: 1, name: "x", trigger: "tr_x"}},
	}
	script := "UPDATE x SET a = 1\nUSE Sales\nUPDATE x SET a = 1"
	for _, tc := range []struct {
		collation string
		refused   bool
	}{
		{"Latin1_General_CS_AS", true},
		// A case-insensitive server's sales is Sales: the USE adds nothing,
		// and the catalog's sales has no trigger.
		{"Latin1_General_CI_AS", false},
	} {
		t.Run(tc.collation, func(t *testing.T) {
			b := checkWrites(t, writeTargetServer(t, tc.collation, "sales", dbs), script)
			if got := b != nil && strings.Contains(b.Describe(), "has trigger [dbo].[tr_x]"); got != tc.refused {
				t.Errorf("refusal %v, want refused %v", b, tc.refused)
			}
		})
	}
}
