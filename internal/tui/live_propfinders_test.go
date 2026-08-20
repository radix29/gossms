//go:build livedb

// Live verification of the property-dialog by-name finders — findSchema,
// findIndex, findStatistic, findForeignKey and the Change Tracking page's
// lookup — against a real SQL Server.
//
// Each one used to fetch a whole listing and scan it for a name; each now
// asks gosmo for the single object. What a unit test cannot settle is
// whether the narrowed query returns the *same* object the scan did,
// including the parts that come from a second query (an index's columns) and
// the ErrNotFound fallback the Change Tracking page relies on.
//
//	go test -tags livedb ./internal/tui/ -run TestLivePropFinders -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
//
// Skipped entirely without -livedb. Creates and drops its own throwaway
// database; touches nothing else.
package tui

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"

	_ "github.com/microsoft/go-mssqldb"
)

var liveDSN = flag.String("livedb", "", "SQL Server DSN for the live property-finder tests")

// livePropConn connects with the DSN's credentials the way the app does, so
// the finders run against a real *db.ServerConn rather than a bare gosmo
// Server.
func livePropConn(t *testing.T) (*db.ServerConn, context.Context) {
	t.Helper()
	if *liveDSN == "" {
		t.Skip("no -livedb DSN given")
	}
	rest, ok := strings.CutPrefix(*liveDSN, "sqlserver://")
	if !ok {
		t.Fatalf("-livedb must be a sqlserver:// DSN, got %q", *liveDSN)
	}
	creds, hostAndQuery, ok := strings.Cut(rest, "@")
	if !ok {
		t.Fatalf("-livedb DSN has no credentials: %q", *liveDSN)
	}
	user, pass, _ := strings.Cut(creds, ":")
	host, _, _ := strings.Cut(hostAndQuery, "?")

	sc, err := db.Connect(config.Connection{
		Server:                 host,
		AuthMethod:             config.AuthSQLServer,
		User:                   user,
		Password:               pass,
		TrustServerCertificate: true,
	})
	if err != nil {
		t.Fatalf("connect %s: %v", host, err)
	}
	t.Cleanup(sc.Close)
	return sc, sc.Context()
}

func TestLivePropFinders(t *testing.T) {
	sc, ctx := livePropConn(t)

	const dbName = "gossms_propfinders_live"

	// The setup DDL goes through a plain driver connection: gosmo has no
	// general Exec, and the point here is the finders, not the DDL.
	raw, err := sql.Open("sqlserver", *liveDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer raw.Close()
	mustExec := func(q string) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("exec %.50q: %v", q, err)
		}
	}
	mustExec(`IF DB_ID('` + dbName + `') IS NOT NULL ALTER DATABASE [` + dbName + `] SET SINGLE_USER WITH ROLLBACK IMMEDIATE`)
	mustExec(`IF DB_ID('` + dbName + `') IS NOT NULL DROP DATABASE [` + dbName + `]`)
	mustExec(`CREATE DATABASE [` + dbName + `]`)
	defer func() {
		c := context.Background()
		raw.ExecContext(c, `ALTER DATABASE [`+dbName+`] SET SINGLE_USER WITH ROLLBACK IMMEDIATE`)
		if _, err := raw.ExecContext(c, `DROP DATABASE [`+dbName+`]`); err != nil {
			t.Errorf("drop %s: %v", dbName, err)
		}
	}()

	mustExec(`ALTER DATABASE [` + dbName + `] SET CHANGE_TRACKING = ON (CHANGE_RETENTION = 2 DAYS)`)
	for _, stmt := range []string{
		`CREATE SCHEMA app AUTHORIZATION dbo`,
		`CREATE TABLE app.parent (id INT NOT NULL CONSTRAINT pk_parent PRIMARY KEY, code NVARCHAR(20) NOT NULL)`,
		`CREATE TABLE app.child (id INT NOT NULL PRIMARY KEY, parent_id INT NOT NULL, note NVARCHAR(50) NULL)`,
		`ALTER TABLE app.child ADD CONSTRAINT fk_child_parent FOREIGN KEY (parent_id)
		 REFERENCES app.parent (id) ON DELETE CASCADE`,
		`CREATE NONCLUSTERED INDEX ix_child_note ON app.child (parent_id DESC) INCLUDE (note)`,
		`CREATE STATISTICS st_child_note ON app.child (note)`,
		`ALTER TABLE app.child ENABLE CHANGE_TRACKING WITH (TRACK_COLUMNS_UPDATED = ON)`,
	} {
		mustExec(`USE [` + dbName + `]; EXEC sp_executesql N'` + strings.ReplaceAll(stmt, "'", "''") + `'`)
	}

	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		t.Fatalf("DatabaseByNameContext: %v", err)
	}

	t.Run("findSchema", func(t *testing.T) {
		s, err := findSchema(ctx, sc, dbName, "app")
		if err != nil {
			t.Fatalf("findSchema: %v", err)
		}
		if s.Name != "app" || s.Owner != "dbo" || s.ID == 0 {
			t.Errorf("findSchema = %+v, want app owned by dbo with a real ID", s)
		}
		if _, err := findSchema(ctx, sc, dbName, "nope"); !errors.Is(err, gosmo.ErrNotFound) {
			t.Errorf("missing schema: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("findIndex", func(t *testing.T) {
		tbl, idx, err := findIndex(ctx, sc, dbName, "app", "child", "ix_child_note")
		if err != nil {
			t.Fatalf("findIndex: %v", err)
		}
		if tbl.Name != "child" || tbl.Schema != "app" {
			t.Errorf("owning table = %s.%s, want app.child", tbl.Schema, tbl.Name)
		}
		// The Index Properties pages script from these; an index that loads
		// without its columns scripts as a CREATE INDEX with empty parens.
		if len(idx.KeyColumns) != 1 || idx.KeyColumns[0].Name != "parent_id" || !idx.KeyColumns[0].Descending {
			t.Errorf("key columns = %+v, want [parent_id DESC]", idx.KeyColumns)
		}
		if len(idx.IncludedColumns) != 1 || idx.IncludedColumns[0].Name != "note" {
			t.Errorf("included columns = %+v, want [note]", idx.IncludedColumns)
		}
		if _, _, err := findIndex(ctx, sc, dbName, "app", "child", "nope"); !errors.Is(err, gosmo.ErrNotFound) {
			t.Errorf("missing index: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("findStatistic", func(t *testing.T) {
		tbl, st, err := findStatistic(ctx, sc, dbName, "app", "child", "st_child_note")
		if err != nil {
			t.Fatalf("findStatistic: %v", err)
		}
		if tbl.Name != "child" || st.Name != "st_child_note" || !st.IsUserCreated {
			t.Errorf("findStatistic = %s / %+v, want a user-created st_child_note on child", tbl.Name, st)
		}
		if _, _, err := findStatistic(ctx, sc, dbName, "app", "child", "nope"); !errors.Is(err, gosmo.ErrNotFound) {
			t.Errorf("missing statistic: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("findForeignKey", func(t *testing.T) {
		tbl, fk, err := findForeignKey(ctx, sc, dbName, "app", "child", "fk_child_parent")
		if err != nil {
			t.Fatalf("findForeignKey: %v", err)
		}
		if tbl.Name != "child" {
			t.Errorf("owning table = %s, want child", tbl.Name)
		}
		if fk.ReferencedSchema != "app" || fk.ReferencedTable != "parent" || fk.DeleteAction != "CASCADE" {
			t.Errorf("fk = %+v, want app.parent ON DELETE CASCADE", fk)
		}
		if len(fk.Columns) != 1 || fk.Columns[0] != "parent_id" ||
			len(fk.ReferencedColumns) != 1 || fk.ReferencedColumns[0] != "id" {
			t.Errorf("fk columns = %v -> %v, want [parent_id] -> [id]", fk.Columns, fk.ReferencedColumns)
		}
		if _, _, err := findForeignKey(ctx, sc, dbName, "app", "child", "nope"); !errors.Is(err, gosmo.ErrNotFound) {
			t.Errorf("missing foreign key: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("changeTracking", func(t *testing.T) {
		on, err := d.TableChangeTrackingForContext(ctx, "app", "child")
		if err != nil {
			t.Fatalf("app.child: %v", err)
		}
		if !on.Enabled || !on.TrackColumnsUpdated {
			t.Errorf("app.child = %+v, want enabled with columns tracked", on)
		}
		// A table with tracking off must come back as a row, not as
		// ErrNotFound — the page shows it as "off", not as an error.
		off, err := d.TableChangeTrackingForContext(ctx, "app", "parent")
		if err != nil {
			t.Fatalf("app.parent: %v", err)
		}
		if off.Enabled {
			t.Errorf("app.parent = %+v, want tracking off", off)
		}
		if _, err := d.TableChangeTrackingForContext(ctx, "app", "nope"); !errors.Is(err, gosmo.ErrNotFound) {
			t.Errorf("missing table: err = %v, want ErrNotFound", err)
		}
	})
}
