package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// profileConfig is two named profiles and one unnamed recent connection, the
// first profile with a saved password.
func profileConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	dev := config.Connection{Server: "devsrv", Port: 1500, Database: "app", User: "agent",
		Password: "s3cret-pw", RememberPassword: true, Encrypt: config.EncryptMandatory,
		ExtraProperties: "packet size=4096;Workstation ID=hush"}
	ro := config.Connection{Server: "prod", User: "reader", Encrypt: config.EncryptStrict}
	recent := config.Connection{Server: "scratch", User: "sa"}
	for _, c := range []config.Connection{dev, ro, recent} {
		cfg.AddOrUpdate(c)
	}
	if err := cfg.SetProfile(dev.GeneratedName(), "dev", config.AllowWrite); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetProfile(ro.GeneratedName(), "prod-ro", config.AllowRead); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func profileDeps(t *testing.T, connect func(context.Context, config.Connection, db.Role) (*db.ServerConn, error)) cmd.Deps {
	cfg := profileConfig(t)
	return cmd.Deps{
		LoadConfig: func() (*config.Config, error) { return cfg, nil },
		Connect:    connect,
	}
}

func runProfile(t *testing.T, d cmd.Deps, argv ...string) runResult {
	t.Helper()
	return runTree(t, context.Background(), newCommandTree(d), argv...)
}

func TestProfileList(t *testing.T) {
	d := profileDeps(t, nil)
	r := runProfile(t, d, "profile", "list", "-q")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
	want := `=== RESULT 1 (2 rows, truncated: no) ===
name    | kind    | server | port | database | auth | user   | max_allow | password
--------+---------+--------+------+----------+------+--------+-----------+---------
dev     | profile | devsrv | 1500 | app      | sql  | agent  | write     | saved
prod-ro | profile | prod   | NULL |          | sql  | reader | read      | none
`
	if r.stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, want)
	}

	r = runProfile(t, d, "profile", "list", "--all", "-o", "json")
	env := envelope(t, r.stdout)
	rows := env["resultSets"].([]any)[0].(map[string]any)["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("--all: %d rows, want 3", len(rows))
	}
	last := rows[2].([]any)
	if last[0] != "scratch,1433,,sa" || last[1] != "recent" || last[7] != nil {
		t.Errorf("recent row = %v", last)
	}
	if strings.Contains(r.stdout, "s3cret") {
		t.Error("profile list printed a password")
	}
}

func TestProfileListEmptySaysHowToMakeOne(t *testing.T) {
	d := cmd.Deps{LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil }}
	r := runProfile(t, d, "profile", "list")
	if r.exit != 0 || !strings.Contains(r.stdout, "Connect once in the TUI") {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
}

func TestProfileShow(t *testing.T) {
	r := runProfile(t, profileDeps(t, nil), "profile", "show", "DEV", "-q")
	if r.exit != 0 {
		t.Fatalf("exit %d\n%s", r.exit, r.stdout)
	}
	for _, line := range []string{
		"NAME: dev", "KIND: profile", "GENERATED_NAME: devsrv,1500,app,agent", "PORT: 1500", "AUTH: sql",
		"ENCRYPT: mandatory", "MAX_ALLOW: write", "PASSWORD: saved",
		// Keys only: a value may be anything.
		"EXTRA_PROPERTIES: Workstation ID, packet size",
	} {
		if !strings.Contains(r.stdout, line+"\n") {
			t.Errorf("no %q in\n%s", line, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "s3cret") || strings.Contains(r.stdout, "hush") {
		t.Errorf("profile show printed a secret:\n%s", r.stdout)
	}
}

func TestProfileNotFound(t *testing.T) {
	d := profileDeps(t, nil)
	for _, argv := range [][]string{
		{"profile", "show", "deb"},
		{"profile", "test", "deb"},
	} {
		r := runProfile(t, d, append(argv, "-o", "json")...)
		if r.exit != 8 {
			t.Errorf("%v: exit %d, want 8", argv, r.exit)
			continue
		}
		e := firstError(t, envelope(t, r.stdout))
		if e["code"] != "NOT_FOUND" || !strings.Contains(e["hint"].(string), "Did you mean dev?") {
			t.Errorf("%v: error %v", argv, e)
		}
		checkMentions(t, newCommandTree(d), "hint", e["hint"].(string))
	}
}

func TestProfileTestMapsConnectFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		exit int
	}{
		{&db.ConnectionError{Server: "devsrv", Cause: "login failed",
			Err: mssql.Error{Number: 18456, Class: 14, Message: "Login failed for user 'agent'."}}, 4},
		{&db.ConnectionError{Server: "devsrv", Cause: "refused", Err: errors.New("connection refused")}, 3},
	} {
		var got config.Connection
		var role db.Role
		d := profileDeps(t, func(_ context.Context, c config.Connection, r db.Role) (*db.ServerConn, error) {
			got, role = c, r
			return nil, tc.err
		})
		r := runProfile(t, d, "profile", "test", "dev", "-d", "master")
		if r.exit != tc.exit {
			t.Errorf("%v: exit %d, want %d\n%s", tc.err, r.exit, tc.exit, r.stdout)
		}
		if got.Password != "s3cret-pw" || got.Database != "master" || role != db.RoleCLI {
			t.Errorf("dialled %+v as %v", got, role)
		}
		if !strings.Contains(r.stdout, "SERVER: devsrv,1500\nDATABASE: master\n") {
			t.Errorf("header does not name the target:\n%s", r.stdout)
		}
		if strings.Contains(r.stdout+r.stderr, "s3cret") {
			t.Error("a password reached the output")
		}
	}
}

// A refusal before dialling: Connect is never called.
func TestProfileTestRefusesBeforeDialling(t *testing.T) {
	d := profileDeps(t, func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
		t.Fatal("dialled")
		return nil, nil
	})
	for _, tc := range []struct {
		argv []string
		exit int
	}{
		{[]string{"profile", "test", "prod-ro"}, 4},                            // no saved password
		{[]string{"profile", "test", "dev", "-S", "elsewhere"}, 4},             // saved password not sent
		{[]string{"profile", "test", "dev", "--auth", "entra-interactive"}, 4}, // prompts
		{[]string{"profile", "test", "dev", "--connect-timeout", "0"}, 2},
	} {
		if r := runProfile(t, d, tc.argv...); r.exit != tc.exit {
			t.Errorf("%v: exit %d, want %d\n%s", tc.argv, r.exit, tc.exit, r.stdout)
		}
	}
}

func TestProfileConfigUnreadable(t *testing.T) {
	d := cmd.Deps{LoadConfig: func() (*config.Config, error) {
		return &config.Config{}, errors.New("config: config.json did not parse")
	}}
	if r := runProfile(t, d, "profile", "list"); r.exit != 1 || !strings.Contains(r.stdout, "did not parse") {
		t.Errorf("exit %d\n%s", r.exit, r.stdout)
	}
}

// Connection options belong to the commands that connect: elsewhere they
// are unknown, never silently ignored.
func TestConnectionOptionsOnlyWhereTheyWork(t *testing.T) {
	for _, argv := range [][]string{
		{"version", "--server", "x"},
		{"profile", "list", "-S", "x"},
		{"profile", "show", "dev", "--password-stdin"},
	} {
		if r := runProfile(t, profileDeps(t, nil), argv...); r.exit != 2 {
			t.Errorf("%v: exit %d, want 2", argv, r.exit)
		}
	}
}
