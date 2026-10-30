package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/audit"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// auditCall is one run against a tree with its audit log at path, stdin and
// the environment given.
func auditCall(t *testing.T, tree *spec.Tree, path, stdin string, vars map[string]string, files map[string][]byte, argv ...string) runResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(k string) string { return vars[k] },
		ReadFile: func(p string) ([]byte, error) {
			if b, ok := files[p]; ok {
				return b, nil
			}
			return nil, os.ErrNotExist
		},
		AuditPath: path,
	}
	exit := run(context.Background(), tree, argv, env)
	return runResult{exit, stdout.String(), stderr.String()}
}

// auditLines is the log at path, one record per line, and its raw text.
func auditLines(t *testing.T, path string) ([]audit.Record, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var recs []audit.Record
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var r audit.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		recs = append(recs, r)
	}
	return recs, string(data)
}

// noDial is profileDeps whose connect fails the test: the calls here are
// all decided before connecting.
func noDial(t *testing.T) cmd.Deps {
	return profileDeps(t, func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
		t.Fatal("connected")
		return nil, nil
	})
}

// TestAuditLogsEveryWritersRefusal runs every command that changes the
// server — query run, and each command with --dry-run — from its first
// example that passes --allow, with --allow and --dry-run taken out so the
// gate refuses it: every one must leave one line naming it, BLOCKED, with
// the profile's target and login and no secret.
func TestAuditLogsEveryWritersRefusal(t *testing.T) {
	tree := newCommandTree(noDial(t))
	var covered []string
	for _, c := range tree.Commands {
		writes := c.Name() == "query run" || slices.ContainsFunc(c.Options, func(o spec.Option) bool { return o.Name == "dry-run" })
		// The first example that reaches above read.
		i := slices.IndexFunc(c.Examples, func(ex string) bool { return strings.Contains(ex, "--allow") })
		if !writes || i < 0 {
			continue
		}
		words, _ := shellWords(c.Examples[i])
		var argv []string
		for i := 1; i < len(words); i++ {
			switch {
			case words[i] == "--allow":
				i++
			case words[i] == "--dry-run":
			case words[i] == "-p" || words[i] == "--profile":
				i++
			default:
				argv = append(argv, words[i])
			}
		}
		argv = append(argv, "-p", "dev", "-o", "json")
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		r := auditCall(t, tree, path, "", nil, map[string][]byte{"fix.sql": []byte("UPDATE t SET a = 1")}, argv...)
		if r.exit != 7 {
			t.Errorf("%s: exit %d, want 7 (BLOCKED)\n%s%s", c.Name(), r.exit, r.stdout, r.stderr)
			continue
		}
		recs, raw := auditLines(t, path)
		if len(recs) != 1 {
			t.Errorf("%s: %d audit lines, want 1:\n%s", c.Name(), len(recs), raw)
			continue
		}
		got := recs[0]
		if got.Command != c.Name() || got.Outcome != "blocked" || got.Exit != 7 || got.Profile != "dev" ||
			got.Server != "devsrv,1500" || got.Database != "app" || got.Login != "agent" || got.Class == "" || got.Class == "read" {
			t.Errorf("%s: record %+v", c.Name(), got)
		}
		if strings.Contains(raw, "s3cret-pw") {
			t.Errorf("%s: the profile's password is in the log: %s", c.Name(), raw)
		}
		covered = append(covered, c.Name())
	}
	for _, want := range []string{"query run", "server config set", "server config reset", "server traceflag on",
		"server traceflag off", "db set", "db scoped-config set", "data import", "monitor kill", "xevent capture"} {
		if !slices.Contains(covered, want) {
			t.Errorf("%s: not covered (covered: %v)", want, covered)
		}
	}
}

// TestAuditQueryRun: a read is not logged; a write's dry run is, with its
// class, dryRun and the script's hash, never its text.
func TestAuditQueryRun(t *testing.T) {
	sess := &fakeSession{t: t}
	d, _ := queryDeps(t, sess)
	tree := newCommandTree(d)
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	if r := auditCall(t, tree, path, "", nil, nil, "query", "run", "-p", "dev", "--sql", "SELECT 1"); r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if recs, raw := auditLines(t, path); len(recs) != 0 {
		t.Fatalf("a read was logged:\n%s", raw)
	}

	script := "UPDATE dbo.t SET secretcol = 1"
	if r := auditCall(t, tree, path, "", nil, nil, "query", "run", "-p", "dev", "--sql", script, "--allow", "write", "--dry-run"); r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	if r := auditCall(t, tree, path, "", nil, nil, "query", "run", "-p", "dev", "--sql", script, "--allow", "write"); r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	recs, raw := auditLines(t, path)
	if len(recs) != 2 {
		t.Fatalf("%d lines, want 2:\n%s", len(recs), raw)
	}
	if strings.Contains(raw, "secretcol") {
		t.Errorf("the script is in the log:\n%s", raw)
	}
	for i, wantDry := range []bool{true, false} {
		r := recs[i]
		if r.Class != "write" || r.DryRun != wantDry || r.Outcome != "ok" || r.SQLSha256 != audit.Hash(script) ||
			r.Command != "query run" || r.Login != "agent" {
			t.Errorf("line %d: %+v", i, r)
		}
		if !slices.Contains(r.Args, "--sql=sha256:"+audit.Hash(script)) || !slices.Contains(r.Args, "--allow=write") {
			t.Errorf("line %d args %q", i, r.Args)
		}
	}
	if !slices.Contains(recs[0].Args, "--dry-run") || slices.Contains(recs[1].Args, "--dry-run") {
		t.Errorf("args: %q / %q", recs[0].Args, recs[1].Args)
	}
}

// TestAuditUnwritableRefusesWritesNotReads: with a log that cannot be
// created, a change and its dry run are refused (exit 1) before connecting;
// a read runs.
func TestAuditUnwritableRefusesWritesNotReads(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "audit.jsonl") // under a regular file: never creatable

	tree := newCommandTree(noDial(t))
	for _, argv := range [][]string{
		{"server", "traceflag", "on", "1222", "-S", "adhoc", "--allow", "admin"},
		{"server", "traceflag", "on", "1222", "-S", "adhoc", "--allow", "admin", "--dry-run"},
		{"monitor", "kill", "55", "--confirm", "55", "-S", "adhoc", "--allow", "admin"},
		{"query", "run", "-p", "dev", "--sql", "DELETE t", "--allow", "write", "--dry-run"},
	} {
		r := auditCall(t, tree, path, "", nil, nil, append(argv, "-o", "json")...)
		if r.exit != 1 || !strings.Contains(r.stdout, "audit log cannot be written") {
			t.Errorf("%q: exit %d, want 1 naming the audit log\n%s%s", argv, r.exit, r.stdout, r.stderr)
		}
	}

	sess := &fakeSession{t: t}
	d, dialled := queryDeps(t, sess)
	r := auditCall(t, newCommandTree(d), path, "", nil, nil, "query", "run", "-p", "dev", "--sql", "SELECT 1")
	if r.exit != 0 || *dialled != 1 {
		t.Errorf("a read: exit %d, dialled %d\n%s%s", r.exit, *dialled, r.stdout, r.stderr)
	}
	// A refusal is still answered as BLOCKED; the log's failure is a warning.
	r = auditCall(t, newCommandTree(d), path, "", nil, nil, "query", "run", "-p", "dev", "--sql", "DELETE t", "-o", "json")
	if r.exit != 7 || !strings.Contains(r.stdout, "The audit log could not be written") {
		t.Errorf("a refusal: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}

// TestAuditRefusalWithoutGate: a refusal no gate made — query classify
// --allow — is logged with the class the script was found to have.
func TestAuditRefusalWithoutGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r := auditCall(t, commandTree(), path, "", nil, nil, "query", "classify", "--sql", "DROP TABLE t", "--allow", "write")
	if r.exit != 7 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	recs, raw := auditLines(t, path)
	if len(recs) != 1 || recs[0].Class != "ddl" || recs[0].Outcome != "blocked" || recs[0].SQLSha256 != audit.Hash("DROP TABLE t") {
		t.Fatalf("log:\n%s", raw)
	}
	// Without --allow, classify refuses nothing and logs nothing.
	auditCall(t, commandTree(), path, "", nil, nil, "query", "classify", "--sql", "DROP TABLE t")
	if recs, raw := auditLines(t, path); len(recs) != 1 {
		t.Fatalf("log:\n%s", raw)
	}
}

// FuzzAuditNeverHoldsSQL: whatever the T-SQL, however it is given — --sql,
// --sql=, -Q, --sql-base64, --file - (stdin), an @file response file — and
// with a parameter value and a password from the environment or stdin,
// none of them reaches the log.
func FuzzAuditNeverHoldsSQL(f *testing.F) {
	for _, s := range []string{"x", "' OR 1=1 --", "\n\r\t\"", "GO\nDROP TABLE t", "ünï©ødé", "a,b=c:int"} {
		for form := range 7 {
			f.Add(s, uint8(form))
		}
	}
	f.Fuzz(func(t *testing.T, text string, form uint8) {
		if strings.ContainsAny(text, "\x00") {
			return
		}
		const marker, paramMark, pw = "Zq7SQLMARK", "Zq7PARAMMARK", "Zq7PASSWORD"
		script := "UPDATE dbo.t SET c = N'" + strings.ReplaceAll(text, "'", "''") + marker + "'"
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		vars := map[string]string{"PW": pw}
		stdin := ""
		files := map[string][]byte{}
		var argv []string
		switch form % 7 {
		case 0:
			argv = []string{"--sql", script}
		case 1:
			argv = []string{"--sql=" + script}
		case 2:
			argv = []string{"-Q", script}
		case 3:
			argv = []string{"--sql-base64", base64.StdEncoding.EncodeToString([]byte(script))}
		case 4:
			argv, stdin = []string{"--file", "-"}, script
		case 5:
			files["args.txt"] = []byte("--sql\n" + strings.ReplaceAll(script, "\n", " ") + "\n")
			argv = []string{"@args.txt"}
		case 6:
			argv, stdin = []string{"--sql", script, "--password-stdin"}, pw+"\n"
		}
		argv = append([]string{"query", "run", "-S", "nowhere", "--user", "u", "--password-env", "PW",
			"--param", "p=" + text + paramMark + ":nvarchar(max)"}, argv...)
		if form%7 == 6 {
			argv = slices.DeleteFunc(argv, func(s string) bool { return s == "--password-env" || s == "PW" })
		}
		auditCall(t, commandTree(), path, stdin, vars, files, argv...)
		_, raw := auditLines(t, path)
		for _, secret := range []string{marker, paramMark, pw, base64.StdEncoding.EncodeToString([]byte(script))} {
			if strings.Contains(raw, secret) {
				t.Fatalf("form %d: %q is in the log:\n%s", form%7, secret, raw)
			}
		}
	})
}
