package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/version"
)

type runResult struct {
	exit           int
	stdout, stderr string
}

func runTree(t *testing.T, ctx context.Context, tree *spec.Tree, argv ...string) runResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdout: &stdout, Stderr: &stderr,
		Getenv: func(string) string { return "" },
		ReadFile: func(path string) ([]byte, error) {
			if path == "args.txt" {
				return []byte("version\n-o json\n"), nil
			}
			return nil, fs.ErrNotExist
		},
	}
	exit := run(ctx, tree, argv, env)
	return runResult{exit, stdout.String(), stderr.String()}
}

func runMain(t *testing.T, argv ...string) runResult {
	t.Helper()
	return runTree(t, context.Background(), commandTree(), argv...)
}

// envelope decodes a JSON result.
func envelope(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return v
}

func firstError(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	errs, _ := env["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("no errors in %v", env)
	}
	return errs[0].(map[string]any)
}

func TestVersion(t *testing.T) {
	r := runMain(t, "version")
	if r.exit != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.exit, r.stderr)
	}
	want := "STATUS: OK\nCOMMAND: version\nDURATION_MS: "
	if !strings.HasPrefix(r.stdout, want) {
		t.Errorf("stdout:\n%s\nwant prefix:\n%s", r.stdout, want)
	}
	for _, f := range version.Fields() {
		if !strings.Contains(r.stdout, "\n"+strings.ToUpper(f.Key)+": "+f.Value+"\n") {
			t.Errorf("no %s line in\n%s", f.Key, r.stdout)
		}
	}
	if !strings.HasSuffix(r.stdout, "\nCLI_SCHEMA: 1.0\n") {
		t.Errorf("no CLI_SCHEMA last:\n%s", r.stdout)
	}

	for _, argv := range [][]string{{"version", "-o", "json"}, {"-o", "json", "version"}, {"@args.txt"}} {
		r := runMain(t, argv...)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d %s", argv, r.exit, r.stderr)
		}
		env := envelope(t, r.stdout)
		data, _ := env["data"].(map[string]any)
		if env["status"] != "ok" || env["command"] != "version" || data["version"] != version.Version || data["cliSchema"] != "1.0" {
			t.Errorf("%v: %v", argv, env)
		}
	}

	r = runMain(t, "version", "-q")
	if strings.Contains(r.stdout, "STATUS") || !strings.HasPrefix(r.stdout, "VERSION: ") {
		t.Errorf("--quiet kept the header:\n%s", r.stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		argv       []string
		msg, hint  string
		jsonOutput bool
	}{
		{[]string{"nope"}, `unknown command "nope"`, "gossms help", false},
		{[]string{"verison"}, `unknown command "verison"`, "Did you mean `gossms version`?", false},
		{[]string{"-p", "x"}, "no command given", "Commands: version, help", false},
		{[]string{"--quiet"}, "no command given", "", false},
		{[]string{"version", "--bogus"}, "unknown option --bogus", "gossms help version", false},
		{[]string{"version", "extra"}, `unexpected argument "extra"`, "", false},
		{[]string{"help", "nope"}, `no help topic "nope"`, "gossms help", false},
		{[]string{"help", "versoin"}, `no help topic "versoin"`, "Did you mean `gossms help version`?", false},
		{[]string{"help", "version", "x"}, `no help topic "version x"`, "", false},
		{[]string{"@missing.txt"}, "response file missing.txt", "", false},
		{[]string{"version", "-o"}, "-o needs a value", "", false},
		{[]string{"version", "-o", "xml"}, `"xml" is not a valid value`, "", false},
		{[]string{"version", "--quiet=yes"}, "--quiet takes no value", "", false},
		{[]string{"version", "-q", "--quiet"}, "--quiet given more than once", "", false},
		// A usage error comes out as JSON when -o json was asked for, even
		// though parsing failed.
		{[]string{"nope", "-o", "json"}, `unknown command "nope"`, "gossms help", true},
		{[]string{"--output=json", "-p", "x"}, "no command given", "", true},
		// Help as JSON is the schema, which does not exist yet.
		{[]string{"help", "-o", "json"}, "help is available as text only", "", true},
		{[]string{"version", "--help", "-o", "json"}, "help is available as text only", "", true},
		{[]string{"--help", "-o", "json"}, "help is available as text only", "", true},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			r := runMain(t, tc.argv...)
			if r.exit != 2 {
				t.Errorf("exit %d, want 2", r.exit)
			}
			checkMentions(t, commandTree(), "stdout", r.stdout)
			if want := "gossms: USAGE_ERROR: "; !strings.HasPrefix(r.stderr, want) || strings.Count(r.stderr, "\n") != 1 {
				t.Errorf("stderr %q, want one line starting %q", r.stderr, want)
			}
			if tc.jsonOutput {
				env := envelope(t, r.stdout)
				e := firstError(t, env)
				if env["status"] != "error" || e["code"] != "USAGE_ERROR" || !strings.Contains(e["message"].(string), tc.msg) {
					t.Errorf("envelope %v", env)
				}
				return
			}
			if !strings.HasPrefix(r.stdout, "STATUS: ERROR\n") ||
				!strings.Contains(r.stdout, "\nERROR_CODE: USAGE_ERROR\nMESSAGE: ") ||
				!strings.Contains(r.stdout, tc.msg) || !strings.Contains(r.stdout, tc.hint) {
				t.Errorf("stdout:\n%s", r.stdout)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	top := runMain(t, "--help")
	if top.exit != 0 || !strings.HasPrefix(top.stdout, agentLine+"\n") {
		t.Fatalf("--help: exit %d\n%s", top.exit, top.stdout)
	}
	for _, s := range []string{"Commands:\n", "  version  ", "  help     ", "Global options:\n", "  -o, --output <text|json|jsonl|csv|tsv|markdown>  Output format (default: text)\n", "      --verbose"} {
		if !strings.Contains(top.stdout, s) {
			t.Errorf("--help lacks %q:\n%s", s, top.stdout)
		}
	}
	for _, argv := range [][]string{{"-h"}, {"help"}, {"-q", "--help"}} {
		if r := runMain(t, argv...); r.exit != 0 || r.stdout != top.stdout {
			t.Errorf("%v differs from --help (exit %d):\n%s", argv, r.exit, r.stdout)
		}
	}

	cmd := runMain(t, "help", "version")
	if cmd.exit != 0 || !strings.HasPrefix(cmd.stdout, "Usage: gossms version [options]\n") ||
		!strings.Contains(cmd.stdout, "\nExamples:\n  gossms version\n  gossms version -o json\n") {
		t.Fatalf("help version: exit %d\n%s", cmd.exit, cmd.stdout)
	}
	for _, argv := range [][]string{{"version", "--help"}, {"version", "-h"}, {"-h", "version"}} {
		if r := runMain(t, argv...); r.exit != 0 || r.stdout != cmd.stdout {
			t.Errorf("%v differs from help version:\n%s", argv, r.stdout)
		}
	}
	if r := runMain(t, "help", "help"); !strings.HasPrefix(r.stdout, "Usage: gossms help [<group>] [<command>] [options]\n") {
		t.Errorf("help help:\n%s", r.stdout)
	}
}

// groupTree is the real tree plus a group with one command whose handler
// does whatever the test sets.
func groupTree(run spec.Handler) *spec.Tree {
	tree := commandTree()
	tree.Commands = append(tree.Commands,
		&spec.Command{Path: []string{"grp"}, Summary: "A group", Class: spec.ClassRead},
		&spec.Command{Path: []string{"grp", "cmd"}, Summary: "A command", Class: spec.ClassRead, Run: run},
	)
	return tree
}

func TestGroupNode(t *testing.T) {
	tree := groupTree(func(context.Context, *spec.Invocation) (*output.Result, error) { return nil, nil })
	r := runTree(t, context.Background(), tree, "grp")
	if r.exit != 2 || !strings.Contains(r.stdout, "MESSAGE: grp: a command is required\nHINT: Commands: cmd. Run `gossms help grp` for usage.") {
		t.Errorf("grp: exit %d\n%s", r.exit, r.stdout)
	}
	r = runTree(t, context.Background(), tree, "grp", "--help")
	if r.exit != 0 || !strings.HasPrefix(r.stdout, "Usage: gossms grp <command>") || !strings.Contains(r.stdout, "  cmd  A command\n") {
		t.Errorf("grp --help: exit %d\n%s", r.exit, r.stdout)
	}
	if h := runTree(t, context.Background(), tree, "help", "grp"); h.stdout != r.stdout {
		t.Errorf("help grp differs:\n%s", h.stdout)
	}
	if h := runTree(t, context.Background(), tree, "help", "grp", "cdm"); !strings.Contains(h.stdout, "Did you mean `gossms help grp cmd`?") {
		t.Errorf("help grp cdm:\n%s", h.stdout)
	}
	r = runTree(t, context.Background(), tree, "grp", "cmd")
	if r.exit != 0 || !strings.Contains(r.stdout, "STATUS: OK\nCOMMAND: grp cmd\n") {
		t.Errorf("grp cmd: exit %d\n%s", r.exit, r.stdout)
	}
}

func TestHandlerErrors(t *testing.T) {
	cases := []struct {
		name    string
		run     spec.Handler
		argv    []string
		exit    int
		code    string
		msg     string
		stderrs []string // substrings of stderr
		nostd   string   // must not be in stderr
	}{
		{"panic", func(context.Context, *spec.Invocation) (*output.Result, error) { panic("boom") },
			nil, 1, "GENERAL_ERROR", "internal error: boom", []string{"gossms: GENERAL_ERROR: internal error: boom\n"}, "goroutine"},
		{"panic verbose", func(context.Context, *spec.Invocation) (*output.Result, error) { panic("boom") },
			[]string{"--verbose"}, 1, "GENERAL_ERROR", "internal error: boom", []string{"panic: boom\n", "goroutine"}, ""},
		{"plain error", func(context.Context, *spec.Invocation) (*output.Result, error) { return nil, errors.New("bad") },
			nil, 1, "GENERAL_ERROR", "bad", []string{"gossms: GENERAL_ERROR: bad\n"}, ""},
		{"coded error", func(context.Context, *spec.Invocation) (*output.Result, error) {
			return nil, output.NewError(output.CodeNotFound, "no such thing", "try x")
		}, nil, 8, "NOT_FOUND", "no such thing", []string{"gossms: NOT_FOUND: no such thing\n"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv := append([]string{"grp", "cmd", "-o", "json"}, c.argv...)
			r := runTree(t, context.Background(), groupTree(c.run), argv...)
			if r.exit != c.exit {
				t.Errorf("exit %d, want %d", r.exit, c.exit)
			}
			env := envelope(t, r.stdout)
			e := firstError(t, env)
			if env["status"] != "error" || env["command"] != "grp cmd" || e["code"] != c.code || e["message"] != c.msg {
				t.Errorf("envelope %v", env)
			}
			for _, s := range c.stderrs {
				if !strings.Contains(r.stderr, s) {
					t.Errorf("stderr lacks %q:\n%s", s, r.stderr)
				}
			}
			if c.nostd != "" && strings.Contains(r.stderr, c.nostd) {
				t.Errorf("stderr has %q:\n%s", c.nostd, r.stderr)
			}
		})
	}
}

// A context cancelled while the handler runs (the SIGINT path) is exit 130
// and CANCELLED, whatever the handler returned.
func TestCancelled(t *testing.T) {
	for name, handlerErr := range map[string]error{
		"handler ok":    nil,
		"handler error": output.NewError(output.CodeSQLError, "Operation cancelled by user", ""),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			tree := groupTree(func(ctx context.Context, _ *spec.Invocation) (*output.Result, error) {
				cancel()
				<-ctx.Done()
				return &output.Result{Fields: []output.Field{{Key: "k", Value: "v"}}}, handlerErr
			})
			r := runTree(t, ctx, tree, "grp", "cmd")
			if r.exit != 130 {
				t.Errorf("exit %d, want 130", r.exit)
			}
			if !strings.HasPrefix(r.stdout, "STATUS: CANCELLED\n") || !strings.Contains(r.stdout, "ERROR_CODE: CANCELLED\n") ||
				strings.Contains(r.stdout, "SQL_ERROR") {
				t.Errorf("stdout:\n%s", r.stdout)
			}
			if r.stderr != "gossms: CANCELLED: cancelled by interrupt\n" {
				t.Errorf("stderr %q", r.stderr)
			}
		})
	}
}

func TestScanOutput(t *testing.T) {
	for argv, want := range map[string]output.Format{
		"":                     output.FormatText,
		"x -o json":            output.FormatJSON,
		"x --output json":      output.FormatJSON,
		"x -o=json":            output.FormatJSON,
		"x --output=json":      output.FormatJSON,
		"x -o xml":             output.FormatText,
		"x -- -o json":         output.FormatText,
		"x -o json -o text":    output.FormatText,
		"x --output=text -o j": output.FormatText,
	} {
		if got := scanOutput(strings.Fields(argv)); got != want {
			t.Errorf("scanOutput(%q) = %s, want %s", argv, got, want)
		}
	}
}

// TestUsageErrorKinds: every parse error kind is a usage error, exit 2. A
// required argument needs a command that has one, which the phase-0 tree
// lacks.
func TestUsageErrorKinds(t *testing.T) {
	tree := groupTree(func(context.Context, *spec.Invocation) (*output.Result, error) { return nil, nil })
	tree.Lookup([]string{"grp", "cmd"}).Args = []spec.Arg{{Name: "name", Help: "A name", Required: true}}
	seen := map[args.Kind]bool{}
	for _, argv := range [][]string{
		{"-p", "x"}, {"nope"}, {"grp", "cmd", "x", "--bogus"}, {"grp", "cmd", "x", "-o"},
		{"grp", "cmd", "x", "-o", "xml"}, {"grp", "cmd", "x", "-q", "-q"}, {"grp", "cmd"},
		{"grp", "cmd", "x", "y"}, {"@missing.txt"},
	} {
		_, err := args.Parse(tree, argv, args.Options{ReadFile: func(string) ([]byte, error) { return nil, fs.ErrNotExist }})
		perr, ok := errors.AsType[*args.Error](err)
		if !ok {
			t.Fatalf("%v: parse error %v", argv, err)
		}
		seen[perr.Kind] = true
		r := runTree(t, context.Background(), tree, argv...)
		if r.exit != 2 || !strings.HasPrefix(r.stderr, "gossms: USAGE_ERROR: ") {
			t.Errorf("%v (kind %d): exit %d, stderr %q", argv, perr.Kind, r.exit, r.stderr)
		}
		checkMentions(t, tree, strings.Join(argv, " "), r.stdout)
	}
	for k := args.KindNoCommand; k <= args.KindResponseFile; k++ {
		if !seen[k] {
			t.Errorf("kind %d not exercised", k)
		}
	}
}

// TestCancelledBeforeRun: an interrupt that lands before the handler starts
// (the parent context already done) is still 130, not a handler outcome.
func TestCancelledBeforeRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	tree := groupTree(func(ctx context.Context, _ *spec.Invocation) (*output.Result, error) {
		ran = true
		return nil, ctx.Err()
	})
	r := runTree(t, ctx, tree, "grp", "cmd", "-o", "json")
	if r.exit != 130 {
		t.Errorf("exit %d, want 130 (handler ran: %v)", r.exit, ran)
	}
	if env := envelope(t, r.stdout); env["status"] != "cancelled" || firstError(t, env)["code"] != "CANCELLED" {
		t.Errorf("envelope %v", env)
	}
}
