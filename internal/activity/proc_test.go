package activity

import (
	"strings"
	"testing"
)

// procs are every helper procedure goSSMS installs. Each rule below holds for
// all of them, so a new one is covered by adding it here.
var procs = []*Proc{BlockProc, WhoIsActiveProc}

// The tempdb copy is not found by the sp_ prefix lookup that resolves a
// master one from any database, so its EXEC has to name the database.
func TestProcExecNamesItsDatabase(t *testing.T) {
	for _, p := range procs {
		for _, tc := range []struct {
			loc          ProcLocation
			wantDatabase string
			wantExec     string
		}{
			{ProcNone, "", ""},
			{ProcMaster, "master", "exec master.dbo." + p.MasterName},
			{ProcTempDB, "tempdb", "exec tempdb.dbo." + p.TempDBName},
		} {
			if got := tc.loc.Database(); got != tc.wantDatabase {
				t.Errorf("Database() = %q, want %q", got, tc.wantDatabase)
			}
			if got := p.Exec(tc.loc); got != tc.wantExec {
				t.Errorf("%s.Exec() = %q, want %q", p.MasterName, got, tc.wantExec)
			}
		}
	}
}

// The tempdb copy must not carry the sp_ prefix: SQL Server would resolve
// that name against master first, which makes CREATE OR ALTER fail there and
// makes DROP delete master's copy instead.
func TestProcTempDBNameHasNoSpPrefix(t *testing.T) {
	for _, p := range procs {
		if !strings.HasPrefix(p.MasterName, "sp_") {
			t.Errorf("master procedure name %q should keep the sp_ prefix", p.MasterName)
		}
		if strings.HasPrefix(p.TempDBName, "sp_") {
			t.Errorf("tempdb procedure name %q must not start with sp_", p.TempDBName)
		}
		for _, loc := range []ProcLocation{ProcMaster, ProcTempDB} {
			want := "create or alter procedure dbo." + p.Name(loc)
			if !strings.Contains(p.Script(loc), want) {
				t.Errorf("the %s script for %s does not create %q",
					loc.Database(), p.MasterName, p.Name(loc))
			}
		}
		// The name the script must *not* still carry: the master script is
		// allowed to name the master copy, the tempdb one is not.
		if strings.Contains(p.Script(ProcTempDB), "create or alter procedure dbo."+p.MasterName) {
			t.Errorf("the tempdb script for %s still creates the master name", p.MasterName)
		}
	}
}

// The script goes to sp_executesql as one batch: a GO in it would be a syntax
// error there, and an unqualified USE would leave the pooled connection in a
// database it was not found in.
func TestProcScriptIsOneUnqualifiedBatch(t *testing.T) {
	for _, p := range procs {
		for _, loc := range []ProcLocation{ProcMaster, ProcTempDB} {
			script := p.Script(loc)
			for _, line := range strings.Split(script, "\n") {
				switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line, "\r"))) {
				case "go":
					t.Errorf("%s %s script contains a GO, which sp_executesql cannot run",
						p.MasterName, loc.Database())
				}
			}
			for _, bad := range []string{"use [", "USE ["} {
				if strings.Contains(script, bad) {
					t.Errorf("%s %s script contains %q", p.MasterName, loc.Database(), bad)
				}
			}
		}
		if got := p.Script(ProcNone); got != "" {
			t.Errorf("%s.Script(ProcNone) = %.40q, want empty", p.MasterName, got)
		}
	}
}
