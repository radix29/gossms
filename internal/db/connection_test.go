package db

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// mustPreview is BuildConnectionString, parsed, failing the test on an error.
func mustPreview(t *testing.T, opts config.Connection) *url.URL {
	t.Helper()
	got, err := BuildConnectionString(opts)
	if err != nil {
		t.Fatalf("BuildConnectionString: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	return u
}

// ConnectionError must stay transparent to errors.Is/As: Connect wraps the
// driver's failure, and callers need the original to tell a login rejection
// from an unreachable host, or to ask gosmo.IsRetryable about it. Flattening
// the cause to a string severs that.
func TestConnectionErrorUnwrapsToCause(t *testing.T) {
	sentinel := errors.New("login failed for user 'sa'")
	err := error(&ConnectionError{Server: "myserver", Cause: sentinel.Error(), Err: sentinel})

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is(err, sentinel) = false, want true — the cause is not reachable through Unwrap")
	}
	if _, ok := errors.AsType[*ConnectionError](err); !ok {
		t.Error("errors.AsType[*ConnectionError] = false, want true")
	}
	if got, want := err.Error(), "connect to myserver: login failed for user 'sa'"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	// Still usable when wrapped further up the stack.
	if !errors.Is(fmt.Errorf("connecting: %w", err), sentinel) {
		t.Error("sentinel unreachable once ConnectionError is itself wrapped")
	}
}

// The preview is the DSN dialled, so it shows what gosmo fills in for an
// empty field rather than leaving it out: the old hand-built preview omitted
// "database" and "app name" and showed ":1433" on a host dialled without one.
func TestBuildConnectionStringShowsTheDialledDefaults(t *testing.T) {
	u := mustPreview(t, config.Connection{Server: "myserver"})
	if got := u.Query().Get("database"); got != "master" {
		t.Errorf("database = %q, want master (what an empty Database dials)", got)
	}
	if got := u.Query().Get("app name"); got != "goSSMS" {
		t.Errorf("app name = %q, want goSSMS", got)
	}
	if got := u.Query().Get("connection timeout"); got != "30" {
		t.Errorf("connection timeout = %q, want 30", got)
	}
	if u.Host != "myserver" {
		t.Errorf("host = %q, want myserver — no port is dialled, so none is shown", u.Host)
	}
	if got := u.Query().Get("encrypt"); got != "optional" {
		t.Errorf("encrypt = %q, want optional for an entry with no mode", got)
	}
}

// Every secret is masked — the preview is on screen and selectable — and
// a masked one is still visibly present, so an empty password is told apart.
func TestBuildConnectionStringMasksThePassword(t *testing.T) {
	cases := []config.Connection{
		{Server: "s", AuthMethod: config.AuthSQLServer, User: "sa", Password: "hunter2"},
		{Server: "s", AuthMethod: config.AuthEntraPassword, User: "u@x", Password: "hunter2"},
		{Server: "s", AuthMethod: config.AuthEntraServicePrincipal, ClientID: "app", Password: "hunter2"},
	}
	for _, c := range cases {
		got, err := BuildConnectionString(c)
		if err != nil {
			t.Fatalf("method %d: %v", c.AuthMethod, err)
		}
		if strings.Contains(got, "hunter2") {
			t.Errorf("method %d: preview %q shows the password", c.AuthMethod, got)
		}
		if !strings.Contains(got, "XXXXX") {
			t.Errorf("method %d: preview %q hides that a password is set", c.AuthMethod, got)
		}
	}
}

func TestBuildConnectionStringEscapesReservedCharacters(t *testing.T) {
	u := mustPreview(t, config.Connection{
		Server: "myserver", Database: "my&db", AuthMethod: config.AuthSQLServer, User: "s@a",
	})
	if u.Host != "myserver" || u.User.Username() != "s@a" || u.Query().Get("database") != "my&db" {
		t.Errorf("host/user/database = %q/%q/%q, want myserver/s@a/my&db", u.Host, u.User.Username(), u.Query().Get("database"))
	}
}

// TestBuildConnectionStringNamedInstance pins that a named instance with no
// port is shown without one. A named instance takes its port from SQL Browser
// (win10cli\SQL2017 answers on 55253), so a ":1433" here would name the
// *default* instance — and copying the preview out would reach it.
func TestBuildConnectionStringNamedInstance(t *testing.T) {
	for _, port := range []int{0, 1433} {
		u := mustPreview(t, config.Connection{Server: `myserver\SQLEXPRESS`, Port: port})
		if u.Host != "myserver" || u.Path != "/SQLEXPRESS" {
			t.Errorf("port %d: host/path = %q/%q, want myserver//SQLEXPRESS", port, u.Host, u.Path)
		}
	}
	u := mustPreview(t, config.Connection{Server: `myserver\SQLEXPRESS`, Port: 1434})
	if u.Host != "myserver:1434" || u.Path != "/SQLEXPRESS" {
		t.Errorf("dialog port: host/path = %q/%q, want myserver:1434//SQLEXPRESS", u.Host, u.Path)
	}
	u = mustPreview(t, config.Connection{Server: "myserver,1434", Port: 1500})
	if u.Host != "myserver:1434" {
		t.Errorf("embedded port: host = %q, want myserver:1434", u.Host)
	}
}

// TestResolveServerLeavesNamedInstanceAlone pins the address handed to gosmo,
// which is what Connect dials — the preview above only renders it. A port
// appended here (":1433" or ",1433") suppresses the SQL Browser lookup and
// lands on the default instance instead.
func TestResolveServerLeavesNamedInstanceAlone(t *testing.T) {
	for _, port := range []int{0, 1433} {
		if got := resolveServer(`myserver\SQLEXPRESS`, port); got != `myserver\SQLEXPRESS` {
			t.Errorf("resolveServer(port=%d) = %q, want myserver\\SQLEXPRESS", port, got)
		}
	}
	if got := resolveServer(`myserver\SQLEXPRESS`, 55253); got != `myserver\SQLEXPRESS,55253` {
		t.Errorf("resolveServer(port=55253) = %q, want myserver\\SQLEXPRESS,55253", got)
	}
	for _, port := range []int{0, 1433} {
		if got := resolveServer("myserver", port); got != "myserver" {
			t.Errorf("resolveServer(host, port=%d) = %q, want myserver", port, got)
		}
	}
}

func TestBuildConnectionStringTLSSettings(t *testing.T) {
	for mode, want := range map[config.EncryptMode]string{
		config.EncryptOptional: "optional", config.EncryptMandatory: "mandatory", config.EncryptStrict: "strict",
	} {
		u := mustPreview(t, config.Connection{Server: "s", Encrypt: mode, HostNameInCertificate: " sql.example.com "})
		if got := u.Query().Get("encrypt"); got != want {
			t.Errorf("mode %q: encrypt = %q, want %q", mode, got, want)
		}
		if got := u.Query().Get("hostNameInCertificate"); got != "sql.example.com" {
			t.Errorf("mode %q: hostNameInCertificate = %q, want sql.example.com", mode, got)
		}
		if u.Query().Has("TrustServerCertificate") {
			t.Errorf("mode %q: TrustServerCertificate present with the box unticked", mode)
		}
	}
	u := mustPreview(t, config.Connection{Server: "s", TrustServerCertificate: true})
	if got := u.Query().Get("TrustServerCertificate"); got != "true" {
		t.Errorf("TrustServerCertificate = %q, want true", got)
	}
}

// BUG-3: each auth method reads its client id from a different gosmo option,
// and the credentials a method does not use are not passed at all.
func TestToGosmoOptionsMapsEachAuthMethod(t *testing.T) {
	full := config.Connection{
		Server: "s", User: "user", Password: "pw", TenantID: "tenant", ClientID: "client",
	}
	type want struct{ user, password, tenant, clientID, appClientID string }
	cases := map[config.AuthMethod]want{
		config.AuthSQLServer:             {user: "user", password: "pw"},
		config.AuthWindows:               {user: "user", password: "pw"},
		config.AuthEntraPassword:         {user: "user", password: "pw", tenant: "tenant"},
		config.AuthEntraMSI:              {tenant: "tenant", clientID: "client"},
		config.AuthEntraServicePrincipal: {user: "client", password: "pw", tenant: "tenant"},
		config.AuthEntraInteractive:      {tenant: "tenant", appClientID: "client"},
		config.AuthEntraDeviceCode:       {tenant: "tenant", appClientID: "client"},
		config.AuthEntraDefault:          {tenant: "tenant"},
		config.AuthEntraAzCLI:            {tenant: "tenant"},
	}
	if len(cases) != len(config.AllAuthMethods()) {
		t.Fatalf("%d cases for %d auth methods — a method is untested", len(cases), len(config.AllAuthMethods()))
	}
	for m, w := range cases {
		opts := full
		opts.AuthMethod = m
		co, err := toGosmoOptions(opts, RoleExplorer)
		if err != nil {
			t.Fatalf("method %d: %v", m, err)
		}
		got := want{co.User, co.Password, co.TenantID, co.ClientID, co.ApplicationClientID}
		if got != w {
			t.Errorf("%s: user/password/tenant/clientID/appClientID = %+v, want %+v", config.AuthMethodName(m), got, w)
		}
	}

	// A service principal saved with its application id in User still reaches
	// gosmo's User — and so the DSN's "user id".
	legacy := config.Connection{Server: "s", AuthMethod: config.AuthEntraServicePrincipal, User: "app-id", Password: "pw"}
	u := mustPreview(t, legacy)
	if got := u.Query().Get("user id"); got != "app-id" {
		t.Errorf("legacy service principal: user id = %q, want app-id", got)
	}
	legacy.ClientID, legacy.TenantID = "new-app-id", "t1"
	u = mustPreview(t, legacy)
	if got := u.Query().Get("user id"); got != "new-app-id@t1" {
		t.Errorf("service principal: user id = %q, want new-app-id@t1 (ClientID wins over User)", got)
	}
}

func TestRoleSetsTheApplicationName(t *testing.T) {
	for role, want := range map[Role]string{
		RoleExplorer: "goSSMS", RoleQuery: "goSSMS - Query", RoleActivityMonitor: "goSSMS - Activity Monitor",
	} {
		co, err := toGosmoOptions(config.Connection{Server: "s"}, role)
		if err != nil {
			t.Fatal(err)
		}
		if co.ApplicationName != want {
			t.Errorf("role %d: ApplicationName = %q, want %q", role, co.ApplicationName, want)
		}
	}
}

func TestParseExtraProperties(t *testing.T) {
	cases := []struct {
		in   string
		want url.Values
	}{
		{"", nil},
		{"  \n ", nil},
		{"packet size=4096", url.Values{"packet size": {"4096"}}},
		{"ApplicationIntent=ReadOnly;MultiSubnetFailover=true;", url.Values{
			"ApplicationIntent": {"ReadOnly"}, "MultiSubnetFailover": {"true"}}},
		{"a=1&b=2", url.Values{"a": {"1"}, "b": {"2"}}},
		{" a = 1 \n b=2\r\n", url.Values{"a": {"1"}, "b": {"2"}}},
		{"keepalive=", url.Values{"keepalive": {""}}},
		{"a=x=y", url.Values{"a": {"x=y"}}},
	}
	for _, c := range cases {
		got, err := ParseExtraProperties(c.in)
		if err != nil {
			t.Errorf("ParseExtraProperties(%q): %v", c.in, err)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("ParseExtraProperties(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"packetsize", "a=1;oops", "=1"} {
		if _, err := ParseExtraProperties(bad); err == nil {
			t.Errorf("ParseExtraProperties(%q): want an error", bad)
		}
	}
}

// BUG-2: extra properties reach the driver, and one naming a setting the
// dialog owns is an error rather than a silent override either way.
func TestExtraPropertiesReachTheDialledDSN(t *testing.T) {
	u := mustPreview(t, config.Connection{Server: "s", Database: "db", ExtraProperties: "ApplicationIntent=ReadOnly; packet size=8192"})
	if got := u.Query().Get("applicationintent"); got != "ReadOnly" {
		t.Errorf("applicationintent = %q, want ReadOnly (%s)", got, u)
	}
	if got := u.Query().Get("packet size"); got != "8192" {
		t.Errorf("packet size = %q, want 8192 (%s)", got, u)
	}

	for _, extra := range []string{"database=other", "Encrypt=disable", "TrustServerCertificate=true", "packetsize"} {
		opts := config.Connection{Server: "s", ExtraProperties: extra}
		_, err := BuildConnectionString(opts)
		if err == nil {
			t.Errorf("preview with %q: want an error", extra)
		} else if strings.Contains(err.Error(), "ConnectionOptions") {
			t.Errorf("preview with %q: %q speaks gosmo's API, not the dialog's", extra, err)
		}
		_, err = Connect(opts)
		if _, ok := errors.AsType[*ConnectionError](err); !ok {
			t.Errorf("Connect with %q: err = %v, want a *ConnectionError before anything is dialled", extra, err)
		}
	}
}

// The preview and Connect share toGosmoOptions; this pins that nothing but
// the secrets differs between the preview and what gosmo renders unmasked.
func TestBuildConnectionStringIsTheDialledDSNMasked(t *testing.T) {
	opts := config.Connection{Server: `h\i`, Port: 1500, AuthMethod: config.AuthSQLServer, User: "sa", Password: "pw",
		Encrypt: config.EncryptMandatory, ExtraProperties: "keepalive=10"}
	preview, err := BuildConnectionString(opts)
	if err != nil {
		t.Fatal(err)
	}
	co, err := toGosmoOptions(opts, RoleExplorer)
	if err != nil {
		t.Fatal(err)
	}
	dialled, err := co.ConnectionString(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Replace(dialled, "sa:pw@", "sa:XXXXX@", 1) != preview {
		t.Errorf("preview %q is not the dialled %q with the password masked", preview, dialled)
	}
}

func TestServerConnLabel(t *testing.T) {
	cases := []struct {
		name  string
		opts  config.Connection
		login string
		want  string
	}{
		{
			"default instance, default port",
			config.Connection{Server: "myserver", User: "sa"},
			"",
			"myserver (sa, SQL Server )",
		},
		{
			"resolved login takes precedence over Opts.User",
			config.Connection{Server: "myserver", User: "app-client-id"},
			"CONTOSO\\alice",
			"myserver (CONTOSO\\alice, SQL Server )",
		},
		{
			"named instance",
			config.Connection{Server: `myserver\SQLEXPRESS`, User: "sa"},
			"",
			`myserver\SQLEXPRESS (sa, SQL Server )`,
		},
		{
			"custom port, no instance",
			config.Connection{Server: "myserver", Port: 1434, User: "sa"},
			"",
			"myserver,1434 (sa, SQL Server )",
		},
		{
			"comma port embedded directly in Server",
			config.Connection{Server: "myserver,1434", User: "sa"},
			"",
			"myserver,1434 (sa, SQL Server )",
		},
		{
			"default port not shown",
			config.Connection{Server: "myserver", Port: 1433, User: "sa"},
			"",
			"myserver (sa, SQL Server )",
		},
		{
			"no user falls back to auth method name",
			config.Connection{Server: "myserver", AuthMethod: config.AuthWindows},
			"",
			"myserver (Windows Authentication, SQL Server )",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := &ServerConn{Opts: c.opts, Login: c.login}
			if got := sc.Label(); got != c.want {
				t.Errorf("Label() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveServer(t *testing.T) {
	cases := []struct {
		name       string
		server     string
		dialogPort int
		want       string
	}{
		{"bare host, default port", "myserver", 1433, "myserver"},
		{"bare host, custom port", "myserver", 1434, "myserver:1434"},
		{"embedded comma port wins over dialog port", "myserver,1434", 1500, "myserver,1434"},
		{"instance, default port: unchanged", `myserver\SQLEXPRESS`, 1433, `myserver\SQLEXPRESS`},
		{"instance, custom port: comma appended", `myserver\SQLEXPRESS`, 1434, `myserver\SQLEXPRESS,1434`},
		// A colon after a bare IPv6 literal is one more group of it.
		{"bare IPv6, custom port: comma appended", "fe80::1", 1434, "fe80::1,1434"},
		{"bracketed IPv6, custom port: colon appended", "[fe80::1]", 1434, "[fe80::1]:1434"},
		{"bare IPv6, default port: unchanged", "2001:db8::5", 1433, "2001:db8::5"},
		{"bracketed IPv6 with port wins over dialog port", "[fe80::1]:1500", 1434, "[fe80::1]:1500"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveServer(c.server, c.dialogPort); got != c.want {
				t.Errorf("resolveServer(%q, %d) = %q, want %q", c.server, c.dialogPort, got, c.want)
			}
		})
	}
}
