//go:build livedb

// Live verification of the pre-2017 server-side file listing: the xp_dirtree
// path gosmo falls back to, and the refusal gossms speaks in place of the
// silent empty result it produces for a login that is not sysadmin.
//
// Neither half is reachable from a unit test. The version gate, the empty
// result and the login's role are all facts about a *real* pre-2017 instance,
// and the failure being guarded — an empty listing that reads as an empty
// folder — is precisely the one a fake cannot exhibit, since a fake returns
// whatever it was scripted to.
//
//	go test -tags livedb ./internal/tui/ -run TestLiveLegacy -v \
//	  -live-legacy-server 'win10cli\sql2016' -live-legacy-sa sa \
//	  -live-legacy-sa-password PASS
//
// -live-legacy-server must name an instance older than SQL Server 2017; the
// tests fail rather than skip if it is newer, since a silent pass here is the
// whole thing being avoided. Creates and drops one throwaway login on that
// instance and touches nothing else. Skipped entirely without the flags.
package tui

import (
	"context"
	"database/sql"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"

	_ "github.com/microsoft/go-mssqldb"
)

var (
	liveLegacyServer = flag.String("live-legacy-server", "", "a pre-2017 SQL Server instance")
	liveLegacySA     = flag.String("live-legacy-sa", "", "sysadmin login on -live-legacy-server")
	liveLegacySAPass = flag.String("live-legacy-sa-password", "", "password for -live-legacy-sa")
	liveLegacyDir    = flag.String("live-legacy-dir", "", "directory on that host to list (default: the instance's backup directory)")
)

// liveLegacyProbeLogin is the non-sysadmin the refusal is written for. Its
// password never leaves this file: it exists for the length of one test.
const (
	liveLegacyProbeLogin = "gossms_legacy_probe"
	liveLegacyProbePass  = "g0ssms-Legacy-Probe!"
)

// liveLegacyConn connects as user, the way the app does, and returns the
// connection plus a bounded context.
func liveLegacyConn(t *testing.T, user, password string) (*db.ServerConn, context.Context) {
	t.Helper()
	if *liveLegacyServer == "" || *liveLegacySA == "" {
		t.Skip("no -live-legacy-server/-live-legacy-sa given")
	}
	sc, err := db.Connect(config.Connection{
		Server:                 *liveLegacyServer,
		AuthMethod:             config.AuthSQLServer,
		User:                   user,
		Password:               password,
		TrustServerCertificate: true,
	})
	if err != nil {
		t.Fatalf("connect %s as %s: %v", *liveLegacyServer, user, err)
	}
	t.Cleanup(sc.Close)
	ctx, cancel := context.WithTimeout(sc.Context(), 60*time.Second)
	t.Cleanup(cancel)
	return sc, ctx
}

// liveLegacySAConn connects as the sysadmin and asserts the instance really is
// the one these tests are about. A 2017+ instance takes the DMV, where every
// assertion below would pass for the wrong reason.
func liveLegacySAConn(t *testing.T) (*db.ServerConn, context.Context) {
	t.Helper()
	sc, ctx := liveLegacyConn(t, *liveLegacySA, *liveLegacySAPass)
	info := sc.Server.Info()
	if info == nil {
		t.Fatalf("%s reported no ServerInfo", *liveLegacyServer)
	}
	if info.VersionMajor >= 14 {
		t.Fatalf("%s is major %d (%s); -live-legacy-server must name a pre-2017 instance, "+
			"or this test passes without exercising the xp_dirtree path at all",
			*liveLegacyServer, info.VersionMajor, info.ProductVersion)
	}
	if !sc.Server.EnumFileSystemIsLegacy() {
		t.Fatalf("major %d did not take the legacy listing path", info.VersionMajor)
	}
	return sc, ctx
}

// liveLegacyListDir is the directory the two tests list. Both must use the
// same one: the second test's whole claim is that a directory the first read
// with rows in it comes back empty for a different login.
func liveLegacyListDir(t *testing.T, sc *db.ServerConn) string {
	t.Helper()
	if *liveLegacyDir != "" {
		return *liveLegacyDir
	}
	// The parent of the backup directory rather than the directory itself: an
	// instance nobody has backed up has an empty Backup folder, and an empty
	// listing cannot tell the two tests below anything. Its parent (the
	// instance's MSSQL folder) always holds DATA, Backup and Log.
	dir := sc.Server.Info().DefaultBackupPath
	if dir == "" {
		t.Fatal("the instance reports no backup directory; pass -live-legacy-dir")
	}
	sep := "/"
	if strings.Contains(dir, `\`) {
		sep = `\`
	}
	if i := strings.LastIndex(strings.TrimRight(dir, sep), sep); i > 0 {
		return dir[:i]
	}
	return dir
}

// TestLiveLegacyFileListingDegradesRatherThanFailing pins the sysadmin half:
// the listing works on a pre-2017 instance, and pays for it in the two fields
// xp_dirtree does not report. gosmo's gate is positive for exactly this trade,
// so a regression that flipped it to the DMV would fail the instance outright.
func TestLiveLegacyFileListingDegradesRatherThanFailing(t *testing.T) {
	sc, ctx := liveLegacySAConn(t)
	dir := liveLegacyListDir(t, sc)

	found, err := sc.Server.EnumFileSystemContext(ctx, dir)
	if err != nil {
		t.Fatalf("list %q: %v", dir, err)
	}
	if len(found) == 0 {
		t.Fatalf("list %q as %s returned nothing; the fixture needs a directory with "+
			"entries in it (-live-legacy-dir)", dir, *liveLegacySA)
	}
	for _, e := range found {
		if e.Size != 0 || !e.LastModified.IsZero() {
			t.Errorf("%q came back with Size=%d LastModified=%v; xp_dirtree reports neither, "+
				"so this listing did not take the legacy path", e.Name, e.Size, e.LastModified)
			break
		}
	}
	// A sysadmin's non-empty listing is not a refusal, and must not be spoken as one.
	if err := legacyListingRefusal(sc, found); err != nil {
		t.Errorf("legacyListingRefusal claimed a refusal for a sysadmin's %d-entry listing: %v", len(found), err)
	}
}

// TestLiveLegacyListingIsRefusedForANonSysadmin is the half that has never run
// live: xp_dirtree answers a non-sysadmin with no rows and no error, which is
// indistinguishable from an empty directory in the result alone.
func TestLiveLegacyListingIsRefusedForANonSysadmin(t *testing.T) {
	sa, saCtx := liveLegacySAConn(t)
	dir := liveLegacyListDir(t, sa)

	// The directory has to have entries in it as sysadmin, or "empty for the
	// probe login" says nothing.
	found, err := sa.Server.EnumFileSystemContext(saCtx, dir)
	if err != nil || len(found) == 0 {
		t.Fatalf("list %q as %s: %d entries, err %v — the fixture needs a non-empty directory", dir, *liveLegacySA, len(found), err)
	}

	raw, err := sql.Open("sqlserver", "sqlserver://"+*liveLegacySA+":"+*liveLegacySAPass+"@"+
		strings.Replace(*liveLegacyServer, `\`, "/", 1)+"?TrustServerCertificate=true")
	if err != nil {
		t.Fatalf("open %s: %v", *liveLegacyServer, err)
	}
	t.Cleanup(func() { raw.Close() })
	exec := func(q string) {
		t.Helper()
		if _, err := raw.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("exec %.60q: %v", q, err)
		}
	}
	drop := "IF SUSER_ID('" + liveLegacyProbeLogin + "') IS NOT NULL DROP LOGIN [" + liveLegacyProbeLogin + "]"
	exec(drop)
	exec("CREATE LOGIN [" + liveLegacyProbeLogin + "] WITH PASSWORD = '" + liveLegacyProbePass + "', CHECK_POLICY = OFF")
	t.Cleanup(func() {
		if _, err := raw.ExecContext(context.Background(), drop); err != nil {
			t.Errorf("drop login %s: %v", liveLegacyProbeLogin, err)
		}
	})
	// What connecting needs and nothing more: the point is the missing sysadmin.
	exec("GRANT VIEW SERVER STATE, VIEW ANY DEFINITION, VIEW ANY DATABASE TO [" + liveLegacyProbeLogin + "]")

	sc, ctx := liveLegacyConn(t, liveLegacyProbeLogin, liveLegacyProbePass)
	caps := sc.Capabilities()
	if !caps.Probed() {
		t.Fatal("the probe login's capabilities were never probed; the refusal is deliberately withheld in that case")
	}
	if caps.IsSysadmin() {
		t.Fatal("the probe login is a sysadmin; nothing is being tested")
	}

	got, err := sc.Server.EnumFileSystemContext(ctx, dir)
	if err != nil {
		t.Fatalf("xp_dirtree raised an error for a non-sysadmin: %v — the refusal exists "+
			"because it does not, so the guard's premise has changed", err)
	}
	if len(got) != 0 {
		t.Fatalf("the non-sysadmin listing of %q returned %d entries; xp_dirtree is "+
			"documented to need sysadmin", dir, len(got))
	}

	if err := legacyListingRefusal(sc, got); err == nil {
		t.Fatal("an empty pre-2017 listing for a probed non-sysadmin was reported as an empty directory")
	} else if !strings.Contains(err.Error(), "sysadmin") {
		t.Errorf("refusal %q does not name the cause", err)
	}

	// And the surface the user actually meets: the Browse dialog's FileSystem.
	fs, ok := newServerFS(sc)
	if !ok {
		t.Fatal("newServerFS refused a live connection")
	}
	entries, err := fs.List(dir)
	if err == nil {
		t.Fatalf("serverFS.List returned %d entries and no error; the refusal never reached the file dialog", len(entries))
	}
	if !strings.Contains(err.Error(), "xp_dirtree") {
		t.Errorf("serverFS.List error %q does not explain the refusal", err)
	}
}
