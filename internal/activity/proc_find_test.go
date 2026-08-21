package activity

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// findQueryFor is the lookup Proc.Find issues, spelled out here so the fake
// answers the query the code actually sends rather than one this test made
// up.
func findQueryFor(p *Proc) string {
	return `select
	case when object_id('master.dbo.` + p.MasterName + `', 'P') is not null then 1 else 0 end,
	case when object_id('tempdb.dbo.` + p.TempDBName + `', 'P') is not null then 1 else 0 end`
}

// Find prefers master, and that preference is the point: a procedure in
// master survives a restart where a tempdb one does not, so a server with
// both must be reported as having master's.
func TestProcFindPrefersMaster(t *testing.T) {
	p := BlockProc
	for _, tc := range []struct {
		name             string
		master, inTempDB bool
		want             ProcLocation
	}{
		{"both", true, true, ProcMaster},
		{"master only", true, false, ProcMaster},
		{"tempdb only", false, true, ProcTempDB},
		{"neither", false, false, ProcNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := scriptedDB(t, map[string]reply{
				findQueryFor(p): {
					cols: []string{"in_master", "in_tempdb"},
					rows: [][]driver.Value{{tc.master, tc.inTempDB}},
				},
			})
			got, err := p.Find(context.Background(), db)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if got != tc.want {
				t.Errorf("Find = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProcFindReportsAFailedLookup(t *testing.T) {
	boom := errors.New("activity_test: lookup failed")
	p := BlockProc
	db, _ := scriptedDB(t, map[string]reply{findQueryFor(p): {err: boom}})

	got, err := p.Find(context.Background(), db)
	if err == nil {
		t.Fatalf("Find returned %v and no error", got)
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the driver's own wrapped", err)
	}
	if got != ProcNone {
		t.Errorf("Find = %v on a failed lookup, want ProcNone", got)
	}
}

// Install must reach the target database without a USE — it runs on a pooled
// connection whose database has to be left as it was found — and must send
// the body as a parameter rather than concatenating it into the batch, since
// the bodies are full of single quotes.
func TestProcInstallGoesThroughTheTargetDatabasesSpExecutesql(t *testing.T) {
	p := BlockProc
	for _, tc := range []struct {
		loc      ProcLocation
		wantStmt string
	}{
		{ProcMaster, "exec master.sys.sp_executesql @stmt"},
		{ProcTempDB, "exec tempdb.sys.sp_executesql @stmt"},
	} {
		t.Run(tc.loc.Database(), func(t *testing.T) {
			db, log := scriptedDB(t, nil)
			if err := p.Install(context.Background(), db, tc.loc); err != nil {
				t.Fatalf("Install: %v", err)
			}
			if n := log.count(tc.wantStmt); n != 1 {
				t.Errorf("Install ran %d statements matching %q; statements: %v", n, tc.wantStmt, log.qs)
			}
			if n := log.count("use "); n != 0 {
				t.Error("Install issued a USE: the pooled connection's database must be left as it was found")
			}
		})
	}
}

// ProcNone names no database, so there is nowhere to install: that has to be
// an error rather than a statement sent at whatever database the pooled
// connection happens to be sitting in.
func TestProcInstallRefusesProcNone(t *testing.T) {
	p := BlockProc
	db, log := scriptedDB(t, nil)

	if err := p.Install(context.Background(), db, ProcNone); err == nil {
		t.Fatal("Install(ProcNone) returned no error")
	}
	if n := len(log.qs); n != 0 {
		t.Errorf("Install(ProcNone) ran %d statements: %v", n, log.qs)
	}
}

// The tempdb copy must never carry the sp_ prefix. An sp_-prefixed name
// falls back to master when the current database has no such procedure, so
// installing into tempdb finds master's copy instead — verified live, where
// DROP PROCEDURE IF EXISTS dbo.sp_block in tempdb deleted master's.
func TestProcTempDBNameIsNotSpPrefixed(t *testing.T) {
	p := BlockProc
	if !strings.HasPrefix(p.MasterName, "sp_") {
		t.Errorf("MasterName = %q, want the sp_ name a hand-installed copy already has", p.MasterName)
	}
	if strings.HasPrefix(p.TempDBName, "sp_") {
		t.Errorf("TempDBName = %q; an sp_ name in tempdb resolves to master's copy", p.TempDBName)
	}
	// "dbo." spelled out: MasterName is a substring of TempDBName (sp_block
	// inside usp_block), so a bare Contains would pass on either name.
	if got := p.Script(ProcTempDB); !strings.Contains(got, "dbo."+p.TempDBName) || strings.Contains(got, "dbo."+p.MasterName) {
		t.Errorf("the tempdb script does not create dbo.%s:\n%s", p.TempDBName, got)
	}
	if got := p.Script(ProcMaster); !strings.Contains(got, "dbo."+p.MasterName) {
		t.Errorf("the master script does not create dbo.%s:\n%s", p.MasterName, got)
	}
	if got := p.Script(ProcNone); got != "" {
		t.Errorf("Script(ProcNone) = %q, want empty", got)
	}
	if got := p.Exec(ProcTempDB); got != "exec tempdb.dbo."+p.TempDBName {
		t.Errorf("Exec(ProcTempDB) = %q", got)
	}
	if got := p.Exec(ProcNone); got != "" {
		t.Errorf("Exec(ProcNone) = %q, want empty", got)
	}
}
