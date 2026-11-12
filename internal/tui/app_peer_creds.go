package tui

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/propsheet"
)

// app_peer_creds.go holds App's answer to db.PeerCredentials: which saved
// connection to reach a given instance with.
//
// Always On is why it matters. Everything the Object Explorer, the AG dialogs and
// the endpoint wizard read off a second instance goes through
// db.ServerConn.Peer, which refuses an instance without a registered connection.
//
// The answer is the connections the user has already made: connect to a replica
// once (File > Connect, or the prompt offerPeerConnect opens where a refusal
// lands) and every later peer read reaches it the same way.

// peerCredStore is how to reach each instance the user has connected to.
// byInstance is keyed by config.InstanceKey; byShortHost maps a short name to
// its unique instance key, or "" when ambiguous. Consulted only when byInstance
// misses. mu guards both: background loaders read them through Peer.
type peerCredStore struct {
	mu          sync.Mutex
	byInstance  map[string]config.Connection
	byShortHost map[string]string
}

// peerCredentialsFor resolves an instance name to its own saved connection; the
// db.PeerCredentials installed on every connection App opens.
//
// Read from background loader goroutines (chiefly the Object Explorer's Always
// On loader, through Peer), so the maps are behind peerCreds.mu.
func (a *App) peerCredentialsFor(server string) (config.Connection, bool) {
	key := config.InstanceKey(server)
	a.peerCreds.mu.Lock()
	defer a.peerCreds.mu.Unlock()
	if c, ok := a.peerCreds.byInstance[key]; ok {
		return c, true
	}
	instance := a.peerCreds.byShortHost[key]
	if instance == "" {
		return config.Connection{}, false
	}
	c, ok := a.peerCreds.byInstance[instance]
	return c, ok
}

// rememberPeerCredentials records conn as the way to reach its instance,
// replacing what was held. Called for each saved connection at startup and on
// every successful connect, so the most recent way the user reached an instance
// is the one a peer read uses.
func (a *App) rememberPeerCredentials(conn config.Connection) {
	if strings.TrimSpace(conn.Server) == "" {
		return
	}
	// ConnectionAddress, not conn.Server: the Connect dialog saves a port in the
	// separate Port field, and keyed without it "win10cli" and its SQL2017 instance
	// on 55253 became one instance with the later login.
	key := config.InstanceKey(config.ConnectionAddress(conn))
	a.peerCreds.mu.Lock()
	defer a.peerCreds.mu.Unlock()
	if a.peerCreds.byInstance == nil {
		a.peerCreds.byInstance = map[string]config.Connection{}
	}
	a.peerCreds.byInstance[key] = conn
	if alias := shortHostKey(key); alias != "" {
		if a.peerCreds.byShortHost == nil {
			a.peerCreds.byShortHost = map[string]string{}
		}
		if previous, exists := a.peerCreds.byShortHost[alias]; exists && previous != key {
			a.peerCreds.byShortHost[alias] = ""
		} else {
			a.peerCreds.byShortHost[alias] = key
		}
	}
}

// shortHostKey is key with the host's domain suffix dropped, or "" when the host
// has none.
//
// The two names for an instance rarely agree: the catalog reports @@SERVERNAME
// (the short machine name), while on a domain network the user usually types the
// FQDN into Connect. Keyed only by exact host, a saved "ubusql2.fritz.box" would
// never answer a peer read for the "ubusql2" sys.availability_replicas reports.
//
// A separate, lower-priority tier: aliases shared by distinct instance keys are
// refused, and a unique alias still dials the original registered destination,
// never the metadata-provided short name.
//
// The "\instance" or ",port" InstanceKey appended is kept: the alias drops the
// domain, not what tells two instances on one host apart.
func shortHostKey(key string) string {
	host, suffix := key, ""
	if i := strings.IndexAny(key, "\\,"); i >= 0 {
		host, suffix = key[:i], key[i:]
	}
	short, _, dotted := strings.Cut(host, ".")
	if !dotted || short == "" {
		return ""
	}
	return short + suffix
}

// loadPeerCredentials seeds the map from the saved connections in stored order
// (oldest first, so the most recently used entry for an instance is the one left).
// Same precedence config.MatchByServer offers the Connect dialog.
//
// An entry whose password could not be decrypted is not seeded. A replaced
// config key blanks every saved password at once (config.Load), so this is a
// whole-file state, not a rare entry. Nor is one whose method requires a
// password and that saved none (config.Connection.NeedsPassword; Remember
// Password unticked is the default): it is certain to fail at login, and unseeded it is refused instead, which offers the
// Connect prompt (offerPeerConnect). A connection made this session is
// registered with its password in memory by rememberConnection.
//
// An entry that still fails reports its own error; Peer never retries with
// another server's credentials.
func (a *App) loadPeerCredentials() {
	if a.cfg == nil {
		return
	}
	for _, c := range a.cfg.Connections {
		if c.PasswordUnreadable() || c.NeedsPassword() {
			continue
		}
		a.rememberPeerCredentials(c)
	}
}

// forgetPeerFailure tells every open connection that server is reachable
// again, dropping the cached connect failure that would otherwise answer for
// it until it expires. See db.ServerConn.ForgetPeerFailure.
func (a *App) forgetPeerFailure(server string) {
	for _, sc := range a.connections {
		sc.ForgetPeerFailure(server)
	}
}

// offerPeerConnect opens the Connect dialog for the instance err names when err
// is Peer refusing one with no registered connection, and reports whether it
// did. then (nil for nothing) runs once that connection succeeds and is
// registered — the operation the refusal stopped.
//
// The SSMS shape: another instance is reached only through a connection the
// user approved, prompted for where it is needed. The dialog is pre-filled
// from parent, the connection the read started from, so a topology sharing one
// login (or Windows authentication) costs one Enter per replica; the name it
// shows is the one that will be dialled.
//
// Declined while the Connect dialog is already open: it is one instance, and a
// second refusal (two probes in flight) must not replace the first prompt.
func (a *App) offerPeerConnect(parent *db.ServerConn, err error, then func()) bool {
	refused, ok := errors.AsType[*db.PeerNotRegisteredError](err)
	if !ok || parent == nil || a.connectDialog.Visible() {
		return false
	}
	a.connectDialog.ShowForPeer(peerConnectTemplate(parent.Opts, refused.Server), then)
	a.setStatus(fmt.Sprintf("%s has no saved connection — connect to it to continue", refused.Server))
	return true
}

// peerConnectTemplate is the Connect prompt's pre-fill for server, reached from
// a connection made with parent: parent's sign-in and transport settings,
// pointed at server with no database.
//
// Only a pre-fill — nothing reaches server until the user presses Connect.
// Port is kept only for parent's own instance spelled without it (the catalog
// reports names portless); on any other instance it is another listener's
// port, and "HOSTA\SQL2017,50110" opening "HOSTB\PROD" would dial HOSTB:50110
// and bypass SQL Browser. Host Name In Certificate names parent's certificate,
// so it is cleared rather than validating server's against it.
func peerConnectTemplate(parent config.Connection, server string) config.Connection {
	opts := parent
	if _, _, port := gosmo.ParseServerAddress(opts.Server); port != 0 {
		opts.Port = port
	}
	if !sameInstanceApartFromPort(parent.Server, server) {
		opts.Port = 0
	}
	opts.Server = server
	opts.Database = ""
	opts.HostNameInCertificate = ""
	opts.Name = ""
	return opts
}

// sameInstanceApartFromPort reports whether a and b name one host and one
// instance name (case-insensitive), ignoring any port either carries.
func sameInstanceApartFromPort(a, b string) bool {
	hostA, instA, _ := gosmo.ParseServerAddress(strings.TrimSpace(a))
	hostB, instB, _ := gosmo.ParseServerAddress(strings.TrimSpace(b))
	return strings.EqualFold(hostA, hostB) && strings.EqualFold(instA, instB)
}

// pageSheet is what offerPeerConnectForSheet needs of a property sheet.
type pageSheet interface {
	PageState(i int) propsheet.PageState
	InvalidatePages(pages []int)
}

// offerPeerConnectForSheet is offerPeerConnect for a property sheet whose page
// load was refused: connecting reloads every page of it showing an error,
// provided live still reports the same showing. pageCount is the sheet's page
// count.
//
// Every errored page, not only the one that asked: a page visited while the
// prompt was up was refused too, and offerPeerConnect declined to prompt for
// it. Another error reloading as well is a harmless F5.
func (a *App) offerPeerConnectForSheet(parent *db.ServerConn, err error, sheet pageSheet, pageCount int, live func() bool) {
	a.offerPeerConnect(parent, err, func() {
		if !live() {
			return
		}
		var errored []int
		for i := range pageCount {
			if sheet.PageState(i) == propsheet.PageError {
				errored = append(errored, i)
			}
		}
		sheet.InvalidatePages(errored)
	})
}
