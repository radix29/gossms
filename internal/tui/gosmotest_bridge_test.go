package tui

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/propsheet"
)

// A bridge from the fakeResponse scripts the page fixtures are written in to a
// gosmotest server (plan step 18). A family's fixtures keep their shape while
// its tests stop depending on fakedb's driver: newGConn answers a scripted
// fakeResponse from gosmo's own fake, and gFake carries the recorders the
// tests read back. A fakeResponse whose db/arg/err/block fields are set keeps
// fakedb's meaning for them. Unlike fakedb, a gosmotest server fails the test
// on a query nothing answers, so loadPageG needs no unscripted-query report.

// gFake is a gosmotest server with read-back helpers for statements and args.
type gFake struct {
	*gosmotest.Fake
	mu        sync.Mutex
	responses []fakeResponse
}

// Rescript replaces the scripted responses, as fakeInstance's responses field
// was reassigned: the next query sees the new script, first match wins.
func (f *gFake) Rescript(responses []fakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = responses
}

// find is the first scripted response matching q.
func (f *gFake) find(q gosmotest.Query) (fakeResponse, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.responses {
		if (r.db == "" || r.db == q.Database) &&
			(r.arg == "" || gHasArg(q.Args, r.arg)) &&
			strings.Contains(q.Text, r.match) {
			return r, true
		}
	}
	return fakeResponse{}, false
}

// newGConn is newFakeConn over gosmotest: a *db.ServerConn answering the
// scripted responses, first match wins.
func newGConn(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	return newGConnWith(t, nil, responses...)
}

// newGConnWith is newGConn with info adjusting the server's identity.
func newGConnWith(t *testing.T, info func(*gosmotest.Info), responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	f := gosmotest.New(t)
	// fakedb's server identity, so a family's paths and platform keep meaning
	// what its fixtures assumed.
	f.Info.Name, f.Info.ProductVersion = `FAKE\SQL`, "16.0.4085.2"
	f.Info.Version = "Microsoft SQL Server 2022 ... on Windows"
	f.Info.DataPath, f.Info.LogPath, f.Info.BackupPath = `C:\Data`, `C:\Log`, `C:\Backup`
	if info != nil {
		info(&f.Info)
	}
	g := &gFake{Fake: f, responses: responses}
	f.OnFunc(func(q gosmotest.Query) bool { _, ok := g.find(q); return ok },
		func(q gosmotest.Query) gosmotest.Result {
			r, _ := g.find(q)
			return gResponder(r)(q)
		})
	// An unscripted database-access probe: fakedb answered it empty, which the
	// page loads treat as "not known to be denied".
	f.OnFunc(func(q gosmotest.Query) bool { return strings.Contains(q.Text, "HAS_DBACCESS") },
		gResponder(fakeResponse{match: "HAS_DBACCESS"}))
	// gosmo's live default-paths read (Server.DefaultPaths): fakedb answered it
	// from the same row as the connect read; here it comes from Info, so a test
	// that blanks Info.DataPath sees the instance report no directory.
	f.OnFunc(func(q gosmotest.Query) bool {
		return strings.Contains(q.Text, "SERVERPROPERTY('InstanceDefaultBackupPath')") &&
			!strings.Contains(q.Text, "SERVERPROPERTY('ServerName')")
	}, func(gosmotest.Query) gosmotest.Result {
		var row []any
		for _, p := range []string{f.Info.DataPath, f.Info.LogPath, f.Info.BackupPath} {
			if p == "" {
				row = append(row, nil)
			} else {
				row = append(row, p)
			}
		}
		return gosmotest.Sets(gosmotest.Set{Columns: []string{"", "", ""}, Rows: [][]any{row}})(gosmotest.Query{})
	})
	return &db.ServerConn{Server: f.Server()}, g
}

// newGConnOnAzure is newGConn for an Azure engine edition (5 SQL Database,
// 8 Managed Instance), as fakedb's newFakeConnOnAzure* were.
func newGConnOnAzure(t *testing.T, edition int, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	return newGConnWith(t, func(i *gosmotest.Info) {
		i.Edition, i.ProductVersion, i.EngineEdition = "SQL Azure", "12.0.2000.8", edition
		i.Version = "Microsoft SQL Azure (RTM) - 12.0.2000.8 ..."
	}, responses...)
}

// newGConnAtVersion is newGConn for an instance reporting a given product
// version, as fakedb's newFakeConnAtVersion was.
func newGConnAtVersion(t *testing.T, version string, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	return newGConnWith(t, func(i *gosmotest.Info) { i.ProductVersion = version }, responses...)
}

// newGConnOnAzureMI is newGConn for an Azure SQL Managed Instance: the values
// a real one answers with (t-qmi-01, 2026-09-08) — EngineEdition 8, the frozen
// 12.0.2000.8 ProductVersion, and a @@VERSION naming no host OS.
func newGConnOnAzureMI(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	return newGConnOnAzure(t, 8, responses...)
}

// newGConnOnLinux is newGConn for an instance whose @@VERSION says Linux.
func newGConnOnLinux(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	return newGConnWith(t, func(i *gosmotest.Info) { i.Version = "Microsoft SQL Server 2025 ... on Linux" }, responses...)
}

// newGConnWithoutSysInfo is newGConn for a login that cannot read
// sys.dm_os_sys_info (no VIEW SERVER STATE): that read fails, the
// SERVERPROPERTY half still answers.
func newGConnWithoutSysInfo(t *testing.T, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	denied := fakeResponse{match: "sys.dm_os_sys_info", err: errors.New("VIEW SERVER STATE permission was denied")}
	return newGConn(t, append([]fakeResponse{denied}, responses...)...)
}

// ReadArgsOf is the arguments of the first read whose text contains want and
// whose first argument is first, nil if none.
func (f *gFake) ReadArgsOf(want, first string) []any {
	for _, q := range f.Queries() {
		if !q.Exec && strings.Contains(q.Text, want) && len(q.Args) > 0 && q.Args[0] == first {
			return q.Args
		}
	}
	return nil
}

func gHasArg(args []any, want string) bool {
	for _, a := range args {
		switch v := a.(type) {
		case string:
			if v == want {
				return true
			}
		case int64:
			if strconv.FormatInt(v, 10) == want {
				return true
			}
		}
	}
	return false
}

func gResponder(r fakeResponse) gosmotest.Responder {
	var resp gosmotest.Responder
	switch {
	case r.err != nil:
		resp = func(gosmotest.Query) gosmotest.Result {
			return gosmotest.Result{Err: r.err, DropConn: r.dropsConn}
		}
	default:
		cols := r.colNames
		if len(cols) != r.cols {
			cols = make([]string, r.cols)
		}
		set := gosmotest.Set{Columns: cols, Rows: make([][]any, len(r.rows))}
		for i, row := range r.rows {
			set.Rows[i] = make([]any, len(row))
			for j, v := range row {
				set.Rows[i][j] = v
			}
		}
		resp = gosmotest.Sets(set)
	}
	if r.block != nil {
		resp = gosmotest.Hold(r.block, resp)
	}
	if r.blockExec != nil {
		resp = gosmotest.Hold(r.blockExec, resp)
	}
	return resp
}

// StatementsIn is the writes that ran in db, each bound (Query.Bound).
func (f *gFake) StatementsIn(db string) []string {
	var out []string
	for _, q := range f.Queries() {
		if q.Exec && q.Database == db && !bareUSE.MatchString(strings.TrimSpace(q.Text)) {
			out = append(out, q.Bound())
		}
	}
	return out
}

// Reads is every read (not a write) whose text contains want.
func (f *gFake) Reads(want string) []string {
	var out []string
	for _, q := range f.Queries() {
		if !q.Exec && strings.Contains(q.Text, want) {
			out = append(out, q.Text)
		}
	}
	return out
}

// QueryCount is the reads the server has seen — fakeInstance.QueryCount counted
// no writes, and a test comparing counts around a write relies on that.
func (f *gFake) QueryCount() int {
	n := 0
	for _, q := range f.Queries() {
		if !q.Exec {
			n++
		}
	}
	return n
}

// Statements is every write, wherever it ran.
func (f *gFake) Statements() []string {
	var out []string
	for _, q := range f.Queries() {
		if q.Exec && !bareUSE.MatchString(strings.TrimSpace(q.Text)) {
			out = append(out, q.Bound())
		}
	}
	return out
}

// loadPageG is loadPage over a gosmotest server.
func loadPageG(t *testing.T, page propPage, _ *gFake) (*propsheet.Form, propApply) {
	t.Helper()
	form, apply, err := page.load(context.Background())
	if err != nil {
		t.Fatalf("page %q load: %v", page.title, err)
	}
	return form, hostApply(form, apply)
}

// gEmpty answers each match with no rows — what fakedb did for a read nothing
// scripted, which a gosmotest server fails the test on instead.
func gEmpty(matches ...string) []fakeResponse {
	out := make([]fakeResponse, len(matches))
	for i, m := range matches {
		out[i] = fakeResponse{match: m}
	}
	return out
}

// ReadArgs is the arguments of the first read whose text contains want, and
// whether there was one.
func (f *gFake) ReadArgs(want string) ([]any, bool) {
	for _, q := range f.Queries() {
		if !q.Exec && strings.Contains(q.Text, want) {
			return q.Args, true
		}
	}
	return nil, false
}

// LastReadArgs is the arguments of the last read whose text contains want, nil
// if none (ReadArgs is the first).
func (f *gFake) LastReadArgs(want string) []any {
	var last []any
	for _, q := range f.Queries() {
		if !q.Exec && strings.Contains(q.Text, want) {
			last = q.Args
		}
	}
	return last
}

// ReadContext is the context of the first read whose text contains want, and
// whether there was one.
func (f *gFake) ReadContext(want string) (context.Context, bool) {
	for _, q := range f.Queries() {
		if !q.Exec && strings.Contains(q.Text, want) {
			return q.Ctx, true
		}
	}
	return nil, false
}

// ExecArgs is the arguments of the first write whose text contains want, and
// whether there was one.
func (f *gFake) ExecArgs(want string) ([]any, bool) {
	for _, q := range f.Queries() {
		if q.Exec && strings.Contains(q.Text, want) {
			return q.Args, true
		}
	}
	return nil, false
}

// StatementsInTx is the writes that ran inside a transaction, each bound —
// fakeInstance.StatementsInTx. TxLog is gosmotest's own.
func (f *gFake) StatementsInTx() []string {
	var out []string
	for _, q := range f.Queries() {
		if q.Exec && q.Tx != 0 && !bareUSE.MatchString(strings.TrimSpace(q.Text)) {
			out = append(out, q.Bound())
		}
	}
	return out
}

// newGConnWithMsdb is fakeConnWithMsdb over gosmotest: a server whose
// collation is the opposite of msdb's, with msdb's read answered first.
func newGConnWithMsdb(t *testing.T, msdb string, responses ...fakeResponse) (*db.ServerConn, *gFake) {
	t.Helper()
	return newGConnWith(t, func(i *gosmotest.Info) { i.Collation = msdbOpposite(msdb) },
		append([]fakeResponse{msdbResponse(msdb)}, responses...)...)
}

// bareUSE matches the standalone "USE [db]" gosmo issues on a transaction's
// connection ahead of a database-scoped call. It anchors to the end on
// purpose: several server-scoped writes begin a batch with "USE master;"
// because SQL Server only accepts them from master, and dropping those would
// hide the whole statement rather than the plumbing in front of it.
var bareUSE = regexp.MustCompile(`^USE \[([^\]]*)\]$`)
