package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/query"
)

// data import's options.
const (
	optInFile             = "in-file"
	optInFormat           = "in-format"
	optTruncateFirst      = "truncate-first"
	optNoCheckConstraints = "no-check-constraints"
	optFireTriggers       = "fire-triggers"
	optTablock            = "tablock"
)

func dataImportCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"data", "import"},
		Summary: "Load a .csv, .tsv or .jsonl file into a table (bulk copy)",
		Description: "Streams --in-file into the table through SQL Server's bulk copy, the way bcp and the Import " +
			"wizard load. The file is what data export writes: .csv or .tsv with a header row (NULL an empty " +
			"field, the empty string \"\"; a byte order mark is ignored), or gossms's .jsonl (a _resultSet line, " +
			"one object per row, the _summary line ignored); one result set per file. --in-format names the format " +
			"for stdin (--in-file -) or another extension. File columns are matched to the table's by name, " +
			"exactly or else ignoring case; a name the table lacks is exit 2. The table's columns missing from the " +
			"file take their defaults; computed, rowversion and GENERATED ALWAYS columns in the file are skipped " +
			"with a warning, so an export loads back; an identity column in the file keeps the file's values. " +
			"Each cell is converted to its column's type and checked before it is sent — range, length, scale, " +
			"code page — and the first that fails is exit 2 naming the line and column. sql_variant, CLR " +
			"(geography, geometry, hierarchyid) and Always Encrypted columns are exit 11 before loading. The load " +
			"is one batch, all or nothing: on any failure the table is as it was. NULLs stay NULL (KEEPNULLS); " +
			"CHECK and FOREIGN KEY constraints are checked unless --no-check-constraints; triggers fire only with " +
			"--fire-triggers. Needs --allow write and INSERT on the table (exit 9 without). --truncate-first " +
			"empties the table first (TRUNCATE TABLE): it is ddl, needs ALTER on the table, and needs --confirm " +
			"with the table as given; it cannot share the load's transaction, so a load that then fails leaves " +
			"the table empty. --dry-run reads and converts the whole file and loads nothing. A record — object, " +
			"type, inFile, format, truncateFirst, rows, dryRun (and tsql, the TRUNCATE, on a dry run) — and set " +
			"columns: each file column and the table column it loads (action load or skipped), then the table's " +
			"columns left to their defaults (action default).",
		Args: []spec.Arg{{Name: "table", Help: "The table to load: schema.name, or a name alone when one schema has it",
			Format: "schema.name", Required: true}},
		Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
			{Name: optInFile, Type: spec.TypeString, Help: "The file to load: .csv, .tsv or .jsonl; - reads stdin (with --in-format)"},
			{Name: optInFormat, Type: spec.TypeEnum, Enum: importFormats,
				Help: "The file's format when its extension does not name one, or for stdin"},
			{Name: optTruncateFirst, Type: spec.TypeBool,
				Help: "TRUNCATE TABLE before loading: ddl, needs ALTER on the table and --confirm"},
			{Name: optConfirm, Type: spec.TypeString, Help: "The table, as given: accepts --truncate-first"},
			{Name: optNoCheckConstraints, Type: spec.TypeBool,
				Help: "Do not check CHECK and FOREIGN KEY constraints on the loaded rows (bcp's default; marks them untrusted)"},
			{Name: optFireTriggers, Type: spec.TypeBool, Help: "Fire the table's INSERT triggers"},
			{Name: optTablock, Type: spec.TypeBool, Help: "Take a bulk-update table lock for the load"},
			{Name: optTimeout, Type: spec.TypeInt, Default: strconv.Itoa(defaultTimeout),
				Help: "Seconds the load may take; 0 is no limit"},
		}),
		Class: spec.ClassWrite,
		Run:   d.dataImport,
		Examples: []string{
			"gossms data import -p dev Sales.Customer --in-file customers.csv --allow write --dry-run",
			"gossms data import -p dev Sales.Customer --in-file customers.jsonl --allow write",
			"gossms data import -p dev Sales.Customer --in-file customers.csv --truncate-first --confirm Sales.Customer --allow ddl",
		},
	}
}

// importInput is --in-file and its format.
type importInput struct {
	path   string // "-" is stdin
	format importFormat
}

// readImportInput checks --in-file and settles its format: --in-format, or
// the extension.
func readImportInput(inv *spec.Invocation) (importInput, error) {
	if !inv.Given[optInFile] {
		return importInput{}, usageError("data import needs --in-file",
			"Name the file to load (.csv, .tsv or .jsonl), or - for stdin with --in-format.")
	}
	in := importInput{path: inv.Values[optInFile].(string)}
	if in.path == "" {
		return in, usageError("--in-file: empty path", "")
	}
	if inv.Given[optInFormat] {
		in.format = importFormat(inv.Values[optInFormat].(string))
		return in, nil
	}
	if in.path == "-" {
		return in, usageError("--in-file -: stdin has no extension to name its format", "Add --in-format csv, tsv or jsonl.")
	}
	switch ext := strings.ToLower(filepath.Ext(in.path)); ext {
	case ".csv", ".tsv", ".jsonl":
		in.format = importFormat(ext[1:])
	case ".json":
		return in, usageError("--in-file "+in.path+": .json is not imported",
			"Export to .jsonl instead — the same rows, one per line, which a load streams.")
	default:
		return in, usageError("--in-file "+in.path+": the extension names no format",
			"Use .csv, .tsv or .jsonl, or add --in-format.")
	}
	return in, nil
}

// openImportFile opens in and reads its header. The caller closes the
// returned closer.
func openImportFile(inv *spec.Invocation, in importInput) (importFile, io.Closer, error) {
	var r io.Reader
	closer := io.NopCloser(nil)
	if in.path == "-" {
		if inv.Stdin == nil {
			return nil, closer, usageError("--in-file -: no stdin to read", "")
		}
		r = inv.Stdin
	} else {
		f, err := os.Open(in.path)
		if err != nil {
			return nil, closer, usageError(fmt.Sprintf("--in-file %s: %v", in.path, errors.Unwrap(err)), "")
		}
		if fi, err := f.Stat(); err == nil && fi.IsDir() {
			f.Close()
			return nil, closer, usageError("--in-file "+in.path+": is a directory", "")
		}
		r, closer = f, f
	}
	var file importFile
	var err error
	switch in.format {
	case importCSV:
		file, err = newCSVImport(r, ',')
	case importTSV:
		file, err = newCSVImport(r, '\t')
	default:
		file, err = newJSONLImport(r)
	}
	if err != nil {
		closer.Close()
		return nil, io.NopCloser(nil), importFileError(in, err)
	}
	return file, closer, nil
}

// importFileError is a failure reading the file: USAGE_ERROR naming where.
func importFileError(in importInput, err error) *output.Error {
	if ie, ok := errors.AsType[*importError](err); ok {
		return usageError(fmt.Sprintf("--in-file %s: %v", in.path, ie), "")
	}
	return usageError(fmt.Sprintf("--in-file %s: %v", in.path, err), "")
}

// readTruncate is --truncate-first and its --confirm, checked before
// connecting: TRUNCATE removes every row, so --confirm must repeat the table
// exactly as given (BLOCKED otherwise).
func readTruncate(inv *spec.Invocation) (bool, error) {
	truncate := inv.Values[optTruncateFirst].(bool)
	confirm, _ := inv.Values[optConfirm].(string)
	if !truncate {
		if inv.Given[optConfirm] {
			return false, usageError("--confirm accepts --truncate-first, which was not given", "")
		}
		return false, nil
	}
	table := inv.Args[0]
	if confirm != table {
		msg := "--truncate-first removes every row of " + table + " before the load, and a failed load leaves it empty"
		if inv.Given[optConfirm] {
			msg += fmt.Sprintf("; --confirm %q is not the table as given", confirm)
		}
		return false, output.NewError(output.CodeBlocked, msg, "Add --confirm "+table+" to accept it.")
	}
	return true, nil
}

// importColumn is one file column and what the load does with it.
type importColumn struct {
	file  string
	col   *gosmo.Column // the table's column it names
	skip  string        // why it is not loaded: "computed", "rowversion", "generated always"
	parse func(string) (any, error)
}

// importPlan is the file's columns mapped onto the table's.
type importPlan struct {
	cols     []importColumn  // in file order
	load     []int           // the file columns loaded, in load order
	defaults []*gosmo.Column // the table's columns the file does not name
}

// loadColumns is the destination columns of the load, in load order.
func (p *importPlan) loadColumns() []string {
	names := make([]string, len(p.load))
	for i, fi := range p.load {
		names[i] = p.cols[fi].col.Name
	}
	return names
}

// skipReason is why a file column naming c is not loaded: the server writes
// c itself, so an exported value cannot go back. "" for a loadable column.
func skipReason(c *gosmo.Column, base string) string {
	switch {
	case c.IsComputed:
		return "computed"
	case base == "timestamp" || base == "rowversion":
		return "rowversion"
	case c.GeneratedAlwaysType != 0:
		return "generated always"
	}
	return ""
}

// matchFileColumn is the table column a file column names: exactly, else
// the one that matches ignoring case.
func matchFileColumn(name string, cols []*gosmo.Column) *gosmo.Column {
	var folded []*gosmo.Column
	for _, c := range cols {
		if c.Name == name {
			return c
		}
		if strings.EqualFold(c.Name, name) {
			folded = append(folded, c)
		}
	}
	if len(folded) == 1 {
		return folded[0]
	}
	return nil
}

// planImport maps the file's columns onto the table's (cols) and builds each
// loaded column's parser. A file column the table lacks, or two naming one
// column, is USAGE_ERROR; a loaded column of a type with no text form is
// UNSUPPORTED — both before anything is loaded.
func (s *dataSource) planImport(ctx context.Context, fileCols []string, cols []*gosmo.Column) (*importPlan, error) {
	aliases, err := s.aliasTypes(ctx, cols)
	if err != nil {
		return nil, err
	}
	base := func(c *gosmo.Column) string {
		b := string(c.DataType)
		if c.IsUserDefinedType && !c.IsTypeHidden {
			b = aliases[c.TypeSchema+"."+b]
		}
		if b = strings.ToLower(b); b == "sysname" {
			b = "nvarchar"
		}
		return b
	}
	codePages := map[string]int{}
	codePage := func(collation string) (int, error) {
		if cp, ok := codePages[collation]; ok {
			return cp, nil
		}
		if collation == "" {
			return 0, output.NewError(output.CodeGeneralError, "a char column's collation, or the database's, was not read", "")
		}
		cp, err := s.cat.CollationCodePage(ctx, collation)
		if err != nil {
			return 0, readError(err)
		}
		codePages[collation] = cp
		return cp, nil
	}

	p := &importPlan{}
	named := map[*gosmo.Column]string{}
	for _, name := range fileCols {
		c := matchFileColumn(name, cols)
		if c == nil {
			return nil, usageError(fmt.Sprintf("the file's column %s is not a column of %s", gosmo.QuoteName(name), s.name()),
				columnNotFound(name, cols, s.name()).Hint+" Remove the column from the file, or rename it.")
		}
		if other, ok := named[c]; ok {
			return nil, usageError(fmt.Sprintf("the file's columns %s and %s both name %s",
				gosmo.QuoteName(other), gosmo.QuoteName(name), gosmo.QuoteName(c.Name)), "")
		}
		named[c] = name
		ic := importColumn{file: name, col: c, skip: skipReason(c, base(c))}
		if ic.skip == "" {
			if ic.parse, err = s.cellParser(c, base(c), codePage); err != nil {
				return nil, err
			}
			p.load = append(p.load, len(p.cols))
		}
		p.cols = append(p.cols, ic)
	}
	if len(p.load) == 0 {
		return nil, usageError("no column of the file can be loaded: each is one the server computes", "")
	}
	for _, c := range cols {
		if _, ok := named[c]; !ok && c.GraphType == 0 && !c.IsDroppedLedgerColumn {
			p.defaults = append(p.defaults, c)
		}
	}
	return p, nil
}

// cellParser is query.NewCellParser for c, whose system type is base; a
// type with no text form is UNSUPPORTED naming the column.
func (s *dataSource) cellParser(c *gosmo.Column, base string, codePage func(string) (int, error)) (func(string) (any, error), error) {
	unsupported := func(why string) error {
		return output.NewError(output.CodeUnsupported,
			fmt.Sprintf("column %s (%s): %s", gosmo.QuoteName(c.Name), c.TypeString(), why),
			"Leave the column out of the file (it then takes its default), or load it with gossms query run.")
	}
	if c.ColumnEncryptionKey != "" {
		return nil, unsupported("an Always Encrypted column is loaded only by a client holding its key")
	}
	tc := query.TargetColumn{Type: base, Length: c.MaxLength, Precision: c.Precision, Scale: c.Scale}
	switch base {
	case "nchar", "nvarchar":
		if tc.Length > 0 {
			tc.Length /= 2 // max_length is bytes; the parser counts UTF-16 units
		}
	case "char", "varchar", "text":
		var err error
		if tc.CodePage, err = codePage(c.Collation); err != nil {
			return nil, err
		}
		if tc.DatabaseCodePage, err = codePage(s.dbase.Collation); err != nil {
			return nil, err
		}
	}
	parse, err := query.NewCellParser(tc)
	if errors.Is(err, query.ErrUnsupportedType) {
		return nil, unsupported("no text form a bulk load can send (" + err.Error() + ")")
	}
	return parse, err
}

// mappingSet is set columns: the file's columns and what each loads into,
// then the table's columns left to their defaults.
func (p *importPlan) mappingSet() output.Set {
	set := output.Set{Name: "columns", Columns: []output.Column{
		{Name: "file_column", Nullable: true}, {Name: "column"}, {Name: "type"}, {Name: "action"},
	}}
	str := func(s string) *string { return &s }
	for _, ic := range p.cols {
		action := "load"
		if ic.skip != "" {
			action = "skipped (" + ic.skip + ")"
		}
		set.Rows = append(set.Rows, []*string{str(ic.file), str(ic.col.Name), str(ic.col.TypeString()), str(action)})
	}
	for _, c := range p.defaults {
		set.Rows = append(set.Rows, []*string{nil, str(c.Name), str(c.TypeString()), str("default")})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// skippedWarning names the file columns the load leaves out, or "".
func (p *importPlan) skippedWarning() string {
	var skipped []string
	for _, ic := range p.cols {
		if ic.skip != "" {
			skipped = append(skipped, gosmo.QuoteName(ic.file)+" ("+ic.skip+")")
		}
	}
	if len(skipped) == 0 {
		return ""
	}
	return "Not loaded — the server writes these columns itself, so the file's values are ignored: " +
		strings.Join(skipped, ", ") + "."
}

// importRows is the load's rows: each file row's loaded cells converted.
// count is the rows yielded so far, and emptyCells the empty strings read
// (a one-column CSV cannot tell them from NULL).
func (p *importPlan) rows(file importFile, count, emptyCells *int64) iter.Seq2[[]any, error] {
	return func(yield func([]any, error) bool) {
		for {
			cells, isNull, line, err := file.Next()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			vals := make([]any, len(p.load))
			for i, fi := range p.load {
				if isNull[fi] {
					continue
				}
				if cells[fi] == "" {
					*emptyCells++
				}
				v, err := p.cols[fi].parse(cells[fi])
				if err != nil {
					yield(nil, &importError{line: line, column: gosmo.QuoteName(p.cols[fi].file), err: err})
					return
				}
				vals[i] = v
			}
			*count++
			if !yield(vals, nil) {
				return
			}
		}
	}
}

func (d Deps) dataImport(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	in, err := readImportInput(inv)
	if err != nil {
		return nil, err
	}
	if in.path == "-" && inv.Values[connect.OptPasswordStdin].(bool) {
		return nil, usageError("--in-file - and --password-stdin both read stdin",
			"Pass the password another way (--password-env), or load from a file.")
	}
	truncate, err := readTruncate(inv)
	if err != nil {
		return nil, err
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	class := spec.ClassWrite
	if truncate {
		class = spec.ClassDDL
	}
	res := &output.Result{}
	cfg, profile, err := d.gateClass(inv, res, class)
	if err != nil {
		return res, err
	}
	// The header is read before connecting: a file that is not one to load
	// never costs a connection.
	file, closer, err := openImportFile(inv, in)
	if err != nil {
		return res, err
	}
	defer closer.Close()

	t, sc, err := d.dial(ctx, inv, cfg, profile, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()
	s := &dataSource{target: t, conn: sc, cat: d.Catalog(sc)}
	if err := s.resolve(ctx, inv.Args[0], res); err != nil {
		return res, err
	}
	if s.kind != kindTable {
		return res, usageError(s.name()+" is a view: data import loads a table", "Load the view's base table.")
	}
	table, err := s.cat.TableByName(ctx, s.dbase, s.obj.Schema, s.obj.Name)
	if err != nil {
		return res, readError(err)
	}
	cols, err := s.cat.TableColumns(ctx, table)
	if err != nil {
		return res, readError(err)
	}
	plan, err := s.planImport(ctx, file.Columns(), cols)
	if err != nil {
		return res, err
	}
	if w := plan.skippedWarning(); w != "" {
		res.Warnings = append(res.Warnings, w)
	}
	res.Sets = []output.Set{plan.mappingSet()}
	format := in.format
	dry := dryRun(inv)
	rowsField := output.Field{Key: "rows", Value: "0"}
	res.Fields = append(s.fields(),
		output.Field{Key: "inFile", Value: in.path},
		output.Field{Key: "format", Value: string(format)},
		output.Field{Key: "truncateFirst", Value: strconv.FormatBool(truncate)})
	finish := func() {
		res.Fields = append(res.Fields, rowsField, output.Field{Key: "dryRun", Value: strconv.FormatBool(dry)})
	}

	var count, empty int64
	rows := plan.rows(file, &count, &empty)
	w := d.Writer(sc)
	oneColumnCSV := len(file.Columns()) == 1 && format != importJSONL
	warnEmpty := func() {
		if oneColumnCSV && empty > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("A one-column %s file writes a NULL as \"\", the same as "+
				"an empty string: its %d \"\" cells were read as empty strings, none as NULL. Export to .jsonl to keep NULLs.",
				format, empty))
		}
	}

	if dry {
		var tsql string
		if truncate {
			sctx, script := gosmo.WithScript(ctx)
			if err := w.TruncateTable(sctx, table); err != nil {
				finish()
				return res, writeError(err)
			}
			tsql = script.String()
		}
		for _, err := range rows {
			if err != nil {
				finish()
				return res, importLoadError(in, err, plan, false)
			}
		}
		rowsField.Value = strconv.FormatInt(count, 10)
		finish()
		if truncate {
			res.Fields = append(res.Fields, output.Field{Key: "tsql", Value: tsql, Verbatim: true})
		}
		warnEmpty()
		return res, nil
	}

	truncated := false
	if truncate {
		if err := w.TruncateTable(ctx, table); err != nil {
			finish()
			return res, truncateError(err, s.name())
		}
		truncated = true
	}
	rctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	bc := gosmo.BulkCopy{Schema: s.obj.Schema, Table: s.obj.Name, Columns: plan.loadColumns(),
		Options: gosmo.BulkOptions{
			KeepNulls:        true,
			CheckConstraints: !inv.Values[optNoCheckConstraints].(bool),
			FireTriggers:     inv.Values[optFireTriggers].(bool),
			TableLock:        inv.Values[optTablock].(bool),
		}}
	n, err := w.BulkInsert(rctx, s.dbase, bc, rows)
	if err != nil {
		finish()
		if ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
			return res, loadTimeoutError(timeout, truncated)
		}
		return res, importLoadError(in, err, plan, truncated)
	}
	rowsField.Value = strconv.FormatInt(n, 10)
	finish()
	warnEmpty()
	return res, nil
}

// truncatedNote ends a failed load's hint when --truncate-first ran.
func truncatedNote(truncated bool) string {
	if !truncated {
		return ""
	}
	return " --truncate-first had already emptied the table, and it stays empty."
}

// importLoadError is a failed load's answer. A file or cell fault is
// USAGE_ERROR naming the line and column; the server's errors map as a
// write's. A refused load is PERMISSION_DENIED: the server says Msg 1088
// "does not exist or you do not have permissions" for a missing INSERT, and
// for a missing ALTER when the file supplies identity values (live on 17),
// but the table was resolved just before, so it is the permission; and Msg
// 5304 for a missing ALTER when constraints go unchecked. Nothing was loaded
// either way: the load is one batch.
func importLoadError(in importInput, err error, plan *importPlan, truncated bool) *output.Error {
	if ie, ok := errors.AsType[*importError](err); ok {
		return usageError(fmt.Sprintf("--in-file %s: %v", in.path, ie),
			"Fix the file and run again: nothing was loaded."+truncatedNote(truncated))
	}
	var e *output.Error
	if gosmo.IsMissingOrDenied(err) {
		e = output.NewError(output.CodePermissionDenied, err.Error(), "")
	} else {
		e = writeError(err)
	}
	if e.Code == output.CodePermissionDenied {
		e.Hint = "A bulk load needs INSERT on the table, and ALTER on it to keep the file's identity values"
		if id := plan.identityColumn(); id != "" {
			e.Hint += " (the file has identity column " + gosmo.QuoteName(id) + ")"
		}
		e.Hint += ", to leave constraints unchecked or triggers unfired on a table that has them. Nothing was loaded."
	} else {
		e.Hint = strings.TrimSpace(e.Hint + " Nothing was loaded: the load is all or nothing.")
	}
	e.Hint += truncatedNote(truncated)
	return e
}

// identityColumn is the loaded identity column, or "".
func (p *importPlan) identityColumn() string {
	for _, fi := range p.load {
		if c := p.cols[fi].col; c.IsIdentity {
			return c.Name
		}
	}
	return ""
}

func loadTimeoutError(d time.Duration, truncated bool) *output.Error {
	return output.NewError(output.CodeTimeout,
		fmt.Sprintf("the load ran longer than --timeout %d s and was cancelled; nothing was loaded", int(d/time.Second)),
		"Raise --timeout (0 is no limit)."+truncatedNote(truncated))
}

// truncateError is a refused TRUNCATE TABLE. The table was just resolved, so
// the server's "does not exist or you do not have permission" (Msg 1088) is
// the missing ALTER.
func truncateError(err error, table string) *output.Error {
	if gosmo.IsMissingOrDenied(err) {
		return output.NewError(output.CodePermissionDenied, err.Error(),
			"--truncate-first needs ALTER on "+table+". Nothing was truncated or loaded.")
	}
	return writeError(err)
}
