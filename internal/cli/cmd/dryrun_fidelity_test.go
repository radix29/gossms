package cmd

import (
	"context"
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
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The dry-run fidelity harness (phase 8, D11): every writing command's
// --dry-run must print the T-SQL its real run then sends. Each case runs a
// command twice over fresh fakes — once with --dry-run, once without — and
// compares the dry run's tsql with the script of what the real run sent.
// Every writing command writes through gosmo directly: it sends its statements to gosmotest, and gosmo's statement observer
// reports each as the server accepted it, and they are rendered as the
// script gosmo.WithScript would have made. A new writing command adds a case
// to fidelityCases; TestDryRunFidelityCoversEveryWriter fails until it
// does.

// fidelityObserver collects what a run sent. In a real run each statement the
// server accepted joins script; in a dry run any statement is a failure.
type fidelityObserver struct {
	t      *testing.T
	dry    bool
	script *gosmo.ScriptCollector
	// loaded is the rows each real bulk load sent. A bulk copy is TDS, not
	// T-SQL: it is compared with the dry run's rows field instead.
	loaded []int64
}

// bulkLoaded is the row count gosmo's observer entry for a bulk load ends
// in: "INSERT BULK [dbo].[t] ([id], [name]) -- 3 rows".
var bulkLoaded = regexp.MustCompile(`^INSERT BULK .* -- (\d+) rows$`)

// observe is the statement observer of a run through gosmo directly: in a
// real run each statement the server accepted joins the script — a bulk
// load's count goes to loaded — and in a dry run any is a failure, a write
// made outside WithScript.
func (w *fidelityObserver) observe(e gosmo.ScriptEntry) {
	w.t.Helper()
	switch m := bulkLoaded.FindStringSubmatch(e.SQL); {
	case w.dry:
		w.t.Errorf("a dry run sent %q", e.SQL)
	case m != nil:
		n, _ := strconv.ParseInt(m[1], 10, 64)
		w.loaded = append(w.loaded, n)
	default:
		w.script.Add(e)
	}
}

func newFidelityObserver(t *testing.T, dry bool) *fidelityObserver {
	return &fidelityObserver{t: t, dry: dry, script: new(gosmo.ScriptCollector)}
}

// fidelityCase is one writing command line: argv after the command's path
// (the ad-hoc connection is added), and fakes, a fresh catalog (a
// gosmotest server's) for each of the two runs.
type fidelityCase struct {
	command string
	argv    string
	fakes   func(t *testing.T) *gosmo.Server
}

// fidelityCases holds at least one case per writing command. Each changes
// something, so its tsql is never empty, and each passes more than one value
// where the command takes several, so order and count are compared too.
var fidelityCases = []fidelityCase{
	{"server config set", "MAXDOP 4 cost-threshold 50 --allow admin", configFakes},
	{"server config set", "maxdop 4 --override --allow admin", configFakes},
	{"server config reset", "maxdop Backup-Compression --allow admin", configFakes},
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
	{"security grant", "SELECT,INSERT --on dbo.Orders --to etl_user --with-grant-option --allow admin", permFakes},
	{"security grant", "SELECT --on Customers --columns Email,Phone --to readers --allow admin", permFakes},
	{"security grant", "VIEW SERVER STATE --on SERVER --to etl --allow admin", permFakes},
	{"security deny", "CONTROL --on SCHEMA::Sales --to etl_user --cascade --allow admin", permFakes},
	{"security revoke", "SELECT --on SCHEMA::Sales --from etl_user --grant-option-only --allow admin", permFakes},
	{"security revoke", "EXECUTE --on TYPE::Phone --from readers --allow admin", permFakes},
	{"security role add-member", "readers ghost --allow admin", permFakes},
	{"security role remove-member", "sysadmin sa --scope server --allow admin", permFakes},
	{"job start", "Backup --step backup --allow admin", jobFakes},
	{"job start", "Backup --allow admin", jobFakes},
	{"job stop", "busy --allow admin", jobFakes},
	{"maint backup", "app --compression --checksum --allow admin", backupFakes},
	{"maint backup", "app --to /b/s1.bak --to /b/s2.bak --type log --copy-only --init --confirm app --allow admin", backupFakes},
	{"maint restore", "app_copy --from /b/app.bak --move-to-default --checksum --norecovery --allow admin", restoreFakes},
	{"maint restore", "hr --from /b/hr.bak --replace --close-connections --confirm hr --allow admin", restoreFakes},
	{"maint restore", "--from /b/multi.bak --file-number 2 --verify-only --allow admin", restoreFakes},
	{"maint index-rebuild", "dbo.Orders --online --maxdop 4 --fill-factor 90 --allow ddl", indexFakes},
	{"maint index-rebuild", "dbo.Orders --index IX_Orders_Date --reorganize --allow ddl", indexFakes},
	{"maint index-rebuild", "dbo.vTotals --index CIX_vTotals --maxdop 2 --allow ddl", indexFakes},
	{"maint update-stats", "dbo.Orders --fullscan --allow ddl", indexFakes},
	{"maint update-stats", "vTotals --statistic CIX_vTotals --sample-percent 20 --allow ddl", indexFakes},
	{"maint update-stats", "--all --resample --allow ddl", indexFakes},
	{"maint checkdb", "app --physical-only --no-index --maxdop 2 --allow admin", indexFakes},
}

// fidelityExempt is the writing commands with no case, and why.
var fidelityExempt = map[string]string{
	"query run":   "--dry-run classifies the script and prints no T-SQL: what runs is the script given",
	"plan actual": "--dry-run classifies the script and prints no T-SQL: what runs is the script given",
}

func configFakes(t *testing.T) *gosmo.Server {
	return newConfigSetServer(t).Server()
}

func traceFlagFakes(t *testing.T) *gosmo.Server {
	return flagServer(t, gosmo.TraceFlag{Flag: 1222, Global: true}).Server()
}

// dbSetFakes, dbCreateFakes and scopedFakes are real gosmo over a dbServer.
func dbSetFakes(t *testing.T) *gosmo.Server {
	return newDBSetServer(t).Server()
}

func dbCreateFakes(t *testing.T) *gosmo.Server {
	return newDBServer(t).Server()
}

func scopedFakes(t *testing.T) *gosmo.Server {
	return newScopedServer(t).Server()
}

func jobFakes(t *testing.T) *gosmo.Server {
	return newJobServer(t).Server()
}

// killFakes is real gosmo over a monitorServer with session 87.
func killFakes(t *testing.T) *gosmo.Server {
	return killServer(t, userSession(87), nil).Server()
}

// captureFakes is real gosmo over an xeServer whose login may create event
// sessions.
func captureFakes(t *testing.T) *gosmo.Server {
	return newXEServer(t).Server()
}

// importFakes is real gosmo over a catalogServer whose database App has the
// one table data import loads.
func importFakes(t *testing.T) *gosmo.Server {
	s := newCatalogServer(t, "App", &gosmo.ObjectInfo{ObjectID: 9, Schema: "dbo", Name: "t", Type: "U", TypeDesc: "USER_TABLE"})
	s.columns[9] = []*gosmo.Column{
		{Name: "id", OrdinalPosition: 1, DataType: "int", TypeSchema: "sys"},
		{Name: "name", OrdinalPosition: 2, DataType: "nvarchar", MaxLength: 40, IsNullable: true, TypeSchema: "sys"},
	}
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{"id", "int", 4, 10, 0, 0, 1252}, {"name", "nvarchar", 40, 0, 0, 1252, 1252}}
	}, "WHERE c.object_id = OBJECT_ID(@p1)")
	return s.Server()
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
	"SecurityCommands":    SecurityCommands,
	"JobCommands":         JobCommands,
	"MaintCommands":       MaintCommands,
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
// with a fidelityObserver.
func runFidelity(t *testing.T, c fidelityCase, csv string, dry bool) (*output.Result, *fidelityObserver, error) {
	t.Helper()
	srv := c.fakes(t)
	w := newFidelityObserver(t, dry)
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			return &db.ServerConn{Server: srv}, nil
		},
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
	ctx := gosmo.WithStatementObserver(context.Background(), w.observe)
	res, err := inv.Command.Run(ctx, inv)
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
			if len(dw.loaded) > 0 {
				t.Errorf("the dry run bulk loaded %d", dw.loaded)
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
