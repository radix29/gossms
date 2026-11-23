package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

func TestSQLScalarExpressionPositions(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		for _, tt := range []struct {
			sql  string
			want bool
		}{
			{"SELECT sub| FROM dbo.Customers", true},
			{"SELECT * FROM dbo.Customers WHERE sub|", true},
			{"SELECT * FROM dbo.Customers WHERE Id = sub|", true},
			{"SELECT * FROM dbo.Customers WHERE Id = 1 AND sub|", true},
			{"SELECT * FROM dbo.Customers WHERE NOT sub|", true},
			{"SELECT * FROM dbo.Customers WHERE Id + sub|", true},
			{"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM sub|", true},
			{"SELECT * FROM dbo.Customers WHERE Id IN (sub|)", true},
			{"SELECT * FROM dbo.Customers WHERE (sub|)", true},
			{"SELECT * FROM dbo.Customers WHERE Id = (sub|)", true},
			{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON sub|", true},
			{"UPDATE dbo.Customers SET Name = sub|", true},
			{"UPDATE dbo.Customers SET Id = 1, Name = sub|", true},
			{"SELECT Name FROM dbo.Customers GROUP BY sub|", !fabric},
			{"SELECT Name FROM dbo.Customers GROUP BY ROLLUP(sub|)", !fabric},
			{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING sub|", true},
			{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING SUBSTRING(sub|", true},
			{"SELECT Name FROM dbo.Customers GROUP BY Name ORDER BY SUBSTRING(sub|", true},
			{"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM dbo.scalar_func(sub|", true},
			{"SELECT Name FROM dbo.Customers QUALIFY sub| = Name", fabric},
			{"SELECT Name FROM dbo.Customers ORDER BY sub|", true},
			{"SELECT ROW_NUMBER() OVER (PARTITION BY sub| ORDER BY Id) FROM dbo.Customers", true},
			{"SELECT ABS(sub|) FROM dbo.Customers", true},
			{"SELECT CONVERT(int, sub|) FROM dbo.Customers", true},
			{"SELECT DATEADD(day, sub|, GETDATE())", true},
			{"SELECT CASE WHEN sub| THEN Name ELSE Email END FROM dbo.Customers", true},
			{"SELECT CASE WHEN Id = 1 THEN sub| ELSE Name END FROM dbo.Customers", true},
			{"SELECT CASE Id WHEN sub| THEN Name END FROM dbo.Customers", true},
			{"SELECT CASE WHEN Id = 1 THEN Name ELSE sub| END FROM dbo.Customers", true},
			{"SELECT * FROM dbo.Customers c CROSS APPLY dbo.fnOrdersFor(sub|) f", true},
			{"SELECT * FROM dbo.Customers c CROSS APPLY (VALUES (sub|)) v(Name)", true},
			{"INSERT dbo.Customers (Name) SELECT ABS(sub|) FROM dbo.Customers", true},
			{"INSERT dbo.Customers (Name) VALUES (sub|)", true},
			{"SELECT TOP (sub|) Name FROM dbo.Customers", true},
			{"SELECT * FROM dbo.Customers WHERE Id |", false},
			{"SELECT * FROM dbo.Customers WHERE Id = 1 |", false},
			{"SELECT * FROM dbo.Customers WHERE Id IS |", false},
			{"SELECT * FROM dbo.Customers WHERE EXISTS |", false},
			{"SELECT * FROM dbo.Customers AS |", false},
			{"SELECT * FROM |", false},
			{"SELECT * FROM dbo.Customers c CROSS APPLY |", false},
			{"SELECT * FROM dbo.Customers c CROSS APPLY c.|", false},
			{"SELECT CAST(Id AS |) FROM dbo.Customers", false},
			{"SELECT CONVERT(|, Id) FROM dbo.Customers", false},
			{"SELECT DATEADD(|, 1, GETDATE())", false},
			{"SELECT * FROM dbo.Customers WITH (|)", false},
			{"UPDATE dbo.Customers SET |", false},
			{"EXEC dbo.proc @value = |", false},
			{"INSERT dbo.Customers (|) VALUES (1)", false},
			{"DECLARE @t TABLE (|)", false},
			{"CREATE TABLE dbo.fnOrdersFor (|)", false},
		} {
			t.Run(fmt.Sprintf("%v/%s", fabric, tt.sql), func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				if fabric {
					qp.conn.Server = fabricConn(t).Server
				}
				lines, row, col := linesAndCursor(t, tt.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if got := containsLabel(items, "SUBSTRING"); got != tt.want {
					t.Fatalf("SUBSTRING present=%v, want %v in %v", got, tt.want, labels(items))
				}
			})
		}
	}
}

func TestSQLPredicateStarterPositions(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		for _, tt := range []struct {
			sql  string
			want bool
		}{
			{"SELECT * FROM dbo.Customers WHERE |", true},
			{"SELECT * FROM dbo.Customers WHERE NOT |", true},
			{"SELECT * FROM dbo.Customers WHERE Id = 1 AND |", true},
			{"SELECT * FROM dbo.Customers WHERE (|)", true},
			{"SELECT CASE WHEN | THEN Name END FROM dbo.Customers", true},
			{"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON |", true},
			{"SELECT * FROM dbo.Customers WHERE Id = |", false},
			{"SELECT * FROM dbo.Customers WHERE Id + |", false},
			{"SELECT * FROM dbo.Customers WHERE Id IN (|)", false},
			{"SELECT | FROM dbo.Customers", false},
			{"SELECT ABS(|) FROM dbo.Customers", false},
			{"SELECT * FROM dbo.Customers WHERE EXISTS |", false},
			{"SELECT * FROM dbo.Customers WHERE Id = 1 |", false},
			{"SELECT * FROM dbo.Customers WHERE CONTAINS(Name, 'abc') |", false},
			{"SELECT * FROM dbo.Customers WHERE FREETEXT(Name, 'abc') |", false},
		} {
			t.Run(fmt.Sprintf("%v/%s", fabric, tt.sql), func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				if fabric {
					qp.conn.Server = fabricConn(t).Server
				}
				lines, row, col := linesAndCursor(t, tt.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				for _, label := range []string{"EXISTS", "CONTAINS", "FREETEXT"} {
					want := tt.want && (label == "EXISTS" || !fabric)
					if got := containsLabel(items, label); got != want {
						t.Errorf("%s present=%v, want %v in %v", label, got, want, labels(items))
					}
				}
			})
		}
	}
}

func TestSQLDistinctPredicateCompletionVersions(t *testing.T) {
	for _, major := range []int{13, 15, 16, 17} {
		for _, tt := range []struct{ sql, label string }{
			{"SELECT * FROM dbo.Customers WHERE Id |", "IS DISTINCT FROM"},
			{"SELECT * FROM dbo.Customers WHERE Id |", "IS NOT DISTINCT FROM"},
			{"SELECT * FROM dbo.Customers WHERE Id IS |", "DISTINCT FROM"},
			{"SELECT * FROM dbo.Customers WHERE Id IS |", "NOT DISTINCT FROM"},
			{"SELECT * FROM dbo.Customers WHERE Id IS NOT |", "DISTINCT FROM"},
		} {
			t.Run(fmt.Sprintf("%d/%s/%s", major, tt.sql, tt.label), func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				qp.conn.Server = fabricConn(t).Server
				qp.conn.Server.Info().EngineEdition = 3
				qp.conn.Server.Info().VersionMajor = major
				lines, row, col := linesAndCursor(t, tt.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if got, want := containsLabel(items, tt.label), major >= 16; got != want {
					t.Fatalf("%s present=%v, want %v in %v", tt.label, got, want, labels(items))
				}
			})
		}
	}
}

func TestSQLExpressionCompletionAccepts(t *testing.T) {
	for _, tt := range []struct{ before, typed, after, want string }{
		{"CREATE TABLE dbo.NewTable (Name ", "varch", ")", "CREATE TABLE dbo.NewTable (Name VARCHAR(|))"},
		{"CREATE TABLE dbo.NewTable (Name ", "varch", "(50))", "CREATE TABLE dbo.NewTable (Name VARCHAR(|50))"},
		{"CREATE TABLE dbo.NewTable (Amount ", "dec", ")", "CREATE TABLE dbo.NewTable (Amount DECIMAL(|))"},
		{"CREATE TABLE dbo.NewTable (Name VARCHAR(50) ", "not", ")", "CREATE TABLE dbo.NewTable (Name VARCHAR(50) NOT NULL|)"},
		{"SELECT * FROM dbo.Customers WHERE ", "subs", "", "SELECT * FROM dbo.Customers WHERE SUBSTRING(|)"},
		{"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM ", "nu", "", "SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM NULL|"},
		{"SELECT * FROM dbo.Customers WHERE ", "subs", " /* keep */ (Name, 1, 2) = 'ab'", "SELECT * FROM dbo.Customers WHERE SUBSTRING /* keep */ (|Name, 1, 2) = 'ab'"},
		{"SELECT * FROM dbo.Customers WHERE ", "exi", "", "SELECT * FROM dbo.Customers WHERE EXISTS (|)"},
		{"SELECT * FROM dbo.Customers WHERE ", "exi", " (SELECT 1 FROM dbo.Orders)", "SELECT * FROM dbo.Customers WHERE EXISTS (|SELECT 1 FROM dbo.Orders)"},
		{"SELECT * FROM dbo.Customers WHERE NOT ", "exi", "", "SELECT * FROM dbo.Customers WHERE NOT EXISTS (|)"},
		{"SELECT * FROM dbo.Customers WHERE ", "not", " /* keep */ EXISTS (SELECT 1)", "SELECT * FROM dbo.Customers WHERE NOT /* keep */ EXISTS (|SELECT 1)"},
		{"SELECT * FROM dbo.Customers WHERE ", "not", " /* keep */ EXISTS", "SELECT * FROM dbo.Customers WHERE NOT /* keep */ EXISTS (|)"},
		{"SELECT * FROM dbo.Customers WHERE ", "not", " /* keep */ EXISTS (SELECT", "SELECT * FROM dbo.Customers WHERE NOT /* keep */ EXISTS (|SELECT"},
		{"SELECT * FROM dbo.Customers WHERE ", "conta", "", "SELECT * FROM dbo.Customers WHERE CONTAINS(|)"},
		{"SELECT * FROM dbo.Customers WHERE ", "freet", "", "SELECT * FROM dbo.Customers WHERE FREETEXT(|)"},
		{"SELECT * FROM dbo.Customers WHERE ", "regexp_l", "", "SELECT * FROM dbo.Customers WHERE REGEXP_LIKE(|)"},
		{"SELECT ", "regexp_re", "", "SELECT REGEXP_REPLACE(|)"},
		{"SELECT ", "edit_d", "", "SELECT EDIT_DISTANCE(|)"},
		{"SELECT ", "date_b", "", "SELECT DATE_BUCKET(|)"},
		{"SELECT ", "row_n", "", "SELECT ROW_NUMBER() OVER (ORDER BY |)"},
		{"SELECT ", "row_n", "(", "SELECT ROW_NUMBER(|"},
		{"SELECT ", "row_n", " /* keep */ (", "SELECT ROW_NUMBER /* keep */ (|"},
		{"SELECT ", "percentile_c", "(0.5", "SELECT PERCENTILE_CONT(|0.5"},
		{"SELECT ", "row_n", "() OVER (ORDER BY Id)", "SELECT ROW_NUMBER() OVER (|ORDER BY Id)"},
		{"SELECT ", "lag", "", "SELECT LAG(|) OVER (ORDER BY )"},
		{"SELECT ", "lag", "(Name) IGNORE NULLS OVER (ORDER BY Id)", "SELECT LAG(|Name) IGNORE NULLS OVER (ORDER BY Id)"},
		{"SELECT ", "first_v", "(Name) RESPECT NULLS OVER (ORDER BY Id)", "SELECT FIRST_VALUE(|Name) RESPECT NULLS OVER (ORDER BY Id)"},
		{"SELECT ", "lead", "(Name) /* keep */ IGNORE /* modifier */ NULLS OVER (ORDER BY Id)", "SELECT LEAD(|Name) /* keep */ IGNORE /* modifier */ NULLS OVER (ORDER BY Id)"},
		{"SELECT ", "last_v", "(Name) IGNORE NULLS", "SELECT LAST_VALUE(|Name) IGNORE NULLS OVER (ORDER BY )"},
		{"SELECT ", "lag", "(Name) IGNORE", "SELECT LAG(|Name) IGNORE"},
		{"SELECT ", "lead", "(Name) RESPECT NU", "SELECT LEAD(|Name) RESPECT NU"},
		{"SELECT ", "first_v", "(Name) /* keep */ IGNO", "SELECT FIRST_VALUE(|Name) /* keep */ IGNO"},
		{"SELECT COALESCE(", "lag", ", 0) FROM dbo.Orders", "SELECT COALESCE(LAG(|) OVER (ORDER BY ), 0) FROM dbo.Orders"},
		{"SELECT COALESCE((", "ab", "), 0) FROM dbo.Orders", "SELECT COALESCE((ABS(|)), 0) FROM dbo.Orders"},
		{"SELECT ", "percentile_c", "", "SELECT PERCENTILE_CONT(|) WITHIN GROUP (ORDER BY ) OVER ()"},
		{"SELECT ", "getdate", "", "SELECT GETDATE()|"},
		{"SELECT ", "getdate", "()", "SELECT GETDATE()|"},
		{"SELECT ", "db_na", "", "SELECT DB_NAME(|)"},
		{"SELECT ", "original_l", "", "SELECT ORIGINAL_LOGIN()|"},
		{"SELECT ", "current_t", "", "SELECT CURRENT_TIMESTAMP|"},
		{"SELECT ", "current_u", "", "SELECT CURRENT_USER|"},
		{"SELECT ", "session_u", "", "SELECT SESSION_USER|"},
		{"SELECT ", "current_da", "", "SELECT CURRENT_DATE|"},
		{"SELECT ", "current_timezone_i", "", "SELECT CURRENT_TIMEZONE_ID()|"},
		{"SELECT ", "error_mess", "", "SELECT ERROR_MESSAGE()|"},
		{"SELECT ", "scope_ident", "", "SELECT SCOPE_IDENTITY()|"},
		{"SELECT ", "ai_generate_em", "", "SELECT AI_GENERATE_EMBEDDINGS(|)"},
		{"SELECT ", "ai_generate_em", " /* keep */ (Name USE MODEL embedding_model)", "SELECT AI_GENERATE_EMBEDDINGS /* keep */ (|Name USE MODEL embedding_model)"},
		{"SELECT * FROM ", "ai_generate_ch", "", "SELECT * FROM AI_GENERATE_CHUNKS(|)"},
		{"SELECT ", "unistr", "", "SELECT UNISTR(|)"},
		{"SELECT PARSE(Name AS ", "dateti", ")", "SELECT PARSE(Name AS DATETIME2|)"},
		{"SELECT TRY_PARSE(Name AS ", "int", ")", "SELECT TRY_PARSE(Name AS INT|)"},
		{"SELECT * FROM ", "generate_s", "", "SELECT * FROM GENERATE_SERIES(|)"},
		{"SELECT * FROM ", "openr", "", "SELECT * FROM OPENROWSET(BULK '|')"},
		{"SELECT * FROM ", "openr", " AS src", "SELECT * FROM OPENROWSET(BULK '|') AS src"},
		{"SELECT N'\u4e2d' AS [Name]\nFROM ", "openr", "", "SELECT N'\u4e2d' AS [Name]\nFROM OPENROWSET(BULK '|')"},
		{"SELECT * FROM dbo.Customers CROSS APPLY (SELECT * FROM ", "openr", ") AS src", "SELECT * FROM dbo.Customers CROSS APPLY (SELECT * FROM OPENROWSET(BULK '|')) AS src"},
		{"SELECT * FROM ", "openr", " /* keep */ (", "SELECT * FROM OPENROWSET /* keep */ (|"},
		{"SELECT * FROM ", "openr", "()", "SELECT * FROM OPENROWSET(|)"},
		{"SELECT * FROM ", "openr", " /* keep */ (BULK 'x', SINGLE_CLOB) AS src", "SELECT * FROM OPENROWSET /* keep */ (|BULK 'x', SINGLE_CLOB) AS src"},
		{"SELECT * FROM ", "openr", "('MSOLEDBSQL', 'Server=example;', 'SELECT 1') AS src", "SELECT * FROM OPENROWSET(|'MSOLEDBSQL', 'Server=example;', 'SELECT 1') AS src"},
		{"SELECT * FROM dbo.Customers CROSS APPLY GENERATE_SERIES(1,10) AS g WHERE g.", "val", "", "SELECT * FROM dbo.Customers CROSS APPLY GENERATE_SERIES(1,10) AS g WHERE g.value|"},
		{"SELECT * FROM dbo.Customers CROSS APPLY STRING_SPLIT(Name, ',') AS g WHERE g.", "val", "", "SELECT * FROM dbo.Customers CROSS APPLY STRING_SPLIT(Name, ',') AS g WHERE g.value|"},
		{"SELECT DATEADD(", "da", ", 1, GETDATE())", "SELECT DATEADD(DAY|, 1, GETDATE())"},
		{"SELECT ", "datefromp", "", "SELECT DATEFROMPARTS(|)"},
		{"SELECT * FROM dbo.Customers CROSS APPLY ", "regexp_m", "", "SELECT * FROM dbo.Customers CROSS APPLY REGEXP_MATCHES(|)"},
		{"SELECT * FROM dbo.Customers CROSS APPLY ", "val", "", "SELECT * FROM dbo.Customers CROSS APPLY (VALUES (|))"},
		{"SELECT * FROM sys.", "dm_ex", "", "SELECT * FROM sys.dm_exec_sql_text(|)"},
		{"SELECT * FROM sys.", "dm_ex", "(0x1234) AS s", "SELECT * FROM sys.dm_exec_sql_text(|0x1234) AS s"},
		{"SELECT * FROM ", "fnOrders", "", "SELECT * FROM dbo.fnOrdersFor(|)"},
		{"SELECT * FROM dbo.", "fnOrders", "(1) AS f", "SELECT * FROM dbo.fnOrdersFor(|1) AS f"},
		{"SELECT * FROM dbo.Customers WHERE Id IS ", "dist", " FROM Name", "SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM |Name"},
	} {
		for _, key := range []tcell.Key{tcell.KeyTab, tcell.KeyEnter} {
			t.Run(fmt.Sprintf("%s/%d", tt.want, key), func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				qp.editor.SetText(tt.before + tt.after)
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
				for range []rune(tt.after) {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
				}

				for _, r := range tt.typed {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
				}
				if !qp.editor.OverlayActive() {
					t.Fatal("expression completion popup did not open")
				}
				qp.editor.HandleKey(tcell.NewEventKey(key, "", tcell.ModNone))
				if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "", 1); got != want {
					t.Fatalf("accepted text=%q, want %q", got, want)
				}
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
				if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "x", 1); got != want {
					t.Fatalf("cursor text=%q, want %q", got, want)
				}
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModNone))
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModNone))
				if got, want := qp.editor.Text(), tt.before+tt.typed+tt.after; got != want {
					t.Fatalf("undo text=%q, want %q", got, want)
				}
			})
		}
	}
}

func TestSQLCompletionPreservesExistingCompoundExpressions(t *testing.T) {
	for _, marked := range []string{
		"SELECT CASE| Id WHEN 1 THEN Name ELSE Email END FROM dbo.Customers",
		"SELECT CASE| /* keep */ (CASE WHEN Id > 1 THEN 1 ELSE 0 END) WHEN 1 THEN Name END FROM dbo.Customers",
		"SELECT CASE| WHEN Id = 1 THEN Name ELSE Email END FROM dbo.Customers",
		"SELECT * FROM dbo.Customers WHERE NOT| EXISTS (SELECT 1)",
		"SELECT * FROM dbo.Customers WHERE NOT| /* keep */ EXISTS (SELECT 1)",
		"SELECT * FROM dbo.Customers WHERE EXISTS| (SELECT 1)",
	} {
		t.Run(marked, func(t *testing.T) {
			assertCompletionLeavesSQL(t, newTestQueryPanelWithFunctions(t), marked)
		})
	}
}

func TestSQLRegexTVFCompletionDialect(t *testing.T) {
	sql25, _ := newGConnAtVersion(t, "17.0.2000.0")
	azure, _ := newGConnOnAzure(t, 5)
	fabricSQL, _ := newGConnOnAzure(t, int(gosmo.EngineFabricSQLDatabase))
	for _, qp := range []struct {
		name string
		conn *db.ServerConn
		want bool
	}{
		{"SQL Server 2025", sql25, true},
		{"Azure SQL", azure, true},
		{"SQL database in Fabric", fabricSQL, true},
		{"Fabric Warehouse", fabricConn(t), false},
	} {
		t.Run(qp.name, func(t *testing.T) {
			panel := newTestQueryPanelWithFunctions(t)
			panel.conn.Server = qp.conn.Server
			for _, sql := range []string{"SELECT * FROM regexp_|", "SELECT * FROM dbo.Customers CROSS APPLY regexp_|"} {
				lines, row, col := linesAndCursor(t, sql)
				items, _ := panel.sqlCompletionCandidates(completionReq(lines, row, col))
				for _, label := range []string{"REGEXP_MATCHES", "REGEXP_SPLIT_TO_TABLE"} {
					if got := containsLabel(items, label); got != qp.want {
						t.Errorf("%s: %s present=%t, want %t; items=%v", sql, label, got, qp.want, labels(items))
					}
				}
			}
			lines, row, col := linesAndCursor(t, "SELECT regexp_c| FROM dbo.Customers")
			items, _ := panel.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, "REGEXP_COUNT") {
				t.Errorf("lost scalar regex: %v", labels(items))
			}
		})
	}
}

func TestSQLCompletionKeepsLargeCallSuffix(t *testing.T) {
	qp := newTestQueryPanelWithFunctions(t)
	payload := strings.Repeat("json-data", 16*1024)
	lines, row, col := linesAndCursor(t, "SELECT sub|(N'"+payload+"', 1, 2)")
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "SUBSTRING") {
		t.Fatalf("missing SUBSTRING in %v", labels(items))
	}
	for _, item := range items {
		if strings.Contains(item.Text, payload) {
			t.Fatalf("%s copied the existing large call into its completion text", item.Label)
		}
	}
}

func TestSQLFullTextPredicateContinuations(t *testing.T) {
	for _, name := range []string{"CONTAINS", "FREETEXT"} {
		for _, tt := range []struct {
			tail string
			want string
		}{
			{" |", ""},
			{"(Name, 'abc') |", "AND"},
		} {
			t.Run(name+tt.tail, func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers WHERE "+name+tt.tail)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if tt.want == "" {
					if len(items) != 0 {
						t.Fatalf("pending predicate call offered %v", labels(items))
					}
				} else if !containsLabel(items, tt.want) || containsLabel(items, "=") || containsLabel(items, "SUBSTRING") {
					t.Fatalf("completed predicate offered %v", labels(items))
				}
			})
		}
	}
}

func TestSQLRecentBuiltinCompletions(t *testing.T) {
	for _, engine := range []struct {
		name           string
		major, edition int
		fabric         bool
	}{
		{"2019", 15, 3, false},
		{"2022", 16, 3, false},
		{"2025", 17, 3, false},
		{"Azure", 12, 5, false},
		{"Fabric SQL database", 12, 12, false},
		{"Fabric", 12, 11, true},
	} {
		for _, fn := range []struct {
			name   string
			major  int
			fabric bool
		}{
			{"DATE_BUCKET", 16, true}, {"DATETRUNC", 16, true},
			{"GREATEST", 16, true}, {"LEAST", 16, true},
			{"BIT_COUNT", 16, true}, {"GET_BIT", 16, true}, {"SET_BIT", 16, true},
			{"LEFT_SHIFT", 16, true}, {"RIGHT_SHIFT", 16, true},
			{"JSON_ARRAY", 16, true}, {"JSON_OBJECT", 16, true}, {"JSON_PATH_EXISTS", 16, true},
			{"REGEXP_COUNT", 17, true}, {"REGEXP_INSTR", 17, true},
			{"REGEXP_REPLACE", 17, true}, {"REGEXP_SUBSTR", 17, true},
			{"EDIT_DISTANCE", 17, false}, {"EDIT_DISTANCE_SIMILARITY", 17, false},
			{"JARO_WINKLER_DISTANCE", 17, false}, {"JARO_WINKLER_SIMILARITY", 17, false},
			{"BASE64_ENCODE", 17, true}, {"BASE64_DECODE", 17, true},
			{"JSON_CONTAINS", 17, false},
			{"VECTOR_DISTANCE", 17, false}, {"VECTOR_NORM", 17, false},
			{"VECTOR_NORMALIZE", 17, false}, {"VECTORPROPERTY", 17, false},
			{"UNISTR", 17, true}, {"CURRENT_DATE", 17, false},
			{"CURRENT_TIMEZONE", 16, true}, {"CURRENT_TIMEZONE_ID", 16, false},
			{"AI_GENERATE_EMBEDDINGS", 17, false},
		} {
			for _, prefix := range []string{"SELECT ", "SELECT * FROM dbo.Customers WHERE Id = "} {
				t.Run(engine.name+"/"+prefix+fn.name, func(t *testing.T) {
					qp := newTestQueryPanelWithFunctions(t)
					switch {
					case engine.fabric:
						qp.conn.Server = fabricConn(t).Server
					case engine.edition == 5 || engine.edition == 12:
						sc, _ := newGConnOnAzure(t, engine.edition)
						qp.conn.Server = sc.Server
					default:
						sc, _ := newGConnAtVersion(t, fmt.Sprintf("%d.0.2000.0", engine.major))
						qp.conn.Server = sc.Server
					}
					lines, row, col := linesAndCursor(t, prefix+strings.ToLower(fn.name)+"|")
					items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
					want := (engine.edition == 5 || engine.edition == 12 || engine.fabric || engine.major >= fn.major) && (!engine.fabric || fn.fabric)
					if got := containsLabel(items, fn.name); got != want {
						t.Fatalf("%s present=%v, want %v in %v", fn.name, got, want, labels(items))
					}
				})
			}
		}
	}
}

func TestSQLAICompletionDialects(t *testing.T) {
	for _, tt := range []struct {
		name        string
		edition     int
		fabric, sql bool
	}{
		{"SQL Server", 3, false, true},
		{"Azure SQL", 5, false, true},
		{"Managed Instance", 8, false, true},
		{"Fabric SQL database", 12, false, true},
		{"Fabric Warehouse", 11, true, false},
		{"Synapse dedicated", 6, false, false},
		{"Synapse serverless", 11, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			} else {
				sc, _ := newGConnAtVersion(t, "17.0.2000.0")
				qp.conn.Server = sc.Server
				qp.conn.Server.Info().EngineEdition = gosmo.EngineEdition(tt.edition)
			}
			for _, fn := range strings.Fields("AI_ANALYZE_SENTIMENT AI_CLASSIFY AI_EXTRACT AI_FIX_GRAMMAR AI_GENERATE_RESPONSE AI_SUMMARIZE AI_TRANSLATE AI_GENERATE_EMBEDDINGS AI_GENERATE_CHUNKS") {
				for _, source := range []bool{false, true} {
					sql := "SELECT " + strings.ToLower(fn) + "|"
					if source {
						sql = "SELECT * FROM " + strings.ToLower(fn) + "|"
					}
					lines, row, col := linesAndCursor(t, sql)
					items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
					want := tt.fabric && !source
					switch fn {
					case "AI_GENERATE_EMBEDDINGS":
						want = tt.sql && !source
					case "AI_GENERATE_CHUNKS":
						want = tt.sql && source
					}
					if got := containsLabel(items, fn); got != want {
						t.Errorf("%s: present=%v, want %v; items=%v", sql, got, want, labels(items))
					}
				}
			}
		})
	}
}

func TestSQLChunkCompletionCompatibility(t *testing.T) {
	for _, compat := range []gosmo.CompatibilityLevel{0, 160, 170} {
		for _, version := range []string{"16.0.2000.0", "17.0.2000.0"} {
			t.Run(fmt.Sprintf("%s/%d", version, compat), func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				sc, _ := newGConnAtVersion(t, version)
				qp.conn.Server = sc.Server
				qp.app.completion.inventories[completionInventoryKey(qp.conn.Opts, qp.database)].compatibility = compat
				lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers CROSS APPLY ai_generate_ch|")
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				want := version[0:2] == "17" && compat != 160
				if got := containsLabel(items, "AI_GENERATE_CHUNKS"); got != want {
					t.Fatalf("present=%v, want %v; items=%v", got, want, labels(items))
				}
			})
		}
	}
}

func TestSQLWarehouseCompletionWithoutEditionMetadata(t *testing.T) {
	qp := newTestQueryPanelWithFunctions(t)
	qp.conn.Server = fabricConn(t).Server
	qp.conn.Server.Info().EngineEdition = 0
	qp.conn.Server.Info().VersionMajor = 0
	if !qp.conn.IsFabricDW() {
		t.Fatal("fixture lost its hostname-derived Warehouse classification")
	}
	for _, tt := range []struct{ sql, label string }{
		{"SELECT ai_generate_em|", "AI_GENERATE_EMBEDDINGS"},
		{"SELECT current_da|", "CURRENT_DATE"},
		{"SELECT * FROM ai_generate_ch|", "AI_GENERATE_CHUNKS"},
		{"SELECT JSON_VALUE(Name, '$.x' |)", "RETURNING"},
	} {
		lines, row, col := linesAndCursor(t, tt.sql)
		items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
		if containsLabel(items, tt.label) {
			t.Errorf("Warehouse offers SQL-only %s without edition metadata", tt.label)
		}
	}
}

func TestSQLAddedBuiltinFamilies(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		qp := newTestQueryPanelWithFunctions(t)
		if fabric {
			qp.conn.Server = fabricConn(t).Server
		}
		for _, family := range []struct {
			names  string
			fabric bool
		}{
			{"PARSE TRY_PARSE CHOOSE IIF COLLATIONPROPERTY PARSENAME STATS_DATE INDEX_COL INDEXKEY_PROPERTY INDEXPROPERTY COMPRESS DECOMPRESS ERROR_MESSAGE ERROR_NUMBER ERROR_PROCEDURE ERROR_SEVERITY ERROR_STATE", true},
			{"ERROR_LINE CONTEXT_INFO CURRENT_TRANSACTION_ID ROWCOUNT_BIG SCOPE_IDENTITY XACT_STATE IDENT_CURRENT IDENT_INCR IDENT_SEED SQL_VARIANT_PROPERTY FORMATMESSAGE DATABASE_PRINCIPAL_ID SESSIONPROPERTY LOGINPROPERTY CRYPT_GEN_RANDOM", false},
		} {
			for _, fn := range strings.Fields(family.names) {
				lines, row, col := linesAndCursor(t, "SELECT "+strings.ToLower(fn)+"| FROM dbo.Customers")
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if got, want := containsLabel(items, fn), !fabric || family.fabric; got != want {
					t.Errorf("fabric=%v %s: present=%v, want %v", fabric, fn, got, want)
				}
			}
		}
	}
}

func TestSQLBuiltinCompletionVersionBoundaries(t *testing.T) {
	for _, tt := range []struct {
		version   string
		functions bool
		concat    bool
		series    bool
	}{
		{"13.0.4001.0", false, false, false},
		{"14.0.2000.0", true, false, false},
		{"16.0.2000.0", true, false, true},
		{"17.0.2000.0", true, true, true},
	} {
		t.Run(tt.version, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			sc, _ := newGConnAtVersion(t, tt.version)
			qp.conn.Server = sc.Server
			for _, name := range []string{"STRING_AGG", "CONCAT_WS", "TRIM", "TRANSLATE", "GENERATE_SERIES", "||"} {
				sql := "SELECT " + name + "| FROM dbo.Customers"
				want := tt.functions
				switch name {
				case "||":
					sql, want = "SELECT * FROM dbo.Customers WHERE Name |", tt.concat
				case "GENERATE_SERIES":
					sql, want = "SELECT * FROM GENERATE_SERIES|", tt.series
				}
				lines, row, col := linesAndCursor(t, sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if got := containsLabel(items, name); got != want {
					t.Errorf("%s present=%v, want %v in %v", name, got, want, labels(items))
				}
			}
		})
	}
}

func TestSQLRecentBuiltinKinds(t *testing.T) {
	for _, tt := range []struct {
		sql, name string
		want      bool
	}{
		{"SELECT * FROM dbo.Customers WHERE reg|", "REGEXP_LIKE", true},
		{"SELECT reg|", "REGEXP_LIKE", false},
		{"SELECT * FROM dbo.Customers WHERE Id = reg|", "REGEXP_LIKE", false},
		{"SELECT * FROM dbo.Customers WHERE REGEXP_LIKE(Name, 'a') |", "AND", true},
		{"SELECT * FROM dbo.Customers WHERE REGEXP_LIKE(Name, 'a') |", "=", false},
		{"SELECT * FROM dbo.Customers WHERE dbo.REGEXP_LIKE(Name, 'a') |", "=", true},
		{"SELECT * FROM dbo.Customers WHERE REGEXP_LIKE |", "REGEXP_COUNT", false},
		{"SELECT * FROM dbo.Customers WHERE REGEXP_LIKE(reg|", "REGEXP_REPLACE", true},
		{"SELECT reg|", "REGEXP_MATCHES", false},
		{"SELECT * FROM reg|", "REGEXP_MATCHES", true},
		{"SELECT * FROM dbo.Customers CROSS APPLY reg|", "REGEXP_MATCHES", true},
		{"SELECT * FROM dbo.Customers OUTER APPLY reg|", "REGEXP_SPLIT_TO_TABLE", true},
		{"SELECT * FROM dbo.Customers CROSS APPLY REGEXP_MATCHES(reg|", "REGEXP_REPLACE", true},
		{"SELECT DATE_BUCKET(sub|", "SUBSTRING", false},
		{"SELECT JSON_VALUE(Name, sub|) FROM dbo.Customers", "SUBSTRING", false},
		{"SELECT JSON_VALUE(Name, |) FROM dbo.Customers", "Id", false},
		{"SELECT JSON_VALUE(sub|, '$.x') FROM dbo.Customers", "SUBSTRING", true},
		{"SELECT JSON_QUERY(Name, sub|) FROM dbo.Customers", "SUBSTRING", false},
		{"SELECT JSON_MODIFY(Name, sub|, 1) FROM dbo.Customers", "SUBSTRING", false},
		{"SELECT JSON_MODIFY(Name, '$.x', sub|) FROM dbo.Customers", "SUBSTRING", true},
		{"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM nu|", "NULL", true},
		{"SELECT DATE_BUCKET(day, sub|", "SUBSTRING", true},
		{"SELECT DATETRUNC(sub|", "SUBSTRING", false},
		{"SELECT DATETRUNC(day, sub|", "SUBSTRING", true},
		{"SELECT DATE_BUCKET(da|", "DAY", true},
		{"SELECT DATETRUNC(iso|", "ISO_WEEK", true},
		{"SELECT DATE_BUCKET(micro|", "MICROSECOND", false},
		{"SELECT DATE_BUCKET(| FROM dbo.Customers", "Name", false},
		{"SELECT DATEADD(da|", "DAY", true},
		{"SELECT DATEDIFF(ye|", "YEAR", true},
		{"SELECT DATEDIFF_BIG(nano|", "NANOSECOND", true},
		{"SELECT DATENAME(weekd|", "WEEKDAY", true},
		{"SELECT DATEPART(iso|", "ISO_WEEK", true},
		{"SELECT DATEPART(tz|", "TZOFFSET", true},
		{"SELECT DATEADD(iso|", "ISO_WEEK", false},
		{"SELECT DATEADD(day, sub|", "SUBSTRING", true},
		{"SELECT JSON_OBJECTAG|", "JSON_OBJECTAGG", true},
		{"SELECT * FROM dbo.Customers WHERE JSON_OBJECTAG|", "JSON_OBJECTAGG", false},
		{"SELECT VECTOR_SE|", "VECTOR_SEARCH", false},
		{"SELECT * FROM VECTOR_SE|", "VECTOR_SEARCH", true},
	} {
		t.Run(tt.sql+"/"+tt.name, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if got := containsLabel(items, tt.name); got != tt.want {
				t.Fatalf("%s present=%v, want %v in %v", tt.name, got, tt.want, labels(items))
			}
		})
	}
}

func TestSQLRestoredBuiltinFamilies(t *testing.T) {
	for _, tt := range []struct {
		sql, name string
		fabric    bool
		want      bool
	}{
		{"SELECT row_n|", "ROW_NUMBER", false, true},
		{"SELECT Id * ab| FROM dbo.Customers", "ABS", false, true},
		{"SELECT 2 * ab| FROM dbo.Customers", "ABS", false, true},
		{"SELECT (Id + 1) * ab| FROM dbo.Customers", "ABS", false, true},
		{"SELECT * ab| FROM dbo.Customers", "ABS", false, false},
		{"SELECT c.* ab| FROM dbo.Customers c", "ABS", false, false},
		{"SELECT TOP (5) * ab| FROM dbo.Customers", "ABS", false, false},
		{"SELECT COALESCE(su|, 0) FROM dbo.Orders", "SUM", false, true},
		{"SELECT COALESCE((ab|), 0) FROM dbo.Orders", "ABS", false, true},
		{"SELECT ((ab|))", "ABS", false, true},
		{"SELECT (((su|))) FROM dbo.Orders", "SUM", false, true},
		{"SELECT * FROM GENERATE_SERIES((ab|), 10)", "ABS", false, true},
		{"SELECT CONVERT((ab|), Id) FROM dbo.Orders", "ABS", false, false},
		{"SELECT * FROM ((ab|))", "ABS", false, false},
		{"SELECT COALESCE(lag|, 0) FROM dbo.Orders", "LAG", false, true},
		{"SELECT (row_n|) FROM dbo.Orders", "ROW_NUMBER", false, true},
		{"SELECT CASE WHEN Id = 1 THEN lag| ELSE 0 END FROM dbo.Orders", "LAG", false, true},
		{"SELECT * FROM dbo.Orders WHERE COALESCE(lag|, 0) = 1", "LAG", false, false},
		{"SELECT SUM(COALESCE(lag|, 0)) FROM dbo.Orders", "LAG", false, false},
		{"SELECT LAG(su|) OVER (ORDER BY Id) FROM dbo.Orders GROUP BY Id", "SUM", false, true},
		{"SELECT LAG(COALESCE(lag|, 0)) OVER (ORDER BY Id) FROM dbo.Orders", "LAG", false, false},
		{"SELECT ROW_NUMBER() OVER (ORDER BY COALESCE(lag|, 0)) FROM dbo.Orders", "LAG", false, false},
		{"SELECT ROW_NUMBER() OVER (ORDER BY su|(Total)) FROM dbo.Orders GROUP BY Id", "SUM", false, true},
		{"SELECT ROW_NUMBER() OVER (PARTITION BY su|(Total) ORDER BY Id) FROM dbo.Orders GROUP BY Id", "SUM", false, true},
		{"SELECT STRING_AGG(Name, ',') WITHIN GROUP (ORDER BY su|) FROM dbo.Orders", "SUM", false, false},
		{"SELECT Id FROM dbo.Orders QUALIFY COALESCE(row_n|, 0) = 1", "ROW_NUMBER", true, true},
		{"SELECT Id FROM dbo.Orders GROUP BY Id HAVING COALESCE(lag|, 0) = 1", "LAG", false, false},
		{"SELECT * FROM dbo.Orders WHERE (cre|)", "CREATE", false, false},
		{"SELECT (su|) FROM dbo.Orders", "SUM", false, true},
		{"SELECT Id FROM dbo.Orders GROUP BY Id HAVING (co| > 1)", "COUNT", false, true},
		{"SELECT SUM(COALESCE(su|, 0)) FROM dbo.Orders", "SUM", false, false},
		{"SELECT SUM(su|(Total)) OVER () FROM dbo.Orders GROUP BY Id", "SUM", false, true},
		{"SELECT AVG(su|(Total)) OVER (ORDER BY Id) FROM dbo.Orders GROUP BY Id", "SUM", false, true},
		{"SELECT SUM(row_n|) OVER () FROM dbo.Orders GROUP BY Id", "ROW_NUMBER", false, false},
		{"SELECT * FROM dbo.Orders WHERE COALESCE(su|, 0) = 1", "SUM", false, false},
		{"SELECT SUM(CASE WHEN Id i| THEN 1 ELSE 0 END) FROM dbo.Customers", "IS NULL", false, true},
		{"SELECT SUM(CASE WHEN Id i| THEN 1 ELSE 0 END) FROM dbo.Customers", "IN", false, true},
		{"SELECT COALESCE(CASE WHEN Id i| THEN 1 ELSE 0 END, 0) FROM dbo.Customers", "IS NULL", true, true},
		{"SELECT dense_r|", "DENSE_RANK", false, true},
		{"SELECT lag|", "LAG", false, true},
		{"SELECT first_v|", "FIRST_VALUE", false, true},
		{"SELECT percentile_c|", "PERCENTILE_CONT", false, true},
		{"SELECT stdev|", "STDEV", false, true},
		{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING stdev|", "STDEV", false, true},
		{"SELECT grouping_i|", "GROUPING_ID", false, true},
		{"SELECT * FROM dbo.Customers WHERE row_n|", "ROW_NUMBER", false, false},
		{"SELECT * FROM dbo.Customers WHERE stdev|", "STDEV", false, false},
		{"SELECT * FROM row_n|", "ROW_NUMBER", false, false},
		{"SELECT any_v|", "ANY_VALUE", false, false},
		{"SELECT median|", "MEDIAN", false, false},
		{"SELECT any_v|", "ANY_VALUE", true, true},
		{"SELECT median|", "MEDIAN", true, true},
		{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING median|", "MEDIAN", true, true},
		{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING percentile_c|", "PERCENTILE_CONT", false, false},
		{"SELECT Name FROM dbo.Customers GROUP BY Name HAVING percentile_c|", "PERCENTILE_CONT", true, true},
		{"SELECT Name FROM dbo.Customers QUALIFY row_n|", "ROW_NUMBER", true, true},
		{"SELECT Name FROM dbo.Customers QUALIFY row_n|", "ROW_NUMBER", false, false},
		{"SELECT * FROM dbo.Customers ORDER BY row_n|", "ROW_NUMBER", false, true},
		{"SELECT * FROM dbo.Customers WHERE cos|", "COS", false, true},
		{"SELECT ABS(acos|)", "ACOS", false, true},
		{"SELECT * FROM dbo.Customers WHERE patindex|", "PATINDEX", false, true},
		{"SELECT datefromp|", "DATEFROMPARTS", false, true},
		{"SELECT translate|", "TRANSLATE", false, true},
		{"SELECT db_name|", "DB_NAME", false, true},
		{"SELECT * FROM dbo.Customers WHERE Id = object_id|", "OBJECT_ID", false, true},
		{"SELECT serverproperty|", "SERVERPROPERTY", false, true},
		{"SELECT hashbytes|", "HASHBYTES", false, true},
		{"SELECT has_perms_by_name|", "HAS_PERMS_BY_NAME", false, true},
		{"SELECT is_srvrolemember|", "IS_SRVROLEMEMBER", false, true},
		{"SELECT is_srvrolemember|", "IS_SRVROLEMEMBER", true, false},
		{"SELECT current_t|", "CURRENT_TIMESTAMP", false, true},
		{"SELECT * FROM dbo.Customers WHERE Name = system_u|", "SYSTEM_USER", false, true},
		{"SELECT * FROM current_t|", "CURRENT_TIMESTAMP", false, false},
		{"SELECT isnumeric|", "ISNUMERIC", false, true},
		{"SELECT * FROM generate_s|", "GENERATE_SERIES", true, true},
		{"SELECT generate_s|", "GENERATE_SERIES", false, false},
		{"SELECT * FROM object_id|", "OBJECT_ID", false, false},
	} {
		t.Run(fmt.Sprintf("%v/%s", tt.fabric, tt.sql), func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if got := containsLabel(items, tt.name); got != tt.want {
				t.Fatalf("%s present=%v, want %v in %v", tt.name, got, tt.want, labels(items))
			}
			seen := map[string]bool{}
			for _, item := range items {
				if seen[item.Label] {
					t.Fatalf("duplicate suggestion %q", item.Label)
				}
				seen[item.Label] = true
			}
		})
	}
}

func assertCompletionLeavesSQL(t *testing.T, qp *QueryPanel, marked string) {
	t.Helper()
	before, after, _ := strings.Cut(marked, "|")
	qp.editor.SetText(before + after)
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
	for range []rune(after) {
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	}
	qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
	if qp.editor.OverlayActive() {
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	}
	if got, want := qp.editor.Text(), before+after; got != want {
		t.Fatalf("completion changed existing SQL to %q; want %q", got, want)
	}
}

func FuzzSQLExpressionCompletion(f *testing.F) {
	for _, sql := range []string{
		"SELECT * FROM dbo.Customers WHERE ",
		"SELECT * FROM dbo.Customers WHERE Id IS DISTINCT FROM ",
		"SELECT * FROM dbo.Customers WHERE NOT EXISTS (SELECT 1) AND ",
		"SELECT CONVERT(int, SUBSTRING(Name, 1, 2)) FROM dbo.Customers",
		"SELECT CASE WHEN Id > 1 THEN SUBSTRING(Name, 1, 2) END FROM dbo.Customers",
		"UPDATE dbo.Customers SET Id = 1, Name = ",
		"SELECT * FROM dbo.Customers c CROSS APPLY dbo.fnOrdersFor(c.Id) f",
		"SELECT geometry::Point(1, 2, 0).ST",
		"DECLARE @g geography; SELECT @g.STBuffer(1).ST",
		"SELECT * FROM dbo.Customers WHERE REGEXP_LIKE(Name, 'abc') AND ",
		"CREATE PROCEDURE dbo.p @id AS int, @name nvarchar(max) OUTPUT AS BEGIN SELECT 1; END",
		"CREATE FUNCTION dbo.f(@id int, @name varchar(50)) RETURNS int AS BEGIN RETURN 1; END",
		"CREATE FUNCTION dbo.f() RETURNS @r TABLE (Id int, Name nvarchar(max)) AS BEGIN INSERT INTO @r VALUES (1, 'x'); RETURN; END",
		"CREATE PROCEDURE dbo.p WITH ENCRYPTION, RECOMPILE AS BEGIN SELECT 1; END",
		"CREATE FUNCTION dbo.f() RETURNS int AS BEGIN RETURN ABS(1); END",
		"CREATE FUNCTION dbo.f() RETURNS int AS BEGIN RETURN (SELECT Id FROM dbo.Customers WHERE Id = 1); END",
		"WITH x AS (SELECT * FROM dbo.Customers) UPDATE x SET Name = 'x' WHERE Id = 1",
		"DELETE TOP (10) FROM dbo.Customers WHERE Id IS DISTINCT FROM 1",
		"TRUNCATE TABLE dbo.Customers",
		"CREATE TABLE dbo.First (Id INT, Name NVARCHAR(MAX))\nCREATE TABLE dbo.Second (Name nv",
		"CREATE TABLE dbo.First (Id INT)\ncre",
		"CREATE FUNCTION dbo.f() RETURNS int AS BEGIN IF 1=0 RETURN 0; SELECT @x=Id FROM dbo.Customers wh",
		"EXEC @rc = dbo.GetCu",
		"UPDATE dbo.Customers SET Name.WRITE(N'x', 0, 1) wh",
		"SELECT * FROM OPENROWSET(BULK N'x.parquet', FORMAT = N'PARQUET', MAXERRORS = 2)",
		"SELECT * FROM OPENROWSET(BULK 'x', ORDER(Id DESC, Name ASC) UNIQUE) WITH (Id INT, doc NVARCHAR(MAX))",
		"SELECT AI_GENERATE_EMBEDDINGS(Name USE MODEL model PARAMETERS JSON_OBJECT('dimensions': 755))",
		"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = Name, CHUNK_TYPE = FIXED, CHUNK_SIZE = 50)",
		"SELECT JSON_ARRAYAGG(Name ORDER BY Id DESC, Name ASC ABSENT ON NULL RETURNING JSON)",
		"SELECT JSON_QUERY(Name, '$.x[*]' WITH ARRAY WRAPPER), JSON_VALUE(Name, '$.x' RETURNING NVARCHAR(MAX))",
		"SELECT TRY_PARSE(Name AS DECIMAL(18,2) USING 'en-US')",
	} {
		f.Add(sql, len([]rune(sql)))
	}
	f.Fuzz(func(t *testing.T, sql string, cursor int) {
		if len(sql) > 1024 || strings.ContainsRune(sql, '|') {
			t.Skip()
		}
		buf := []rune(sql)
		cursor = max(0, min(cursor, len(buf)))
		lines, row, col := linesAndCursor(t, string(buf[:cursor])+"|"+string(buf[cursor:]))
		qp := newTestQueryPanelWithFunctions(t)
		items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
		if from < 0 || from > col {
			t.Fatalf("invalid replacement column %d at %d", from, col)
		}
		for _, item := range items {
			if !item.Placeholder && item.Text == "" {
				t.Fatalf("empty completion %+v", item)
			}
		}
	})
}
