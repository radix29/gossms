package cmd

import (
	"context"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/query"
)

// optOutFile is the global option data export requires.
const optOutFile = "out-file"

func dataExportCommand(d Deps) *spec.Command {
	object := dataObjectArg
	object.Required = false
	object.Help = "The table or view to export: schema.name, or a name alone when one schema has it; or give --sql, --file or --sql-base64"
	return &spec.Command{
		Path:    []string{"data", "export"},
		Summary: "Write every row of a table, a view or a read query to a file",
		Description: "Streams every row — no row, width or byte limit — to --out-file, which is required; its extension " +
			"names the format as for any command: .csv and .tsv (the TUI's Results To File writer: NULL an empty " +
			"field, the empty string \"\", the same bytes for the same rows), .jsonl (typed rows, then the summary), " +
			".json, .md, .txt. The file is replaced only when the export succeeds, and an existing one needs --force. " +
			"Give an object — SELECT * of it, with --columns, --where and --order-by as data top's (the composed " +
			"statement must stay one SELECT, else BLOCKED, exit 7) — or the T-SQL in exactly one of --sql, --file or " +
			"--sql-base64, which runs as query run does with the class fixed at read: anything above read is BLOCKED " +
			"(exit 7) before connecting, and the run stops at the first failing batch. Every result set goes to the " +
			"file. stdout gets the record — object, type, tsql; or detectedClass and statementCount — with each " +
			"set's row count and outFile.",
		Args: []spec.Arg{object},
		Options: append(append(append(ConnectOptions(), sqlInputOptions()...),
			paramOption(),
			columnsOption("The columns to export, comma-separated, in that order: a,[b c]; default every column (object form)"),
			whereOption(),
			spec.Option{Name: optOrderBy, Type: spec.TypeString,
				Help: "A T-SQL ORDER BY list, without ORDER BY (object form): id"}),
			timeoutOption(),
		),
		Class: spec.ClassRead,
		Run:   d.dataExport,
		Examples: []string{
			"gossms data export -p dev Sales.Customer --out-file customers.csv",
			"gossms data export -p dev Sales.SalesOrderHeader --where Status=5 --order-by SalesOrderID --out-file orders.jsonl",
			"gossms data export -p dev --file report.sql --out-file report.tsv --force",
		},
	}
}

// exportStatement is data export's SELECT of an object: data top's without
// TOP.
func exportStatement(cols []string, object string, f fragments) string {
	return "SELECT " + selectList(cols) + "\nFROM " + object + f.compose()
}

func (d Deps) dataExport(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	if !inv.Given[optOutFile] {
		return nil, usageError("data export needs --out-file",
			"Name the file to write: its extension (.csv .tsv .jsonl .json .md .txt) is the format. "+
				"To look at rows on stdout, use data top or query run.")
	}
	var sqlGiven, objectOnly []string
	for _, n := range []string{optSQL, optFile, optSQLBase64, optParam} {
		if inv.Given[n] {
			sqlGiven = append(sqlGiven, "--"+n)
		}
	}
	for _, n := range []string{optColumns, optWhere, optOrderBy} {
		if inv.Given[n] {
			objectOnly = append(objectOnly, "--"+n)
		}
	}
	if len(inv.Args) == 0 {
		if len(objectOnly) > 0 {
			return nil, usageError(strings.Join(objectOnly, ", ")+": only with an object",
				"Give the table or view to export, or write the condition into the T-SQL.")
		}
		return d.exportScript(ctx, inv)
	}
	if len(sqlGiven) > 0 {
		return nil, usageError("an object and "+strings.Join(sqlGiven, ", ")+" both given",
			"Export either the object or the T-SQL's result.")
	}
	return d.selectRows(ctx, inv, exportStatement)
}

// exportScript is data export's T-SQL form: query run held at read.
func (d Deps) exportScript(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	in, err := readScriptInput(inv)
	if err != nil {
		return nil, err
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	// query run's check, whose hint would name an --allow export does not
	// have.
	if b := safety.Check(in.sc, safety.Policy{Allow: spec.ClassRead}); b != nil {
		if b.Detected != spec.ClassRead {
			b.Hint = "data export runs reads only; make the change with gossms query run --allow " + string(b.Detected) + "."
		}
		return blockedResult(b)
	}
	run := runSettings{allow: spec.ClassRead, transaction: tranAuto, timeout: timeout}
	res, _, err := d.runScript(ctx, inv, in, run,
		func(ctx context.Context, sess Session, send string, opts []query.Option) *query.Result {
			return sess.ExecuteToSink(ctx, send, inv.Sink, opts...)
		})
	if res != nil {
		res.Fields = append([]output.Field{
			{Key: "detectedClass", Value: string(in.sc.Class)},
			{Key: "statementCount", Value: strconv.Itoa(len(in.sc.Statements))},
		}, res.Fields...)
	}
	return res, err
}
