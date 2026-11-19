package activity

import (
	"regexp"
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
			{ProcMaster, "master", "exec master.dbo." + p.MasterName + fixedArgs(p)},
			{ProcTempDB, "tempdb", "exec tempdb.dbo." + p.TempDBName + fixedArgs(p)},
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

// fixedArgs is how p's Fixed arguments trail its exec, with the leading space.
func fixedArgs(p *Proc) string {
	var args []string
	for _, o := range p.Fixed {
		args = append(args, o.Param+" = "+o.Value)
	}
	if len(args) == 0 {
		return ""
	}
	return " " + strings.Join(args, ", ")
}

// sp_WhoIsActive must run in its fixed-width format: the default pads numbers
// for proportional fonts, which leaves them ragged in a terminal grid.
func TestWhoIsActiveRunsFixedWidthFormat(t *testing.T) {
	if got := WhoIsActiveProc.Exec(ProcTempDB); !strings.Contains(got, "@format_output = 2") {
		t.Errorf("Exec = %q, want @format_output = 2", got)
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
			for line := range strings.SplitSeq(script, "\n") {
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

// An option that is on is passed by name, in the order given; one that is off
// is left to the procedure's default.
func TestProcExecPassesOptionsThatAreOn(t *testing.T) {
	p := WhoIsActiveProc
	if len(p.Options) < 2 {
		t.Fatalf("%s offers %d options, want at least 2", p.MasterName, len(p.Options))
	}
	a, b := p.Options[0], p.Options[1]
	base := "exec tempdb.dbo." + p.TempDBName + fixedArgs(p)
	sep := ", "
	if len(p.Fixed) == 0 {
		sep = " "
	}
	for _, tc := range []struct {
		on   []ProcOption
		want string
	}{
		{nil, base},
		{[]ProcOption{a}, base + sep + a.Param + " = " + a.Value},
		{[]ProcOption{a, b}, base + sep + a.Param + " = " + a.Value + ", " + b.Param + " = " + b.Value},
	} {
		if got := p.Exec(ProcTempDB, tc.on...); got != tc.want {
			t.Errorf("Exec(%v) = %q, want %q", tc.on, got, tc.want)
		}
	}
	if got := p.Exec(ProcNone, a); got != "" {
		t.Errorf("Exec(ProcNone) = %q, want empty", got)
	}
}

// Every option names a parameter the installed procedure declares, so a typo
// is caught here rather than as Msg 8145 on the tab.
func TestProcOptionsNameDeclaredParameters(t *testing.T) {
	for _, p := range procs {
		script := p.Script(ProcTempDB)
		// The declarations end at the line holding only AS (the script is CRLF).
		head := script
		if loc := regexp.MustCompile(`(?m)^AS\r?$`).FindStringIndex(script); loc != nil {
			head = script[:loc[0]]
		} else if len(p.Options) > 0 {
			t.Fatalf("%s: no AS line ends the parameter list", p.MasterName)
		}
		for _, o := range p.Options {
			if !strings.HasPrefix(o.Param, "@") || o.Label == "" || o.Value == "" || o.Key == 0 {
				t.Errorf("%s option %+v is incomplete", p.MasterName, o)
				continue
			}
			if !regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(o.Param) + `\s+\w`).MatchString(head) {
				t.Errorf("%s declares no parameter %s", p.MasterName, o.Param)
			}
		}
	}
}
