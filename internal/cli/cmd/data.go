package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// The data commands' options.
const (
	optRows        = "rows"
	optColumns     = "columns"
	optWhere       = "where"
	optOrderBy     = "order-by"
	optApproximate = "approximate"
	optTopValues   = "top-values"
)

// defaultDataRows is data top's --rows default.
const defaultDataRows = 100

// dataObjectArg is the table or view a data command reads.
var dataObjectArg = spec.Arg{Name: "object", Help: "The table or view: schema.name, or a name alone when one schema has it",
	Format: "schema.name", Required: true}

func rowsOption(help string) spec.Option {
	return spec.Option{Name: optRows, Type: spec.TypeInt, Default: strconv.Itoa(defaultDataRows), Help: help}
}

func columnsOption(help string) spec.Option {
	return spec.Option{Name: optColumns, Type: spec.TypeString, Help: help}
}

func whereOption() spec.Option {
	return spec.Option{Name: optWhere, Type: spec.TypeString,
		Help: "A T-SQL search condition, without WHERE; it must leave the statement one SELECT"}
}

// DataCommands is the data group: its node and its commands.
func DataCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"data"},
			Summary: "Read a table's or view's rows, export them, load a file into a table",
			Description: "data import bulk-loads a file into a table; see its help. Every other command composes one SELECT over a table or view and runs it as query run would, so cells " +
				"are the grid's text, JSON types them, and --max-rows and --out-file apply. --where and --order-by are " +
				"T-SQL fragments spliced into that SELECT: the composed statement must still be one read statement, " +
				"or the call is BLOCKED (exit 7) before connecting — a ';', a second statement, GO, a procedure call " +
				"or dynamic SQL cannot slip in. The login's permissions remain the boundary: it needs SELECT on the object.",
			Class: spec.ClassRead,
		},
		{
			Path:    []string{"data", "top"},
			Summary: "Select the first rows of a table or view (Select Top N Rows)",
			Description: "SELECT TOP (--rows) of the object, as Object Explorer's Select Top 1000 Rows, with every column " +
				"(*) unless --columns names some. --columns is a comma-separated list of column names, bracketed where " +
				"a name needs it ([Order Date]); each is checked against the object's columns (exit 8 naming close " +
				"matches), never spliced as written. Without --order-by the order is whatever the server returns. " +
				"A record — object, type, tsql (the SELECT sent) — then the rows; --max-rows still cuts what stdout " +
				"shows, so --rows above it needs --max-rows 0 or --out-file.",
			Args: []spec.Arg{dataObjectArg},
			Options: append(ConnectOptions(),
				rowsOption("The rows to select: TOP (n); 0 returns the columns alone"),
				columnsOption("The columns to select, comma-separated, in that order: a,[b c]; default every column"),
				whereOption(),
				spec.Option{Name: optOrderBy, Type: spec.TypeString,
					Help: "A T-SQL ORDER BY list, without ORDER BY: CreatedAt DESC, id"},
				timeoutOption(),
			),
			Class: spec.ClassRead,
			Run:   d.dataTop,
			Examples: []string{
				"gossms data top -p dev Sales.Customer",
				"gossms data top -p dev Sales.SalesOrderHeader --rows 10 --order-by OrderDate -o json",
				"gossms data top -p dev Sales.Customer --columns CustomerID,AccountNumber --where TerritoryID=4",
			},
		},
		{
			Path:    []string{"data", "sample"},
			Summary: "Select random rows of a table or view",
			Description: "SELECT TOP (--rows) of the object ORDER BY NEWID(): a uniformly random sample, different on " +
				"every call. It is exact, not TABLESAMPLE's page sample, so it reads every row --where keeps (a full " +
				"scan of a large table) and sorts them; data top is the cheap look. --columns and --where are data " +
				"top's. A record — object, type, tsql — then the rows.",
			Args: []spec.Arg{dataObjectArg},
			Options: append(ConnectOptions(),
				rowsOption("The rows to sample: TOP (n); 0 returns the columns alone"),
				columnsOption("The columns to select, comma-separated, in that order: a,[b c]; default every column"),
				whereOption(),
				timeoutOption(),
			),
			Class: spec.ClassRead,
			Run:   d.dataSample,
			Examples: []string{
				"gossms data sample -p dev Sales.Customer --rows 20",
				"gossms data sample -p dev Sales.SalesOrderHeader --where Status=5 -o json",
			},
		},
		dataProfileCommand(d),
		{
			Path:    []string{"data", "count"},
			Summary: "Count a table's or view's rows, exactly or from partition statistics",
			Description: "SELECT COUNT_BIG(*) of the object, with --where when given: exact, and it reads the table " +
				"(or the view's query). --approximate reads the table's partition statistics instead, as Object " +
				"Explorer's row count does: no scan, and no --where; a view has none (exit 2). A record: object, " +
				"type, rows, method (exact or approximate), and tsql for an exact count.",
			Args: []spec.Arg{dataObjectArg},
			Options: append(ConnectOptions(),
				whereOption(),
				spec.Option{Name: optApproximate, Type: spec.TypeBool,
					Help: "Read the row count from partition statistics: no scan, a table only, no --where"},
				timeoutOption(),
			),
			Class: spec.ClassRead,
			Run:   d.dataCount,
			Examples: []string{
				"gossms data count -p dev Sales.SalesOrderDetail",
				"gossms data count -p dev Sales.SalesOrderDetail --approximate",
				"gossms data count -p dev Sales.Customer --where TerritoryID=4 -o json",
			},
		},
		dataExportCommand(d),
		dataImportCommand(d),
	}
}

// dataSource is a data command's resolved object on its open connection.
type dataSource struct {
	target *connect.Target
	conn   *db.ServerConn
	cat    Catalog
	dbase  *gosmo.Database
	obj    *gosmo.ObjectInfo
	kind   string // kindTable or kindView
}

// name is the object as the composed SELECT names it: [schema].[name].
func (s *dataSource) name() string { return gosmo.QualifiedName(s.obj.Schema, s.obj.Name) }

// fields is the record every data command starts with.
func (s *dataSource) fields() []output.Field {
	return []output.Field{{Key: "object", Value: s.name()}, {Key: "type", Value: s.kind}}
}

// openDataSource connects, resolves inv's object argument in the
// connection's database (D2) and checks it is a table or a view. The caller
// closes conn.
func (d Deps) openDataSource(ctx context.Context, inv *spec.Invocation, res *output.Result) (*dataSource, error) {
	cfg, profile, _, err := d.profileCeiling(inv)
	if err != nil {
		return nil, err
	}
	t, sc, err := d.dial(ctx, inv, cfg, profile, res)
	if err != nil {
		return nil, err
	}
	s := &dataSource{target: t, conn: sc, cat: d.Catalog(sc)}
	if err := s.resolve(ctx, inv.Args[0], res); err != nil {
		sc.Close()
		return nil, err
	}
	return s, nil
}

func (s *dataSource) resolve(ctx context.Context, arg string, res *output.Result) error {
	var err error
	if s.dbase, err = currentDatabase(ctx, s.cat, res); err != nil {
		return err
	}
	n, err := parseObjectName(arg, s.dbase.Name)
	if err != nil {
		return err
	}
	if s.obj, err = resolveObject(ctx, s.cat, s.dbase, n); err != nil {
		return err
	}
	switch s.kind = kindOfDesc(s.obj.TypeDesc); s.kind {
	case kindTable, kindView:
		return nil
	}
	hint := "Give a table or a view."
	if s.obj.Type == "SN" {
		hint = "Give the synonym's base object (gossms object describe " + s.name() + " names it)."
	}
	return usageError(fmt.Sprintf("%s is a %s, whose rows the data commands do not read", s.name(), s.kind), hint)
}

// columns is the object's columns, in ordinal order.
func (s *dataSource) columns(ctx context.Context) ([]*gosmo.Column, error) {
	if s.kind == kindTable {
		t, err := s.cat.TableByName(ctx, s.dbase, s.obj.Schema, s.obj.Name)
		if err != nil {
			return nil, readError(err)
		}
		cols, err := s.cat.TableColumns(ctx, t)
		if err != nil {
			return nil, readError(err)
		}
		return cols, nil
	}
	v, err := s.cat.ViewByName(ctx, s.dbase, s.obj.Schema, s.obj.Name)
	if err != nil {
		return nil, readError(err)
	}
	cols, err := s.cat.ViewColumns(ctx, v)
	if err != nil {
		return nil, readError(err)
	}
	return cols, nil
}

// session opens the query session the SELECT runs on, in the database the
// object was resolved in.
func (d Deps) dataSession(ctx context.Context, s *dataSource) (Session, error) {
	sess, err := d.OpenSession(ctx, s.conn, s.dbase.Name)
	if err != nil {
		return nil, connect.Classify(err, false, s.target)
	}
	return sess, nil
}

// fragments is a data command's --where and --order-by, checked.
type fragments struct {
	where, orderBy string
}

// readFragments reads --where and, for a command that has it, --order-by.
// Each must be something to splice: not blank, and closing every string,
// quoted name and block comment it opens — an open one would swallow the
// rest of the statement (D9). A trailing -- comment is fine: a line break
// follows each fragment.
func readFragments(inv *spec.Invocation) (fragments, error) {
	var f fragments
	for _, o := range []struct {
		name string
		to   *string
	}{{optWhere, &f.where}, {optOrderBy, &f.orderBy}} {
		if !inv.Given[o.name] {
			continue
		}
		v := inv.Values[o.name].(string)
		if strings.TrimSpace(v) == "" {
			return f, usageError("--"+o.name+": empty", "")
		}
		if open := openToken(v); open != "" {
			return f, usageError(fmt.Sprintf("--%s: %s is not closed", o.name, open),
				"Close it inside the fragment: the fragment is spliced into a statement, and an open one would run into the rest.")
		}
		*o.to = v
	}
	return f, nil
}

// openToken names what s leaves open at its end — a string, a quoted name, a
// block comment — or "".
func openToken(s string) string {
	buf := []rune(s)
	st := sqltext.State{}
	for i := 0; i < len(buf); {
		var t sqltext.Token
		t, st = sqltext.Next(buf, i, len(buf), st)
		if t.Kind == sqltext.KindEnd {
			break
		}
		i = t.End
	}
	switch st.Mode {
	case sqltext.ModeString:
		return "a string literal"
	case sqltext.ModeBracket, sqltext.ModeQuoted:
		return "a quoted name"
	case sqltext.ModeBlockComment:
		return "a /* comment"
	}
	return ""
}

// compose is the clauses after FROM: WHERE (<where>\n) and ORDER BY
// <order-by>\n. Each fragment ends at a line break, so a trailing -- comment
// ends there and cannot swallow what follows (D9).
func (f fragments) compose() string {
	var b strings.Builder
	if f.where != "" {
		b.WriteString("\nWHERE (" + f.where + "\n)")
	}
	if f.orderBy != "" {
		b.WriteString("\nORDER BY " + f.orderBy + "\n")
	}
	return b.String()
}

// checkOneRead refuses stmt, a composed SELECT, unless it classifies as one
// read statement in one batch with no flag and no ';' outside a literal or
// comment: what a fragment spliced in can then add is a condition or an
// ordering, never another statement (D9). The answer is query run's refusal:
// BLOCKED, exit 7, with the record naming what was found.
func checkOneRead(stmt string) (*output.Result, error) {
	sc := safety.Classify(stmt)
	var why string
	switch {
	case batchCount(stmt) != 1:
		why = "a GO line splits it into batches"
	case hasSemicolon(stmt):
		why = "a ';' ends the SELECT early"
	case len(sc.Statements) != 1:
		why = fmt.Sprintf("it holds %d statements", len(sc.Statements))
	case sc.Class != spec.ClassRead || sc.Statements[0].Leader != "SELECT":
		why = "it is not a read"
	case sc.Flags != 0:
		why = "it has " + strings.Join(sc.Flags.Names(), ", ")
	default:
		return nil, nil
	}
	descs := make([]string, len(sc.Statements))
	for i, st := range sc.Statements {
		descs[i] = st.Describe()
	}
	return &output.Result{Fields: []output.Field{
		{Key: "detectedClass", Value: string(sc.Class)},
		{Key: "statements", Value: strings.Join(descs, "; ")},
	}}, output.NewError(output.CodeBlocked,
		"--where and --order-by must leave the statement one SELECT: "+why,
		"Give a search condition and an ORDER BY list alone; for anything more, use gossms query run.")
}

// hasSemicolon reports a ';' in s outside literals, quoted names and comments.
func hasSemicolon(s string) bool {
	buf := []rune(s)
	st := sqltext.State{}
	for i := 0; i < len(buf); {
		var t sqltext.Token
		t, st = sqltext.Next(buf, i, len(buf), st)
		if t.Kind == sqltext.KindEnd {
			return false
		}
		if t.Kind == sqltext.KindPunct && buf[t.Start] == ';' {
			return true
		}
		i = t.End
	}
	return false
}

// parseColumnList splits --columns into column names, each unquoted: names
// or [bracketed]/"quoted" names separated by commas. Anything else — an
// expression, a literal, a comment — is a usage error: the names are matched
// against the object's columns, never spliced as written.
func parseColumnList(s string) ([]string, error) {
	bad := func(why string) error {
		return usageError(fmt.Sprintf("--columns %q: %s", s, why),
			"List column names separated by commas; bracket one holding a comma or space: id,[Order Date].")
	}
	buf := []rune(s)
	var names []string
	wantName := true
	for i, st := 0, (sqltext.State{}); ; {
		var t sqltext.Token
		t, st = sqltext.Next(buf, i, len(buf), st)
		i = t.End
		text := string(buf[t.Start:t.End])
		switch t.Kind {
		case sqltext.KindEnd:
			switch {
			case st.Mode != sqltext.ModeNormal:
				return nil, bad("an unclosed quoted name")
			case len(names) == 0:
				return nil, bad("no column named")
			case wantName:
				return nil, bad("it ends in a comma")
			}
			return names, nil
		case sqltext.KindWord, sqltext.KindQuotedIdent:
			if !wantName {
				return nil, bad("names must be separated by commas")
			}
			if t.Kind == sqltext.KindQuotedIdent {
				text = gosmo.UnquoteName(text)
			}
			if text == "" {
				return nil, bad("a name is empty")
			}
			names, wantName = append(names, text), false
		case sqltext.KindPunct:
			if text != "," || wantName {
				return nil, bad(fmt.Sprintf("unexpected %q", text))
			}
			wantName = true
		default:
			return nil, bad(fmt.Sprintf("unexpected %q", strings.TrimSpace(text)))
		}
	}
}

// matchColumns maps each name to the object's column it names — exactly, or
// ignoring case when that finds one — in the order given. A name twice is a
// usage error; one matching nothing is NOT_FOUND naming the columns whose
// names contain it, or every column.
func matchColumns(names []string, cols []*gosmo.Column, object string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		var match string
		for _, c := range cols {
			if c.Name == n {
				match = c.Name
				break
			}
		}
		if match == "" {
			var folded []string
			for _, c := range cols {
				if strings.EqualFold(c.Name, n) {
					folded = append(folded, c.Name)
				}
			}
			if len(folded) == 1 {
				match = folded[0]
			}
		}
		if match == "" {
			return nil, columnNotFound(n, cols, object)
		}
		if slices.Contains(out, match) {
			return nil, usageError(fmt.Sprintf("--columns: %s is named twice", gosmo.QuoteName(match)), "")
		}
		out = append(out, match)
	}
	return out, nil
}

func columnNotFound(name string, cols []*gosmo.Column, object string) *output.Error {
	var similar, all []string
	for _, c := range cols {
		all = append(all, c.Name)
		if strings.Contains(strings.ToLower(c.Name), strings.ToLower(name)) && len(similar) < maxSuggestions {
			similar = append(similar, c.Name)
		}
	}
	hint := object + "'s columns: " + strings.Join(all, ", ") + "."
	if len(similar) > 0 {
		hint = "Did you mean " + strings.Join(similar, ", ") + "?"
	}
	return output.NewError(output.CodeNotFound, fmt.Sprintf("--columns: %s has no column %s", object, gosmo.QuoteName(name)), hint)
}

// selectList is the SELECT list: every column (*), or the columns named,
// each bracketed.
func selectList(cols []string) string {
	if len(cols) == 0 {
		return "*"
	}
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = gosmo.QuoteName(c)
	}
	return strings.Join(q, ", ")
}

// topStatement is data top's SELECT: the TUI's Select Top 1000 Rows
// (SELECT TOP 1000 *\nFROM fqn), with --rows, --columns and the fragments.
func topStatement(rows int, cols []string, object string, f fragments) string {
	return fmt.Sprintf("SELECT TOP (%d) %s\nFROM %s", rows, selectList(cols), object) + f.compose()
}

// sampleStatement is data sample's SELECT: data top's ordered by NEWID(),
// so TOP keeps a uniformly random n of the rows --where keeps.
func sampleStatement(rows int, cols []string, object string, f fragments) string {
	f.orderBy = "NEWID()"
	return topStatement(rows, cols, object, f)
}

// countStatement is data count's exact SELECT.
func countStatement(object string, f fragments) string {
	return "SELECT COUNT_BIG(*) AS [rows]\nFROM " + object + f.compose()
}

// placeholderObject stands in for the object in the check that runs before
// connecting: one bracketed identifier classifies as the resolved
// [schema].[name] will.
const placeholderObject = "[object]"

func (d Deps) dataTop(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	return d.dataRows(ctx, inv, topStatement)
}

func (d Deps) dataSample(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	return d.dataRows(ctx, inv, sampleStatement)
}

// dataRows is data top and data sample: --rows read and checked, then
// selectRows.
func (d Deps) dataRows(ctx context.Context, inv *spec.Invocation,
	compose func(rows int, cols []string, object string, f fragments) string) (*output.Result, error) {
	rows := inv.Values[optRows].(int)
	if rows < 0 {
		return nil, usageError(fmt.Sprintf("--rows: %d is negative", rows), "Give the rows to select, or 0 for the columns alone.")
	}
	return d.selectRows(ctx, inv, func(cols []string, object string, f fragments) string {
		return compose(rows, cols, object, f)
	})
}

// selectRows is data top, sample and export's object form: --columns and the
// fragments read and checked, the object resolved, and compose's SELECT run
// to the sink.
func (d Deps) selectRows(ctx context.Context, inv *spec.Invocation,
	compose func(cols []string, object string, f fragments) string) (*output.Result, error) {
	names, err := readColumnNames(inv)
	if err != nil {
		return nil, err
	}
	frag, err := readFragments(inv)
	if err != nil {
		return nil, err
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	// Checked before connecting: a refused fragment never reaches the server.
	if res, err := checkOneRead(compose(names, placeholderObject, frag)); err != nil {
		return res, err
	}

	res := &output.Result{}
	s, err := d.openDataSource(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer s.conn.Close()
	var cols []string
	if names != nil {
		all, err := s.columns(ctx)
		if err != nil {
			return res, err
		}
		if cols, err = matchColumns(names, all, s.name()); err != nil {
			return res, err
		}
	}
	stmt := compose(cols, s.name(), frag)
	if r, err := checkOneRead(stmt); err != nil {
		return r, err
	}
	res.Fields = append(s.fields(), output.Field{Key: "tsql", Value: stmt, Verbatim: true})
	err = d.runDataSelect(ctx, s, res, timeout, func(ctx context.Context, sess Session, opts []query.Option) *query.Result {
		return sess.ExecuteToSink(ctx, stmt, inv.Sink, opts...)
	})
	return res, err
}

// readColumnNames is --columns parsed, nil when it is not given.
func readColumnNames(inv *spec.Invocation) ([]string, error) {
	if !inv.Given[optColumns] {
		return nil, nil
	}
	return parseColumnList(inv.Values[optColumns].(string))
}

// runDataSelect opens the session and runs a data command's SELECT through
// exec under --timeout, collecting its messages and errors into res.
func (d Deps) runDataSelect(ctx context.Context, s *dataSource, res *output.Result, timeout time.Duration,
	exec func(ctx context.Context, sess Session, opts []query.Option) *query.Result) error {
	sess, err := d.dataSession(ctx, s)
	if err != nil {
		return err
	}
	defer sess.Close()
	rctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	qr := exec(rctx, sess, []query.Option{query.WithoutSinkNotices()})
	collect(res, qr, rctx.Err() != nil)
	for _, e := range res.Errors {
		if e.Code == output.CodePermissionDenied {
			// collect's hint is query run's, about a script.
			e.Hint = "Reading " + s.name() + " needs SELECT on it, its schema or the database."
		}
	}
	if ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
		res.Errors = append([]*output.Error{timeoutError(timeout)}, res.Errors...)
	}
	return nil
}

func (d Deps) dataCount(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	frag, err := readFragments(inv)
	if err != nil {
		return nil, err
	}
	approximate := inv.Values[optApproximate].(bool)
	if approximate && frag.where != "" {
		return nil, usageError("--approximate and --where both given",
			"Partition statistics count every row; drop --approximate to count the rows --where keeps.")
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	if res, err := checkOneRead(countStatement(placeholderObject, frag)); err != nil {
		return res, err
	}

	res := &output.Result{}
	s, err := d.openDataSource(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer s.conn.Close()
	res.Fields = s.fields()

	if approximate {
		if s.kind != kindTable {
			return res, usageError(s.name()+" is a view: it has no partition statistics to count from",
				"Drop --approximate: an exact count runs the view's query.")
		}
		t, err := s.cat.TableByName(ctx, s.dbase, s.obj.Schema, s.obj.Name)
		if err != nil {
			return res, readError(err)
		}
		n, err := s.cat.RowCount(ctx, t)
		if err != nil {
			return res, readError(err)
		}
		res.Fields = append(res.Fields,
			output.Field{Key: "rows", Value: strconv.FormatInt(n, 10)},
			output.Field{Key: "method", Value: "approximate"})
		return res, nil
	}

	stmt := countStatement(s.name(), frag)
	if r, err := checkOneRead(stmt); err != nil {
		return r, err
	}
	var qr *query.Result
	err = d.runDataSelect(ctx, s, res, timeout, func(ctx context.Context, sess Session, opts []query.Option) *query.Result {
		qr = sess.Execute(ctx, stmt, opts...)
		// The count is a record field, not a set: the set's own "(1 row
		// affected)" would describe nothing the answer shows.
		qr.Messages = slices.DeleteFunc(qr.Messages, func(m query.Message) bool {
			return m.RowsAffected != nil && m.RowsAffected.AfterSet
		})
		return qr
	})
	if err != nil {
		return res, err
	}
	rows := output.Field{Key: "rows", Null: true}
	if qr != nil && len(res.Errors) == 0 && len(qr.Sets) == 1 && len(qr.Sets[0].Rows) == 1 && len(qr.Sets[0].Rows[0]) == 1 {
		rows.Value, rows.Null = qr.Sets[0].Rows[0][0], false
	}
	res.Fields = append(res.Fields, rows,
		output.Field{Key: "method", Value: "exact"},
		output.Field{Key: "tsql", Value: stmt, Verbatim: true})
	return res, nil
}
