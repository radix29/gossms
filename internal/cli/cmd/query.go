package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// The SQL input options every query command takes, exactly one of them.
const (
	optSQL       = "sql"
	optFile      = "file"
	optSQLBase64 = "sql-base64"
)

// The options of the commands that run T-SQL.
const (
	optAllow           = "allow"
	optProfile         = "profile"
	optDryRun          = "dry-run"
	optTimeout         = "timeout"
	optStatistics      = "statistics"
	optContinueOnError = "continue-on-error"
	optTransaction     = "transaction"
	optMaxRowsAffected = "max-rows-affected"
)

// defaultTimeout is --timeout's default, in seconds.
const defaultTimeout = 60

// --transaction's values.
const (
	tranAuto     = "auto"
	tranCommit   = "commit"
	tranRollback = "rollback"
)

// What a wrapped run's transaction came to: the "transaction" field.
const (
	outcomeCommitted     = "committed"
	outcomeRolledBack    = "rolled-back"
	outcomeEndedByScript = "ended-by-script"
)

// maxScriptBytes bounds a script read from a file or stdin, so a wrong path
// (a disk image, /dev/zero) fails rather than exhausting memory.
const maxScriptBytes = 64 << 20

// QueryCommands is the query group: its node and its commands.
func QueryCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"query"},
			Summary: "Run, parse and classify T-SQL",
			Description: "The query editor's commands. Every one takes its T-SQL from exactly one of --sql (-Q), " +
				"--file (-i; - reads stdin) or --sql-base64. For anything but a one-line query prefer --file - or " +
				"--sql-base64: no shell can damage them.",
			Class: spec.ClassRead,
		},
		{
			Path:    []string{"query", "classify"},
			Summary: "Report each statement's operation class (read, write, ddl, admin) and the script's",
			Description: "Classifies without connecting: one row per statement with its batch, line, class, leading " +
				"keywords, the object it acts on, why it is above its leader's class, and flags " +
				"(transaction-control, not-in-transaction, nocount, dynamic-sql, exec, mode-option, output-clause). " +
				"Anything unrecognised counts as write. With --allow it also applies the check a run would " +
				"(SET PARSEONLY, NOEXEC and SHOWPLAN_* are refused too) and answers BLOCKED, exit 7, when the script " +
				"would be refused.",
			Options: append(sqlInputOptions(),
				spec.Option{Name: optAllow, Type: spec.TypeEnum, Enum: classNames(),
					Help: "Check the script against this class, as a run with --allow would"}),
			Class: spec.ClassRead,
			Run:   d.queryClassify,
			Examples: []string{
				"gossms query classify --file fix.sql",
				"gossms query classify --file - --allow write -o json",
				"gossms query classify --sql-base64 VVBEQVRFIGRiby50IFNFVCB4ID0gMQ==",
			},
		},
		{
			Path:    []string{"query", "run"},
			Summary: "Run T-SQL and return its result sets, messages and rows affected",
			Description: "Runs the script on one session, as a query window does: temp tables, SET options and USE carry " +
				"across its GO batches, and nothing carries across calls. The script is classified first and refused " +
				"(BLOCKED, exit 7, before connecting) when it is above --allow (default read) or the profile's ceiling. " +
				"The run stops after the first batch that fails unless --continue-on-error. --transaction rollback runs " +
				"the whole script in one transaction under SET XACT_ABORT ON and rolls it back, to measure rowsAffected " +
				"without keeping the change; commit commits it only when every batch succeeded; --max-rows-affected " +
				"commits only when the rows changed stay at or under the limit. --dry-run classifies and checks without " +
				"connecting. --param name=value:type runs the script (one batch) through sp_executesql with @name as a " +
				"typed parameter, so a value is never pasted into the T-SQL; the script is classified as written.",
			Options: append(append(append(sqlInputOptions(), profileOption()), connect.Options()...),
				spec.Option{Name: optAllow, Type: spec.TypeEnum, Enum: classNames(), Default: string(spec.ClassRead),
					Help: "Highest operation class the script may reach; the profile's ceiling (max_allow) caps it"},
				spec.Option{Name: optDryRun, Type: spec.TypeBool,
					Help: "Classify and check the script as a run would, without connecting or running anything"},
				spec.Option{Name: optTransaction, Type: spec.TypeEnum, Enum: []string{tranAuto, tranCommit, tranRollback},
					Default: tranAuto,
					Help:    "auto: each statement commits itself; rollback: run in one transaction and roll it back; commit: commit it only if every batch succeeded"},
				spec.Option{Name: optMaxRowsAffected, Type: spec.TypeInt,
					Help: "Run in one transaction and commit only if the rows changed stay at or under this; otherwise roll back (exit 7)"},
				spec.Option{Name: optContinueOnError, Type: spec.TypeBool,
					Help: "Run every batch even after one fails (exit 10 when some succeeded); refused with a transaction"},
				timeoutOption(),
				spec.Option{Name: optStatistics, Type: spec.TypeList,
					Help: "io, time: SET STATISTICS IO / TIME ON for the run; their output goes to messages"},
				spec.Option{Name: optParam, Type: spec.TypeRepeat,
					Help: "name=value:type, e.g. id=42:int: run the script through sp_executesql with @name declared as type; one batch only"},
			),
			Class: spec.ClassRead,
			Run:   d.queryRun,
			Examples: []string{
				"gossms query run -p dev --file report.sql",
				"gossms query run -p dev --file - -o json",
				"gossms query run -p dev --file fix.sql --allow write --transaction rollback",
				"gossms query run -p dev --file fix.sql --allow write --max-rows-affected 10",
				"gossms query run -p dev --file fix.sql --allow write --dry-run",
				"gossms query run -p dev --file big.sql --out-file rows.csv",
				"gossms query run -p dev --file by-id.sql --param id=42:int --param since=2026-01-01:date",
			},
		},
		{
			Path:    []string{"query", "parse"},
			Summary: "Check T-SQL syntax on the server without running it (SET PARSEONLY ON)",
			Description: "Sends every batch under SET PARSEONLY ON: the server checks the syntax and runs nothing, so " +
				"no --allow is needed. Syntax errors are SQL_ERROR (exit 5) with their script line. Names are not " +
				"resolved: a missing table or column is not an error here, only at run time. SET PARSEONLY, NOEXEC " +
				"and SHOWPLAN_* in the script are refused (exit 7).",
			Options: append(append(append(sqlInputOptions(), profileOption()), connect.Options()...), timeoutOption()),
			Class:   spec.ClassRead,
			Run:     d.queryParse,
			Examples: []string{
				"gossms query parse -p dev --file fix.sql",
				"gossms query parse -p dev --file - -o json",
			},
		},
	}
}

func timeoutOption() spec.Option {
	return spec.Option{Name: optTimeout, Type: spec.TypeInt, Default: strconv.Itoa(defaultTimeout),
		Help: "Seconds the script may run (the connect has --connect-timeout); 0 is no limit"}
}

func classNames() []string {
	return []string{string(spec.ClassRead), string(spec.ClassWrite), string(spec.ClassDDL), string(spec.ClassAdmin)}
}

// sqlInputOptions is --sql, --file and --sql-base64.
func sqlInputOptions() []spec.Option {
	return []spec.Option{
		{Name: optSQL, Short: "Q", Type: spec.TypeString, Help: "Inline T-SQL (fine for a short query)"},
		{Name: optFile, Short: "i", Type: spec.TypeString,
			Help: "T-SQL file; - reads stdin. GO separates batches. UTF-8, or UTF-16 with a byte-order mark"},
		{Name: optSQLBase64, Type: spec.TypeString, Help: "T-SQL as Base64 of its UTF-8 text, safe from any shell quoting"},
	}
}

// readSQL is the script from the one SQL input option given.
func readSQL(inv *spec.Invocation) (string, error) {
	var given []string
	for _, n := range []string{optSQL, optFile, optSQLBase64} {
		if inv.Given[n] {
			given = append(given, "--"+n)
		}
	}
	switch len(given) {
	case 0:
		return "", usageError("no T-SQL given", "Give one of --sql (-Q), --file (-i; - for stdin) or --sql-base64.")
	case 1:
	default:
		return "", usageError(strings.Join(given, " and ")+" both given", "Give exactly one T-SQL source.")
	}
	var data []byte
	what := given[0]
	switch given[0] {
	case "--" + optSQL:
		return inv.Values[optSQL].(string), nil
	case "--" + optSQLBase64:
		raw := strings.TrimSpace(inv.Values[optSQLBase64].(string))
		var err error
		if data, err = base64.StdEncoding.DecodeString(raw); err != nil {
			if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
				return "", usageError("--sql-base64: not Base64: "+err.Error(), "Encode the UTF-8 text with standard Base64.")
			}
		}
	default:
		path := inv.Values[optFile].(string)
		if path == "-" {
			if stdin, _ := inv.Values[connect.OptPasswordStdin].(bool); stdin {
				return "", usageError("--file - and --password-stdin both read stdin",
					"Pass the password with --password-env, or the T-SQL with --file <path> or --sql-base64.")
			}
			if inv.Stdin == nil {
				return "", usageError("--file -: no stdin", "")
			}
			var err error
			if data, err = io.ReadAll(io.LimitReader(inv.Stdin, maxScriptBytes+1)); err != nil {
				return "", usageError("--file -: "+err.Error(), "")
			}
			what = "stdin"
		} else {
			if inv.ReadFile == nil {
				return "", usageError("--file: files cannot be read here", "")
			}
			var err error
			if data, err = inv.ReadFile(path); err != nil {
				return "", usageError("--file "+path+": "+err.Error(), "")
			}
			what = path
		}
		if len(data) > maxScriptBytes {
			return "", usageError(fmt.Sprintf("%s: the script is over %d MB", what, maxScriptBytes>>20), "")
		}
	}
	text, ok := decodeScript(data)
	if !ok {
		return "", usageError(what+": the T-SQL is not valid UTF-8", "Save the file as UTF-8, or UTF-16 with a byte-order mark.")
	}
	return text, nil
}

// decodeScript is a script's bytes as text: a UTF-8 byte-order mark is dropped
// and UTF-16 (which SSMS saved by default) decoded by its mark; anything else
// must be valid UTF-8. A mark is the only evidence read, as the TUI's File >
// Open does (internal/tui/text_encoding.go): a guessed encoding would run a
// script the caller never wrote. A leading U+FEFF would otherwise reach the
// server and fail as "Incorrect syntax near" an invisible character.
func decodeScript(data []byte) (string, bool) {
	var order func([]byte) uint16
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		order = func(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		order = func(b []byte) uint16 { return uint16(b[1]) | uint16(b[0])<<8 }
	}
	if order == nil {
		return string(data), utf8.Valid(data)
	}
	data = data[2:]
	if len(data)%2 != 0 {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(data))
	for i := 0; i < len(data); i += 2 {
		r := rune(order(data[i:]))
		if utf16.IsSurrogate(r) {
			// A pair, or the file is not UTF-16: an unpaired surrogate would
			// become U+FFFD, a silent change to the script.
			if i+3 >= len(data) {
				return "", false
			}
			if r = utf16.DecodeRune(r, rune(order(data[i+2:]))); r == utf8.RuneError {
				return "", false
			}
			i += 2
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

func usageError(msg, hint string) *output.Error {
	return output.NewError(output.CodeUsageError, msg, hint)
}

func (d Deps) queryClassify(_ context.Context, inv *spec.Invocation) (*output.Result, error) {
	script, err := readSQL(inv)
	if err != nil {
		return nil, err
	}
	sc := safety.Classify(script)
	if !inv.Given[optAllow] {
		return classification(sc, "", nil), nil
	}
	allow := spec.Class(inv.Values[optAllow].(string))
	blocked := safety.Check(sc, safety.Policy{Allow: allow})
	res := classification(sc, allow, blocked)
	if blocked != nil {
		return res, blockedError(blocked)
	}
	return res, nil
}

// classification is query classify's answer, which query run --dry-run gives
// too: the summary record, with allowedClass when a check ran and statements
// when it refused, and one row per statement.
func classification(sc *safety.Script, allowed spec.Class, blocked *safety.Blocked) *output.Result {
	res := &output.Result{Fields: []output.Field{{Key: "detectedClass", Value: string(sc.Class)}}}
	if allowed != "" {
		res.Fields = append(res.Fields, output.Field{Key: "allowedClass", Value: string(allowed)})
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "flags", Value: strings.Join(sc.Flags.Names(), ", ")},
		output.Field{Key: "statementCount", Value: strconv.Itoa(len(sc.Statements))})
	res.Sets = []output.Set{statementSet(sc.Statements)}
	if blocked != nil {
		res.Fields = append(res.Fields, output.Field{Key: "statements", Value: blocked.Describe()})
	}
	return res
}

// blockedResult is a refused run's answer: the record fields that name what
// was refused (DETECTED_CLASS, ALLOWED_CLASS, STATEMENTS) and the BLOCKED
// error.
func blockedResult(b *safety.Blocked) (*output.Result, error) {
	return &output.Result{Fields: []output.Field{
		{Key: "detectedClass", Value: string(b.Detected)},
		{Key: "allowedClass", Value: string(b.Allowed)},
		{Key: "statements", Value: b.Describe()},
	}}, blockedError(b)
}

// statementSet is the classification as a result set, one row per statement.
func statementSet(stmts []safety.Statement) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "batch", Type: "int"}, {Name: "repeat", Type: "int"}, {Name: "line", Type: "int"},
		{Name: "class"}, {Name: "leader"}, {Name: "object", Nullable: true}, {Name: "reason", Nullable: true},
		{Name: "flags"},
	}}
	orNull := func(s string) *string {
		if s == "" {
			return nil
		}
		return ptr(s)
	}
	for _, st := range stmts {
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(st.Batch)), ptr(strconv.Itoa(st.Repeat)), ptr(strconv.Itoa(st.Line)),
			ptr(string(st.Class)), ptr(st.Leader), orNull(st.Object), orNull(st.Reason),
			ptr(strings.Join(st.Flags.Names(), ", ")),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// blockedError is a refusal as the error Main reports: BLOCKED, exit 7.
func blockedError(b *safety.Blocked) *output.Error {
	return output.NewError(output.CodeBlocked, b.Message, b.Hint)
}

// Session is the query session a query command runs on: *query.Session, or a
// test's stand-in.
type Session interface {
	Execute(ctx context.Context, script string, opts ...query.Option) *query.Result
	ExecuteToSink(ctx context.Context, script string, sink query.RowSink, opts ...query.Option) *query.Result
	EndTransactions(ctx context.Context, commit bool) error
	Close()
}

// OpenQuerySession is Deps.OpenSession: a query.Session on sc's pool, in
// database when it is not empty.
func OpenQuerySession(ctx context.Context, sc *db.ServerConn, database string) (Session, error) {
	s, _, err := query.Open(ctx, sc.Server.DB(), database)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// runSettings is query run's execution options, checked.
type runSettings struct {
	allow           spec.Class
	dryRun          bool
	transaction     string // tranAuto, tranCommit, tranRollback
	maxRowsAffected int64  // when limited
	limited         bool   // --max-rows-affected given
	continueOnError bool
	timeout         time.Duration // 0: none
	statistics      []string      // "IO", "TIME"
}

// wrapped reports whether the script runs inside one transaction the command
// ends itself.
func (r runSettings) wrapped() bool { return r.transaction != tranAuto || r.limited }

func readRunSettings(inv *spec.Invocation) (runSettings, error) {
	r := runSettings{
		allow:           spec.Class(inv.Values[optAllow].(string)),
		dryRun:          inv.Values[optDryRun].(bool),
		transaction:     inv.Values[optTransaction].(string),
		limited:         inv.Given[optMaxRowsAffected],
		continueOnError: inv.Values[optContinueOnError].(bool),
	}
	if r.limited {
		n := inv.Values[optMaxRowsAffected].(int)
		if n < 0 {
			return r, usageError("--max-rows-affected: must be 0 or more", "")
		}
		r.maxRowsAffected = int64(n)
	}
	// Under SET XACT_ABORT ON an error rolls the wrapping transaction back and
	// ends its batch, so a later batch would run in autocommit: a transaction
	// and --continue-on-error cannot both hold.
	if r.wrapped() && r.continueOnError {
		return r, usageError("--continue-on-error cannot run inside a transaction",
			"A failed batch rolls the transaction back, and later batches would then commit on their own. "+
				"Drop --continue-on-error, or run without --transaction and --max-rows-affected.")
	}
	var err error
	if r.timeout, err = readTimeout(inv); err != nil {
		return r, err
	}
	for _, s := range inv.Values[optStatistics].([]string) {
		switch v := strings.ToUpper(s); v {
		case "IO", "TIME":
			if !slices.Contains(r.statistics, v) {
				r.statistics = append(r.statistics, v)
			}
		default:
			return r, usageError(fmt.Sprintf("--statistics: %q is not io or time", s), "Give --statistics io,time or either one.")
		}
	}
	return r, nil
}

func readTimeout(inv *spec.Invocation) (time.Duration, error) {
	secs := inv.Values[optTimeout].(int)
	if secs < 0 {
		return 0, usageError("--timeout: must be 0 or more", "0 means no limit.")
	}
	return time.Duration(secs) * time.Second, nil
}

// profileCeiling is the configuration and the profile's ceiling for the
// --profile given; an ad-hoc call reads no configuration and has no ceiling.
func (d Deps) profileCeiling(inv *spec.Invocation) (*config.Config, string, spec.Class, error) {
	name := inv.Values[optProfile].(string)
	if name == "" {
		return &config.Config{}, "", "", nil
	}
	cfg, err := d.load()
	if err != nil {
		return nil, "", "", err
	}
	c, lerr := cfg.ProfileByName(name)
	if lerr != nil {
		return nil, "", "", connect.ProfileNotFound(cfg, name)
	}
	return cfg, name, spec.Class(c.Ceiling()), nil
}

// connected is a command's open connection and query session.
type connected struct {
	target *connect.Target
	conn   *db.ServerConn
	sess   Session
}

func (c *connected) close() {
	if c.sess != nil {
		c.sess.Close()
	}
	c.conn.Close()
}

// connectSession connects as inv asks and opens the query session; res's
// header names the target from the start, so a failure says where it failed.
func (d Deps) connectSession(ctx context.Context, inv *spec.Invocation, cfg *config.Config, profile string, res *output.Result) (*connected, error) {
	t, sc, err := d.dial(ctx, inv, cfg, profile, res)
	if err != nil {
		return nil, err
	}
	c := &connected{target: t, conn: sc}
	if c.sess, err = d.OpenSession(ctx, sc, t.Conn.Database); err != nil {
		c.close()
		return nil, connect.Classify(err, false, t)
	}
	return c, nil
}

// withTimeout bounds ctx by d; 0 is no bound.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func timeoutError(d time.Duration) *output.Error {
	return output.NewError(output.CodeTimeout, fmt.Sprintf("the script ran longer than --timeout %d s and was cancelled", int(d/time.Second)),
		"Raise --timeout (0 is no limit), or narrow the query.")
}

func (d Deps) queryRun(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	script, err := readSQL(inv)
	if err != nil {
		return nil, err
	}
	run, err := readRunSettings(inv)
	if err != nil {
		return nil, err
	}
	// What is sent: the script, or the script inside sp_executesql when it
	// has parameters. The classifier reads the script either way.
	send := script
	if params, err := readParams(inv.Values[optParam].([]string)); err != nil {
		return nil, err
	} else if params != nil {
		if send, err = parameterize(script, params); err != nil {
			return nil, err
		}
	}
	cfg, profile, ceiling, err := d.profileCeiling(inv)
	if err != nil {
		return nil, err
	}
	// The check runs before anything connects: a refused script never reaches
	// the server, and --dry-run answers the same refusal.
	sc := safety.Classify(script)
	policy := safety.Policy{Allow: run.allow, Ceiling: ceiling, Transaction: run.wrapped(), MaxRowsAffected: run.limited}
	blocked := safety.Check(sc, policy)
	if run.dryRun {
		res := classification(sc, policy.Allowed(), blocked)
		if blocked != nil {
			return res, blockedError(blocked)
		}
		return res, nil
	}
	if blocked != nil {
		return blockedResult(blocked)
	}

	res := &output.Result{}
	c, err := d.connectSession(ctx, inv, cfg, profile, res)
	if err != nil {
		return res, err
	}
	defer c.close()

	rctx, cancel := withTimeout(ctx, run.timeout)
	defer cancel()
	timedOut := func() bool { return ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) }

	var setup []string
	if len(run.statistics) > 0 {
		setup = append(setup, "SET STATISTICS "+strings.Join(run.statistics, ", ")+" ON;")
	}
	if run.wrapped() {
		setup = append(setup, "SET XACT_ABORT ON; BEGIN TRANSACTION;")
	}
	if len(setup) > 0 {
		pre := c.sess.Execute(rctx, strings.Join(setup, " "))
		if pre.HasErrors() {
			collect(res, pre, rctx.Err() != nil)
			if timedOut() {
				res.Errors = append([]*output.Error{timeoutError(run.timeout)}, res.Errors...)
			}
			return res, nil
		}
	}

	opts := []query.Option{query.WithoutSinkNotices()}
	if !run.continueOnError {
		opts = append(opts, query.WithStopOnError())
	}
	qr := c.sess.ExecuteToSink(rctx, send, inv.Sink, opts...)
	collect(res, qr, rctx.Err() != nil)
	if qr.Database != "" {
		res.Database = qr.Database
	}

	switch {
	case ctx.Err() != nil:
		// Main reports CANCELLED; closing the session (deferred) makes the
		// server roll back an open transaction.
		if run.wrapped() {
			res.Fields = append(res.Fields, output.Field{Key: "transaction", Value: outcomeRolledBack})
		}
		return res, nil
	case timedOut():
		res.Errors = append([]*output.Error{timeoutError(run.timeout)}, res.Errors...)
		if run.wrapped() {
			res.Fields = append(res.Fields, output.Field{Key: "transaction", Value: outcomeRolledBack})
		}
		return res, nil
	}
	if !qr.SessionLost {
		if run.continueOnError && qr.FailedBatches > 0 && qr.FailedBatches < qr.Batches {
			res.Errors = append([]*output.Error{output.NewError(output.CodePartial,
				fmt.Sprintf("%d of %d batches failed", qr.FailedBatches, qr.Batches),
				"The batches that succeeded kept their changes; see errors for the failures.")}, res.Errors...)
		}
		if total := batchCount(send); qr.StoppedOnError && qr.Batches < total {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"The run stopped after the failing batch: %d of %d batches were sent, later ones did not run.", qr.Batches, total))
		}
	}
	if run.wrapped() {
		d.endTransaction(ctx, c.sess, qr, run, res)
	}
	return res, nil
}

// batchCount is the batches script sends, each repetition of a GO n counted.
func batchCount(script string) int {
	n := 0
	for _, b := range sqltext.SplitBatches(script) {
		n += b.Count
	}
	return n
}

// endTransaction ends a wrapped run's transaction by its outcome and records
// that outcome in res: rolled back unless the run asked to commit, every batch
// succeeded and the rows changed stayed within --max-rows-affected.
//
// The script cannot control the transaction itself (safety.Check refuses
// it), but a procedure it calls can: its COMMIT ends the wrapper (Msg 266 as
// it returns), and what follows runs in autocommit. That is found after the
// fact — @@TRANCOUNT 0 — and reported as an error, since changes may have been
// committed (docs/cli.md § Transactions).
func (d Deps) endTransaction(ctx context.Context, sess Session, qr *query.Result, run runSettings, res *output.Result) {
	failed := len(res.Errors) > 0
	outcome := func(v string) { res.Fields = append(res.Fields, output.Field{Key: "transaction", Value: v}) }
	if run.limited {
		res.Fields = append(res.Fields, output.Field{Key: "maxRowsAffected", Value: strconv.FormatInt(run.maxRowsAffected, 10)})
	}
	if qr.SessionLost || qr.State == nil {
		// The server rolls back a transaction whose connection is gone, and
		// closing the session (deferred) makes it so.
		if !qr.SessionLost {
			res.Errors = append(res.Errors, output.NewError(output.CodeGeneralError,
				"the session's transaction count could not be read after the run; the transaction was rolled back", ""))
		}
		outcome(outcomeRolledBack)
		return
	}
	if qr.State.TranCount == 0 {
		// XACT_ABORT rolled the wrapper back on an error — unless the script
		// ended it itself, which a clean run, or Msg 266, gives away.
		if !failed || hasSQLError(res, 266) {
			e := output.NewError(output.CodeSQLError,
				"the script ended the wrapping transaction, so its changes may be committed",
				"A procedure or dynamic SQL in the script committed or rolled back. --transaction covers the script's "+
					"own statements, not what a procedure does with the transaction; check the data.")
			res.Errors = append([]*output.Error{e}, res.Errors...)
			outcome(outcomeEndedByScript)
			return
		}
		outcome(outcomeRolledBack)
		return
	}

	commit := !failed && run.transaction != tranRollback
	if run.limited && res.RowsAffected != nil && *res.RowsAffected > run.maxRowsAffected {
		commit = false
		e := output.NewError(output.CodeBlocked,
			fmt.Sprintf("%d rows affected, over --max-rows-affected %d; the transaction was rolled back", *res.RowsAffected, run.maxRowsAffected),
			"Check the WHERE clause with --transaction rollback, or raise --max-rows-affected if the change is intended.")
		if failed {
			res.Errors = append(res.Errors, e)
		} else {
			res.Errors = append([]*output.Error{e}, res.Errors...)
		}
	}
	if err := sess.EndTransactions(ctx, commit); err != nil {
		code := output.CodeGeneralError
		if _, ok := gosmo.AsSQLError(err); ok {
			code = output.CodeSQLError
		}
		verb := "roll back"
		if commit {
			verb = "commit"
		}
		res.Errors = append(res.Errors, output.NewError(code, fmt.Sprintf("could not %s the transaction: %v", verb, err),
			"The session is closed, so the server rolls back what is still open."))
		outcome(outcomeRolledBack)
		return
	}
	if commit {
		outcome(outcomeCommitted)
	} else {
		outcome(outcomeRolledBack)
	}
}

func hasSQLError(res *output.Result, number int32) bool {
	return slices.ContainsFunc(res.Errors, func(e *output.Error) bool { return e.SQL != nil && e.SQL.Number == number })
}

// collect moves qr's messages into res: SQL Server errors (severity 11 and
// up) to Errors, each with its number, severity, state, line and procedure;
// everything else to Messages. A DML statement's count adds to RowsAffected; a
// count after a result set (a SELECT's) does not. cancelled drops the
// executor's own cancel notice and errors: the caller reports the cancel or
// timeout itself.
func collect(res *output.Result, qr *query.Result, cancelled bool) {
	var errText string // the text line that follows an error's header
	for _, m := range qr.Messages {
		switch {
		case m.SQL != nil && m.SQL.IsError():
			res.Errors = append(res.Errors, sqlError(m.SQL))
			errText = m.SQL.Message
		case m.SQL != nil:
			// A header below severity 11 is information, its text the next line.
			errText = ""
			res.Messages = append(res.Messages, output.Message{Type: "info", Text: m.Text})
		case m.IsError && errText != "" && m.Text == errText:
			errText = ""
		case m.IsError:
			if !cancelled {
				res.Errors = append(res.Errors, output.NewError(output.CodeGeneralError, m.Text, ""))
			}
		default:
			if rc := m.RowsAffected; rc != nil && !rc.AfterSet {
				n := rc.N
				if res.RowsAffected != nil {
					n += *res.RowsAffected
				}
				res.RowsAffected = &n
			}
			res.Messages = append(res.Messages, output.Message{Type: "info", Text: m.Text})
		}
	}
	if qr.SessionLost && !cancelled {
		// The connection broke mid-run (D11: CONNECTION_FAILED, exit 3); the
		// driver's error, when there is one, says how.
		lost := output.NewError(output.CodeConnectionFailed, query.ErrSessionLost.Error(), "Run the script again; nothing after the failure ran.")
		for i, e := range res.Errors {
			if e.Code == output.CodeGeneralError {
				lost.Message += ": " + e.Message
				res.Errors = slices.Delete(res.Errors, i, i+1)
				break
			}
		}
		res.Errors = append([]*output.Error{lost}, res.Errors...)
	}
}

// sqlError is a SQL Server error as the envelope reports it: SQL_ERROR, or
// PERMISSION_DENIED when the server says the login lacks a permission (D11).
// A missing object stays SQL_ERROR: NOT_FOUND is for what the command line
// names.
func sqlError(se *gosmo.SQLError) *output.Error {
	code, hint := output.CodeSQLError, ""
	// gosmo classifies the driver's error; the message kept only its fields.
	if gosmo.IsPermissionDenied(mssql.Error{Number: se.Number, State: se.State, Class: se.Class, Message: se.Message}) {
		code = output.CodePermissionDenied
		hint = "The login lacks a permission the script needs; the classifier is not the boundary, the login's permissions are."
	}
	e := output.NewError(code, se.Message, hint)
	e.SQL = &output.SQLError{Number: se.Number, Severity: se.Class, State: se.State, Line: int(se.LineNo), Procedure: se.ProcName}
	return e
}

// parseOnly switches the session to syntax checking: the server parses each
// batch and runs nothing.
const (
	parseOnlyOn  = "SET PARSEONLY ON"
	parseOnlyOff = "SET PARSEONLY OFF"
)

func (d Deps) queryParse(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	script, err := readSQL(inv)
	if err != nil {
		return nil, err
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	// Nothing runs, so the class is not checked; but a SET PARSEONLY OFF (or
	// NOEXEC, SHOWPLAN_*) in the script would switch the mode off mid-script,
	// and the statements after it would run. Any mention is refused.
	sc := safety.Classify(script)
	if lines := safety.ModeWords(script); len(lines) > 0 {
		parts := make([]string, len(lines))
		for i, l := range lines {
			parts[i] = "line " + strconv.Itoa(l)
		}
		return &output.Result{Fields: []output.Field{
			{Key: "detectedClass", Value: string(sc.Class)},
			{Key: "statements", Value: strings.Join(parts, "; ")},
		}}, output.NewError(output.CodeBlocked,
			"the script sets PARSEONLY, NOEXEC or SHOWPLAN_*, which query parse runs under itself",
			"Remove those SET statements; query parse needs none.")
	}
	cfg, profile, _, err := d.profileCeiling(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	c, err := d.connectSession(ctx, inv, cfg, profile, res)
	if err != nil {
		return res, err
	}
	defer c.close()

	rctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	if on := c.sess.Execute(rctx, parseOnlyOn); on.HasErrors() {
		collect(res, on, rctx.Err() != nil)
		return res, nil
	}
	// Every batch, so every syntax error is reported in one call.
	qr := c.sess.ExecuteToSink(rctx, script, inv.Sink, query.WithoutSinkNotices())
	// The session is closed next anyway; switching back is for tidiness, and
	// a failure changes nothing the caller sees.
	off, offCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	c.sess.Execute(off, parseOnlyOff)
	offCancel()

	collect(res, qr, rctx.Err() != nil)
	if ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
		res.Errors = append([]*output.Error{timeoutError(timeout)}, res.Errors...)
	}
	res.Fields = []output.Field{
		{Key: "valid", Value: strconv.FormatBool(len(res.Errors) == 0)},
		{Key: "batchCount", Value: strconv.Itoa(qr.Batches)},
		{Key: "detectedClass", Value: string(sc.Class)},
	}
	return res, nil
}
