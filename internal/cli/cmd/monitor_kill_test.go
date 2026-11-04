package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// killCatalog serves SessionByID: the first read target (nil: not found),
// every later one after (nil: not found), so a test sees the session before
// and after the KILL. noVSS answers HasViewServerState false.
type killCatalog struct {
	Catalog
	target, after *gosmo.ActiveSession
	noVSS         bool
	reads         int
}

func (c *killCatalog) HasViewServerState(context.Context) (bool, error) { return !c.noVSS, nil }

func (c *killCatalog) SessionByID(_ context.Context, id int) (*gosmo.ActiveSession, error) {
	c.reads++
	s := c.target
	if c.reads > 1 {
		s = c.after
	}
	if s == nil || s.SessionID != id {
		return nil, fmt.Errorf("gosmo: session %d not found: %w", id, gosmo.ErrNotFound)
	}
	return s, nil
}

// killWriter records the KILLs, failing them with err. Under WithScript it is
// the real serverWriter, so a dry run captures gosmo's KILL.
type killWriter struct {
	Writer
	killed []int
	err    error
}

func (w *killWriter) KillSession(ctx context.Context, id int) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.KillSession(ctx, id)
	}
	w.killed = append(w.killed, id)
	return w.err
}

// runKill runs monitor kill <argv> over cat and w, an ad-hoc connection;
// dials counts the connects.
func runKill(t *testing.T, cat *killCatalog, w *killWriter, argv string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
	}
	tree := &spec.Tree{Commands: MonitorCommands(d)}
	inv, perr := args.Parse(tree, append([]string{"monitor", "kill", "--server", "srv", "--auth", "windows"}, strings.Fields(argv)...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

// userSession is an idle user session spid of another client, with an open
// transaction.
func userSession(spid int) *gosmo.ActiveSession {
	return &gosmo.ActiveSession{SessionID: spid, LoginName: "app_user", HostName: "ws7", ProgramName: "app",
		DatabaseName: "Sales", Status: "sleeping", OpenTransactionCount: 1, RecentText: "BEGIN TRAN; UPDATE t SET v = 1",
		HostProcessID: 4242, IsUserProcess: true}
}

func TestMonitorKill(t *testing.T) {
	cat, w := &killCatalog{target: userSession(87)}, &killWriter{}
	res, dials, err := runKill(t, cat, w, "87 --confirm 87 --allow admin")
	if err != nil {
		t.Fatal(err)
	}
	if dials != 1 || !slices.Equal(w.killed, []int{87}) || cat.reads != 2 {
		t.Fatalf("%d dials, killed %v, %d reads", dials, w.killed, cat.reads)
	}
	if len(res.Sets) != 1 || res.Sets[0].Name != "session" || len(res.Sets[0].Rows) != 1 ||
		*res.Sets[0].Rows[0][0] != "87" || *res.Sets[0].Rows[0][1] != "app_user" {
		t.Errorf("sets %+v", res.Sets)
	}
	if fieldValue(res, "dryRun") != "false" || fieldValue(res, "killed") != "true" ||
		fieldValue(res, "ended") != "true" || hasField(res, "tsql") || len(res.Warnings) != 0 {
		t.Errorf("fields %+v, warnings %q", res.Fields, res.Warnings)
	}
}

func TestMonitorKillDryRun(t *testing.T) {
	cat, w := &killCatalog{target: userSession(87)}, &killWriter{}
	res, _, err := runKill(t, cat, w, "87 --confirm 87 --allow admin --dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.killed) != 0 || cat.reads != 1 {
		t.Errorf("killed %v, %d reads", w.killed, cat.reads)
	}
	if fieldValue(res, "dryRun") != "true" || fieldValue(res, "killed") != "false" || hasField(res, "ended") ||
		strings.TrimSpace(fieldValue(res, "tsql")) != "KILL 87\nGO" || len(res.Sets) != 1 {
		t.Errorf("fields %+v", res.Fields)
	}
}

// The rollback of a killed session's transaction is reported, not waited
// for; a new session that took the spid is not the killed one.
func TestMonitorKillAfterRead(t *testing.T) {
	rolling := userSession(87)
	rolling.Command = "KILLED/ROLLBACK"
	reused := userSession(87)
	reused.LoginName = "someone_else"
	for _, tc := range []struct {
		name      string
		after     *gosmo.ActiveSession
		ended     string
		wantAWarn string
	}{
		{"rolling back", rolling, "false", "is rolling back its open transaction; not waited for: KILL 87 WITH STATUSONLY"},
		{"still there", userSession(87), "false", "has not ended yet"},
		{"spid reused", reused, "true", ""},
	} {
		res, _, err := runKill(t, &killCatalog{target: userSession(87), after: tc.after}, &killWriter{}, "87 --confirm 87 --allow admin")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if fieldValue(res, "killed") != "true" || fieldValue(res, "ended") != tc.ended {
			t.Errorf("%s: fields %+v", tc.name, res.Fields)
		}
		if got := strings.Join(res.Warnings, "\n"); tc.wantAWarn == "" && got != "" || !strings.Contains(got, tc.wantAWarn) {
			t.Errorf("%s: warnings %q", tc.name, res.Warnings)
		}
	}
}

// Before connecting: a bad spid is a usage error, a missing or different
// --confirm and a class above --allow are refused.
func TestMonitorKillRefusedBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		argv string
		want output.Code
	}{
		{"abc --confirm abc --allow admin", output.CodeUsageError},
		{"0 --confirm 0 --allow admin", output.CodeUsageError},
		{"32768 --confirm 32768 --allow admin", output.CodeUsageError},
		{"87 --allow admin", output.CodeBlocked},
		{"87 --confirm 78 --allow admin", output.CodeBlocked},
		{"87 --confirm 087 --allow admin", output.CodeBlocked},
		{"87 --confirm 87", output.CodeBlocked},
		{"87 --confirm 87 --allow ddl --dry-run", output.CodeBlocked},
	} {
		cat, w := &killCatalog{target: userSession(87)}, &killWriter{}
		_, dials, err := runKill(t, cat, w, tc.argv)
		if errCode(err) != tc.want || dials != 0 || len(w.killed) != 0 {
			t.Errorf("%q: %v (%s), %d dials, killed %v; want %s", tc.argv, err, errCode(err), dials, w.killed, tc.want)
		}
	}
}

// After the read, and before any KILL: an ended session, one of this
// process's connections, a system session, and a login without VIEW SERVER
// STATE (which would see every other session as ended).
func TestMonitorKillRefusedAfterRead(t *testing.T) {
	own := userSession(87)
	own.HostProcessID, own.FromCallerHost = os.Getpid(), true
	sameHostOtherPID := userSession(87)
	sameHostOtherPID.FromCallerHost = true
	system := userSession(87)
	system.IsUserProcess, system.LoginName = false, "sa"
	for _, tc := range []struct {
		name   string
		cat    *killCatalog
		want   output.Code
		reads  int
		killed bool
	}{
		{"ended", &killCatalog{}, output.CodeNotFound, 1, false},
		{"own process", &killCatalog{target: own}, output.CodeUsageError, 1, false},
		{"system", &killCatalog{target: system}, output.CodeBlocked, 1, false},
		{"no VSS", &killCatalog{target: userSession(87), noVSS: true}, output.CodePermissionDenied, 0, false},
		{"another process on this host", &killCatalog{target: sameHostOtherPID}, "", 2, true},
	} {
		w := &killWriter{}
		_, _, err := runKill(t, tc.cat, w, "87 --confirm 87 --allow admin")
		if errCode(err) != tc.want || tc.cat.reads != tc.reads || (len(w.killed) == 1) != tc.killed {
			t.Errorf("%s: %v (%s), %d reads, killed %v; want %q", tc.name, err, errCode(err), tc.cat.reads, w.killed, tc.want)
		}
	}
}

// The server's refusals of the KILL itself, in monitor kill's codes.
func TestMonitorKillServerRefusals(t *testing.T) {
	for _, tc := range []struct {
		number int32
		class  uint8
		want   output.Code
		hint   string
	}{
		{6106, 16, output.CodeNotFound, "ended after it was read"},
		{6107, 14, output.CodeBlocked, ""},
		{6104, 16, output.CodeUsageError, ""},
		{6102, 14, output.CodePermissionDenied, "ALTER ANY CONNECTION"},
	} {
		w := &killWriter{err: fmt.Errorf("gosmo: kill session 87: %w",
			mssql.Error{Number: tc.number, Class: tc.class, Message: fmt.Sprintf("Msg %d", tc.number)})}
		res, _, err := runKill(t, &killCatalog{target: userSession(87)}, w, "87 --confirm 87 --allow admin")
		e, _ := err.(*output.Error)
		if errCode(err) != tc.want || e == nil || !strings.Contains(e.Hint, tc.hint) || hasField(res, "killed") {
			t.Errorf("Msg %d: %v (%s), fields %+v; want %s", tc.number, err, errCode(err), res.Fields, tc.want)
		}
	}
}
