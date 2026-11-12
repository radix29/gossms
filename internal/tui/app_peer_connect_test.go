package tui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/propsheet"
)

// The prompt's pre-fill is the parent's sign-in pointed at the refused
// instance. Each field cleared here would otherwise be the parent's and wrong
// for another instance: its port (another listener's), its database (may not
// exist there) and its certificate host name (validating the peer against the
// parent's certificate).
func TestPeerConnectTemplateCarriesTheSignInNotTheDestination(t *testing.T) {
	parent := config.Connection{
		Server: `hosta\SQL2017`, Port: 50110, Database: "SalesDB",
		AuthMethod: config.AuthSQLServer, User: "sa", Password: "pw", RememberPassword: true,
		Encrypt: config.EncryptMandatory, HostNameInCertificate: "hosta.example",
		ExtraProperties: "app name=x", Name: "hosta saved",
	}
	got := peerConnectTemplate(parent, `HOSTB\PROD`)
	want := parent
	want.Server, want.Port, want.Database, want.HostNameInCertificate, want.Name = `HOSTB\PROD`, 0, "", "", ""
	if got != want {
		t.Errorf("template = %+v\nwant %+v", got, want)
	}
}

func TestPeerConnectTemplateKeepsThePortOnlyForTheSameInstance(t *testing.T) {
	cases := []struct {
		parent     string
		parentPort int
		peer       string
		wantPort   int
	}{
		{`hosta\SQL2017,50110`, 0, `HOSTB\PROD`, 0},
		{`hosta\SQL2017`, 50110, `HOSTA\SQL2016`, 0},
		{"ubusql1,14330", 0, "ubusql2", 0},
		{"ubusql1,14330", 0, "UBUSQL1", 14330},
		{`win10cli\SQL2017`, 55253, `WIN10CLI\sql2017`, 55253},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s→%s", tc.parent, tc.peer), func(t *testing.T) {
			got := peerConnectTemplate(config.Connection{Server: tc.parent, Port: tc.parentPort}, tc.peer)
			if got.Server != tc.peer || got.Port != tc.wantPort {
				t.Errorf("template = %q port %d, want %q port %d", got.Server, got.Port, tc.peer, tc.wantPort)
			}
		})
	}
}

// A refusal opens the Connect dialog in peer mode for the refused name, and
// only a refusal does: any other error (a real login failure, a timeout) is
// reported where it landed, not answered with a prompt.
func TestOfferPeerConnectOpensThePromptOnlyForARefusal(t *testing.T) {
	a := newTestApp()
	parent := &db.ServerConn{Opts: config.Connection{Server: "ubusql1", User: "sa", Password: "pw"}}

	if a.offerPeerConnect(parent, errors.New("login failed"), nil) || a.connectDialog.Visible() {
		t.Fatal("an ordinary error opened the Connect prompt")
	}

	refusal := fmt.Errorf("connect to ubusql2: %w", &db.PeerNotRegisteredError{Server: "UBUSQL2"})
	if !a.offerPeerConnect(parent, refusal, nil) {
		t.Fatal("a wrapped refusal did not open the Connect prompt")
	}
	d := a.connectDialog
	if !d.Visible() || !d.forPeer {
		t.Fatal("the Connect dialog is not open in peer mode")
	}
	if got := d.fServer.Value(); got != "UBUSQL2" {
		t.Errorf("server field = %q, want the refused instance", got)
	}
	if d.fUser.Value() != "sa" || d.fPassword.Value() != "pw" {
		t.Error("the prompt is not pre-filled with the parent's sign-in")
	}
	if d.cbRemember.Checked() {
		t.Error("Remember Password is ticked for a password the parent did not remember")
	}
	if got := d.Title(); got != "Connect to UBUSQL2" {
		t.Errorf("title = %q, want it to name the instance", got)
	}

	// A second refusal while the first prompt is up must not replace it.
	other := &db.PeerNotRegisteredError{Server: "ubusql3"}
	if a.offerPeerConnect(parent, other, nil) || d.fServer.Value() != "UBUSQL2" {
		t.Error("a second refusal replaced the open prompt")
	}

	d.Hide()
	if d.forPeer || d.peerThen != nil || d.Title() != connectDialogTitle {
		t.Error("Hide left peer mode behind; File > Connect would register instead of connecting")
	}
}

// A failed peer connect leaves the prompt open with the error, as any Connect
// does, and does not run the operation it was opened for.
func TestFailedPeerConnectKeepsThePromptAndSkipsTheRetry(t *testing.T) {
	a := newTestApp()
	a.alertDialog = dialogs.NewAlertDialog(nil)
	parent := &db.ServerConn{Opts: config.Connection{Server: "ubusql1", User: "sa", Password: "pw"}}
	ran := false
	// Loopback port 1 refuses at once.
	a.offerPeerConnect(parent, &db.PeerNotRegisteredError{Server: "127.0.0.1,1"}, func() { ran = true })
	d := a.connectDialog
	d.startConnect(d.currentOptions())
	waitAndDrain(t, a)

	if ran {
		t.Error("the refused operation ran after the connect failed")
	}
	if !d.Visible() || !d.forPeer {
		t.Error("the prompt closed (or left peer mode) on a failed connect")
	}
	if !a.alertDialog.Visible() {
		t.Error("the connect failure was not reported")
	}
	if _, ok := a.peerCredentialsFor("127.0.0.1,1"); ok {
		t.Error("a connection that failed was registered for peer reads")
	}
}

// The Object Explorer note for a primary with no registered connection is
// activatable, and activating it opens the prompt for that primary.
func TestUnregisteredPrimaryNoteOpensThePrompt(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "ubusql1")
	l := loaderCtx{sc: sc}

	note := agView{unreachable: "UBUSQL2", unregistered: true}.followNote(l)
	if note.data.UnregisteredPeer != "UBUSQL2" {
		t.Fatalf("note = %+v, want it to carry the primary", note.data)
	}
	plain := agView{unreachable: "UBUSQL2"}.followNote(l)
	if plain.data.UnregisteredPeer != "" {
		t.Fatal("an unreachable (not unregistered) primary's note offers a Connect")
	}

	root := a.explorer.roots[0]
	a.explorer.SetChildren(root, []*explorerNode{note})
	if !a.explorer.handleActivate(note.id) {
		t.Fatal("activating the note did nothing")
	}
	if d := a.connectDialog; !d.Visible() || !d.forPeer || d.fServer.Value() != "UBUSQL2" {
		t.Error("activating the note did not open the prompt for its primary")
	}
}

// A Properties page reaching the group's primary without a registered
// connection offers the prompt, and connecting reloads the refused page.
// A refusal the user does nothing about leaves the page's error in place.
func TestRefusedPropertiesPageOffersThePromptAndReloads(t *testing.T) {
	a := newTestApp()
	a.propDialog = NewPropDialog(a)
	sc := addTestConn(a, "ubusql1")

	var loads atomic.Int32
	d := a.propDialog
	d.show(sc, "", "Availability Group Properties", "AAG1", "Server: ubusql1", func() []propPage {
		return []propPage{{
			title: "General",
			load: func(context.Context) (*propsheet.Form, propApply, error) {
				if loads.Add(1) == 1 {
					return nil, nil, fmt.Errorf("connect to primary replica UBUSQL2: %w", &db.PeerNotRegisteredError{Server: "UBUSQL2"})
				}
				return propsheet.NewForm(), nil, nil
			},
		}}
	})
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageError }, "the refused page to report")

	cd := a.connectDialog
	if !cd.Visible() || !cd.forPeer || cd.fServer.Value() != "UBUSQL2" {
		t.Fatal("a refused page load did not offer the Connect prompt for the primary")
	}
	// What registerPeer does once the connect succeeds.
	then := cd.peerThen
	cd.Hide()
	then()
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageReady }, "the page to reload")
	if got := loads.Load(); got != 2 {
		t.Errorf("the page loaded %d times, want 2 (the refusal, then the reload)", got)
	}
}

// Connecting after the dialog closed must not reload anything into it, nor
// into a later showing of the same dialog (another object's pages).
func TestPeerPromptRetryIsDroppedForAClosedPropertiesDialog(t *testing.T) {
	a := newTestApp()
	a.propDialog = NewPropDialog(a)
	sc := addTestConn(a, "ubusql1")

	var loads atomic.Int32
	d := a.propDialog
	d.show(sc, "", "Availability Group Properties", "AAG1", "Server: ubusql1", func() []propPage {
		return []propPage{{
			title: "General",
			load: func(context.Context) (*propsheet.Form, propApply, error) {
				loads.Add(1)
				return nil, nil, &db.PeerNotRegisteredError{Server: "UBUSQL2"}
			},
		}}
	})
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageError }, "the refused page to report")
	then := a.connectDialog.peerThen
	a.connectDialog.Hide()
	d.Dismiss()

	var later atomic.Int32
	d.show(sc, "", "Availability Group Properties", "AAG2", "Server: ubusql1", func() []propPage {
		return []propPage{{
			title: "General",
			load: func(context.Context) (*propsheet.Form, propApply, error) {
				later.Add(1)
				return nil, nil, errors.New("timeout")
			},
		}}
	})
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageError }, "the later showing's page to fail")
	then()
	// A reload would have moved the page to PageLoading synchronously.
	if got := d.PageState(0); got != propsheet.PageError {
		t.Errorf("page state = %v after the earlier showing's retry, want PageError — it reloaded the later showing", got)
	}
}

// The New-object dialogs (Add Database, Add Listener, Add Replica) read the
// group through its primary in their prefetch; a refusal there offers the
// prompt too, and connecting refetches.
func TestRefusedNewObjectPrefetchOffersThePromptAndRefetches(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "ubusql1")

	var fetches atomic.Int32
	d := &newObjectDialog[testPrefetch]{}
	d.init(a, newObjectConfig[testPrefetch]{
		title: "Add Database to Availability Group",
		noun:  "Database",
		pages: []string{"General"},
		fetch: func(context.Context, *db.ServerConn) (*testPrefetch, error) {
			if fetches.Add(1) == 1 {
				return nil, &db.PeerNotRegisteredError{Server: "UBUSQL2"}
			}
			return &testPrefetch{}, nil
		},
		build: func(*testPrefetch) {
			d.forms = []*propsheet.Form{propsheet.NewForm()}
			d.applyFns = []propApply{nil}
		},
		refresh: func(*db.ServerConn) {},
	})
	d.show(sc)
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageError }, "the refused prefetch to report")

	cd := a.connectDialog
	if !cd.Visible() || !cd.forPeer {
		t.Fatal("a refused prefetch did not offer the Connect prompt")
	}
	then := cd.peerThen
	cd.Hide()
	then()
	drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageReady }, "the prefetch to rerun")
	if got := fetches.Load(); got != 2 {
		t.Errorf("fetched %d times, want 2", got)
	}
}
