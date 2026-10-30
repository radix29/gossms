package connect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// testTree is one command taking a profile and the connection options, so
// Resolve is fed what the real parser produces.
func testTree() *spec.Tree {
	return &spec.Tree{Commands: []*spec.Command{{
		Path: []string{"t"}, Summary: "t", Class: spec.ClassRead,
		Args:    []spec.Arg{{Name: "profile", Help: "p"}},
		Options: Options(),
	}}}
}

// savedConfig holds one SQL profile "dev" with a saved password, Encrypt
// Mandatory, certificate validated, and one Entra MFA profile.
func savedConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	dev := config.Connection{Server: `srv\inst`, Port: 1500, Database: "app", User: "agent",
		Password: "saved-pw", RememberPassword: true, Encrypt: config.EncryptMandatory}
	mfa := config.Connection{Server: "az", AuthMethod: config.AuthEntraInteractive, Encrypt: config.EncryptStrict}
	nopw := config.Connection{Server: "nopw", User: "sa", Encrypt: config.EncryptMandatory}
	for name, c := range map[string]config.Connection{"dev": dev, "mfa": mfa, "nopw": nopw} {
		cfg.AddOrUpdate(c)
		if err := cfg.SetProfile(c.GeneratedName(), name, config.AllowWrite); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

type resolveCase struct {
	argv   []string
	env    map[string]string
	stdin  string
	code   output.Code // "" for success
	check  func(t *testing.T, tg *Target)
	errHas string
}

func resolve(t *testing.T, tc resolveCase) (*Target, error) {
	t.Helper()
	getenv := func(k string) string { return tc.env[k] }
	inv, err := args.Parse(testTree(), append([]string{"t"}, tc.argv...), args.Options{Getenv: getenv})
	if err != nil {
		t.Fatalf("parse %v: %v", tc.argv, err)
	}
	inv.Getenv = getenv
	inv.Stdin = strings.NewReader(tc.stdin)
	profile := ""
	if len(inv.Args) > 0 {
		profile = inv.Args[0]
	}
	return Resolve(inv, savedConfig(t), profile)
}

func TestResolve(t *testing.T) {
	keeps := func(t *testing.T, tg *Target) {
		t.Helper()
		if tg.Conn.Password != "saved-pw" || tg.Password != "profile" || tg.SavedPasswordDropped {
			t.Errorf("password %q from %s, dropped %v: want the saved one", tg.Conn.Password, tg.Password, tg.SavedPasswordDropped)
		}
	}
	for name, tc := range map[string]resolveCase{
		"profile as saved": {argv: []string{"DEV"}, check: func(t *testing.T, tg *Target) {
			keeps(t, tg)
			c := tg.Conn
			if c.Server != `srv\inst` || c.Port != 1500 || c.Database != "app" || tg.Profile != "dev" ||
				tg.Ceiling != config.AllowWrite || tg.Timeout != DefaultConnectTimeout*time.Second {
				t.Errorf("target = %+v", tg)
			}
		}},
		"generated name":  {argv: []string{`srv\inst,1500,app,agent`}, check: keeps},
		"unknown profile": {argv: []string{"deb"}, code: output.CodeNotFound, errHas: "Did you mean dev?"},

		// What keeps the saved password: same destination, TLS no weaker.
		"database override": {argv: []string{"dev", "-d", "other"}, check: func(t *testing.T, tg *Target) {
			keeps(t, tg)
			if tg.Conn.Database != "other" {
				t.Errorf("database %q", tg.Conn.Database)
			}
		}},
		"application intent": {argv: []string{"dev", "--application-intent", "readonly"}, check: func(t *testing.T, tg *Target) {
			keeps(t, tg)
			if tg.Conn.ExtraProperties != "ApplicationIntent=ReadOnly" {
				t.Errorf("extra properties %q", tg.Conn.ExtraProperties)
			}
		}},
		"stronger encrypt":  {argv: []string{"dev", "--encrypt", "strict"}, check: keeps},
		"same server again": {argv: []string{"dev", "-S", `SRV\inst,1500`}, check: keeps},
		"no-trust":          {argv: []string{"dev", "--no-trust-server-certificate"}, check: keeps},

		// What drops it.
		"other server":    {argv: []string{"dev", "-S", "evil"}, code: output.CodeAuthFailed, errHas: "not sent to an overridden"},
		"other host only": {argv: []string{"dev", "-S", `evil\inst,1500`}, code: output.CodeAuthFailed},
		"other port":      {argv: []string{"dev", "--port", "1501"}, code: output.CodeAuthFailed},
		"other user":      {argv: []string{"dev", "-U", "sa"}, code: output.CodeAuthFailed},
		"weaker encrypt":  {argv: []string{"dev", "--encrypt", "optional"}, code: output.CodeAuthFailed},
		"trust cert":      {argv: []string{"dev", "--trust-server-certificate"}, code: output.CodeAuthFailed},
		"cert name":       {argv: []string{"dev", "--host-name-in-certificate", "x"}, code: output.CodeAuthFailed},
		"env server":      {argv: []string{"dev"}, env: map[string]string{"GOSSMS_SERVER": "evil"}, code: output.CodeAuthFailed},
		"other server with env password": {argv: []string{"dev", "-S", "evil", "--password-env", "PW"},
			env: map[string]string{"PW": "from-env"}, check: func(t *testing.T, tg *Target) {
				if tg.Conn.Server != "evil" || tg.Conn.Password != "from-env" || tg.Password != "env:PW" || !tg.SavedPasswordDropped {
					t.Errorf("target = %+v", tg)
				}
			}},

		// Password sources.
		"stdin first line": {argv: []string{"dev", "--password-stdin"}, stdin: "from-stdin\r\nrest\n", check: func(t *testing.T, tg *Target) {
			if tg.Conn.Password != "from-stdin" || tg.Password != "stdin" {
				t.Errorf("password %q from %s", tg.Conn.Password, tg.Password)
			}
		}},
		"empty stdin":    {argv: []string{"dev", "--password-stdin"}, code: output.CodeAuthFailed},
		"unset env":      {argv: []string{"dev", "--password-env", "NOPE"}, code: output.CodeAuthFailed},
		"both sources":   {argv: []string{"dev", "--password-env", "PW", "--password-stdin"}, code: output.CodeUsageError},
		"no saved pw":    {argv: []string{"nopw"}, code: output.CodeAuthFailed, errHas: "--password-env"},
		"windows no pw":  {argv: []string{"nopw", "--auth", "windows"}},
		"msi takes none": {argv: []string{"nopw", "--auth", "entra-msi", "--password-env", "PW"}, code: output.CodeUsageError},

		// Refusals.
		"saved mfa":                   {argv: []string{"mfa"}, code: output.CodeAuthFailed, errHas: "prompts"},
		"device code":                 {argv: []string{"nopw", "--auth", "entra-device-code"}, code: output.CodeAuthFailed},
		"port twice":                  {argv: []string{"dev", "-S", "h,1600", "--port", "1700"}, code: output.CodeUsageError},
		"bad port":                    {argv: []string{"dev", "--port", "70000"}, code: output.CodeUsageError},
		"timeout range":               {argv: []string{"dev", "--connect-timeout", "31"}, code: output.CodeUsageError},
		"ad hoc no server":            {code: output.CodeUsageError},
		"ad hoc sql needs a password": {argv: []string{"-S", "h"}, code: output.CodeAuthFailed},
		"ad hoc": {argv: []string{"-S", "h,1600", "-U", "sa", "--password-env", "PW", "--connect-timeout", "5"},
			env: map[string]string{"PW": "x"}, check: func(t *testing.T, tg *Target) {
				c := tg.Conn
				if c.Server != "h" || c.Port != 1600 || c.Encrypt != config.EncryptMandatory || tg.Ceiling != config.AllowAdmin ||
					tg.Profile != "" || tg.Timeout != 5*time.Second {
					t.Errorf("target = %+v", tg)
				}
			}},
	} {
		t.Run(name, func(t *testing.T) {
			tg, err := resolve(t, tc)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("error %v", err)
				}
				if tc.check != nil {
					tc.check(t, tg)
				}
				return
			}
			e, ok := errors.AsType[*output.Error](err)
			if !ok || e.Code != tc.code {
				t.Fatalf("error %v, want code %s", err, tc.code)
			}
			if tc.errHas != "" && !strings.Contains(e.Message+" "+e.Hint, tc.errHas) {
				t.Errorf("error %q / hint %q lacks %q", e.Message, e.Hint, tc.errHas)
			}
		})
	}
}

// A field the method does not read is not sent, as in the Connect dialog.
func TestResolveDropsFieldsTheMethodIgnores(t *testing.T) {
	tg, err := resolve(t, resolveCase{argv: []string{"nopw", "--auth", "entra-msi", "--tenant-id", "t", "--client-id", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if tg.Conn.User != "" || tg.Conn.TenantID != "" || tg.Conn.ClientID != "c" {
		t.Errorf("msi connection = %+v", tg.Conn)
	}
}

func TestWithApplicationIntentReplaces(t *testing.T) {
	got, err := withApplicationIntent("packet size=4096; Application Intent=ReadOnly", "readwrite")
	if err != nil {
		t.Fatal(err)
	}
	if got != "packet size=4096;ApplicationIntent=ReadWrite" {
		t.Errorf("got %q", got)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	tg := &Target{Timeout: 15 * time.Second, Password: "profile"}
	for _, tc := range []struct {
		err      error
		timedOut bool
		want     output.Code
	}{
		{fmt.Errorf("login error: %w", mssql.Error{Number: 18456, Class: 14, Message: "Login failed for user 'x'."}), false, output.CodeAuthFailed},
		{mssql.Error{Number: 4060, Class: 11, Message: "Cannot open database"}, false, output.CodeNotFound},
		{fmt.Errorf("ping: %w", mssql.Error{Number: 4063, Class: 11, Message: "Cannot open database"}), false, output.CodeNotFound},
		{&db.ConnectionError{Server: "s", Cause: "dial", Err: timeoutErr{}}, false, output.CodeTimeout},
		{errors.New("context ended"), true, output.CodeTimeout},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), false, output.CodeTimeout},
		{errors.New("connection refused"), false, output.CodeConnectionFailed},
		{mssql.Error{Number: 208, Class: 16}, false, output.CodeConnectionFailed},
	} {
		if got := Classify(tc.err, tc.timedOut, tg); got.Code != tc.want {
			t.Errorf("Classify(%v, %v) = %s, want %s", tc.err, tc.timedOut, got.Code, tc.want)
		}
	}
}

// Open bounds the dial by --connect-timeout and dials as RoleCLI.
func TestOpenTimesOutAndUsesTheCLIRole(t *testing.T) {
	var role db.Role
	var deadline time.Time
	_, err := Open(context.Background(), &Target{Timeout: 50 * time.Millisecond},
		func(ctx context.Context, _ config.Connection, r db.Role) (*db.ServerConn, error) {
			role = r
			deadline, _ = ctx.Deadline()
			<-ctx.Done()
			return nil, &db.ConnectionError{Server: "s", Cause: "cancelled", Err: ctx.Err()}
		})
	if role != db.RoleCLI {
		t.Errorf("role %v, want RoleCLI", role)
	}
	if deadline.IsZero() || time.Until(deadline) > time.Second {
		t.Errorf("dial deadline %v", deadline)
	}
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodeTimeout {
		t.Errorf("error %v, want TIMEOUT", err)
	}
}

// TestDescribe: the target and login an audit line names, read without a
// password — --password-stdin's stdin is never touched.
func TestDescribe(t *testing.T) {
	cfg := savedConfig(t)
	for _, c := range []struct {
		profile             string
		argv                []string
		server, dbase, user string
	}{
		{"dev", nil, `srv\inst,1500`, "app", "agent"},
		{"dev", []string{"-d", "other", "-U", "someone", "--password-stdin"}, `srv\inst,1500`, "other", "someone"},
		{"", []string{"-S", "host", "--auth", "windows"}, "host", "", "(integrated)"},
		{"", []string{"-S", "host", "--auth", "entra-msi"}, "host", "", "(entra)"},
		{"", []string{"-S", "host", "--auth", "entra-password", "-U", "a@b"}, "host", "", "a@b"},
		{"", []string{"-S", "host"}, "host", "", ""},
		{"", nil, "", "", ""}, // no server: Resolve refuses it
	} {
		inv, err := args.Parse(testTree(), append([]string{"t"}, c.argv...), args.Options{})
		if err != nil {
			t.Fatal(err)
		}
		inv.Stdin = iotest.ErrReader(errors.New("stdin read"))
		server, dbase, user := Describe(inv, cfg, c.profile)
		if server != c.server || dbase != c.dbase || user != c.user {
			t.Errorf("%s %q: Describe = %q, %q, %q; want %q, %q, %q", c.profile, c.argv, server, dbase, user, c.server, c.dbase, c.user)
		}
	}
}
