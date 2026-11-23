package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

func TestSQLBulkOptionCompletion(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		for _, tt := range []struct {
			sql, present, absent string
		}{
			{"SELECT * FROM OPENROWSET(|)", "BULK", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK |)", "", "BULK,FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', |)", "FORMAT,CODEPAGE,DATAFILETYPE,MAXERRORS,FIRSTROW,LASTROW,ROWS_PER_BATCH,ROWTERMINATOR,FIELDTERMINATOR,FIELDQUOTE,ERRORFILE", "SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = 'CSV', |)", "CODEPAGE,FIELDTERMINATOR,ERRORFILE", "FORMAT,SINGLE_CLOB,SINGLE_BLOB,SINGLE_NCLOB"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = 'PARQUET', |)", "MAXERRORS,ROWS_PER_BATCH", "FORMAT,CODEPAGE,FIELDQUOTE,FIRSTROW,ERRORFILE,PARSER_VERSION,HEADER_ROW"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = 'JSONL', |)", "CODEPAGE,FIRSTROW,ERRORFILE", "FIELDQUOTE,ROWTERMINATOR,HEADER_ROW,PARSER_VERSION,LASTROW"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = |)", "'CSV','PARQUET'", "'JSON','XML'"},
			{"SELECT * FROM OPENROWSET(BULK 'x', DATAFILETYPE = |)", "'char','widechar'", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', CODEPAGE = |)", "'ACP','OEM','RAW','65001'", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FIRSTROW = |)", "", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT |)", "=", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT | = 'CSV')", "", "=,FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', |, FORMAT = 'CSV')", "FIELDQUOTE", "FORMAT"},
			{"SELECT * FROM OPENROWSET(BULK 'FORMAT, SINGLE_CLOB', /* FORMAT = 'PARQUET' */ |)", "FORMAT,FIELDQUOTE", "SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = 'CSV' |)", "", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = N'PARQUET', |)", "MAXERRORS,ROWS_PER_BATCH", "FIELDQUOTE,CODEPAGE"},
			{"SELECT * FROM OPENROWSET(BULK 'x', SINGLE_CLOB, |)", "CODEPAGE", "SINGLE_CLOB,SINGLE_BLOB,SINGLE_NCLOB,FORMAT,FORMATFILE"},
			{"SELECT * FROM OPENROWSET(BULK 'x', ORDER (Id |))", "", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x|', FORMAT = 'CSV')", "", "FORMAT,SUBSTRING"},
			{"SELECT * FROM OPENROWSET(BULK 'x', /* here | */ FORMAT='CSV')", "", "FORMAT,SUBSTRING"},
			{"SELECT * FROM dbo.OPENROWSET(BULK 'x', |)", "", "SINGLE_CLOB,DATAFILETYPE"},
		} {
			t.Run(fmt.Sprintf("%v/%s", fabric, tt.sql), func(t *testing.T) {
				qp := newTestQueryPanelWithFunctions(t)
				if fabric {
					qp.conn.Server = fabricConn(t).Server
				}
				lines, row, col := linesAndCursor(t, tt.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				for _, label := range strings.Split(tt.present, ",") {
					if label != "" && !containsLabel(items, label) {
						t.Errorf("missing %s in %v", label, labels(items))
					}
				}
				for _, label := range strings.Split(tt.absent, ",") {
					if label != "" && containsLabel(items, label) {
						t.Errorf("unexpected %s in %v", label, labels(items))
					}
				}
			})
		}
	}
}

func TestSQLRowsetSchemaCompletion(t *testing.T) {
	for _, tt := range []struct {
		sql, present, absent string
	}{
		{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = 'CSV') |", "WITH,WHERE,JOIN,AS", "SUBSTRING"},
		{"SELECT * FROM OPENROWSET(BULK 'x', SINGLE_CLOB) |", "WHERE,JOIN,AS", "WITH"},
		{"SELECT * FROM OPENJSON(@j) |", "WITH,WHERE,JOIN,AS", ""},
		{"SELECT * FROM OPENROWSET(BULK 'x') WITH (amount |)", "INT,DECIMAL,VARCHAR", "SUBSTRING,NULL,NOT NULL"},
		{"SELECT * FROM OPENROWSET(BULK 'x') WITH (amount DECIMAL(18,2), name |)", "VARCHAR,NVARCHAR", "SUBSTRING,NULL,NOT NULL"},
		{"SELECT * FROM OPENJSON(@j) WITH (doc NVARCHAR(|))", "MAX", "SUBSTRING"},
		{"SELECT * FROM OPENJSON(@j) WITH (doc NVARCHAR(MAX) |)", "AS JSON", "NULL,NOT NULL"},
		{"SELECT * FROM OPENJSON(@j) WITH (doc NVARCHAR(MAX) '$.doc' AS |)", "JSON", "NULL"},
		{"SELECT * FROM OPENJSON(@j) WITH (doc NVARCHAR(100) |)", "", "AS JSON,NULL"},
		{"SELECT * FROM OPENROWSET(BULK 'x', ORDER(Id |))", "ASC,DESC", "UNIQUE,FORMAT"},
		{"SELECT * FROM OPENROWSET(BULK 'x', ORDER(Id ASC) |)", "UNIQUE", "ASC,DESC,FORMAT"},
		{"SELECT * FROM OPENROWSET(BULK 'x') | WITH (Id INT)", "WHERE,AS", "WITH"},
		{"SELECT * FROM OPENJSON(@j) WITH (doc NVARCHAR(MAX), other |)", "INT,NVARCHAR", "AS JSON"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, label := range strings.Split(tt.present, ",") {
				if label != "" && !containsLabel(items, label) {
					t.Errorf("missing %s in %v", label, labels(items))
				}
			}
			for _, label := range strings.Split(tt.absent, ",") {
				if label != "" && containsLabel(items, label) {
					t.Errorf("unexpected %s in %v", label, labels(items))
				}
			}
		})
	}
}

func TestSQLJSONOptionVersionAndDialect(t *testing.T) {
	for _, tt := range []struct {
		name, version  string
		fabric         bool
		native, isjson bool
	}{
		{"2019", "15.0.2000.0", false, false, false},
		{"2022", "16.0.2000.0", false, false, true},
		{"2025", "17.0.2000.0", false, true, true},
		{"Warehouse", "", true, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			} else {
				sc, _ := newGConnAtVersion(t, tt.version)
				qp.conn.Server = sc.Server
			}
			for _, opt := range []struct {
				sql, label string
				want       bool
			}{
				{"SELECT JSON_VALUE(Name, '$.x' |)", "RETURNING", tt.native},
				{"SELECT JSON_QUERY(Name, '$.x' |)", "WITH ARRAY WRAPPER", tt.native},
				{"SELECT JSON_ARRAY(Name |)", "RETURNING", tt.native},
				{"SELECT ISJSON(Name, |)", "SCALAR", tt.isjson},
				{"SELECT AI_SUMMARIZE(Name |)", "NULL ON ERROR", tt.fabric},
			} {
				lines, row, col := linesAndCursor(t, opt.sql)
				items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
				if got := containsLabel(items, opt.label); got != opt.want {
					t.Errorf("%s: %s present=%v, want %v", opt.sql, opt.label, got, opt.want)
				}
			}
		})
	}
}

func TestSQLBulkOptionDialects(t *testing.T) {
	for _, tt := range []struct {
		name, version string
		edition       int
		fabric        bool
	}{
		{"2016", "13.0.4001.0", 3, false},
		{"2017", "14.0.2000.0", 3, false},
		{"2022", "16.0.2000.0", 3, false},
		{"2025", "17.0.2000.0", 3, false},
		{"Azure SQL", "12.0.2000.0", 5, false},
		{"Managed Instance", "12.0.2000.0", 8, false},
		{"Fabric SQL database", "12.0.2000.0", 12, false},
		{"Fabric Warehouse", "12.0.2000.0", 11, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			} else {
				sc, _ := newGConnAtVersion(t, tt.version)
				qp.conn.Server = sc.Server
				qp.conn.Server.Info().EngineEdition = gosmo.EngineEdition(tt.edition)
			}
			lines, row, col := linesAndCursor(t, "SELECT * FROM OPENROWSET(BULK 'x', |)")
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, label := range strings.Fields("SINGLE_CLOB SINGLE_BLOB SINGLE_NCLOB FORMATFILE ORDER") {
				if got, want := containsLabel(items, label), !tt.fabric; got != want {
					t.Errorf("%s present=%v, want %v", label, got, want)
				}
			}
			for _, label := range strings.Fields("HEADER_ROW PARSER_VERSION ESCAPECHAR") {
				if got := containsLabel(items, label); got != tt.fabric {
					t.Errorf("%s present=%v, want %v", label, got, tt.fabric)
				}
			}
			if got, want := containsLabel(items, "FIELDQUOTE"), tt.name != "2016"; got != want {
				t.Errorf("FIELDQUOTE present=%v, want %v", got, want)
			}
			lines, row, col = linesAndCursor(t, "SELECT * FROM OPENROWSET(BULK 'x', FORMAT = |)")
			items, _ = qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if got, want := containsLabel(items, "'JSONL'"), tt.fabric; got != want {
				t.Errorf("JSONL present=%v, want %v", got, want)
			}
			if got, want := containsLabel(items, "'DELTA'"), !tt.fabric && tt.name != "2016" && tt.name != "2017" && tt.edition != 12; got != want {
				t.Errorf("DELTA present=%v, want %v", got, want)
			}
		})
	}
}

func TestSQLFabricBulkFormatAndParserOptions(t *testing.T) {
	for _, tt := range []struct {
		sql, present, absent string
	}{
		{"SELECT * FROM OPENROWSET(BULK 'x.parquet', |)", "MAXERRORS,ROWS_PER_BATCH", "FIELDQUOTE,HEADER_ROW,SINGLE_CLOB,DATA_SOURCE"},
		{"SELECT * FROM OPENROWSET(BULK 'x.csv', PARSER_VERSION = '1.0', |)", "LASTROW,FIELDQUOTE", "HEADER_ROW,PARSER_VERSION"},
		{"SELECT * FROM OPENROWSET(BULK 'x.csv', PARSER_VERSION = '2.0', |)", "HEADER_ROW,FIELDQUOTE", "LASTROW,PARSER_VERSION"},
		{"SELECT * FROM OPENROWSET(BULK 'x.csv', PARSER_VERSION = N'2.0', |)", "HEADER_ROW", "LASTROW,PARSER_VERSION"},
		{"SELECT * FROM OPENROWSET(BULK 'x.csv', PARSER_VERSION = |)", "'1.0','2.0'", "FORMAT"},
		{"SELECT * FROM OPENROWSET(BULK 'x.csv', HEADER_ROW = |)", "TRUE,FALSE", "FORMAT"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			qp.conn.Server = fabricConn(t).Server
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, label := range strings.Split(tt.present, ",") {
				if !containsLabel(items, label) {
					t.Errorf("missing %s in %v", label, labels(items))
				}
			}
			for _, label := range strings.Split(tt.absent, ",") {
				if containsLabel(items, label) {
					t.Errorf("unexpected %s in %v", label, labels(items))
				}
			}
		})
	}
}

func TestSQLAIFunctionOptions(t *testing.T) {
	for _, tt := range []struct {
		sql, present string
	}{
		{"SELECT AI_SUMMARIZE(Name |) FROM dbo.Customers", "NULL ON ERROR,ERROR ON ERROR,DEFAULT"},
		{"SELECT AI_SUMMARIZE(NULL |)", "NULL ON ERROR,ERROR ON ERROR,DEFAULT"},
		{"SELECT AI_CLASSIFY(Name, 'a', 'b' |) FROM dbo.Customers", "NULL ON ERROR,ERROR ON ERROR,DEFAULT"},
		{"SELECT AI_TRANSLATE(CONCAT(Name, Email), 'de' |) FROM dbo.Customers", "NULL ON ERROR,ERROR ON ERROR,DEFAULT"},
		{"SELECT AI_SUMMARIZE(Name NULL |)", "ON ERROR"},
		{"SELECT AI_SUMMARIZE(Name ERROR ON |)", "ERROR"},
		{"SELECT AI_SUMMARIZE(Name DEFAULT 'unknown' |)", "ON ERROR"},
		{"SELECT AI_SUMMARIZE(Name DEFAULT COALESCE(Email, 'unknown') |)", "ON ERROR"},
		{"SELECT AI_SUMMARIZE(Name DEFAULT 'unknown' ON |)", "ERROR"},
		{"SELECT AI_CLASSIFY(Name, |)", "SUBSTRING"},
		{"SELECT AI_SUMMARIZE(|)", "SUBSTRING"},
		{"SELECT AI_SUMMARIZE(Name DEFAULT |)", "SUBSTRING"},
		{"SELECT AI_CLASSIFY(Name, 'a', 'b' DEFAULT |)", "SUBSTRING"},
		{"SELECT AI_EXTRACT(Name, 'a' |)", "NULL ON ERROR,ERROR ON ERROR,DEFAULT"},
		{"SELECT AI_GENERATE_RESPONSE('prompt', Name |)", "NULL ON ERROR,ERROR ON ERROR,DEFAULT"},
		{"SELECT AI_SUMMARIZE(Name NULL ON ERROR |)", ""},
		{"SELECT dbo.AI_SUMMARIZE(Name |)", ""},
		{"SELECT [AI_SUMMARIZE](Name |)", ""},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			qp.conn.Server = fabricConn(t).Server
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if tt.present == "" {
				for _, label := range []string{"NULL ON ERROR", "ERROR ON ERROR", "DEFAULT", "ON ERROR"} {
					if containsLabel(items, label) {
						t.Errorf("unexpected %s in %v", label, labels(items))
					}
				}
			} else {
				for _, label := range strings.Split(tt.present, ",") {
					if !containsLabel(items, label) {
						t.Errorf("missing %s in %v", label, labels(items))
					}
				}
			}
		})
	}
}

func TestSQLJSONFunctionOptions(t *testing.T) {
	for _, tt := range []struct {
		sql, present, absent string
	}{
		{"SELECT JSON_OBJECT('name':Name |) FROM dbo.Customers", "NULL ON NULL,ABSENT ON NULL,RETURNING", "NULL ON ERROR,ERROR ON ERROR"},
		{"SELECT JSON_ARRAY(Name, NULL |)", "NULL ON NULL,ABSENT ON NULL,RETURNING", "ON NULL"},
		{"SELECT JSON_ARRAY(Name ABSENT |)", "ON NULL", "ABSENT ON NULL"},
		{"SELECT JSON_ARRAY(Name NULL ON |)", "NULL", "ABSENT ON NULL"},
		{"SELECT JSON_ARRAY(Name ABSENT ON NULL |)", "RETURNING", "NULL ON NULL,ABSENT ON NULL"},
		{"SELECT JSON_ARRAY(Name RETURNING |)", "JSON", "NULL ON NULL,ABSENT ON NULL"},
		{"SELECT JSON_ARRAYAGG(Name |)", "ORDER BY,NULL ON NULL,ABSENT ON NULL,RETURNING", "NULL ON ERROR"},
		{"SELECT JSON_ARRAYAGG(Name ORDER BY Id |)", "NULL ON NULL,ABSENT ON NULL,RETURNING", "ORDER BY"},
		{"SELECT JSON_ARRAYAGG(Name ORDER BY Id DESC |)", "NULL ON NULL,ABSENT ON NULL,RETURNING", "ORDER BY"},
		{"SELECT JSON_ARRAYAGG(Name ORDER BY Id, Name DESC ABSENT |)", "ON NULL", "ABSENT ON NULL,ORDER BY"},
		{"SELECT JSON_ARRAYAGG(Name ORDER BY Id, Name |)", "NULL ON NULL,ABSENT ON NULL,RETURNING", "ORDER BY"},
		{"SELECT JSON_VALUE(Name, '$.x' |)", "RETURNING", "NULL ON ERROR,ERROR ON ERROR"},
		{"SELECT JSON_VALUE(Name, '$.x' RETURNING |)", "INT,NVARCHAR,DATE", "JSON,NULL ON ERROR"},
		{"SELECT JSON_QUERY(Name, '$.x' |)", "WITH ARRAY WRAPPER", "NULL ON ERROR"},
		{"SELECT JSON_QUERY(Name, '$.x' WITH |)", "ARRAY WRAPPER", "NULL ON ERROR"},
		{"SELECT JSON_QUERY(Name, '$.x' WITH ARRAY |)", "WRAPPER", "NULL ON ERROR"},
		{"SELECT ISJSON(Name, |)", "VALUE,ARRAY,OBJECT,SCALAR", "SUBSTRING"},
		{"SELECT JSON_VALUE(Name, |)", "", "RETURNING,SUBSTRING"},
		{"SELECT JSON_ARRAY + (Name |) FROM dbo.Customers", "", "NULL ON NULL,ABSENT ON NULL,RETURNING"},
		{"SELECT JSON_ARRAY + (|) FROM dbo.Customers", "SUBSTRING", "NULL ON NULL,ABSENT ON NULL,RETURNING"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			for _, label := range strings.Split(tt.present, ",") {
				if label != "" && !containsLabel(items, label) {
					t.Errorf("missing %s in %v", label, labels(items))
				}
			}
			for _, label := range strings.Split(tt.absent, ",") {
				if label != "" && containsLabel(items, label) {
					t.Errorf("unexpected %s in %v", label, labels(items))
				}
			}
		})
	}
}

func TestSQLAIModelAndChunkArgumentCompletion(t *testing.T) {
	for _, tt := range []struct {
		sql, present string
	}{
		{"SELECT AI_GENERATE_EMBEDDINGS(Name USE MODEL model PARAMETERS |)", "JSON_OBJECT"},
		{"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE |)", "="},
		{"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = |)", "SUBSTRING"},
		{"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = Name, CHUNK_SIZE |)", "="},
		{"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = Name, OVERLAP |)", "="},
		{"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = Name, ENABLE_CHUNK_SET_ID |)", "="},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			lines, row, col := linesAndCursor(t, tt.sql)
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			if !containsLabel(items, tt.present) {
				t.Fatalf("missing %s in %v", tt.present, labels(items))
			}
		})
	}
}

func TestSQLParseTypeCompletion(t *testing.T) {
	for _, fn := range []string{"PARSE", "TRY_PARSE"} {
		qp := newTestQueryPanelWithFunctions(t)
		lines, row, col := linesAndCursor(t, "SELECT "+fn+"(Name AS |)")
		items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
		for _, label := range []string{"INT", "DECIMAL", "DATE", "DATETIME2"} {
			if !containsLabel(items, label) {
				t.Errorf("%s missing %s in %v", fn, label, labels(items))
			}
		}
		for _, label := range []string{"VARCHAR", "BIT", "XML", "JSON", "UNIQUEIDENTIFIER"} {
			if containsLabel(items, label) {
				t.Errorf("%s offers unsupported type %s", fn, label)
			}
		}
		lines, row, col = linesAndCursor(t, "SELECT "+fn+"(Name AS DECIMAL(18,2) |)")
		items, _ = qp.sqlCompletionCandidates(completionReq(lines, row, col))
		if !containsLabel(items, "USING") {
			t.Errorf("%s missing USING in %v", fn, labels(items))
		}
	}
}

func TestSQLFunctionOptionAcceptance(t *testing.T) {
	for _, tt := range []struct {
		sql, label, want string
		fabric           bool
	}{
		{"SELECT * FROM OPENROWSET(BULK 'x', for|)", "FORMAT", "SELECT * FROM OPENROWSET(BULK 'x', FORMAT = |)", true},
		{"SELECT * FROM OPENROWSET(BULK 'x', for| = 'CSV')", "FORMAT", "SELECT * FROM OPENROWSET(BULK 'x', FORMAT |= 'CSV')", true},
		{"SELECT * FROM OPENROWSET(BULK 'x', single_c|)", "SINGLE_CLOB", "SELECT * FROM OPENROWSET(BULK 'x', SINGLE_CLOB |)", false},
		{"SELECT * FROM OPENROWSET(BULK 'x', FORMAT = |)", "'CSV'", "SELECT * FROM OPENROWSET(BULK 'x', FORMAT = 'CSV'|)", true},
		{"SELECT AI_SUMMARIZE(Name err|)", "ERROR ON ERROR", "SELECT AI_SUMMARIZE(Name ERROR ON ERROR |)", true},
		{"SELECT AI_SUMMARIZE(Name err| /* keep */ ON ERROR)", "ERROR ON ERROR", "SELECT AI_SUMMARIZE(Name ERROR |/* keep */ ON ERROR)", true},
		{"SELECT AI_SUMMARIZE(Name DEFAULT 'N/A' on|)", "ON ERROR", "SELECT AI_SUMMARIZE(Name DEFAULT 'N/A' ON ERROR |)", true},
		{"SELECT AI_GENERATE_EMBEDDINGS(Name us|)", "USE MODEL", "SELECT AI_GENERATE_EMBEDDINGS(Name USE MODEL |)", false},
		{"SELECT * FROM AI_GENERATE_CHUNKS(so|)", "SOURCE", "SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = |)", false},
		{"SELECT * FROM AI_GENERATE_CHUNKS(SOURCE |)", "=", "SELECT * FROM AI_GENERATE_CHUNKS(SOURCE = |)", false},
		{"SELECT AI_GENERATE_EMBEDDINGS(Name USE MODEL model PARAMETERS json_ob|)", "JSON_OBJECT", "SELECT AI_GENERATE_EMBEDDINGS(Name USE MODEL model PARAMETERS JSON_OBJECT(|))", false},
		{"SELECT TRY_PARSE(Name AS INT us|)", "USING", "SELECT TRY_PARSE(Name AS INT USING |)", false},
		{"SELECT JSON_ARRAY(Name ab|)", "ABSENT ON NULL", "SELECT JSON_ARRAY(Name ABSENT ON NULL |)", false},
		{"SELECT JSON_ARRAYAGG(Name ORDER BY Id DESC ab|)", "ABSENT ON NULL", "SELECT JSON_ARRAYAGG(Name ORDER BY Id DESC ABSENT ON NULL |)", false},
		{"SELECT JSON_QUERY(Name, '$.x' wi|)", "WITH ARRAY WRAPPER", "SELECT JSON_QUERY(Name, '$.x' WITH ARRAY WRAPPER |)", false},
	} {
		t.Run(tt.sql+"/"+tt.label, func(t *testing.T) {
			qp := newTestQueryPanelWithFunctions(t)
			if tt.fabric {
				qp.conn.Server = fabricConn(t).Server
			}
			lines, row, col := linesAndCursor(t, tt.sql)
			qp.editor.SetText(strings.Replace(tt.sql, "|", "", 1))
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModCtrl))
			_, after, _ := strings.Cut(tt.sql, "|")
			for range []rune(after) {
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
			}
			items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
			index := slices.Index(labels(items), tt.label)
			if index < 0 {
				t.Fatalf("missing %s in %v", tt.label, labels(items))
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
			if qp.editor.OverlayActive() {
				for range index {
					qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
				}
				qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
			}
			if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "", 1); got != want {
				t.Fatalf("accepted text=%q, want %q", got, want)
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone))
			if got, want := qp.editor.Text(), strings.Replace(tt.want, "|", "x", 1); got != want {
				t.Fatalf("cursor text=%q, want %q", got, want)
			}
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModNone))
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyCtrlZ, "", tcell.ModNone))
			if got, want := qp.editor.Text(), strings.Replace(tt.sql, "|", "", 1); got != want {
				t.Fatalf("undo text=%q, want %q", got, want)
			}
		})
	}
}
