package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// --- monitor kill -----------------------------------------------------------

// The server's KILL errors monitor kill maps to its own codes (captured on
// 17): 6106 "Process ID n is not an active process ID." (it ended between
// the read and the KILL), 6107 "Only user processes can be killed." and 6104
// "Cannot use KILL to kill your own process.". 6102, KILL without ALTER ANY
// CONNECTION, gosmo classifies as permission denied.
const (
	killNotActive   = 6106
	killNotUser     = 6107
	killOwnProcess  = 6104
	killPermissions = "KILL needs ALTER ANY CONNECTION (sysadmin and processadmin have it)."
)

func monitorKillCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "kill"},
		Summary: "End a session (KILL), rolling back its open transaction",
		Description: "KILL <spid>. Needs --allow admin and --confirm with the spid exactly as given (missing or " +
			"different: exit 7, before connecting). The session is read first (as monitor sessions shows it, " +
			"the session set): one that does not exist → exit 8; a system session → exit 7 and nothing sent " +
			"(the server would refuse it, Msg 6107); a session of this command's own connections → exit 2. " +
			"Needs VIEW SERVER STATE to read the session (without it: exit 9, nothing sent) and ALTER ANY " +
			"CONNECTION for the KILL (without it: exit 9). --dry-run prints the KILL and the session without " +
			"sending it. After the KILL the session is read again: ended is false while it rolls back an " +
			"open transaction, which can take as long as the work it undoes; that is a warning and not waited " +
			"for — KILL <spid> WITH STATUSONLY reports the rollback's progress.",
		Args: []spec.Arg{{Name: "spid", Help: "The session id to end", Required: true}},
		Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optConfirm, Type: spec.TypeString, Help: "The spid again, exactly as given: accepts the KILL"},
		}),
		Class: spec.ClassAdmin,
		Run:   d.monitorKill,
		Examples: []string{
			"gossms monitor kill 87 --confirm 87 -p dev --allow admin --dry-run",
			"gossms monitor kill 87 --confirm 87 -p dev --allow admin",
		},
	}
}

// killTarget is monitor kill's spid and its --confirm, checked before
// connecting: a whole number from 1 to 32767 (a smallint), else USAGE_ERROR;
// --confirm must repeat it exactly as given (§6.3), else BLOCKED.
func killTarget(inv *spec.Invocation) (int, error) {
	arg := inv.Args[0]
	spid, err := strconv.Atoi(arg)
	if err != nil || spid < 1 || spid > 32767 {
		return 0, usageError(fmt.Sprintf("spid %q is not a session id (a whole number from 1 to 32767)", arg), "")
	}
	confirm, _ := inv.Values[optConfirm].(string)
	if confirm != arg {
		msg := "monitor kill ends session " + arg + " and rolls back its open transaction"
		if inv.Given[optConfirm] {
			msg += fmt.Sprintf("; --confirm %q is not the spid as given", confirm)
		}
		return 0, output.NewError(output.CodeBlocked, msg, "Add --confirm "+arg+" to accept it.")
	}
	return spid, nil
}

// isOwnProcessSession reports whether s is a connection of this process: its
// client process id, from the reading connection's own host — any of the
// pool's connections, not only the one the KILL would go through.
func isOwnProcessSession(s *gosmo.ActiveSession) bool {
	return s.HostProcessID == os.Getpid() && s.FromCallerHost
}

func (d Deps) monitorKill(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	spid, err := killTarget(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	// Without VIEW SERVER STATE the read sees only the login's own session,
	// so every other spid would look ended.
	ok, err := cat.HasViewServerState(ctx)
	if err == nil && !ok {
		err = viewServerStateDenied("monitor kill")
	}
	if err != nil {
		return res, readError(err)
	}
	s, err := cat.SessionByID(ctx, spid)
	if errors.Is(err, gosmo.ErrNotFound) {
		return res, output.NewError(output.CodeNotFound, fmt.Sprintf("session %d does not exist", spid),
			"It has ended, or the id is wrong: monitor sessions lists the sessions.")
	}
	if err != nil {
		return res, readError(err)
	}
	set := sessionsSet([]*gosmo.ActiveSession{s})
	set.Name = "session"
	res.Sets = []output.Set{set}
	switch {
	case isOwnProcessSession(s):
		return res, usageError(fmt.Sprintf("session %d is one of this command's own connections", spid), "")
	case !s.IsUserProcess:
		return res, output.NewError(output.CodeBlocked,
			fmt.Sprintf("session %d is a system session: only user sessions can be killed", spid), "")
	}

	werr := applyWrite(ctx, inv, res, func(ctx context.Context) error { return w.KillSession(ctx, spid) })
	if werr != nil {
		return res, killError(werr, spid)
	}
	killed := !dryRun(inv)
	res.Fields = append(res.Fields, output.Field{Key: "killed", Value: strconv.FormatBool(killed)})
	if !killed {
		return res, nil
	}
	after, err := cat.SessionByID(ctx, spid)
	// A different login or client process on the spid is a new session that
	// took the ended one's id.
	ended := errors.Is(err, gosmo.ErrNotFound) ||
		err == nil && (after.LoginName != s.LoginName || after.HostProcessID != s.HostProcessID)
	if err != nil && !ended {
		res.Warnings = append(res.Warnings, "the KILL ran, but the session could not be read back: "+err.Error())
		return res, nil
	}
	res.Fields = append(res.Fields, output.Field{Key: "ended", Value: strconv.FormatBool(ended)})
	if !ended {
		what := "has not ended yet"
		if strings.Contains(after.Command, "ROLLBACK") {
			what = "is rolling back its open transaction"
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("session %d %s; not waited for: KILL %d WITH STATUSONLY "+
			"reports the rollback's progress.", spid, what, spid))
	}
	return res, nil
}

// killError is writeError for the KILL, with the server's refusals in
// monitor kill's codes.
func killError(err error, spid int) error {
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.SQL == nil {
		return err
	}
	switch e.SQL.Number {
	case killNotActive:
		e.Code = output.CodeNotFound
		e.Hint = fmt.Sprintf("Session %d ended after it was read.", spid)
	case killNotUser:
		e.Code = output.CodeBlocked
	case killOwnProcess:
		e.Code = output.CodeUsageError
	}
	if e.Code == output.CodePermissionDenied {
		e.Hint = killPermissions + " Use a login that has it, or have it granted."
	}
	return e
}
