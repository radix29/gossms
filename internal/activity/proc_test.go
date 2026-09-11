package activity

import (
	"strings"
	"testing"
)

// procs are every helper procedure goSSMS installs; add new ones here to cover
// them.
var procs = []*Proc{BlockProc, WhoIsActiveProc}

// The tempdb copy isn't found by sp_ lookup, so its EXEC must name the
// database.
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

// The tempdb copy must not be sp_-prefixed: the name would resolve to master,
// failing CREATE OR ALTER and making DROP delete master's copy.
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
		// Only the master script may name the master copy.
		if strings.Contains(p.Script(ProcTempDB), "create or alter procedure dbo."+p.MasterName) {
			t.Errorf("the tempdb script for %s still creates the master name", p.MasterName)
		}
	}
}

// The script must be one batch for sp_executesql: GO is a syntax error there,
// and USE would move the pooled connection.
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
