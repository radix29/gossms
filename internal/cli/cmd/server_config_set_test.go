package cmd

import (
	"context"
	"errors"
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

// configWriter applies ApplyConfiguration's changes to its catalog as the
// server would: the configured value always, the running one for a dynamic
// option (min server memory 0 runs as 16). With err set it applies the
// first applied changes and fails; with pending too, it configures every
// change and runs none, as a refused RECONFIGURE does. Under WithScript it is the real
// serverWriter, so a dry run captures gosmo's batch and changes nothing.
type configWriter struct {
	Writer
	cat      *serverInfoCatalog
	batches  [][]gosmo.ConfigChange
	override []bool
	err      error
	applied  int
	pending  bool
}

func (w *configWriter) ApplyConfiguration(ctx context.Context, changes []gosmo.ConfigChange, opts gosmo.ConfigApplyOptions) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.ApplyConfiguration(ctx, changes, opts)
	}
	w.batches, w.override = append(w.batches, changes), append(w.override, opts.Override)
	for i, c := range changes {
		if w.err != nil && !w.pending && i >= w.applied {
			break
		}
		for _, o := range w.cat.opts {
			if o.Name != c.Name {
				continue
			}
			o.Value = c.Value
			if o.IsDynamic && !w.pending {
				o.ValueInUse = c.Value
				if o.Name == minServerMemory && c.Value == 0 {
					o.ValueInUse = 16
				}
			}
		}
	}
	return w.err
}

// newConfigSetCatalog is newServerConfigCatalog with the memory options'
// real ranges, show advanced options off, and locks, a self-configuring
// option whose default lies below its minimum.
func newConfigSetCatalog() *serverInfoCatalog {
	c := newServerConfigCatalog()
	for _, o := range c.opts {
		switch o.Name {
		case maxServerMemory:
			o.Minimum, o.Maximum = 128, 2147483647
		case minServerMemory:
			o.Maximum = 2147483647
		}
	}
	c.opts = append(c.opts,
		&gosmo.ConfigurationOption{Name: "show advanced options", Maximum: 1, HasDefault: true, IsDynamic: true},
		&gosmo.ConfigurationOption{Name: "locks", Value: 5000, ValueInUse: 5000, Minimum: 5000, Maximum: 2147483647,
			HasDefault: true, IsAdvanced: true},
		&gosmo.ConfigurationOption{Name: "min memory per query (KB)", Value: 1024, ValueInUse: 1024, Minimum: 512,
			Maximum: 2147483647, Default: 1024, HasDefault: true, IsDynamic: true, IsAdvanced: true},
	)
	return c
}

// runConfigWrite runs server config <argv> (set or reset first) over cat
// and w, an ad-hoc connection; dials counts the connects.
func runConfigWrite(t *testing.T, cat *serverInfoCatalog, w *configWriter, argv string) (res *output.Result, dials int, err error) {
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
	args0 := append([]string{"server", "config", fields[0], "--server", "srv", "--auth", "windows"}, fields[1:]...)
	inv, perr := args.Parse(tree, args0, args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

func errCode(err error) output.Code {
	if e, ok := errors.AsType[*output.Error](err); ok {
		return e.Code
	}
	return ""
}

func TestServerConfigSet(t *testing.T) {
	cat, w := newConfigSetCatalog(), &configWriter{}
	res, dials, err := runConfigWrite(t, cat, w, "set MAXDOP 4 cost-threshold 50 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if dials != 1 || len(w.batches) != 1 || w.override[0] {
		t.Fatalf("%d dials, batches %v, override %v", dials, w.batches, w.override)
	}
	if want := []gosmo.ConfigChange{{Name: "max degree of parallelism", Value: 4}, {Name: "cost threshold for parallelism", Value: 50}}; !slices.Equal(w.batches[0], want) {
		t.Errorf("batch %v, want %v", w.batches[0], want)
	}
	set := res.Sets[0]
	if set.Name != "change" {
		t.Errorf("set %q", set.Name)
	}
	want := [][]string{
		{"max degree of parallelism", "8", "4", "4", "4", "0"},
		{"cost threshold for parallelism", "5", "50", "50", "50", "0"},
	}
	if got := setRows(set); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 {
		t.Errorf("fields %+v, warnings %q", res.Fields, res.Warnings)
	}
	// --override reaches gosmo.
	w = &configWriter{}
	if _, _, err := runConfigWrite(t, newConfigSetCatalog(), w, "set maxdop 4 --allow admin --override"); err != nil || !w.override[0] {
		t.Errorf("%v, override %v", err, w.override)
	}
}

// Without --allow admin nothing dials (the 5.b gate, on the real command).
func TestServerConfigSetGate(t *testing.T) {
	for _, argv := range []string{"set maxdop 4", "set maxdop 4 --dry-run", "reset maxdop --allow write"} {
		w := &configWriter{}
		_, dials, err := runConfigWrite(t, newConfigSetCatalog(), w, argv)
		if errCode(err) != output.CodeBlocked || dials != 0 || len(w.batches) != 0 {
			t.Errorf("%q: %v, %d dials, %d batches", argv, err, dials, len(w.batches))
		}
	}
}

func TestParseConfigValue(t *testing.T) {
	opts := newConfigSetCatalog().opts
	opt := func(name string) *gosmo.ConfigurationOption {
		o, err := resolveConfig(opts, name)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	for _, tc := range []struct {
		name, value string
		want        int64 // -1: a usage error
	}{
		{"max-memory", "56GB", 57344},
		{"max-memory", "56gb", 57344},
		{"max-memory", "1.5GB", 1536},
		{"max-memory", "1TB", 1 << 20},
		{"max-memory", "4096", 4096},
		{"max-memory", "4096MB", 4096},
		{"max-memory", "1.3MB", -1},
		{"max-memory", "2KB", -1},
		{"max-memory", "1e3GB", -1},
		{"max-memory", "1/2GB", -1},
		{"max-memory", ".5GB", -1},
		{"max-memory", "99999999999999TB", -1}, // beyond int64
		{"min memory per query (KB)", "2MB", 2048},
		{"min memory per query (KB)", "2GB", -1},
		{"maxdop", "4", 4},
		{"maxdop", "-1", -1},
		{"maxdop", "4MB", -1},
		{"maxdop", "4.0", -1},
		{"maxdop", "four", -1},
	} {
		got, err := parseConfigValue(opt(tc.name), tc.value)
		switch {
		case tc.want == -1 && tc.value == "-1":
			if err != nil || got != -1 {
				t.Errorf("%s %s: %d, %v", tc.name, tc.value, got, err)
			}
		case tc.want == -1:
			if errCode(err) != output.CodeUsageError {
				t.Errorf("%s %s: %d, %v; want a usage error", tc.name, tc.value, got, err)
			}
		case err != nil || got != tc.want:
			t.Errorf("%s %s: %d, %v; want %d", tc.name, tc.value, got, err, tc.want)
		}
	}
}

// Every mistake in the arguments is caught before the batch: usage errors
// (exit 2) and an unknown name (exit 8).
func TestServerConfigSetArguments(t *testing.T) {
	for _, tc := range []struct {
		argv string
		code output.Code
	}{
		{"set maxdop 4 cost-threshold", output.CodeUsageError},
		{"set maxdop 40000", output.CodeUsageError},
		{"set max-memory 64", output.CodeUsageError}, // below its minimum, 128
		{"set maxdop 4 MAXDOP 2", output.CodeUsageError},
		{"set maxdop 4GB", output.CodeUsageError},
		{"set nothing 4", output.CodeNotFound},
		{"reset tiered-memory-enabled", output.CodeNotFound},
	} {
		w := &configWriter{}
		_, _, err := runConfigWrite(t, newConfigSetCatalog(), w, tc.argv+" --allow admin")
		if errCode(err) != tc.code || len(w.batches) != 0 {
			t.Errorf("%q: %v, %d batches; want %s and none", tc.argv, err, len(w.batches), tc.code)
		}
	}
	// A self-configuring option takes its default 0 below its minimum.
	w := &configWriter{}
	if _, _, err := runConfigWrite(t, newConfigSetCatalog(), w, "set locks 0 --allow admin"); err != nil || len(w.batches) != 1 {
		t.Errorf("locks 0: %v", err)
	}
}

// D10's blocks: exit 7, nothing runs, unless --accept-risk.
func TestServerConfigSetRisky(t *testing.T) {
	for _, tc := range []struct {
		argv, want string
	}{
		{"set max-memory 80GB", "max server memory (81920 MB) is above physical memory (65536 MB)."},
		{"set min-memory 70000", "min server memory (70000 MB) is above physical memory"},
		{"set max-memory 4GB min-memory 8GB", "max server memory (4096 MB) is below min server memory (8192 MB)."},
		{"set maxdop 16", "max degree of parallelism (16) is above the logical CPUs (8)."},
	} {
		w := &configWriter{}
		_, _, err := runConfigWrite(t, newConfigSetCatalog(), w, tc.argv+" --allow admin")
		e, ok := errors.AsType[*output.Error](err)
		if !ok || e.Code != output.CodeBlocked || e.Exit() != 7 || !strings.Contains(e.Message, tc.want) ||
			!strings.Contains(e.Hint, "--accept-risk") || len(w.batches) != 0 {
			t.Errorf("%q: %v, %d batches; want BLOCKED %q", tc.argv, err, len(w.batches), tc.want)
		}
		w = &configWriter{}
		if _, _, err := runConfigWrite(t, newConfigSetCatalog(), w, tc.argv+" --allow admin --accept-risk"); err != nil || len(w.batches) != 1 {
			t.Errorf("%q --accept-risk: %v, %d batches", tc.argv, err, len(w.batches))
		}
	}
	// max below the min already configured, and a default never blocks:
	// max server memory's is above any physical memory.
	cat := newConfigSetCatalog()
	cat.opts[5].Value = 8192 // min server memory
	if _, _, err := runConfigWrite(t, cat, &configWriter{}, "set max-memory 4GB --allow admin"); errCode(err) != output.CodeBlocked {
		t.Errorf("max below the configured min: %v", err)
	}
	if _, _, err := runConfigWrite(t, newConfigSetCatalog(), &configWriter{}, "reset max-memory maxdop --allow admin"); err != nil {
		t.Errorf("reset to the defaults: %v", err)
	}

	// Without VIEW SERVER STATE the figures are unknown: blocked, failing
	// closed, naming the permission.
	cat = newConfigSetCatalog()
	cat.info.SysInfoUnavailable, cat.info.PhysicalMemoryMB, cat.info.LogicalCPUCount = true, 0, 0
	for _, argv := range []string{"set maxdop 4", "set max-memory 8GB"} {
		_, _, err := runConfigWrite(t, cat, &configWriter{}, argv+" --allow admin")
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodeBlocked || !strings.Contains(e.Message, "VIEW SERVER STATE") {
			t.Errorf("%q: %v", argv, err)
		}
	}
	if _, _, err := runConfigWrite(t, cat, &configWriter{}, "set cost-threshold 50 --allow admin"); err != nil {
		t.Errorf("an unchecked option blocked: %v", err)
	}
}

func TestServerConfigSetCommittedWarning(t *testing.T) {
	res, _, err := runConfigWrite(t, newConfigSetCatalog(), &configWriter{}, "set max-memory 32GB --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "below currently committed memory (61210 MB)") {
		t.Errorf("warnings %q", res.Warnings)
	}
	cat := newConfigSetCatalog()
	cat.statsErr = denied("memory stats")
	res, _, _ = runConfigWrite(t, cat, &configWriter{}, "set max-memory 32GB --allow admin")
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "VIEW SERVER STATE") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// --dry-run: gosmo's batch, the planned change, nothing applied.
func TestServerConfigSetDryRun(t *testing.T) {
	cat, w := newConfigSetCatalog(), &configWriter{}
	res, _, err := runConfigWrite(t, cat, w, "set maxdop 4 backup-compression 1 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.batches) != 0 {
		t.Errorf("a dry run applied %v", w.batches)
	}
	if o, _ := resolveConfig(cat.opts, "maxdop"); o.Value != 8 {
		t.Errorf("maxdop changed to %d", o.Value)
	}
	tsql := fieldValue(res, "tsql")
	for _, want := range []string{"name IN (N'max degree of parallelism', N'backup compression default')",
		"EXEC sys.sp_configure N'max degree of parallelism', 4;", "EXEC sys.sp_configure N'backup compression default', 1;\nRECONFIGURE;"} {
		if !strings.Contains(tsql, want) {
			t.Errorf("tsql lacks %q:\n%s", want, tsql)
		}
	}
	if fieldValue(res, "dryRun") != "true" || res.Sets[0].Name != "planned_change" {
		t.Errorf("fields %+v, set %q", res.Fields, res.Sets[0].Name)
	}
	want := [][]string{
		{"max degree of parallelism", "8", "4", "1", "1", "1"},
		{"backup compression default", "0", "1", "1", "0", "0"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	// show advanced options already running 1: the first step does not fire.
	cat.opts[slices.IndexFunc(cat.opts, func(o *gosmo.ConfigurationOption) bool { return o.Name == "show advanced options" })].ValueInUse = 1
	res, _, _ = runConfigWrite(t, cat, w, "set maxdop 4 --allow admin --dry-run --override")
	if got := column(res.Sets[0], "enables_show_advanced"); !slices.Equal(got, []string{"0"}) {
		t.Errorf("enables_show_advanced %q", got)
	}
	if !strings.Contains(fieldValue(res, "tsql"), "RECONFIGURE WITH OVERRIDE;") {
		t.Errorf("--override not in the batch")
	}
}

// The dry-run warning for an option that is not dynamic. (runConfigWrite
// splits on spaces, so the option is renamed to one without.)
func TestServerConfigSetDryRunRestart(t *testing.T) {
	cat := newConfigSetCatalog()
	o, _ := resolveConfig(cat.opts, "fill factor (%)")
	o.Name = "fillfactor"
	res, _, err := runConfigWrite(t, cat, &configWriter{}, "set fillfactor 80 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "fillfactor is not dynamic: it runs 80 only after the SQL Server service restarts") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// After the batch, a second read: restart_required where the running value
// lags, min server memory's 0-runs-16 aside.
func TestServerConfigSetRestart(t *testing.T) {
	cat := newConfigSetCatalog()
	o, _ := resolveConfig(cat.opts, "fill factor (%)")
	o.Name = "fillfactor"
	res, _, err := runConfigWrite(t, cat, &configWriter{}, "set fillfactor 80 min-memory 0 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"fillfactor", "90", "80", "80", "0", "1"},
		{minServerMemory, "0", "0", "0", "16", "0"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "fillfactor is configured 80 but runs 0 until the SQL Server service restarts") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

// A batch that fails part way still reports what landed, and exits 5.
func TestServerConfigSetPartialFailure(t *testing.T) {
	cat := newConfigSetCatalog()
	w := &configWriter{applied: 1, err: fmt.Errorf("gosmo: apply configuration: %w",
		mssql.Error{Number: 5807, Class: 16, Message: "Recovery intervals above 10 minutes not recommended."})}
	res, _, err := runConfigWrite(t, cat, w, "set maxdop 4 cost-threshold 50 --allow admin")
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Exit() != 5 {
		t.Fatalf("%v, want exit 5", err)
	}
	want := [][]string{
		{"max degree of parallelism", "8", "4", "4", "4", "0"},
		{"cost threshold for parallelism", "5", "50", "5", "5", "0"},
	}
	if got := setRows(res.Sets[0]); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows %q, want %q", got, want)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "cost threshold for parallelism is configured 5, not the 50 requested") {
		t.Errorf("warnings %q", res.Warnings)
	}
	if strings.Contains(e.Hint, "--override") {
		t.Errorf("hint %q for a change that is not pending", e.Hint)
	}
}

// A refused RECONFIGURE leaves the changes configured but not running: the
// warning and the hint say they are pending.
func TestServerConfigSetReconfigureRefused(t *testing.T) {
	cat := newConfigSetCatalog()
	w := &configWriter{err: fmt.Errorf("gosmo: apply configuration: %w",
		mssql.Error{Number: 5807, Class: 16, Message: "Recovery intervals above 60 minutes not recommended."})}
	w.pending = true
	res, _, err := runConfigWrite(t, cat, w, "set cost-threshold 50 --allow admin")
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Exit() != 5 || !strings.Contains(e.Hint, "configured but not running") || !strings.Contains(e.Hint, "--override") {
		t.Fatalf("%v, want exit 5 hinting --override", err)
	}
	if got := setRows(res.Sets[0]); !slices.Equal(got[0], []string{"cost threshold for parallelism", "5", "50", "50", "5", "1"}) {
		t.Errorf("row %q", got[0])
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "the next RECONFIGURE on this server, anyone's, will") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestServerConfigReset(t *testing.T) {
	cat, w := newConfigSetCatalog(), &configWriter{}
	res, _, err := runConfigWrite(t, cat, w, "reset maxdop Backup-Checksum maxdop --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := []gosmo.ConfigChange{{Name: "max degree of parallelism", Value: 0}, {Name: "Backup checksum default", Value: 0}}; !slices.Equal(w.batches[0], want) {
		t.Errorf("batch %v, want %v", w.batches[0], want)
	}
	if got := column(res.Sets[0], "after_configured"); !slices.Equal(got, []string{"0", "0"}) {
		t.Errorf("after %q", got)
	}
	// No default known: exit 11, nothing runs.
	w = &configWriter{}
	_, _, err = runConfigWrite(t, newConfigSetCatalog(), w, "reset maxdop tiered --allow admin")
	if errCode(err) != output.CodeNotFound {
		t.Errorf("tiered: %v", err)
	}
	cat = newConfigSetCatalog()
	o, _ := resolveConfig(cat.opts, "tiered memory enabled")
	o.Name = "tiered"
	_, _, err = runConfigWrite(t, cat, w, "reset maxdop tiered --allow admin")
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Exit() != 11 || !strings.Contains(e.Hint, "server config set") || len(w.batches) != 0 {
		t.Errorf("%v, %d batches; want exit 11 and none", err, len(w.batches))
	}
}
