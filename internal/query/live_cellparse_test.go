//go:build livedb

// Live check of NewCellParser against a real bulk load: a table of every
// loadable type is read as grid text, the text parsed and bulk-loaded through
// gosmo into an empty copy, and the copy must equal the source. Also the two
// bulk-load facts 6.i had to establish before import is built on them: an
// identity column in the load keeps the file's values, and a load that fails
// part-way leaves nothing.
//
//	go test -tags livedb ./internal/query/ -run TestLiveCellParse -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gosmo"
)

const liveCellDB = "gossms_p6i_live"

// liveCellColumns is every type a bulk load fills from text, with the
// collations that make char encoding matter.
var liveCellColumns = []string{
	"id int IDENTITY(10,5) PRIMARY KEY",
	"b bit", "ti tinyint", "si smallint", "i int", "bi bigint",
	"dec decimal(38,6)", "num numeric(5,2)", "m money", "sm smallmoney",
	"f float", "r real",
	"d date", "t0 time(0)", "t7 time(7)", "dt datetime", "sdt smalldatetime",
	"dt2_3 datetime2(3)", "dt2_7 datetime2(7)", "dto0 datetimeoffset(0)", "dto7 datetimeoffset(7)",
	"g uniqueidentifier", "bin binary(4)", "vb varbinary(8)", "vbm varbinary(max)", "img image",
	"c char(5)", "vc varchar(20) COLLATE Latin1_General_CI_AS", "vcm varchar(max)", "txt text COLLATE Latin1_General_CI_AS",
	"vcj varchar(20) COLLATE Japanese_CI_AS", "vcc varchar(20) COLLATE Cyrillic_General_CI_AS",
	"nc nchar(3)", "nvc nvarchar(10)", "nvcm nvarchar(max)", "ntx ntext COLLATE Latin1_General_CI_AS", "x xml",
}

// liveCellRows are INSERT value lists for every column after id: each type's
// extremes, a row of NULLs, and the strings a file round trip must keep apart.
var liveCellRows = []string{
	`0, 0, -32768, -2147483648, -9223372036854775808, -99999999999999999999999999999999.999999, -999.99,
	 -922337203685477.5808, -214748.3648, -1.7976931348623157E+308, -3.4028235E+38,
	 '0001-01-01', '00:00:00', '00:00:00', '1753-01-01', '1900-01-01',
	 '0001-01-01', '0001-01-01', '0001-01-01 00:00:00 -14:00', '0001-01-01 14:00:00 +14:00',
	 '00000000-0000-0000-0000-000000000000', 0x00, 0x, 0x, 0x,
	 '', '', '', '', '', '', N'', N'', N'', N'', N''`,
	`1, 255, 32767, 2147483647, 9223372036854775807, 99999999999999999999999999999999.999999, 999.99,
	 922337203685477.5807, 214748.3647, 2.2250738585072014E-308, 1.17549435E-38,
	 '9999-12-31', '23:59:59', '23:59:59.9999999', '9999-12-31 23:59:59.997', '2079-06-06 23:59',
	 '9999-12-31 23:59:59.999', '9999-12-31 23:59:59.9999999', '9999-12-31 23:59:59 +05:30', '9999-12-31 14:29:59.9999999 -09:30',
	 '6F9619FF-8B86-D011-B42D-00C04FD430C8', 0xDEADBEEF, 0x0102030405060708, 0xFFFF, 0xABCD,
	 'abcde', 'NULL', REPLICATE(CAST('x' AS varchar(max)), 9000), 'text',
	 N'日本語', N'Жизнь', N'日😀', N'0123456789', REPLICATE(CAST(N'é' AS nvarchar(max)), 5000), N'ntext', N'<a b="1">é</a>'`,
	`NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
	 NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
	 NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL`,
	`1, 42, 1, 1, 1, 0.000001, 0.5, 0.0001, 12.5, 0.1, 0.1,
	 '2024-02-29', '13:45:30', '13:45:30.1234567', '2024-02-29 13:45:30.127', '2024-02-29 13:46',
	 '2024-02-29 13:45:30.123', '2024-02-29 13:45:30.1234567', '2024-02-29 13:45:30 +00:00', '2024-02-29 13:45:30.1 -00:30',
	 NEWID(), 0x01, 0x00, 0x00, 0x00,
	 ' a ', 'café € "q" ,', 'line1' + CHAR(13) + CHAR(10) + 'line2', CHAR(9) + 'tab',
	 N'ｱｲｳ', N'ёЁ', N'  ', N'NULL', N'"quoted", comma', N'a' + NCHAR(0) + N'b', N'<x/>'`,
}

func TestLiveCellParseRoundTrip(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	srv, utf8DB := liveCellServer(t, ctx)

	liveExec(t, ctx, db, "CREATE TABLE dbo.Src ("+strings.Join(liveCellColumns, ", ")+")")
	for _, values := range liveCellRows {
		if !utf8DB {
			// A Latin1 database's bulk load cannot carry Japanese or
			// Cyrillic to a varchar column (TargetColumn.DatabaseCodePage).
			values = strings.NewReplacer("N'日本語', N'Жизнь'", "'abc', 'def'", "N'ｱｲｳ', N'ёЁ'", "'ghi', 'jkl'").Replace(values)
		}
		liveExec(t, ctx, db, "INSERT dbo.Src VALUES ("+values+")")
	}
	// SELECT INTO keeps the identity property and no default; the default
	// here is what KeepNulls must not let replace a file's NULL.
	liveExec(t, ctx, db, "SELECT * INTO dbo.Dst FROM dbo.Src WHERE 1 = 0; ALTER TABLE dbo.Dst ADD DEFAULT 7 FOR ti")

	src := liveGrid(t, ctx, db, "dbo.Src")
	cols := liveTargetColumns(t, ctx, db, "dbo.Src")
	parsers := make([]func(string) (any, error), len(cols))
	for i, c := range cols {
		var err error
		if parsers[i], err = NewCellParser(c); err != nil {
			t.Fatalf("%s %+v: %v", src.Columns[i], c, err)
		}
	}
	rows := func(yield func([]any, error) bool) {
		for r := range src.Rows {
			vals := make([]any, len(cols))
			for c := range cols {
				if src.IsNull(r, c) {
					continue
				}
				v, err := parsers[c](src.Rows[r][c])
				if err != nil {
					yield(nil, fmt.Errorf("row %d %s %q: %w", r, src.Columns[c], src.Rows[r][c], err))
					return
				}
				vals[c] = v
			}
			if !yield(vals, nil) {
				return
			}
		}
	}
	n, err := srv.DatabaseRef(liveCellDB).BulkInsert(ctx, gosmo.BulkCopy{
		Schema: "dbo", Table: "Dst", Columns: src.Columns,
		Options: gosmo.BulkOptions{KeepNulls: true, CheckConstraints: true},
	}, rows)
	if err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}
	if n != int64(len(src.Rows)) {
		t.Fatalf("loaded %d rows, want %d", n, len(src.Rows))
	}

	// The grid text of the copy is the source's, cell for cell and NULL for
	// NULL; identity values included.
	dst := liveGrid(t, ctx, db, "dbo.Dst")
	for r := range src.Rows {
		for c := range src.Columns {
			if src.IsNull(r, c) != dst.IsNull(r, c) || src.Rows[r][c] != dst.Rows[r][c] {
				t.Errorf("row %d %s: copy %q (null %v), source %q (null %v)", r, src.Columns[c],
					trunc(dst.Rows[r][c]), dst.IsNull(r, c), trunc(src.Rows[r][c]), src.IsNull(r, c))
			}
		}
	}
	// And the stored values, which the text could hide (a char column's code
	// page reads back the same mojibake it was written with on both sides
	// only if the bytes match): EXCEPT both ways, LOB types cast.
	sel := make([]string, len(cols))
	for i, c := range cols {
		name := "[" + src.Columns[i] + "]"
		switch c.Type {
		case "text":
			name = "CAST(" + name + " AS varchar(max)) AS " + name
		case "ntext", "xml":
			name = "CAST(" + name + " AS nvarchar(max)) AS " + name
		case "image":
			name = "CAST(" + name + " AS varbinary(max)) AS " + name
		}
		sel[i] = name
	}
	list := strings.Join(sel, ", ")
	for _, q := range []string{
		"SELECT COUNT(*) FROM (SELECT " + list + " FROM dbo.Src EXCEPT SELECT " + list + " FROM dbo.Dst) x",
		"SELECT COUNT(*) FROM (SELECT " + list + " FROM dbo.Dst EXCEPT SELECT " + list + " FROM dbo.Src) x",
	} {
		var diff int
		if err := db.QueryRowContext(ctx, "USE "+liveCellDB+"; "+q).Scan(&diff); err != nil || diff != 0 {
			t.Errorf("%d rows differ (%v): %s", diff, err, q)
		}
	}
	// The bytes stored in the code-page columns, pinned: the server converts
	// what the load sends, so the bytes sent are not the bytes stored.
	var vc, vcj, vcc string
	if err := db.QueryRowContext(ctx, "USE "+liveCellDB+"; SELECT CONVERT(varchar(100), CAST(vc AS varbinary(100)), 1), CONVERT(varchar(100), CAST(vcj AS varbinary(100)), 1), CONVERT(varchar(100), CAST(vcc AS varbinary(100)), 1) FROM dbo.Dst WHERE id = 25").Scan(&vc, &vcj, &vcc); err != nil {
		t.Fatal(err)
	}
	if want := "0x636166E9208020227122202C"; vc != want {
		t.Errorf("varchar 1252 bytes %s, want %s", vc, want)
	}
	if want := "0xB1B2B3"; utf8DB && vcj != want {
		t.Errorf("varchar 932 bytes %s, want %s", vcj, want)
	}
	if want := "0xB8A8"; utf8DB && vcc != want {
		t.Errorf("varchar 1251 bytes %s, want %s", vcc, want)
	}
}

// TestLiveBulkLoadIsAllOrNothing: a load with no RowsPerBatch is one batch,
// so a failure anywhere — the source's error part-way, or the server's at the
// flush — leaves the table as it was.
func TestLiveBulkLoadIsAllOrNothing(t *testing.T) {
	db, ctx, done := livePlanDB(t)
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	srv, _ := liveCellServer(t, ctx)
	liveExec(t, ctx, db, "CREATE TABLE dbo.Pk (id int PRIMARY KEY, v nvarchar(10))")

	load := func(rows iter.Seq2[[]any, error]) error {
		_, err := srv.DatabaseRef(liveCellDB).BulkInsert(ctx, gosmo.BulkCopy{
			Schema: "dbo", Table: "Pk", Columns: []string{"id", "v"},
			Options: gosmo.BulkOptions{KeepNulls: true, CheckConstraints: true},
		}, rows)
		return err
	}
	numbered := func(n int, last []any, lastErr error) iter.Seq2[[]any, error] {
		return func(yield func([]any, error) bool) {
			for i := range n {
				if !yield([]any{int64(i), "r"}, nil) {
					return
				}
			}
			if last != nil || lastErr != nil {
				yield(last, lastErr)
			}
		}
	}
	errSource := errors.New("conversion failed")
	for _, c := range []struct {
		name string
		rows iter.Seq2[[]any, error]
	}{
		{"source error after 20000 rows", numbered(20000, nil, errSource)},
		{"duplicate key in the last row", numbered(20000, []any{int64(5), "dup"}, nil)},
		{"over-long value in the last row", numbered(20000, []any{int64(99999), "waytoolongvalue"}, nil)},
	} {
		if err := load(c.rows); err == nil {
			t.Errorf("%s: no error", c.name)
		}
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+liveCellDB+".dbo.Pk").Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows left (%v), want 0", c.name, n, err)
		}
	}
	// And the pool is healthy after the abandoned loads.
	if err := load(numbered(3, nil, nil)); err != nil {
		t.Fatalf("a good load after the failures: %v", err)
	}
}

// liveCellServer creates the disposable database and returns a gosmo Server
// on its own pool; both are cleaned up with the test. The database's default
// collation is UTF-8 where the server has one (2019 and later), which lets a
// bulk load carry any character to a varchar column whose code page has it.
func liveCellServer(t *testing.T, ctx context.Context) (srv *gosmo.Server, utf8DB bool) {
	t.Helper()
	pool, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatal(err)
	}
	drop := "IF DB_ID('" + liveCellDB + "') IS NOT NULL BEGIN ALTER DATABASE " + liveCellDB +
		" SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE " + liveCellDB + " END"
	var major int
	if err := pool.QueryRowContext(ctx, "SELECT CAST(SERVERPROPERTY('ProductMajorVersion') AS int)").Scan(&major); err != nil {
		t.Fatal(err)
	}
	utf8DB = major >= 15
	collation := "Latin1_General_CI_AS"
	if utf8DB {
		collation = "Latin1_General_100_CI_AS_SC_UTF8"
	}
	if _, err := pool.ExecContext(ctx, drop+"; CREATE DATABASE "+liveCellDB+" COLLATE "+collation); err != nil {
		t.Fatalf("create database: %v", err)
	}
	srv, err = gosmo.NewServer(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Close()
		db, err := sql.Open("sqlserver", *liveDSN)
		if err == nil {
			db.Exec(drop)
			db.Close()
		}
	})
	return srv, utf8DB
}

func liveExec(t *testing.T, ctx context.Context, db *sql.DB, stmt string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "USE "+liveCellDB+"; "+stmt); err != nil {
		t.Fatalf("%s: %v", trunc(stmt), err)
	}
}

// liveGrid reads table as the grid shows it, ordered by id.
func liveGrid(t *testing.T, ctx context.Context, db *sql.DB, table string) ResultSet {
	t.Helper()
	s, _, err := Open(ctx, db, liveCellDB)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res := s.Execute(ctx, "SELECT * FROM "+table+" ORDER BY id")
	if res.HasErrors() || len(res.Sets) != 1 {
		t.Fatalf("read %s: %+v", table, res.Messages)
	}
	return res.Sets[0]
}

// liveTargetColumns reads table's columns as NewCellParser takes them — what
// data import will do from the catalog.
func liveTargetColumns(t *testing.T, ctx context.Context, db *sql.DB, table string) []TargetColumn {
	t.Helper()
	rows, err := db.QueryContext(ctx, "USE "+liveCellDB+`;
SELECT TYPE_NAME(c.system_type_id),
       CASE WHEN c.max_length = -1 THEN -1
            WHEN TYPE_NAME(c.system_type_id) IN ('nchar', 'nvarchar') THEN c.max_length / 2
            ELSE c.max_length END,
       c.precision, c.scale, ISNULL(CAST(COLLATIONPROPERTY(c.collation_name, 'CodePage') AS int), 0),
       CAST(COLLATIONPROPERTY(CAST(DATABASEPROPERTYEX(DB_NAME(), 'Collation') AS nvarchar(128)), 'CodePage') AS int)
FROM sys.columns c WHERE c.object_id = OBJECT_ID(@p1) ORDER BY c.column_id`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []TargetColumn
	for rows.Next() {
		var c TargetColumn
		if err := rows.Scan(&c.Type, &c.Length, &c.Precision, &c.Scale, &c.CodePage, &c.DatabaseCodePage); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return slices.Clip(cols)
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
