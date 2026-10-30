package cmd

import (
	"context"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The dry-run fidelity harness (phase 8, D11): every writing command's
// --dry-run must print the T-SQL its real run then sends. Each case runs a
// command twice over fresh fakes — once with --dry-run, once without — and
// compares the dry run's tsql with what a fidelityWriter recorded the real
// run would have sent. A new writing command adds a case to
// fidelityCases; TestDryRunFidelityCoversEveryWriter fails until it does,
// and a new Writer method does not compile until fidelityWriter shadows it.

// fidelityWriter wraps a command's own fake writer. In a real run it makes
// each write twice: first through inner under one shared gosmo.WithScript
// context — every fake hands a scripted call to the real serverWriter, so
// script collects the batch gosmo would send — then through inner with the
// command's context, so the fake's simulated server state changes as the
// real write would change it and the reads after it see that. In a dry run
// it passes every call straight to inner, whose context is already
// applyWrite's. A call made under the other mode's context is a test
// failure: a dry run that writes, or a real run that only scripts.
type fidelityWriter struct {
	t      *testing.T
	inner  Writer
	dry    bool
	sctx   context.Context
	script *gosmo.ScriptCollector
	// sessions maps the session the fake's real create returned to the one
	// the scripted create did: the fake's has no Server to script through.
	sessions map[*gosmo.EventSession]*gosmo.EventSession
	// loaded is the rows each real bulk load consumed. A bulk copy is TDS,
	// not T-SQL: it is compared with the dry run's rows field instead.
	loaded []int64
}

var _ Writer = (*fidelityWriter)(nil)

func newFidelityWriter(t *testing.T, inner Writer, dry bool) *fidelityWriter {
	sctx, script := gosmo.WithScript(context.Background())
	return &fidelityWriter{t: t, inner: inner, dry: dry, sctx: sctx, script: script,
		sessions: map[*gosmo.EventSession]*gosmo.EventSession{}}
}

// shadow is a write's scripted twin in a real run: record runs it under the
// shared script. It fails the test for a call made under the wrong mode.
func (w *fidelityWriter) shadow(ctx context.Context, name string, record func(context.Context) error) {
	w.t.Helper()
	switch scripted := gosmo.Scripting(ctx); {
	case w.dry && !scripted:
		w.t.Errorf("%s: a dry run made a real write", name)
	case !w.dry && scripted:
		w.t.Errorf("%s: a real run made the write under WithScript", name)
	case !w.dry:
		if err := record(w.sctx); err != nil {
			w.t.Errorf("%s: scripting the real run's write: %v", name, err)
		}
	}
}

func (w *fidelityWriter) ApplyConfiguration(ctx context.Context, changes []gosmo.ConfigChange, opts gosmo.ConfigApplyOptions) error {
	w.shadow(ctx, "ApplyConfiguration", func(sctx context.Context) error {
		return w.inner.ApplyConfiguration(sctx, changes, opts)
	})
	return w.inner.ApplyConfiguration(ctx, changes, opts)
}

func (w *fidelityWriter) SetTraceFlag(ctx context.Context, flag int, on bool) error {
	w.shadow(ctx, "SetTraceFlag", func(sctx context.Context) error { return w.inner.SetTraceFlag(sctx, flag, on) })
	return w.inner.SetTraceFlag(ctx, flag, on)
}

func (w *fidelityWriter) KillSession(ctx context.Context, sessionID int) error {
	w.shadow(ctx, "KillSession", func(sctx context.Context) error { return w.inner.KillSession(sctx, sessionID) })
	return w.inner.KillSession(ctx, sessionID)
}

func (w *fidelityWriter) CreateEventSession(ctx context.Context, def gosmo.EventSessionDefinition) (*gosmo.EventSession, error) {
	var scripted *gosmo.EventSession
	w.shadow(ctx, "CreateEventSession", func(sctx context.Context) (err error) {
		scripted, err = w.inner.CreateEventSession(sctx, def)
		return err
	})
	es, err := w.inner.CreateEventSession(ctx, def)
	if scripted != nil && es != nil {
		w.sessions[es] = scripted
	}
	return es, err
}

// session is es's scripted twin, es itself in a dry run.
func (w *fidelityWriter) session(es *gosmo.EventSession) *gosmo.EventSession {
	if s, ok := w.sessions[es]; ok {
		return s
	}
	return es
}

func (w *fidelityWriter) StartEventSession(ctx context.Context, es *gosmo.EventSession) error {
	w.shadow(ctx, "StartEventSession", func(sctx context.Context) error { return w.inner.StartEventSession(sctx, w.session(es)) })
	return w.inner.StartEventSession(ctx, es)
}

func (w *fidelityWriter) StopEventSession(ctx context.Context, es *gosmo.EventSession) error {
	w.shadow(ctx, "StopEventSession", func(sctx context.Context) error { return w.inner.StopEventSession(sctx, w.session(es)) })
	return w.inner.StopEventSession(ctx, es)
}

func (w *fidelityWriter) DropEventSession(ctx context.Context, es *gosmo.EventSession) error {
	w.shadow(ctx, "DropEventSession", func(sctx context.Context) error { return w.inner.DropEventSession(sctx, w.session(es)) })
	return w.inner.DropEventSession(ctx, es)
}

func (w *fidelityWriter) SetDatabaseScopedConfig(ctx context.Context, d *gosmo.Database, name, value string, replica gosmo.ScopedConfigReplica) error {
	w.shadow(ctx, "SetDatabaseScopedConfig", func(sctx context.Context) error {
		return w.inner.SetDatabaseScopedConfig(sctx, d, name, value, replica)
	})
	return w.inner.SetDatabaseScopedConfig(ctx, d, name, value, replica)
}

func (w *fidelityWriter) SetDatabaseOption(ctx context.Context, d *gosmo.Database, opt gosmo.DatabaseOption, value string, term gosmo.Termination) error {
	w.shadow(ctx, "SetDatabaseOption", func(sctx context.Context) error {
		return w.inner.SetDatabaseOption(sctx, d, opt, value, term)
	})
	return w.inner.SetDatabaseOption(ctx, d, opt, value, term)
}

func (w *fidelityWriter) SetRecoveryModel(ctx context.Context, d *gosmo.Database, model gosmo.RecoveryModel) error {
	w.shadow(ctx, "SetRecoveryModel", func(sctx context.Context) error { return w.inner.SetRecoveryModel(sctx, d, model) })
	return w.inner.SetRecoveryModel(ctx, d, model)
}

func (w *fidelityWriter) SetCompatibilityLevel(ctx context.Context, d *gosmo.Database, level gosmo.CompatibilityLevel) error {
	w.shadow(ctx, "SetCompatibilityLevel", func(sctx context.Context) error { return w.inner.SetCompatibilityLevel(sctx, d, level) })
	return w.inner.SetCompatibilityLevel(ctx, d, level)
}

func (w *fidelityWriter) SetReadOnly(ctx context.Context, d *gosmo.Database, readOnly bool, term gosmo.Termination) error {
	w.shadow(ctx, "SetReadOnly", func(sctx context.Context) error { return w.inner.SetReadOnly(sctx, d, readOnly, term) })
	return w.inner.SetReadOnly(ctx, d, readOnly, term)
}

func (w *fidelityWriter) SetQueryStoreOptions(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreOptions) error {
	w.shadow(ctx, "SetQueryStoreOptions", func(sctx context.Context) error { return w.inner.SetQueryStoreOptions(sctx, d, opts) })
	return w.inner.SetQueryStoreOptions(ctx, d, opts)
}

func (w *fidelityWriter) CreateDatabase(ctx context.Context, req gosmo.CreateDatabaseRequest) error {
	w.shadow(ctx, "CreateDatabase", func(sctx context.Context) error { return w.inner.CreateDatabase(sctx, req) })
	return w.inner.CreateDatabase(ctx, req)
}

func (w *fidelityWriter) DropDatabase(ctx context.Context, d *gosmo.Database, term gosmo.Termination) error {
	w.shadow(ctx, "DropDatabase", func(sctx context.Context) error { return w.inner.DropDatabase(sctx, d, term) })
	return w.inner.DropDatabase(ctx, d, term)
}

func (w *fidelityWriter) TruncateTable(ctx context.Context, t *gosmo.Table) error {
	w.shadow(ctx, "TruncateTable", func(sctx context.Context) error { return w.inner.TruncateTable(sctx, t) })
	return w.inner.TruncateTable(ctx, t)
}

// BulkInsert has no scripted twin: a dry run never sends one (it reads the
// whole file and counts its rows), so a dry run reaching it is a failure.
func (w *fidelityWriter) BulkInsert(ctx context.Context, d *gosmo.Database, bc gosmo.BulkCopy, rows iter.Seq2[[]any, error]) (int64, error) {
	if w.dry || gosmo.Scripting(ctx) {
		w.t.Errorf("BulkInsert: a dry run (or a scripted context) reached the bulk load")
	}
	n, err := w.inner.BulkInsert(ctx, d, bc, rows)
	w.loaded = append(w.loaded, n)
	return n, err
}

// fidelityCase is one writing command line: argv after the command's path
// (the ad-hoc connection is added), and fakes, a fresh catalog and writer
// for each of the two runs.
type fidelityCase struct {
	command string
	argv    string
	fakes   func(t *testing.T) (Catalog, Writer)
}

// fidelityCases holds at least one case per writing command. Each changes
// something, so its tsql is never empty, and each passes more than one value
// where the command takes several, so order and count are compared too.
var fidelityCases = []fidelityCase{
	{"server config set", "MAXDOP 4 cost-threshold 50 --allow admin", configFakes},
	{"server config set", "maxdop 4 --override --allow admin", configFakes},
	{"server config reset", "maxdop Backup-Checksum --allow admin", configFakes},
	{"server traceflag on", "7745 1222 --allow admin", traceFlagFakes},
	{"server traceflag off", "1222 --allow admin", traceFlagFakes},
	{"db set", "app --option RECOVERY=FULL --option PAGE_VERIFY=CHECKSUM --option QUERY_STORE=ON " +
		"--option COMPATIBILITY_LEVEL=140 --allow admin", dbSetFakes},
	{"db set", "app --option READ_ONLY=ON --rollback-immediate --confirm app --allow admin", dbSetFakes},
	{"db create", "Scratch --collation Latin1_General_100_CI_AS --recovery full --compat 140 --allow admin", dbCreateFakes},
	{"db drop", "hr --confirm hr --allow admin", dbCreateFakes},
	{"db drop", "hr --confirm hr --close-connections --allow admin", dbCreateFakes},
	{"db scoped-config set", "app MAXDOP 4 --allow admin", scopedFakes},
	{"db scoped-config set", "app MAXDOP 2 --for-secondary --allow admin", scopedFakes},
	{"monitor kill", "87 --confirm 87 --allow admin", killFakes},
	{"xevent capture", "--template waits --duration 5 --session-database Sales --allow admin", captureFakes},
	{"data import", "dbo.t --in-file {csv} --truncate-first --confirm dbo.t --allow ddl", importFakes},
}

// fidelityExempt is the writing commands with no case, and why.
var fidelityExempt = map[string]string{
	"query run":   "--dry-run classifies the script and prints no T-SQL: what runs is the script given",
	"plan actual": "--dry-run classifies the script and prints no T-SQL: what runs is the script given",
}

func configFakes(*testing.T) (Catalog, Writer) {
	cat := newConfigSetCatalog()
	return cat, &configWriter{cat: cat}
}

func traceFlagFakes(*testing.T) (Catalog, Writer) {
	cat := &traceFlagCatalog{flags: []gosmo.TraceFlag{{Flag: 1222, Global: true}}}
	return cat, &traceFlagWriter{cat: cat}
}

func dbSetFakes(*testing.T) (Catalog, Writer) {
	cat := newDBSetCatalog()
	return cat, &dbSetWriter{cat: cat}
}

func dbCreateFakes(*testing.T) (Catalog, Writer) {
	cat := newDropCatalog()
	return cat, &dropWriter{cat: cat}
}

func scopedFakes(*testing.T) (Catalog, Writer) {
	cat := newScopedCatalog()
	return cat, &scopedWriter{cat: cat}
}

func killFakes(*testing.T) (Catalog, Writer) {
	return &killCatalog{target: userSession(87)}, &killWriter{}
}

func captureFakes(*testing.T) (Catalog, Writer) {
	cat, w := newCapture()
	return cat, w
}

// importCatalog is dbCatalog with the one table data import loads, in a
// database handle that scripts through a never-connected Server.
type importCatalog struct {
	dbCatalog
}

func (c *importCatalog) TableByName(_ context.Context, d *gosmo.Database, schema, name string) (*gosmo.Table, error) {
	return (&gosmo.Server{}).DatabaseRef(d.Name).TableRef(schema, name), nil
}

func (c *importCatalog) TableColumns(context.Context, *gosmo.Table) ([]*gosmo.Column, error) {
	return []*gosmo.Column{
		{Name: "id", DataType: "int", TypeSchema: "sys"},
		{Name: "name", DataType: "nvarchar", MaxLength: 40, IsNullable: true, TypeSchema: "sys"},
	}, nil
}

func (c *importCatalog) CollationCodePage(context.Context, string) (int, error) { return 1252, nil }

// importWriter scripts the TRUNCATE through the real serverWriter, ignores
// the real one, and consumes every row of a load.
type importWriter struct{ Writer }

func (importWriter) TruncateTable(ctx context.Context, t *gosmo.Table) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{}.TruncateTable(ctx, t)
	}
	return nil
}

func (importWriter) BulkInsert(_ context.Context, _ *gosmo.Database, _ gosmo.BulkCopy, rows iter.Seq2[[]any, error]) (int64, error) {
	var n int64
	for _, err := range rows {
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func importFakes(*testing.T) (Catalog, Writer) {
	cat := &importCatalog{dbCatalog{current: "App", objectCatalog: objectCatalog{objects: []*gosmo.ObjectInfo{
		{ObjectID: 9, Schema: "dbo", Name: "t", Type: "U", TypeDesc: "USER_TABLE"}}}}}
	return cat, importWriter{}
}

// commandGroups is every group constructor tree.go assembles the command
// line from, so the harness sees the same commands; the coverage test checks
// it against tree.go.
var commandGroups = map[string]func(Deps) []*spec.Command{
	"AgentContextCommand": func(d Deps) []*spec.Command { return []*spec.Command{AgentContextCommand(d)} },
	"ProfileCommands":     ProfileCommands,
	"ServerCommands":      ServerCommands,
	"DBCommands":          DBCommands,
	"ObjectCommands":      ObjectCommands,
	"QueryCommands":       QueryCommands,
	"PlanCommands":        PlanCommands,
	"ScriptCommands":      ScriptCommands,
	"DataCommands":        DataCommands,
	"MonitorCommands":     MonitorCommands,
	"XEventCommands":      XEventCommands,
}

func fidelityTree(d Deps) *spec.Tree {
	tree := &spec.Tree{Globals: []spec.Option{
		{Name: "output", Short: "o", Type: spec.TypeEnum, Enum: output.Formats, Default: "text"}}}
	for _, name := range slices.Sorted(maps.Keys(commandGroups)) {
		tree.Commands = append(tree.Commands, commandGroups[name](d)...)
	}
	return tree
}

// runFidelity runs c once, as a dry run when dry, over fresh fakes wrapped
// in a fidelityWriter.
func runFidelity(t *testing.T, c fidelityCase, csv string, dry bool) (*output.Result, *fidelityWriter, error) {
	t.Helper()
	cat, inner := c.fakes(t)
	w := newFidelityWriter(t, inner, dry)
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
	}
	argv := append(strings.Fields(c.command), "--server", "srv", "--auth", "windows")
	argv = append(argv, strings.Fields(strings.ReplaceAll(c.argv, "{csv}", csv))...)
	if dry {
		argv = append(argv, "--dry-run")
	}
	inv, err := args.Parse(fidelityTree(d), argv, args.Options{})
	if err != nil {
		t.Fatalf("%q: %v", argv, err)
	}
	if got := inv.Command.Name(); got != c.command {
		t.Fatalf("%q parsed as %s", argv, got)
	}
	res, err := inv.Command.Run(context.Background(), inv)
	return res, w, err
}

// TestDryRunFidelity: for every case, the dry run's tsql is byte for byte
// the script of the statements the real run sends, in the same order.
func TestDryRunFidelity(t *testing.T) {
	// xevent capture names its session at random and waits its duration.
	oldName, oldSleep := captureSessionName, sleepCtx
	t.Cleanup(func() { captureSessionName, sleepCtx = oldName, oldSleep })
	captureSessionName = func() string { return testCaptureSession }
	sleepCtx = func(context.Context, time.Duration) error { return nil }

	csv := filepath.Join(t.TempDir(), "t.csv")
	if err := os.WriteFile(csv, []byte("id,name\n1,a\n2,b\n3,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range fidelityCases {
		t.Run(c.command+" "+c.argv, func(t *testing.T) {
			dryRes, dw, err := runFidelity(t, c, csv, true)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			realRes, rw, err := runFidelity(t, c, csv, false)
			if err != nil {
				t.Fatalf("real run: %v", err)
			}
			if got := fieldValue(dryRes, "dryRun") + "/" + fieldValue(realRes, "dryRun"); got != "true/false" {
				t.Errorf("dryRun fields %s, want true/false", got)
			}
			if dw.script.Len() != 0 {
				t.Errorf("the dry run scripted outside applyWrite:\n%s", dw.script.String())
			}
			dry, sent := fieldValue(dryRes, "tsql"), rw.script.String()
			if sent == "" {
				t.Fatal("the real run sent nothing: the case changes nothing")
			}
			if dry != sent {
				t.Errorf("dry run's tsql:\n%s\nthe real run sent:\n%s", dry, sent)
			}
			if hasField(realRes, "tsql") {
				t.Error("the real run printed tsql")
			}
			if len(rw.loaded) > 0 {
				var n int64
				for _, l := range rw.loaded {
					n += l
				}
				if got := fieldValue(dryRes, "rows"); got != strconv.FormatInt(n, 10) {
					t.Errorf("dry run's rows %s, the real run loaded %d", got, n)
				}
			}
		})
	}
}

// TestDryRunFidelityCoversEveryWriter: every command with --dry-run has a
// case or an exemption, and every case names one; commandGroups is the list
// tree.go assembles.
func TestDryRunFidelityCoversEveryWriter(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "tree.go"))
	if err != nil {
		t.Fatal(err)
	}
	var inTree []string
	for _, m := range regexp.MustCompile(`cmd\.(\w+Commands?)\(d\)`).FindAllStringSubmatch(string(src), -1) {
		inTree = append(inTree, m[1])
	}
	slices.Sort(inTree)
	if want := slices.Sorted(maps.Keys(commandGroups)); !slices.Equal(inTree, want) {
		t.Errorf("tree.go assembles %q; commandGroups has %q", inTree, want)
	}

	writers := map[string]bool{}
	for _, c := range fidelityTree(Deps{}).Commands {
		if slices.ContainsFunc(c.Options, func(o spec.Option) bool { return o.Name == optDryRun }) {
			writers[c.Name()] = true
		}
	}
	cased := map[string]bool{}
	for _, c := range fidelityCases {
		cased[c.command] = true
		if !writers[c.command] {
			t.Errorf("case %q: not a command with --dry-run", c.command)
		}
	}
	for name := range fidelityExempt {
		if !writers[name] || cased[name] {
			t.Errorf("exemption %q: not a writer, or it has a case", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(writers)) {
		if !cased[name] && fidelityExempt[name] == "" {
			t.Errorf("%s has --dry-run and no fidelity case: add one to fidelityCases", name)
		}
	}
}
