package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// TestPeerCredentialsResolveByInstanceNotSpelling is the lookup half. The
// catalog reports "UBUSQL2\PROD" while the user connected as
// "ubusql2.fritz.box\prod,1433"; a map keyed by the raw string would miss on
// exactly the connection that was meant to supply the credentials.
func TestPeerCredentialsResolveByInstanceNotSpelling(t *testing.T) {
	a := newTestApp()
	a.cfg.Connections = []config.Connection{
		{Server: "UbuSQL2.fritz.box\\Prod,1433", Port: 1433, User: "replica_login", Password: "replica-pw"},
	}
	a.loadPeerCredentials()

	got, ok := a.peerCredentialsFor("UBUSQL2\\PROD")
	if !ok {
		t.Fatal("no credentials found for the instance under the catalog's spelling of its name")
	}
	if got.User != "replica_login" {
		t.Errorf("resolved user %q, want %q", got.User, "replica_login")
	}

	if _, ok := a.peerCredentialsFor("ubusql9"); ok {
		t.Error("an instance nobody has connected to resolved to something; the fallback to the parent is unreachable")
	}
	// A named instance must not resolve to the default instance's login.
	if _, ok := a.peerCredentialsFor("ubusql2"); ok {
		t.Error("the bare host resolved to the named instance's credentials")
	}
}

// TestPeerCredentialsPreferTheMostRecent pins the precedence. config stores
// connections oldest-first, and two entries can name one instance (a different
// database, a different login). The most recent way the user reached it is the
// one that should be used, matching what MatchByServer offers the Connect
// dialog.
func TestPeerCredentialsPreferTheMostRecent(t *testing.T) {
	a := newTestApp()
	a.cfg.Connections = []config.Connection{
		{Server: "ubusql2", Port: 1433, User: "old_login", Password: "old-pw"},
		{Server: "ubusql2", Port: 1433, User: "new_login", Password: "new-pw"},
	}
	a.loadPeerCredentials()

	got, ok := a.peerCredentialsFor("ubusql2")
	if !ok {
		t.Fatal("no credentials found for ubusql2")
	}
	if got.User != "new_login" {
		t.Errorf("resolved user %q, want the most recently saved %q", got.User, "new_login")
	}
}

// TestRememberPeerCredentialsRefreshesTheMap is the other half of "connect to
// it once and every peer read uses it": a connection made *this* session has
// to reach the resolver without a restart, and has to displace whatever was
// loaded from disk for that instance.
func TestRememberPeerCredentialsRefreshesTheMap(t *testing.T) {
	a := newTestApp()
	a.cfg.Connections = []config.Connection{
		{Server: "ubusql2", Port: 1433, User: "stale_login", Password: "stale-pw"},
	}
	a.loadPeerCredentials()

	a.rememberPeerCredentials(config.Connection{
		Server: "ubusql2,1433", Port: 1433, User: "fresh_login", Password: "fresh-pw",
	})

	got, ok := a.peerCredentialsFor("UBUSQL2")
	if !ok {
		t.Fatal("no credentials for ubusql2 after connecting to it")
	}
	if got.User != "fresh_login" {
		t.Errorf("resolved user %q, want the connection just made (%q)", got.User, "fresh_login")
	}
}

// peerCredentialsFor has to keep satisfying db.PeerCredentials, which is what
// the three SetPeerCredentials calls in app_connections.go pass it as. Pinned
// at compile time because a signature drift there would otherwise only show up
// as a peer read quietly using the wrong login.
var _ db.PeerCredentials = (*App)(nil).peerCredentialsFor

// TestPeerCredentialsPreferExactHostOverAShortAlias pins the tiering. Two
// instances can share a short name across domains, so the FQDN fallback must
// never win against an exact match — folding them onto one key would hand one
// instance the other's login.
func TestPeerCredentialsPreferExactHostOverAShortAlias(t *testing.T) {
	a := newTestApp()
	a.cfg.Connections = []config.Connection{
		{Server: "sql.b.example", User: "b_login"},
		{Server: "sql.a.example", User: "a_login"},
		{Server: "sql", User: "bare_login"},
	}
	a.loadPeerCredentials()

	for _, tc := range []struct{ server, want string }{
		{"sql.a.example", "a_login"},
		{"sql.b.example", "b_login"},
		// The bare name has an exact entry of its own, which must beat both
		// aliases regardless of which was registered last.
		{"sql", "bare_login"},
	} {
		got, ok := a.peerCredentialsFor(tc.server)
		if !ok {
			t.Errorf("no credentials for %q", tc.server)
			continue
		}
		if got.User != tc.want {
			t.Errorf("%q resolved to %q, want %q", tc.server, got.User, tc.want)
		}
	}
}

// TestShortHostKeyOnlyAliasesADottedHost pins what does and does not get a
// fallback entry — an already-short host must not register an alias equal to
// its own key, which would put it in both tiers and make the tiering moot.
func TestShortHostKeyOnlyAliasesADottedHost(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"ubusql2.fritz.box", "ubusql2"},
		{"ubusql2.fritz.box\\prod", "ubusql2\\prod"},
		{"ubusql2", ""},
		{"ubusql2\\prod", ""},
		{"", ""},
	} {
		if got := shortHostKey(tc.key); got != tc.want {
			t.Errorf("shortHostKey(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// A saved connection whose password this session cannot decrypt must not be
// seeded. It would win over the parent connection's own credentials — which
// are live and working — and connect with an empty password, turning a peer
// read that used to work into a login failure. A replaced config key blanks
// every saved password at once, so this is the whole file, not one entry.
//
// Driven through config.Load rather than a hand-built Connection: the sealed
// ciphertext that marks the state is unexported, and Load is what produces it.
func TestPeerCredentialsSkipAPasswordThisSessionCannotRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "gossms")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"connections": [
		{"server": "ubusql2", "user": "sa", "password": "not-a-ciphertext"},
		{"server": "ubusql3", "auth_method": 1}
	]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	a.cfg = config.Load()
	a.loadPeerCredentials()

	if got, ok := a.peerCredentialsFor("ubusql2"); ok {
		t.Errorf("an entry with an unreadable password was seeded as %+v; the peer would sign in with an empty password", got)
	}
	// The other half: an entry that never had a password is not collateral
	// damage. Windows authentication does not use one.
	if _, ok := a.peerCredentialsFor("ubusql3"); !ok {
		t.Error("a Windows-authentication entry was dropped along with it")
	}
}
