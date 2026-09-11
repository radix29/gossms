package query

import (
	"database/sql"
	"database/sql/driver"
	"strconv"
	"testing"
	"time"
)

func benchRows(n int) [][]driver.Value {
	ts := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	rows := make([][]driver.Value, n)
	for i := range rows {
		// Distinct per row, as a real driver allocates per cell; a shared
		// literal would hide the arena's savings.
		rows[i] = []driver.Value{int64(i), "row value " + strconv.Itoa(i), ts, []byte{0xDE, 0xAD, 0xBE, 0xEF}, nil}
	}
	return rows
}

func BenchmarkScanResultSetArena(b *testing.B) {
	rows := benchRows(50000)
	b.ReportAllocs()
	for b.Loop() {
		db := openFakeRowsDB(streamTestCols, rows)
		r, _ := db.Query("SELECT 1")
		rs, err := scanResultSet(r, nil)
		if err != nil || len(rs.Rows) != 50000 {
			b.Fatal(err)
		}
		r.Close()
		db.Close()
	}
}

// scanResultSetNaive is one string per cell and one slice per row, for
// benchmark comparison.
func scanResultSetNaive(rows *sql.Rows) (ResultSet, error) {
	sc, err := newRowScanner(rows)
	if err != nil {
		return ResultSet{}, err
	}
	rs := ResultSet{Columns: sc.cols}
	for rows.Next() {
		if err := rows.Scan(sc.ptrs...); err != nil {
			return rs, err
		}
		row := make([]string, len(sc.cols))
		for i := range sc.cols {
			if g := sc.guids[i]; g != nil {
				row[i] = formatGUID(*g)
			} else {
				row[i] = formatValue(sc.vals[i], sc.decimalLike[i], sc.layouts[i])
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, nil
}

func BenchmarkScanResultSetNaive(b *testing.B) {
	rows := benchRows(50000)
	b.ReportAllocs()
	for b.Loop() {
		db := openFakeRowsDB(streamTestCols, rows)
		r, _ := db.Query("SELECT 1")
		rs, err := scanResultSetNaive(r)
		if err != nil || len(rs.Rows) != 50000 {
			b.Fatal(err)
		}
		r.Close()
		db.Close()
	}
}
