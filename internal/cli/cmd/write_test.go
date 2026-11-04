package cmd

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

// fakeWriter records the writes a command makes, and whether each ran under
// gosmo.WithScript.
type fakeWriter struct {
	Writer
	calls    []string
	scripted []bool
	err      error
}

func (w *fakeWriter) SetTraceFlag(ctx context.Context, flag int, on bool) error {
	w.calls = append(w.calls, "SetTraceFlag")
	w.scripted = append(w.scripted, gosmo.Scripting(ctx))
	return w.err
}

// writeHarness is a writing command over fakes: dials counts the connects,
// and the command turns trace flag 3604 on, as server traceflag on will.
type writeHarness struct {
	dials  int
	writer Writer
	cfg    *config.Config
}

func (h *writeHarness) deps() Deps {
	return Deps{
		LoadConfig: func() (*config.Config, error) { return h.cfg, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			h.dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return nil },
		Writer:  func(*db.ServerConn) Writer { return h.writer },
	}
}

func (h *writeHarness) command(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"test", "write"},
		Options: append(ConnectOptions(), writeOptions()...),
		Class:   spec.ClassAdmin,
		Run: func(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
			res := &output.Result{}
			sc, _, w, err := d.connectWriter(ctx, inv, res)
			if err != nil {
				return res, err
			}
			defer sc.Close()
			res.Fields = append(res.Fields, output.Field{Key: "flag", Value: "3604"})
			return res, applyWrite(ctx, inv, res, func(ctx context.Context) error { return w.SetTraceFlag(ctx, 3604, true) })
		},
	}
}

func (h *writeHarness) run(t *testing.T, argv string) (*output.Result, error) {
	t.Helper()
	if h.cfg == nil {
		h.cfg = &config.Config{}
	}
	d := h.deps()
	tree := &spec.Tree{Commands: []*spec.Command{{Path: []string{"test"}, Class: spec.ClassRead}, h.command(d)}}
	inv, err := args.Parse(tree, append([]string{"test", "write"}, strings.Fields(argv)...), args.Options{})
	if err != nil {
		t.Fatalf("%q: %v", argv, err)
	}
	return inv.Command.Run(context.Background(), inv)
}

const adHoc = "--server srv --auth windows "

// A refused write is BLOCKED before anything dials, --dry-run included, and
// names both classes (D1).
func TestWriteGateBlocksBeforeDial(t *testing.T) {
	for _, argv := range []string{"", "--allow write", "--dry-run", "--allow ddl --dry-run"} {
		fw := &fakeWriter{}
		h := &writeHarness{writer: fw}
		res, err := h.run(t, adHoc+argv)
		e, ok := errors.AsType[*output.Error](err)
		if !ok || e.Code != output.CodeBlocked || e.Exit() != 7 {
			t.Errorf("%q: %v, want BLOCKED", argv, err)
			continue
		}
		if h.dials != 0 || len(fw.calls) != 0 {
			t.Errorf("%q: %d dials, writes %q; want none", argv, h.dials, fw.calls)
		}
		if !strings.Contains(e.Message, "test write is admin") || !strings.Contains(e.Hint, "--allow admin") {
			t.Errorf("%q: message %q, hint %q", argv, e.Message, e.Hint)
		}
		if got := fieldValue(res, "detectedClass") + "/" + fieldValue(res, "allowedClass"); !strings.HasPrefix(got, "admin/") ||
			got == "admin/admin" {
			t.Errorf("%q: classes %q", argv, got)
		}
	}
}

// --allow admin passes, dials once and makes the write for real.
func TestWriteGateAllows(t *testing.T) {
	fw := &fakeWriter{}
	h := &writeHarness{writer: fw}
	res, err := h.run(t, adHoc+"--allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if h.dials != 1 || len(fw.calls) != 1 || fw.scripted[0] {
		t.Errorf("%d dials, writes %q scripted %v; want one real write", h.dials, fw.calls, fw.scripted)
	}
	if got := fieldValue(res, "dryRun"); got != "false" {
		t.Errorf("dryRun %q", got)
	}
	if hasField(res, "tsql") {
		t.Error("a real run printed tsql")
	}
}

// The profile's ceiling caps --allow, and the hint says no --allow raises
// it.
func TestWriteGateCeiling(t *testing.T) {
	cfg := &config.Config{}
	c := config.Connection{Server: "srv", User: "sa"}
	cfg.AddOrUpdate(c)
	if err := cfg.SetProfile(c.GeneratedName(), "dev", config.AllowWrite); err != nil {
		t.Fatal(err)
	}
	for argv, want := range map[string]string{
		"--allow admin": "ceiling (max_allow) is write",
		"":              "above the profile's ceiling (max_allow write)",
	} {
		h := &writeHarness{writer: &fakeWriter{}, cfg: cfg}
		res, err := h.run(t, "-p dev "+argv)
		e, ok := errors.AsType[*output.Error](err)
		if !ok || e.Code != output.CodeBlocked || !strings.Contains(e.Hint, want) {
			t.Errorf("%q: %v, want BLOCKED with %q", argv, err, want)
			continue
		}
		if h.dials != 0 {
			t.Errorf("%q: dialled", argv)
		}
		if got := fieldValue(res, "allowedClass"); got != "write" && argv != "" {
			t.Errorf("%q: allowedClass %q", argv, got)
		}
	}
}

// --dry-run makes the writes under WithScript: nothing reaches the server.
func TestWriteDryRunScripts(t *testing.T) {
	fw := &fakeWriter{}
	h := &writeHarness{writer: fw}
	res, err := h.run(t, adHoc+"--allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(fw.scripted) != 1 || !fw.scripted[0] {
		t.Errorf("writes scripted %v, want one under WithScript", fw.scripted)
	}
	if fieldValue(res, "dryRun") != "true" || !hasField(res, "tsql") {
		t.Errorf("fields %+v", res.Fields)
	}
}

// A dry run's tsql is gosmo's own batch, captured through the real
// serverWriter (a Server never connected: WithScript intercepts the exec).
func dryRunResult(t *testing.T) *output.Result {
	t.Helper()
	h := &writeHarness{writer: serverWriter{&gosmo.Server{}}}
	res, err := h.run(t, adHoc+"--allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestWriteDryRunTSQL(t *testing.T) {
	if got := fieldValue(dryRunResult(t), "tsql"); !strings.Contains(got, "DBCC TRACEON(3604, -1) WITH NO_INFOMSGS") {
		t.Errorf("tsql %q", got)
	}
}

// A failed write maps like a read: a request gosmo refuses before sending is
// a usage error, a dry run's included.
func TestWriteError(t *testing.T) {
	for _, dry := range []string{"", "--dry-run"} {
		fw := &fakeWriter{err: errors.Join(gosmo.ErrInvalidRequest, errors.New("bad flag"))}
		h := &writeHarness{writer: fw}
		_, err := h.run(t, adHoc+"--allow admin "+dry)
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodeUsageError {
			t.Errorf("%q: %v, want USAGE_ERROR", dry, err)
		}
	}
}

// The dry-run envelope in text and json (D2): status ok, DRY_RUN beside the
// command's fields, the batch as a verbatim TSQL section.
func TestWriteDryRunEnvelope(t *testing.T) {
	res := dryRunResult(t)
	res.Status, res.Command = output.StatusOK, "test write"
	for _, f := range []output.Format{output.FormatText, output.FormatJSON} {
		var b bytes.Buffer
		if err := output.Render(&b, res, output.DefaultOptions(f)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("testdata", "dry_run."+string(f)+".golden")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if b.String() != string(want) {
			t.Errorf("%s differs from %s (go test -update once the change is meant):\n%s", f, path, b.String())
		}
	}
}

func fieldValue(res *output.Result, key string) string {
	for _, f := range res.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

func hasField(res *output.Result, key string) bool {
	for _, f := range res.Fields {
		if f.Key == key {
			return true
		}
	}
	return false
}
