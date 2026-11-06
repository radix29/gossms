package cmd

import (
	"context"
	"errors"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The shared write layer (phase 5, D1 and D2): what every command that
// changes the server goes through. --allow and --dry-run are the writing
// commands' own options, not globals, so a read command neither accepts nor
// advertises a flag that would do nothing there.

// writeOptions is --allow and --dry-run: the options a writing command adds
// to its own.
func writeOptions() []spec.Option {
	return []spec.Option{
		{Name: optAllow, Type: spec.TypeEnum, Enum: classNames(), Default: string(spec.ClassRead),
			Help: "Highest operation class the command may reach; the profile's ceiling (max_allow) caps it"},
		{Name: optDryRun, Type: spec.TypeBool,
			Help: "Read what the change needs and print the T-SQL it would run, without running it"},
	}
}

// dryRun reports whether inv asked for --dry-run.
func dryRun(inv *spec.Invocation) bool {
	v, _ := inv.Values[optDryRun].(bool)
	return v
}

// gate is a writing command's class check (D1), made before it connects: a
// refused change never reaches the server, and --dry-run is refused alike.
// The command's Class is checked against --allow capped by the profile's
// ceiling, with query run's wording and hints. A refusal is BLOCKED (exit 7)
// and res names the classes; there are no statements to list. It returns
// the configuration and profile the command then connects through.
func (d Deps) gate(inv *spec.Invocation, res *output.Result) (*config.Config, string, error) {
	return d.gateClass(inv, res, inv.Command.Class)
}

// gateClass is gate for a command whose class depends on its options
// (data import --truncate-first is ddl): class in place of the Command's.
//
// A command that passes is next checked against the audit log: one that
// cannot be written refuses the change (and its dry run) before it connects.
func (d Deps) gateClass(inv *spec.Invocation, res *output.Result, class spec.Class) (*config.Config, string, error) {
	inv.Audit.Class = class
	cfg, profile, ceiling, err := d.profileCeiling(inv)
	if err != nil {
		return nil, "", err
	}
	noteTarget(inv, cfg, profile)
	p := safety.Policy{Allow: spec.Class(inv.Values[optAllow].(string)), Ceiling: ceiling}
	b := p.Refuse(inv.Command.Name(), class)
	if b == nil {
		if err := checkAudit(inv); err != nil {
			return nil, "", err
		}
		return cfg, profile, nil
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "detectedClass", Value: string(b.Detected)},
		output.Field{Key: "allowedClass", Value: string(b.Allowed)})
	return nil, "", blockedError(b)
}

// connectGated is connectCatalog for a writing command: the gate first,
// then the connection and its reads. The command writes through gosmo
// directly (sc.Server, or a handle read through it). The caller closes sc.
func (d Deps) connectGated(ctx context.Context, inv *spec.Invocation, res *output.Result) (*db.ServerConn, Catalog, error) {
	cfg, profile, err := d.gate(inv, res)
	if err != nil {
		return nil, nil, err
	}
	_, sc, err := d.dial(ctx, inv, cfg, profile, res)
	if err != nil {
		return nil, nil, err
	}
	return sc, d.Catalog(sc), nil
}

// applyWrite runs write, the command's gosmo writes (D2). Under --dry-run it
// runs them inside gosmo.WithScript instead: nothing reaches the server, and
// the tsql field is the exact batch gosmo would have sent. res gains dryRun
// either way, after the command's own fields, so a caller reads one shape;
// the status stays ok. Reads made with ctx before or after are real reads
// in both cases — a dry run's context comes from the server.
func applyWrite(ctx context.Context, inv *spec.Invocation, res *output.Result, write func(context.Context) error) error {
	dry := dryRun(inv)
	res.Fields = append(res.Fields, output.Field{Key: "dryRun", Value: strconv.FormatBool(dry)})
	if !dry {
		if err := write(ctx); err != nil {
			return writeError(err)
		}
		return nil
	}
	sctx, script := gosmo.WithScript(ctx)
	if err := write(sctx); err != nil {
		return writeError(err)
	}
	res.Fields = append(res.Fields, output.Field{Key: "tsql", Value: script.String(), Verbatim: true})
	return nil
}

// writeError is readError for a gosmo write: a request gosmo refuses before
// sending (a value the command should have caught) is USAGE_ERROR, and the
// permission hint speaks of a change.
func writeError(err error) *output.Error {
	if errors.Is(err, gosmo.ErrInvalidRequest) {
		return usageError(err.Error(), "")
	}
	_, own := errors.AsType[*output.Error](err)
	e := readError(err)
	if own {
		// The command explained the failure itself.
		return e
	}
	if se, ok := gosmo.AsSQLError(err); ok && e.SQL != nil && isStatementFailed(se.Number) {
		// The code too is the cause's: a BACKUP refused for want of a
		// permission ends in the plain Msg 3013.
		e = sqlError(alterCause(se))
	}
	if e.Code == output.CodePermissionDenied && e.SQL != nil {
		e.Hint = "The login lacks a permission this change needs: the message names it. " +
			"Use a login that has it, or have it granted."
	}
	return e
}

// The contentless errors that end a refused statement after the message
// that says why: "ALTER DATABASE statement failed." and "BACKUP (or
// RESTORE) DATABASE is terminating abnormally.".
const (
	alterDatabaseFailed = 5069
	backupTerminated    = 3013
)

func isStatementFailed(n int32) bool { return n == alterDatabaseFailed || n == backupTerminated }

// alterCause is the first error of se's batch that is not a contentless
// Msg 5069 or 3013 — the one that names the cause (CONTAINMENT = PARTIAL's
// Msg 12824, live on 17) — or se when there is none.
func alterCause(se *gosmo.SQLError) *gosmo.SQLError {
	for _, m := range se.All {
		if m.IsError() && !isStatementFailed(m.Number) {
			return &m
		}
	}
	return se
}

// noteTarget records in inv's audit facts the profile and where the call
// would connect, read without connecting: a refused call is logged with them
// too.
func noteTarget(inv *spec.Invocation, cfg *config.Config, profile string) {
	inv.Audit.Profile = profile
	inv.Audit.Server, inv.Audit.Database, inv.Audit.Login = connect.Describe(inv, cfg, profile)
}

// confirmRefusal is err, a command's --confirm check made before gate,
// after noting where the call would connect when err refuses it (BLOCKED):
// the refusal's audit line then names its target as a gated one does. A
// profile that cannot be read leaves the target empty; the refusal stands.
func (d Deps) confirmRefusal(inv *spec.Invocation, err error) error {
	if e, ok := errors.AsType[*output.Error](err); ok && e.Code == output.CodeBlocked {
		if cfg, profile, _, perr := d.profileCeiling(inv); perr == nil {
			noteTarget(inv, cfg, profile)
		}
	}
	return err
}

// checkAudit is the audit log's check before a change (docs/cli.md § Audit
// log): a log that cannot be written refuses the call, since the guardrail
// is not optional. Nil when it can, or when there is no log (tests).
func checkAudit(inv *spec.Invocation) error {
	if inv.Audit.Check == nil {
		return nil
	}
	if err := inv.Audit.Check(); err != nil {
		return output.NewError(output.CodeGeneralError, "the audit log cannot be written: "+err.Error(),
			"Nothing was changed: every change is recorded in the audit log first. Make the file (or its directory) writable.")
	}
	return nil
}
