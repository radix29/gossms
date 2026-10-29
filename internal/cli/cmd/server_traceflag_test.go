package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// traceFlagCatalog answers DBCC TRACESTATUS from flags, in flag order, as the
// server does; readErr fails every read after the first afterReads.
type traceFlagCatalog struct {
	Catalog
	flags      []gosmo.TraceFlag
	reads      int
	readErr    error
	afterReads int
}

func (c *traceFlagCatalog) TraceFlags(context.Context) ([]gosmo.TraceFlag, error) {
	c.reads++
	if c.readErr != nil && c.reads > c.afterReads {
		return nil, c.readErr
	}
	out := slices.Clone(c.flags)
	slices.SortFunc(out, func(a, b gosmo.TraceFlag) int { return a.Flag - b.Flag })
	return out, nil
}

// traceFlagWriter turns flags on and off in its catalog as the server does,
// skipping a startup-only flag without an error; with err set it fails at
// flag failOn. Under WithScript it is the real serverWriter, so a dry run
// captures gosmo's DBCC and changes nothing.
type traceFlagWriter struct {
	Writer
	cat     *traceFlagCatalog
	calls   []string
	startup []int
	failOn  int
	err     error
}

func (w *traceFlagWriter) SetTraceFlag(ctx context.Context, flag int, on bool) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.SetTraceFlag(ctx, flag, on)
	}
	w.calls = append(w.calls, fmt.Sprintf("%d %v", flag, on))
	if w.err != nil && flag == w.failOn {
		return w.err
	}
	if slices.Contains(w.startup, flag) {
		return nil
	}
	w.cat.flags = slices.DeleteFunc(w.cat.flags, func(f gosmo.TraceFlag) bool { return f.Flag == flag })
	if on {
		w.cat.flags = append(w.cat.flags, gosmo.TraceFlag{Flag: flag, Global: true})
	}
	return nil
}

// runTraceFlag runs server traceflag <argv> (list, on or off first) over cat
// and w, an ad-hoc connection; dials counts the connects.
func runTraceFlag(t *testing.T, cat *traceFlagCatalog, w *traceFlagWriter, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	w.cat = cat
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
	}
	tree := &spec.Tree{Commands: ServerCommands(d)}
	fields := strings.Fields(argv)
	args0 := append([]string{"server", "traceflag", fields[0], "--server", "srv", "--auth", "windows"}, fields[1:]...)
	inv, perr := args.Parse(tree, args0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

// sqlErr is the server's error number n, wrapped as gosmo wraps it.
func sqlErr(n int32, class uint8, msg string) error {
	return fmt.Errorf("gosmo: TRACEON: %w", mssql.Error{Number: n, Class: class, Message: msg})
}

func TestServerTraceFlagList(t *testing.T) {
	cat := &traceFlagCatalog{flags: []gosmo.TraceFlag{{Flag: 7745, Global: true}, {Flag: 1222, Global: true}, {Flag: 3604, Session: true}}}
	res, _, err := runTraceFlag(t, cat, &traceFlagWriter{}, "list")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"1222", "1", "0"}, {"3604", "0", "1"}, {"7745", "1", "0"}}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	// None on: an empty set, not an error.
	res, _, err = runTraceFlag(t, &traceFlagCatalog{}, &traceFlagWriter{}, "list")
	if err != nil || len(res.Sets) != 1 || len(res.Sets[0].Rows) != 0 {
		t.Errorf("%v, sets %+v", err, res.Sets)
	}
}

func TestServerTraceFlagOn(t *testing.T) {
	cat, w := &traceFlagCatalog{flags: []gosmo.TraceFlag{{Flag: 1222, Global: true}}}, &traceFlagWriter{}
	res, dials, err := runTraceFlag(t, cat, w, "on 7745 1222 7745 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if dials != 1 || !slices.Equal(w.calls, []string{"7745 true", "1222 true"}) || cat.reads != 2 {
		t.Fatalf("%d dials, calls %q, %d reads", dials, w.calls, cat.reads)
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
	cat, w := &traceFlagCatalog{flags: []gosmo.TraceFlag{{Flag: 7745, Global: true}}}, &traceFlagWriter{}
	res, _, err := runTraceFlag(t, cat, w, "off 7745 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, [][]string{{"7745", "1", "0", "0"}}, slices.Equal) {
		t.Errorf("rows %q", got)
	}
	if len(cat.flags) != 0 || len(res.Warnings) != 0 {
		t.Errorf("flags %v, warnings %q", cat.flags, res.Warnings)
	}
}

// Without --allow admin nothing dials (the 5.b gate); list needs none.
func TestServerTraceFlagGate(t *testing.T) {
	for _, argv := range []string{"on 7745", "off 7745 --dry-run", "on 7745 --allow write"} {
		w := &traceFlagWriter{}
		_, dials, err := runTraceFlag(t, &traceFlagCatalog{}, w, argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(w.calls) != 0 {
			t.Errorf("%q: %v, %d dials, calls %q", argv, err, dials, w.calls)
		}
	}
}

// A flag that is not a positive whole number is exit 2 before connecting.
func TestServerTraceFlagArguments(t *testing.T) {
	for _, argv := range []string{"on 0 --allow admin", "on abc --allow admin", "on 7745 1.5 --allow admin",
		"off --allow admin -- -1", "on 0x10 --allow admin"} {
		w := &traceFlagWriter{}
		_, dials, err := runTraceFlag(t, &traceFlagCatalog{}, w, argv)
		if errCode(err) != output.CodeUsageError || dials != 0 || len(w.calls) != 0 {
			t.Errorf("%q: %v, %d dials, calls %q", argv, err, dials, w.calls)
		}
	}
}

// --dry-run: gosmo's DBCC, the planned change, nothing applied.
func TestServerTraceFlagDryRun(t *testing.T) {
	cat, w := &traceFlagCatalog{flags: []gosmo.TraceFlag{{Flag: 1222, Global: true}}}, &traceFlagWriter{}
	res, _, err := runTraceFlag(t, cat, w, "on 7745 1222 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 || len(cat.flags) != 1 || cat.reads != 1 {
		t.Errorf("a dry run applied %q, flags %v, %d reads", w.calls, cat.flags, cat.reads)
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
	cat, w := &traceFlagCatalog{}, &traceFlagWriter{startup: []int{834}}
	res, _, err := runTraceFlag(t, cat, w, "on 834 7745 --allow admin")
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
	res, _, _ = runTraceFlag(t, &traceFlagCatalog{}, &traceFlagWriter{startup: []int{834}}, "on 834 --allow admin")
	if len(res.Warnings) != 1 || strings.Contains(res.Warnings[0], "lost when") {
		t.Errorf("warnings %q", res.Warnings)
	}
	// off of a flag the server keeps on.
	cat, w = &traceFlagCatalog{flags: []gosmo.TraceFlag{{Flag: 834, Global: true}, {Flag: 835, Global: true}}}, &traceFlagWriter{startup: []int{834, 835}}
	res, _, _ = runTraceFlag(t, cat, w, "off 834 835 --allow admin")
	if !slices.Equal(res.Warnings, []string{"trace flags 834, 835 are still on after DBCC TRACEOFF."}) {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// The first refused flag stops the run; the change set still shows what the
// ones before it did, and a flag number the server does not know gets a hint.
func TestServerTraceFlagPartialFailure(t *testing.T) {
	cat := &traceFlagCatalog{}
	w := &traceFlagWriter{failOn: 98765, err: sqlErr(2560, 16, "Parameter 1 is incorrect for this DBCC statement.")}
	res, _, err := runTraceFlag(t, cat, w, "on 7745 98765 1222 --allow admin")
	if errCode(err) != output.CodeSQLError || !strings.Contains(err.(*output.Error).Hint, "knows no trace flag") {
		t.Fatalf("%v", err)
	}
	if !slices.Equal(w.calls, []string{"7745 true", "98765 true"}) {
		t.Errorf("calls %q", w.calls)
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
	w := &traceFlagWriter{failOn: 3604, err: sqlErr(2571, 14, "User 'guest' does not have permission to run DBCC TRACEON.")}
	_, _, err := runTraceFlag(t, &traceFlagCatalog{}, w, "on 3604 --allow admin")
	if errCode(err) != output.CodePermissionDenied || !strings.Contains(err.(*output.Error).Hint, "sysadmin") {
		t.Errorf("%v", err)
	}
}

// A failed second read after a run that worked says the change ran.
func TestServerTraceFlagReadBackFails(t *testing.T) {
	cat := &traceFlagCatalog{readErr: fmt.Errorf("gosmo: TRACESTATUS: connection reset"), afterReads: 1}
	_, _, err := runTraceFlag(t, cat, &traceFlagWriter{}, "on 7745 --allow admin")
	if e, ok := err.(*output.Error); !ok || !strings.HasPrefix(e.Message, "the change ran, but DBCC TRACESTATUS could not be read back") {
		t.Errorf("%v", err)
	}
}
