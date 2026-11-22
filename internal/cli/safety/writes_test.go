package safety

import (
	"slices"
	"strings"
	"testing"
)

// writesOf is each write of script as "VERB parts|why", parts joined by '.'.
func writesOf(script string) []string {
	var out []string
	for _, w := range Classify(script).Writes() {
		s := w.Verb + " " + strings.Join(w.Parts, ".")
		if w.Unresolvable != "" {
			s = w.Verb + " " + w.Target + "|" + w.Unresolvable
		}
		out = append(out, s)
	}
	return out
}

func TestWritesNameTheTableWritten(t *testing.T) {
	for _, tt := range []struct {
		script string
		want   []string
	}{
		{"UPDATE dbo.t SET a = 1", []string{"UPDATE dbo.t"}},
		{"update [Sales].[Order Detail] set a = 1", []string{"UPDATE Sales.Order Detail"}},
		{"DELETE FROM t WHERE id = 1", []string{"DELETE t"}},
		{"DELETE TOP (5) FROM dbo.t", []string{"DELETE dbo.t"}},
		{"INSERT INTO db.dbo.t (a) VALUES (1)", []string{"INSERT db.dbo.t"}},
		{"INSERT db..t VALUES (1)", []string{"INSERT db..t"}},
		{"INSERT #t VALUES (1); INSERT @t VALUES (1)", []string{"INSERT #t", "INSERT @t"}},
		{"MERGE dbo.t AS tgt USING dbo.s AS src ON tgt.id = src.id\n" +
			"WHEN MATCHED THEN UPDATE SET a = src.a\nWHEN NOT MATCHED THEN INSERT (id) VALUES (src.id)\n" +
			"WHEN NOT MATCHED BY SOURCE THEN DELETE;", []string{"MERGE dbo.t"}},
		// Nested behind IF and inside BEGIN … END.
		{"IF @x = 1 UPDATE dbo.t SET a = 1 ELSE DELETE dbo.u", []string{"UPDATE dbo.t", "DELETE dbo.u"}},
		{"WHILE 1 = 1 BEGIN DELETE TOP (100) dbo.t; IF @@ROWCOUNT = 0 BREAK; END", []string{"DELETE dbo.t"}},
		// An alias its FROM clause defines.
		{"UPDATE c SET a = 1 FROM dbo.Customer AS c JOIN dbo.o o ON o.c = c.id", []string{"UPDATE dbo.Customer"}},
		{"DELETE c FROM dbo.o o JOIN Sales.Customer c ON o.c = c.id", []string{"DELETE Sales.Customer"}},
		{"DELETE FROM c FROM dbo.Customer c", []string{"DELETE dbo.Customer"}},
		{"UPDATE c SET a = (SELECT x c FROM dbo.z) FROM dbo.Customer c", []string{"UPDATE dbo.Customer"}},
		// A name no FROM clause aliases is the table itself.
		{"UPDATE t SET a = 1 FROM t JOIN dbo.u ON u.id = t.id", []string{"UPDATE t"}},
		// Not a write of the statement's own.
		{"GRANT INSERT, UPDATE ON dbo.t TO u", nil},
		{"GRANT UPDATE (a) ON dbo.t TO u", nil},
		{"CREATE TABLE dbo.c (p INT REFERENCES dbo.p (id) ON DELETE CASCADE ON UPDATE NO ACTION)", nil},
		{"DECLARE c CURSOR FOR SELECT a FROM dbo.t FOR UPDATE OF a", nil},
		{"CREATE TRIGGER tr ON dbo.t AFTER INSERT, UPDATE AS UPDATE dbo.u SET a = 1", nil},
		{"SELECT * FROM dbo.t WITH (UPDLOCK) OPTION (USE HINT ('DISABLE_OPTIMIZED_NESTED_LOOP'))", nil},
	} {
		if got := writesOf(tt.script); !slices.Equal(got, tt.want) {
			t.Errorf("%q:\n got %q\nwant %q", tt.script, got, tt.want)
		}
	}
}

func TestWritesTheCheckCannotName(t *testing.T) {
	for _, tt := range []struct {
		script, why string
	}{
		{"WITH c AS (SELECT * FROM dbo.t) UPDATE c SET a = 1", "common table expression"},
		{"WITH c (x) AS (SELECT a FROM dbo.t), d AS (SELECT 1 y) DELETE FROM d", "common table expression"},
		{"UPDATE d SET a = 1 FROM (SELECT * FROM dbo.t) AS d", "derived table"},
		{"DELETE j FROM dbo.t CROSS APPLY OPENJSON(t.doc) j", "derived table"},
		{"UPDATE x SET a = 1 FROM dbo.t x JOIN dbo.u x2 ON 1 = 1, dbo.v x", "derived table"},
		{"UPDATE srv.db.dbo.t SET a = 1", "linked server"},
		{"UPDATE c SET a = 1 FROM srv.db.dbo.t c", "linked server"},
		{"INSERT OPENQUERY(srv, 'SELECT a FROM t') VALUES (1)", "rowset function"},
		{"UPDATE 42 SET a = 1", "no table name"},
	} {
		w := Classify(tt.script).Writes()
		if len(w) != 1 || !strings.Contains(w[0].Unresolvable, tt.why) {
			t.Errorf("%q: writes %+v, want one unresolvable as %q", tt.script, w, tt.why)
		}
	}
}

func TestWriteLocal(t *testing.T) {
	for _, tt := range []struct {
		script string
		local  bool
	}{
		{"INSERT #t VALUES (1)", true},
		{"INSERT ##t VALUES (1)", true},
		{"DELETE @t", true},
		{"INSERT tempdb..#t VALUES (1)", true},
		{"INSERT dbo.t VALUES (1)", false},
	} {
		if w := Classify(tt.script).Writes(); len(w) != 1 || w[0].Local() != tt.local {
			t.Errorf("%q: writes %+v, want one with Local %v", tt.script, w, tt.local)
		}
	}
}

// --max-rows-affected refuses, before connecting, a write whose table the
// check cannot name; a nameable one passes to the server-side check.
func TestCheckRefusesUnnameableWritesUnderMaxRows(t *testing.T) {
	p := Policy{Allow: write, Transaction: true, MaxRowsAffected: true}
	if b := Check(Classify("UPDATE dbo.t SET a = 1\nDELETE c FROM dbo.Customer c"), p); b != nil {
		t.Errorf("nameable writes refused: %s", b.Message)
	}
	b := Check(Classify("SELECT 1\nWITH c AS (SELECT * FROM dbo.t)\nUPDATE c SET a = 1"), p)
	if b == nil {
		t.Fatal("a CTE's write was not refused")
	}
	if want := "UPDATE c (line 3): a common table expression, whose base tables the check cannot see"; b.Describe() != want {
		t.Errorf("Describe() = %q\nwant         %q", b.Describe(), want)
	}
	if !strings.Contains(b.Message, "triggers and cascading foreign keys") {
		t.Errorf("message %q", b.Message)
	}
	// Without the limit there is nothing to count.
	if b := Check(Classify("WITH c AS (SELECT * FROM dbo.t) UPDATE c SET a = 1"), Policy{Allow: write, Transaction: true}); b != nil {
		t.Errorf("refused without --max-rows-affected: %s", b.Message)
	}
}

func TestNameParts(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"[db].[dbo].[t]", []string{"db", "dbo", "t"}},
		{"[srv].[db].[dbo].[t]", []string{"srv", "db", "dbo", "t"}},
		{"dbo.[a]]b]", []string{"dbo", "a]b"}},
		{"t", []string{"t"}},
		{"db..t", []string{"db", "", "t"}},
		{"a.b.c.d.e", nil},
		{"dbo.t x", nil},
		{"", nil},
	} {
		got, ok := NameParts(tt.in)
		if ok != (tt.want != nil) || !slices.Equal(got, tt.want) {
			t.Errorf("NameParts(%q) = %q, %v; want %q", tt.in, got, ok, tt.want)
		}
	}
}

func TestUseDatabases(t *testing.T) {
	script := "USE app\nGO\nIF @x = 1 USE [Other DB] ELSE USE APP\n" +
		"SELECT 1 OPTION (USE HINT ('X'))\nSELECT 1 OPTION (USE PLAN N'<x/>')\n-- USE commented\nSELECT 'USE quoted'"
	if got, want := UseDatabases(script), []string{"app", "Other DB"}; !slices.Equal(got, want) {
		t.Errorf("UseDatabases = %q, want %q", got, want)
	}
}
