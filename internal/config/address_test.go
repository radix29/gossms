package config

import "testing"

// The address handed to gosmo (what Connect dials) must not append a port to a
// named instance; that suppresses SQL Browser.
func TestResolveServerLeavesNamedInstanceAlone(t *testing.T) {
	for _, port := range []int{0, 1433} {
		if got := ResolveServer(`myserver\SQLEXPRESS`, port); got != `myserver\SQLEXPRESS` {
			t.Errorf("ResolveServer(port=%d) = %q, want myserver\\SQLEXPRESS", port, got)
		}
	}
	if got := ResolveServer(`myserver\SQLEXPRESS`, 55253); got != `myserver\SQLEXPRESS,55253` {
		t.Errorf("ResolveServer(port=55253) = %q, want myserver\\SQLEXPRESS,55253", got)
	}
	for _, port := range []int{0, 1433} {
		if got := ResolveServer("myserver", port); got != "myserver" {
			t.Errorf("ResolveServer(host, port=%d) = %q, want myserver", port, got)
		}
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
			if got := ResolveServer(c.server, c.dialogPort); got != c.want {
				t.Errorf("ResolveServer(%q, %d) = %q, want %q", c.server, c.dialogPort, got, c.want)
			}
		})
	}
}

// Peer cache and credential lookup share this normalizer. The catalog reports
// "HOST\INSTANCE", the user types "host,1433"; keeping case or the default port
// would miss.
func TestInstanceKeyNormalizesSpellings(t *testing.T) {
	groups := [][]string{
		{"UBUSQL2", "ubusql2", "ubusql2,1433", "  ubusql2  ", "UbuSQL2:1433"},
		{"HOST\\INST", "host\\inst", "HOST\\inst,1433", "host\\inst,1500"},
		{"win10cli,55253", "WIN10CLI:55253", " win10cli,55253 "},
	}
	for _, g := range groups {
		want := InstanceKey(g[0])
		if want == "" {
			t.Fatalf("InstanceKey(%q) is empty", g[0])
		}
		for _, spelling := range g[1:] {
			if got := InstanceKey(spelling); got != want {
				t.Errorf("InstanceKey(%q) = %q, want %q (same instance as %q)", spelling, got, want, g[0])
			}
		}
	}
	// Distinct instances must not share a key (that hands over a login). A
	// named instance isn't its host's default; without a name, the port
	// distinguishes them (win10cli's SQL2017 at "win10cli,55253").
	for _, pair := range [][2]string{
		{"host", "host\\inst"},
		{"host", "host,55253"},
		{"host,1500", "host,55253"},
	} {
		if InstanceKey(pair[0]) == InstanceKey(pair[1]) {
			t.Errorf("InstanceKey collapses %q and %q onto %q", pair[0], pair[1], InstanceKey(pair[0]))
		}
	}
}

// The dialog stores the port in Port, so keys must fold it in, or win10cli's
// two instances share an entry.
func TestConnectionAddressFoldsInTheDialogPort(t *testing.T) {
	for _, tc := range []struct {
		conn Connection
		want string
	}{
		{Connection{Server: "win10cli", Port: 55253}, InstanceKey("win10cli,55253")},
		{Connection{Server: "win10cli", Port: 1433}, InstanceKey("win10cli")},
		{Connection{Server: "win10cli"}, InstanceKey("win10cli")},
		// A port in the address wins over the dialog's, as in Connect.
		{Connection{Server: "win10cli,55253", Port: 1433}, InstanceKey("win10cli,55253")},
		{Connection{Server: "win10cli\\sql2017", Port: 55253}, InstanceKey("win10cli\\sql2017")},
	} {
		if got := InstanceKey(ConnectionAddress(tc.conn)); got != tc.want {
			t.Errorf("InstanceKey(ConnectionAddress(%q port %d)) = %q, want %q",
				tc.conn.Server, tc.conn.Port, got, tc.want)
		}
	}
}

// T9: caches keyed by IdentityKey must not be shared by two identities on one
// server. User is empty for every method below but SQL and Entra Password, so
// a User-keyed cache gave Windows, Entra Default, MSI and both service
// principals one catalog.
func TestIdentityKeySeparatesIdentitiesOnOneServer(t *testing.T) {
	conns := []Connection{
		{Server: "srv", AuthMethod: AuthSQLServer, User: "sa"},
		{Server: "srv", AuthMethod: AuthSQLServer, User: "app"},
		{Server: "srv", AuthMethod: AuthWindows},
		{Server: "srv", AuthMethod: AuthEntraDefault},
		{Server: "srv", AuthMethod: AuthEntraMSI},
		{Server: "srv", AuthMethod: AuthEntraServicePrincipal, ClientID: "app-1"},
		{Server: "srv", AuthMethod: AuthEntraServicePrincipal, ClientID: "app-2"},
		// A login name may hold a comma; the separator must not let it
		// collide with an address port.
		{Server: "srv,1500", AuthMethod: AuthSQLServer, User: "sa"},
		{Server: "srv", AuthMethod: AuthSQLServer, User: "1500,sa"},
	}
	seen := map[string]int{}
	for i, c := range conns {
		k := c.IdentityKey()
		if j, dup := seen[k]; dup {
			t.Errorf("IdentityKey collapses %+v and %+v onto %q", conns[j], c, k)
		}
		seen[k] = i
	}
}

// T60: the instance part folds as InstanceKey(ConnectionAddress(c)) does, so
// one identity is one key however the address is spelled, and the database
// (a per-database cache's own suffix) plays no part.
func TestIdentityKeyFoldsTheAddress(t *testing.T) {
	want := Connection{Server: "HOST", User: "sa", Database: "a"}.IdentityKey()
	for _, c := range []Connection{
		{Server: "host", User: "sa"},
		{Server: "host", Port: 1433, User: "sa"},
		{Server: " host,1433 ", User: "sa", Database: "b"},
	} {
		if got := c.IdentityKey(); got != want {
			t.Errorf("IdentityKey(%+v) = %q, want %q", c, got, want)
		}
	}
	if (Connection{Server: "host", Port: 55253, User: "sa"}).IdentityKey() == want {
		t.Error("IdentityKey drops a Port-field port, so two instances on one host share a key")
	}
}
