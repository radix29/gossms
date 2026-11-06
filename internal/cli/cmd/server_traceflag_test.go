package cmd

import (
	"errors"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// flagServer is an instanceServer with the trace flags on.
func flagServer(t *testing.T, flags ...gosmo.TraceFlag) *instanceServer {
	s := newInstanceServer(t)
	s.flags = flags
	return s
}

// runTraceFlag runs server traceflag <argv> (list, on or off first) over s;
// dials counts the connects.
func runTraceFlag(t *testing.T, s *instanceServer, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	return runServerCmd(t, s, "traceflag "+argv)
}

// traceCalls is the DBCC statements s was sent, as "7745 on".
func traceCalls(s *instanceServer) []string {
	var out []string
	for _, w := range s.writes {
		m := dbccTrace.FindStringSubmatch(strings.TrimSpace(w))
		out = append(out, m[2]+" "+map[string]string{"TRACEON": "on", "TRACEOFF": "off"}[m[1]])
	}
	return out
}

func TestServerTraceFlagList(t *testing.T) {
	cat := flagServer(t, gosmo.TraceFlag{Flag: 7745, Global: true}, gosmo.TraceFlag{Flag: 1222, Global: true}, gosmo.TraceFlag{Flag: 3604, Session: true})
	res, _, err := runTraceFlag(t, cat, "list")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"1222", "1", "0"}, {"3604", "0", "1"}, {"7745", "1", "0"}}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	// None on: an empty set, not an error.
	res, _, err = runTraceFlag(t, flagServer(t), "list")
	if err != nil || len(res.Sets) != 1 || len(res.Sets[0].Rows) != 0 {
		t.Errorf("%v, sets %+v", err, res.Sets)
	}
}

func TestServerTraceFlagOn(t *testing.T) {
	cat := flagServer(t, gosmo.TraceFlag{Flag: 1222, Global: true})
	res, dials, err := runTraceFlag(t, cat, "on 7745 1222 7745 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if dials != 1 || !slices.Equal(traceCalls(cat), []string{"7745 on", "1222 on"}) || cat.flagReads != 2 {
		t.Fatalf("%d dials, calls %q, %d reads", dials, traceCalls(cat), cat.flagReads)
	}
	set := res.Sets[0]
	want := [][]string{{"7745", "0", "1", "1"}, {"1222", "1", "1", "1"}}
	if got := setRows(set); set.Name != "change" || !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("set %q rows %q, want %q", set.Name, got, want)
	}
	if fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") {
		t.Errorf("fields %+v", res.Fields)
	}
	if !slices.Equal(res.Warnings, []string{restartNote}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestServerTraceFlagOff(t *testing.T) {
	cat := flagServer(t, gosmo.TraceFlag{Flag: 7745, Global: true})
	res, _, err := runTraceFlag(t, cat, "off 7745 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, [][]string{{"7745", "1", "0", "0"}}, slices.Equal) {
		t.Errorf("rows %q", got)
	}
	if len(cat.flags) != 0 || len(res.Warnings) != 0 || !slices.Equal(traceCalls(cat), []string{"7745 off"}) {
		t.Errorf("flags %v, calls %q, warnings %q", cat.flags, traceCalls(cat), res.Warnings)
	}
}

// Without --allow admin nothing dials (the 5.b gate); list needs none.
func TestServerTraceFlagGate(t *testing.T) {
	for _, argv := range []string{"on 7745", "off 7745 --dry-run", "on 7745 --allow write"} {
		s := flagServer(t)
		_, dials, err := runTraceFlag(t, s, argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(s.writes) != 0 {
			t.Errorf("%q: %v, %d dials, writes %q", argv, err, dials, s.writes)
		}
	}
}

// A flag that is not a positive whole number is exit 2 before connecting.
func TestServerTraceFlagArguments(t *testing.T) {
	for _, argv := range []string{"on 0 --allow admin", "on abc --allow admin", "on 7745 1.5 --allow admin",
		"off --allow admin -- -1", "on 0x10 --allow admin"} {
		s := flagServer(t)
		_, dials, err := runTraceFlag(t, s, argv)
		if errCode(err) != output.CodeUsageError || dials != 0 || len(s.writes) != 0 {
			t.Errorf("%q: %v, %d dials, writes %q", argv, err, dials, s.writes)
		}
	}
}

// --dry-run: gosmo's DBCC, the planned change, nothing applied.
func TestServerTraceFlagDryRun(t *testing.T) {
	cat := flagServer(t, gosmo.TraceFlag{Flag: 1222, Global: true})
	res, _, err := runTraceFlag(t, cat, "on 7745 1222 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.writes) != 0 || len(cat.flags) != 1 || cat.flagReads != 1 {
		t.Errorf("a dry run sent %q, flags %v, %d reads", cat.writes, cat.flags, cat.flagReads)
	}
	tsql := fieldValue(res, "tsql")
	i, j := strings.Index(tsql, "DBCC TRACEON(7745, -1) WITH NO_INFOMSGS"), strings.Index(tsql, "DBCC TRACEON(1222, -1) WITH NO_INFOMSGS")
	if i < 0 || j < i {
		t.Errorf("tsql %q", tsql)
	}
	set := res.Sets[0]
	want := [][]string{{"7745", "0", "1"}, {"1222", "1", "1"}}
	if got := setRows(set); set.Name != "planned_change" || !slices.EqualFunc(got, want, slices.Equal) || fieldValue(res, "dryRun") != "true" {
		t.Errorf("set %q rows %q, fields %+v", set.Name, got, res.Fields)
	}
	if !slices.Equal(res.Warnings, []string{restartNote}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// A startup-only flag is skipped without an error: the second read says so.
func TestServerTraceFlagNotTaken(t *testing.T) {
	cat := flagServer(t)
	cat.startup = []int{834}
	res, _, err := runTraceFlag(t, cat, "on 834 7745 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"834", "0", "1", "0"}, {"7745", "0", "1", "1"}}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if len(res.Warnings) != 2 || !strings.HasPrefix(res.Warnings[1], "trace flag 834 is not on: the server skips a startup-only flag") {
		t.Errorf("warnings %q", res.Warnings)
	}
	// None took: no restart note.
	cat = flagServer(t)
	cat.startup = []int{834}
	res, _, _ = runTraceFlag(t, cat, "on 834 --allow admin")
	if len(res.Warnings) != 1 || strings.Contains(res.Warnings[0], "lost when") {
		t.Errorf("warnings %q", res.Warnings)
	}
	// off of a flag the server keeps on.
	cat = flagServer(t, gosmo.TraceFlag{Flag: 834, Global: true}, gosmo.TraceFlag{Flag: 835, Global: true})
	cat.startup = []int{834, 835}
	res, _, _ = runTraceFlag(t, cat, "off 834 835 --allow admin")
	if !slices.Equal(res.Warnings, []string{"trace flags 834, 835 are still on after DBCC TRACEOFF."}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// The first refused flag stops the run; the change set still shows what the
// ones before it did, and a flag number the server does not know gets a hint.
func TestServerTraceFlagPartialFailure(t *testing.T) {
	cat := flagServer(t)
	cat.failFlag, cat.writeErr = 98765, sqlErr(2560, 16, "Parameter 1 is incorrect for this DBCC statement.")
	res, _, err := runTraceFlag(t, cat, "on 7745 98765 1222 --allow admin")
	if errCode(err) != output.CodeSQLError || !strings.Contains(err.(*output.Error).Hint, "knows no trace flag") {
		t.Fatalf("%v", err)
	}
	if !slices.Equal(traceCalls(cat), []string{"7745 on", "98765 on"}) {
		t.Errorf("calls %q", traceCalls(cat))
	}
	want := [][]string{{"7745", "0", "1", "1"}, {"98765", "0", "1", "0"}, {"1222", "0", "1", "0"}}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if !slices.Equal(res.Warnings, []string{restartNote}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// Without sysadmin, Msg 2571 (live-captured) is PERMISSION_DENIED, exit 9.
func TestServerTraceFlagPermissionDenied(t *testing.T) {
	s := flagServer(t)
	s.failFlag, s.writeErr = 3604, sqlErr(2571, 14, "User 'guest' does not have permission to run DBCC TRACEON.")
	_, _, err := runTraceFlag(t, s, "on 3604 --allow admin")
	if errCode(err) != output.CodePermissionDenied || !strings.Contains(err.(*output.Error).Hint, "sysadmin") {
		t.Errorf("%v", err)
	}
}

// A failed second read after a run that worked says the change ran.
func TestServerTraceFlagReadBackFails(t *testing.T) {
	cat := flagServer(t)
	cat.flagErr, cat.flagOK = errors.New("connection reset"), 1
	_, _, err := runTraceFlag(t, cat, "on 7745 --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.HasPrefix(e.Message, "the change ran, but DBCC TRACESTATUS could not be read back") {
		t.Errorf("%v", err)
	}
}
