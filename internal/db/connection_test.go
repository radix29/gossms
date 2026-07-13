package db

import (
	"net/url"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

func TestBuildConnectionStringSQLServerAuth(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:     "myserver",
		Port:       1433,
		Database:   "mydb",
		AuthMethod: config.AuthSQLServer,
		User:       "sa",
		Password:   "s3cr3t123",
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.User.Username() != "sa" {
		t.Errorf("user = %q, want sa", u.User.Username())
	}
	if pw, _ := u.User.Password(); pw != "s3cr3t123" {
		t.Errorf("password = %q, want s3cr3t123", pw)
	}
	if got := u.Query().Get("database"); got != "mydb" {
		t.Errorf("database = %q, want mydb", got)
	}
}

// TestBuildConnectionStringEscapesReservedCharacters guards against a
// regression to the earlier implementation, which interpolated User/
// Password/Database directly into an "sqlserver://user:pass@host" string:
// a value containing a URL-reserved character (here "@" and "&") shifted
// where net/url split userinfo from host, corrupting the result.
func TestBuildConnectionStringEscapesReservedCharacters(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:     "myserver",
		Database:   "my&db",
		AuthMethod: config.AuthSQLServer,
		User:       "sa",
		Password:   "p@ss:word/1",
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.Host != "myserver:1433" {
		t.Errorf("host = %q, want myserver:1433 (unaffected by the reserved chars elsewhere)", u.Host)
	}
	if u.User.Username() != "sa" {
		t.Errorf("user = %q, want sa", u.User.Username())
	}
	if pw, _ := u.User.Password(); pw != "p@ss:word/1" {
		t.Errorf("password = %q, want p@ss:word/1", pw)
	}
	if got := u.Query().Get("database"); got != "my&db" {
		t.Errorf("database = %q, want my&db", got)
	}
}

func TestBuildConnectionStringDefaultsPortTo1433(t *testing.T) {
	got := BuildConnectionString(config.Connection{Server: "myserver"})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.Host != "myserver:1433" {
		t.Errorf("host = %q, want myserver:1433", u.Host)
	}
}

func TestBuildConnectionStringNamedInstance(t *testing.T) {
	got := BuildConnectionString(config.Connection{Server: `myserver\SQLEXPRESS`})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.Host != "myserver:1433" {
		t.Errorf("host = %q, want myserver:1433 (no %%5C mangling)", u.Host)
	}
	if u.Path != "/SQLEXPRESS" {
		t.Errorf("path = %q, want /SQLEXPRESS", u.Path)
	}
}

func TestBuildConnectionStringNamedInstanceWithDialogPort(t *testing.T) {
	// A non-default Port field alongside a "\instance" Server must combine
	// as "host\instance,port" (comma), not get corrupted into part of the
	// instance name.
	got := BuildConnectionString(config.Connection{Server: `myserver\SQLEXPRESS`, Port: 1434})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.Host != "myserver:1434" {
		t.Errorf("host = %q, want myserver:1434", u.Host)
	}
	if u.Path != "/SQLEXPRESS" {
		t.Errorf("path = %q, want /SQLEXPRESS", u.Path)
	}
}

func TestBuildConnectionStringCommaPort(t *testing.T) {
	// A port embedded directly in the Server field (SSMS-native "host,port")
	// takes precedence over the dialog's default Port field value.
	got := BuildConnectionString(config.Connection{Server: "myserver,1434"})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.Host != "myserver:1434" {
		t.Errorf("host = %q, want myserver:1434", u.Host)
	}
}

func TestServerConnLabel(t *testing.T) {
	cases := []struct {
		name string
		opts config.Connection
		want string
	}{
		{
			"default instance, default port",
			config.Connection{Server: "myserver", User: "sa"},
			"myserver (sa, SQL Server )",
		},
		{
			"named instance",
			config.Connection{Server: `myserver\SQLEXPRESS`, User: "sa"},
			`myserver\SQLEXPRESS (sa, SQL Server )`,
		},
		{
			"custom port, no instance",
			config.Connection{Server: "myserver", Port: 1434, User: "sa"},
			"myserver,1434 (sa, SQL Server )",
		},
		{
			"comma port embedded directly in Server",
			config.Connection{Server: "myserver,1434", User: "sa"},
			"myserver,1434 (sa, SQL Server )",
		},
		{
			"default port not shown",
			config.Connection{Server: "myserver", Port: 1433, User: "sa"},
			"myserver (sa, SQL Server )",
		},
		{
			"no user falls back to auth method name",
			config.Connection{Server: "myserver", AuthMethod: config.AuthWindows},
			"myserver (Windows Authentication, SQL Server )",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := &ServerConn{Opts: c.opts}
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveServer(c.server, c.dialogPort); got != c.want {
				t.Errorf("resolveServer(%q, %d) = %q, want %q", c.server, c.dialogPort, got, c.want)
			}
		})
	}
}

func TestBuildConnectionStringSQLServerAuthNoUser(t *testing.T) {
	got := BuildConnectionString(config.Connection{Server: "myserver", AuthMethod: config.AuthSQLServer})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.User != nil {
		t.Errorf("User = %v, want nil when no User is set", u.User)
	}
}

func TestBuildConnectionStringWindowsAuth(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:     "myserver",
		AuthMethod: config.AuthWindows,
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	// "integrated security" round-trips through url.Values as
	// "integrated+security" (space encoded); Query().Get decodes it back.
	if got := u.Query().Get("integrated security"); got != "true" {
		t.Errorf(`"integrated security" = %q, want true`, got)
	}
}

func TestBuildConnectionStringEntraWithCredentials(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:     "myserver",
		AuthMethod: config.AuthEntraPassword,
		User:       "user@tenant.onmicrosoft.com",
		Password:   "pw",
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.User.Username() != "user@tenant.onmicrosoft.com" {
		t.Errorf("user = %q, want user@tenant.onmicrosoft.com", u.User.Username())
	}
	if got := u.Query().Get("fedauth"); got != "ActiveDirectoryPassword" {
		t.Errorf("fedauth = %q, want ActiveDirectoryPassword", got)
	}
}

func TestBuildConnectionStringEntraWithoutCredentials(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:     "myserver",
		AuthMethod: config.AuthEntraDefault,
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if u.User != nil {
		t.Errorf("User = %v, want nil when no credentials are set", u.User)
	}
	if got := u.Query().Get("fedauth"); got != "ActiveDirectoryDefault" {
		t.Errorf("fedauth = %q, want ActiveDirectoryDefault", got)
	}
}

func TestBuildConnectionStringEncryptAndTrustFlags(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:                 "myserver",
		Encrypt:                true,
		TrustServerCertificate: false,
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if got := u.Query().Get("encrypt"); got != "true" {
		t.Errorf("encrypt = %q, want true", got)
	}
	if got := u.Query().Get("TrustServerCertificate"); got != "false" {
		t.Errorf("TrustServerCertificate = %q, want false", got)
	}
}

func TestBuildConnectionStringExtraPropertiesAppended(t *testing.T) {
	got := BuildConnectionString(config.Connection{
		Server:          "myserver",
		ExtraProperties: "packetsize=4096",
	})
	if !strings.HasSuffix(got, "&packetsize=4096") {
		t.Errorf("got %q, want it to end with &packetsize=4096", got)
	}
}

func TestBuildConnectionStringNoExtraPropertiesNoTrailingSeparator(t *testing.T) {
	got := BuildConnectionString(config.Connection{Server: "myserver"})
	if strings.HasSuffix(got, "&") {
		t.Errorf("got %q, want no trailing separator when ExtraProperties is empty", got)
	}
}
