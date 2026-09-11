package db

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/microsoft/go-mssqldb/azuread"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/radix29/gossms/internal/config"
)

// mustPreview is BuildConnectionString parsed, failing on error.
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

// ConnectionError must unwrap so callers can tell a login rejection from an
// unreachable host, or ask gosmo.IsRetryable.
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

	// Still matches when wrapped further.
	if !errors.Is(fmt.Errorf("connecting: %w", err), sentinel) {
		t.Error("sentinel unreachable once ConnectionError is itself wrapped")
	}
}

// The preview is the dialled DSN, so it shows gosmo's defaults for empty fields
// ("database", "app name") and no ":1433" on a host dialled without one.
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

// Every secret is masked but visibly present, so an empty password is
// distinguishable.
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

// A named instance without a port is shown without one: it gets its port from
// SQL Browser, and ":1433" would reach the default instance.
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

// The address handed to gosmo (what Connect dials) must not append a port to a
// named instance; that suppresses SQL Browser.
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

// BUG-3: each auth method reads its client id from a different gosmo option;
// unused credentials aren't passed.
func TestToGosmoOptionsMapsEachAuthMethod(t *testing.T) {
	full := config.Connection{
		Server: "s", User: "user", Password: "pw", TenantID: "tenant", ClientID: "client",
	}
	type want struct{ user, password, tenant, clientID, appClientID string }
	cases := map[config.AuthMethod]want{
		config.AuthSQLServer: {user: "user", password: "pw"},
		config.AuthWindows:   {user: "user", password: "pw"},
		// ClientID is the app registration the user signs in through.
		config.AuthEntraPassword:         {user: "user", password: "pw", tenant: "tenant", appClientID: "client"},
		config.AuthEntraMSI:              {clientID: "client"},
		config.AuthEntraServicePrincipal: {user: "client", password: "pw", tenant: "tenant"},
		// User is the login hint.
		config.AuthEntraInteractive: {user: "user", tenant: "tenant", appClientID: "client"},
		config.AuthEntraDeviceCode:  {tenant: "tenant", appClientID: "client"},
		config.AuthEntraDefault:     {tenant: "tenant"},
		config.AuthEntraAzCLI:       {tenant: "tenant"},
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

	// A legacy service principal with its application id in User reaches
	// gosmo's User ("user id").
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

// What the driver parses from each Entra method's DSN (docs/testing.md). Every
// method must pass azuread's validator with the fields where the credential
// reads them, including Password and MFA with empty ClientID (gosmo fills the
// public client).
func TestEveryEntraMethodBuildsADriverConnector(t *testing.T) {
	full := config.Connection{
		Server: "s", User: "user", Password: "pw", TenantID: "tenant", ClientID: "client",
	}
	type params struct{ fedauth, userID, appClientID, tenantID string }
	cases := map[config.AuthMethod]params{
		config.AuthEntraDefault:          {"ActiveDirectoryDefault", "", "", "tenant"},
		config.AuthEntraPassword:         {"ActiveDirectoryPassword", "user", "client", "tenant"},
		config.AuthEntraMSI:              {"ActiveDirectoryManagedIdentity", "client", "", ""},
		config.AuthEntraServicePrincipal: {"ActiveDirectoryServicePrincipal", "client@tenant", "", "tenant"},
		config.AuthEntraInteractive:      {"ActiveDirectoryInteractive", "user", "client", "tenant"},
		config.AuthEntraDeviceCode:       {"ActiveDirectoryDeviceCode", "", "client", "tenant"},
		config.AuthEntraAzCLI:            {"ActiveDirectoryAzCli", "", "", "tenant"},
	}
	for _, m := range config.AllAuthMethods() {
		if !config.IsEntraMethod(m) {
			continue
		}
		w, ok := cases[m]
		if !ok {
			t.Errorf("%s: no case — every Entra method must be checked against the driver", config.AuthMethodName(m))
			continue
		}
		opts := full
		opts.AuthMethod = m
		p := driverParams(t, opts)
		if got := (params{p["fedauth"], p["user id"], p["applicationclientid"], p["tenantid"]}); got != w {
			t.Errorf("%s: driver reads fedauth/user id/applicationclientid/tenantid = %+v, want %+v",
				config.AuthMethodName(m), got, w)
		}
	}
	for _, m := range []config.AuthMethod{config.AuthEntraPassword, config.AuthEntraInteractive} {
		opts := full
		opts.AuthMethod, opts.ClientID = m, ""
		if p := driverParams(t, opts); p["applicationclientid"] == "" {
			t.Errorf("%s with no ClientID: driver got no applicationclientid, which it requires", config.AuthMethodName(m))
		}
	}
}

// driverParams builds opts' unmasked DSN, has the azuread connector parse and
// validate it without dialling, and returns the parsed parameters.
func driverParams(t *testing.T, opts config.Connection) map[string]string {
	t.Helper()
	co, err := toGosmoOptions(opts, RoleExplorer)
	if err != nil {
		t.Fatalf("%s: toGosmoOptions: %v", config.AuthMethodName(opts.AuthMethod), err)
	}
	dsn, err := co.ConnectionString(false)
	if err != nil {
		t.Fatalf("%s: ConnectionString: %v", config.AuthMethodName(opts.AuthMethod), err)
	}
	if _, err := azuread.NewConnector(dsn); err != nil {
		t.Fatalf("%s: the driver refuses the DSN: %v", config.AuthMethodName(opts.AuthMethod), err)
	}
	cfg, err := msdsn.Parse(dsn)
	if err != nil {
		t.Fatalf("%s: msdsn.Parse: %v", config.AuthMethodName(opts.AuthMethod), err)
	}
	return cfg.Parameters
}

// A missing required credential is refused in the dialog's field names, not
// gosmo's ("User" for a service principal's ClientID).
func TestMissingCredentialNamesTheDialogsField(t *testing.T) {
	cases := []struct {
		opts config.Connection
		want string
	}{
		{config.Connection{AuthMethod: config.AuthEntraServicePrincipal, Password: "pw"}, "needs a ClientID"},
		{config.Connection{AuthMethod: config.AuthEntraServicePrincipal, ClientID: "app"}, "needs a Password (the client secret)"},
		{config.Connection{AuthMethod: config.AuthEntraPassword, Password: "pw"}, "needs a User"},
		{config.Connection{AuthMethod: config.AuthEntraPassword, User: "u@contoso.com"}, "needs a Password"},
	}
	for _, c := range cases {
		c.opts.Server = "s"
		_, err := BuildConnectionString(c.opts)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "gosmo") {
			t.Errorf("%s %+v: err = %v, want one saying %q in the dialog's terms",
				config.AuthMethodName(c.opts.AuthMethod), c.opts, err, c.want)
		}
	}
	// The legacy service principal (application id in User) still passes.
	legacy := config.Connection{Server: "s", AuthMethod: config.AuthEntraServicePrincipal, User: "app", Password: "pw"}
	if _, err := BuildConnectionString(legacy); err != nil {
		t.Errorf("legacy service principal: %v", err)
	}
}

// Every Entra connection shares one sign-in cache — per pool would mean a
// browser sign-in per window — and the process's device-code prompt instead of
// gosmo's stdout print.
func TestEntraConnectionsShareOneSignIn(t *testing.T) {
	for _, m := range config.AllAuthMethods() {
		for _, role := range []Role{RoleExplorer, RoleQuery, RoleActivityMonitor} {
			co, err := toGosmoOptions(config.Connection{Server: "s", AuthMethod: m, User: "u", Password: "p", ClientID: "c"}, role)
			if err != nil {
				t.Fatalf("%s: %v", config.AuthMethodName(m), err)
			}
			entra := config.IsEntraMethod(m)
			if (co.EntraCache == entraCache && entraCache != nil) != entra || (co.DeviceCodePrompt != nil) != entra {
				t.Errorf("%s, role %d: cache shared %v, prompt set %v; want both %v",
					config.AuthMethodName(m), role, co.EntraCache == entraCache, co.DeviceCodePrompt != nil, entra)
			}
		}
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

// BUG-2: extra properties reach the driver; one naming a dialog-owned setting
// is an error, not a silent override.
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

// Preview and Connect share toGosmoOptions; only secrets differ from gosmo's
// unmasked rendering.
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
		// A colon after a bare IPv6 literal is another group.
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
