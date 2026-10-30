package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/connect"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/safety"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
)

// The plan commands' options.
const (
	optPlanFormat = "plan-format"
	optPlanFile   = "plan-file"
	optForce      = "force" // the global option, which also covers --plan-file and --script-file
)

// --plan-format's values; the first is the default.
const (
	planSummary = "summary"
	planTree    = "tree"
	planXML     = "xml"
)

// defaultPlanTop is plan's --top default: the operators set's length.
const defaultPlanTop = 10

// planTextWidth is the characters of a statement's text the statements set
// keeps.
const planTextWidth = 200

// misestimateRatio is how far actual rows may stray from the estimate, either
// way, before an operator is flagged.
const misestimateRatio = 10

// PlanCommands is the plan group: its node and its commands.
func PlanCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"plan"},
			Summary: "Show execution plans",
			Description: "The query editor's Display Estimated Execution Plan and Include Actual Execution Plan. The " +
				"T-SQL comes as for query run: exactly one of --sql (-Q), --file (-i; - reads stdin) or --sql-base64.",
			Class: spec.ClassRead,
		},
		{
			Path:    []string{"plan", "estimated"},
			Summary: "Show the estimated execution plan of T-SQL without running it (SET SHOWPLAN_XML ON)",
			Description: "Compiles every batch under SET SHOWPLAN_XML ON on one session: the server returns each " +
				"statement's plan and runs nothing, so no --allow is needed whatever the script does. Because " +
				"nothing runs, a statement that uses a #temp table the script creates earlier fails to compile " +
				"(Msg 208), as in SSMS. The login needs SHOWPLAN in the database (exit 9 without it) and rights on " +
				"the objects. SET PARSEONLY, NOEXEC and SHOWPLAN_* in the script are refused (exit 7). " +
				"--plan-format summary (default) answers sets statements (cost, rows, DOP, memory grant), operators " +
				"(the --top costliest by own estimated cost across statements, cost_pct its share of its statement), " +
				"missing_indexes (with a CREATE INDEX to review) and warnings; tree is every operator in preorder " +
				"with its depth; xml is the plan document itself. --plan-file writes that document, as the query " +
				"editor's Save Execution Plan As does (UTF-8), for SSMS or the plan viewer to open; an existing file " +
				"needs --force. There is no --param: under SHOWPLAN_XML the server plans an sp_executesql call as the " +
				"EXEC alone, not the statement inside (found live on 17); write the values into the script.",
			Options: append(append(append(sqlInputOptions(), profileOption()), connect.Options()...),
				spec.Option{Name: optPlanFormat, Type: spec.TypeEnum, Enum: []string{planSummary, planTree, planXML},
					Default: planSummary, Help: "summary: statements, costliest operators, missing indexes, warnings; tree: every operator; xml: the plan document"},
				spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultPlanTop),
					Help: "Operators the summary keeps, costliest first; 0 for every one"},
				spec.Option{Name: optPlanFile, Type: spec.TypeString,
					Help: "Also write the plan document to this file (.sqlplan opens in SSMS); --force replaces one"},
				timeoutOption(),
			),
			Class: spec.ClassRead,
			Run:   d.planEstimated,
			Examples: []string{
				"gossms plan estimated -p dev --file report.sql",
				"gossms plan estimated -p dev --file - -o json",
				"gossms plan estimated -p dev --file report.sql --plan-format tree",
				"gossms plan estimated -p dev --file report.sql --plan-format xml --plan-file report.sqlplan",
			},
		},
		{
			Path:    []string{"plan", "actual"},
			Summary: "Run T-SQL and show its actual execution plan (SET STATISTICS XML ON)",
			Description: "Runs the script as query run does — the same --allow check and ceiling, --transaction, " +
				"--max-rows-affected, --param, stop on the first failing batch unless --continue-on-error, --timeout, " +
				"--dry-run — under SET STATISTICS XML ON, and answers each executed statement's plan with its actual " +
				"rows, executions and times. The script really runs: a write needs --allow write, and --transaction " +
				"rollback measures it without keeping the change. Its rows are counted, not returned: set " +
				"result_sets has each set's columns and row count (query run returns the rows). The login needs " +
				"SHOWPLAN in the database (exit 9 without it). SET STATISTICS XML in the script is refused (exit 7), " +
				"as are SET PARSEONLY, NOEXEC and SHOWPLAN_* ON. --plan-format, --top and --plan-file as for plan " +
				"estimated; the summary's actual columns (actual_rows, elapsed_ms, cpu_ms, misestimate) are filled. " +
				"A failed or timed-out run still answers the plans the server sent before it stopped; a batch cut by " +
				"--timeout loses its own, which the server had not yet sent.",
			Options: append(append(append(append(sqlInputOptions(), profileOption()), connect.Options()...), runOptions()...),
				timeoutOption(),
				paramOption(),
				spec.Option{Name: optPlanFormat, Type: spec.TypeEnum, Enum: []string{planSummary, planTree, planXML},
					Default: planSummary, Help: "summary: statements, costliest operators, missing indexes, warnings; tree: every operator; xml: the plan document"},
				spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultPlanTop),
					Help: "Operators the summary keeps, costliest first; 0 for every one"},
				spec.Option{Name: optPlanFile, Type: spec.TypeString,
					Help: "Also write the plan document to this file (.sqlplan opens in SSMS); --force replaces one"},
			),
			Class: spec.ClassRead,
			Run:   d.planActual,
			Examples: []string{
				"gossms plan actual -p dev --file report.sql",
				"gossms plan actual -p dev --file fix.sql --allow write --transaction rollback",
				"gossms plan actual -p dev --file by-id.sql --param id=42:int --plan-format tree",
				"gossms plan actual -p dev --file report.sql --plan-format xml --plan-file report.sqlplan",
			},
		},
	}
}

// planSettings is a plan command's own options, checked.
type planSettings struct {
	format string
	top    int    // 0: every operator
	file   string // --plan-file; empty when not given
}

func readPlanSettings(inv *spec.Invocation) (planSettings, error) {
	p := planSettings{format: inv.Values[optPlanFormat].(string), top: inv.Values[optTop].(int)}
	if p.top < 0 {
		return p, usageError(fmt.Sprintf("--top: %d is negative", p.top),
			"Give the number of operators to keep, or --top 0 for every one.")
	}
	// Checked before connecting, as --out-file is: a plan the caller cannot
	// keep should not cost a compile.
	var err error
	p.file, err = writableFile(inv, optPlanFile)
	return p, err
}

func (d Deps) planEstimated(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	in, err := readScriptInput(inv)
	if err != nil {
		return nil, err
	}
	timeout, err := readTimeout(inv)
	if err != nil {
		return nil, err
	}
	ps, err := readPlanSettings(inv)
	if err != nil {
		return nil, err
	}
	// Nothing runs, so the class is not checked and the profile's ceiling does
	// not apply; but a mode option in the script is refused (refuseModeWords).
	if res, err := refuseModeWords(in.script, in.sc, "plan estimated",
		"Remove those SET statements; plan estimated sets SHOWPLAN_XML itself."); err != nil {
		return res, err
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
	// Every batch, so every compile error is reported in one call.
	qr := c.sess.ExecuteEstimatedPlan(rctx, in.send)
	// Nothing ran, so a row count is the showplan set's own: noise.
	qr.Messages = slices.DeleteFunc(qr.Messages, func(m query.Message) bool { return m.RowsAffected != nil })
	collect(res, qr, rctx.Err() != nil)
	if qr.Database != "" {
		res.Database = qr.Database
	}
	if ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
		res.Errors = append([]*output.Error{timeoutError(timeout)}, res.Errors...)
	}
	hintPlanErrors(res, "estimated")

	res.Fields = []output.Field{{Key: "planType", Value: "estimated"}, {Key: "detectedClass", Value: string(in.sc.Class)}}
	if len(qr.PlanXML) == 0 {
		res.Fields = append(res.Fields, output.Field{Key: "statementCount", Value: "0"})
		if len(res.Errors) == 0 {
			res.Warnings = append(res.Warnings, "The server returned no plan for the script.")
		}
		planFileNotWritten(res, ps, "there is no plan")
		return res, nil
	}
	plan, err := showplan.ParseAll(qr.PlanXML)
	if err != nil {
		res.Errors = append(res.Errors, output.NewError(output.CodeGeneralError, "the server's plan could not be read: "+err.Error(), ""))
		return res, nil
	}
	addPlan(res, plan, ps)
	return res, nil
}

func (d Deps) planActual(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	in, err := readScriptInput(inv)
	if err != nil {
		return nil, err
	}
	run, err := readRunSettings(inv)
	if err != nil {
		return nil, err
	}
	ps, err := readPlanSettings(inv)
	if err != nil {
		return nil, err
	}
	if res, err := refuseStatisticsXML(in); err != nil {
		return res, err
	}
	counts := &countingSink{}
	res, qr, err := d.runScript(ctx, inv, in, run,
		func(ctx context.Context, sess Session, send string, opts []query.Option) *query.Result {
			qr := sess.ExecuteToSinkWithPlan(ctx, send, gosmo.PlanActual, counts, opts...)
			// Each plan arrives as a one-row set with its own count: noise
			// beside the counts of the script's own sets.
			qr.Messages = slices.DeleteFunc(qr.Messages, func(m query.Message) bool {
				return m.RowsAffected != nil && m.RowsAffected.AfterPlan
			})
			return qr
		})
	if qr == nil {
		// Refused, a dry run, or the script never reached the server.
		return res, err
	}
	hintPlanErrors(res, "actual")
	// The record leads with what the plan is; the transaction's outcome, set
	// by the run, follows the plan's fields.
	tx := res.Fields
	res.Fields = []output.Field{{Key: "planType", Value: "actual"}, {Key: "detectedClass", Value: string(in.sc.Class)}}
	addRunPlan(res, qr, ps)
	res.Sets = append(res.Sets, counts.set())
	res.Fields = append(res.Fields, tx...)
	return res, err
}

// addRunPlan adds the plans qr captured to res, as addPlan does, or says why
// there are none.
func addRunPlan(res *output.Result, qr *query.Result, ps planSettings) {
	if len(qr.PlanXML) == 0 {
		res.Fields = append(res.Fields, output.Field{Key: "statementCount", Value: "0"})
		if len(res.Errors) == 0 {
			res.Warnings = append(res.Warnings, "The server returned no plan for the script: no statement in it ran a query plan.")
		}
		planFileNotWritten(res, ps, "there is no plan")
		return
	}
	plan, err := showplan.ParseAll(qr.PlanXML)
	if err != nil {
		res.Errors = append(res.Errors, output.NewError(output.CodeGeneralError, "the server's plan could not be read: "+err.Error(), ""))
		return
	}
	addPlan(res, plan, ps)
}

// refuseStatisticsXML refuses a script that sets STATISTICS XML itself, ON
// or OFF: plan actual captures under it, and the script's OFF would lose the
// plans of what follows. Nil when it does not.
func refuseStatisticsXML(in scriptInput) (*output.Result, error) {
	lines := safety.StatisticsXMLWords(in.script)
	if len(lines) == 0 {
		return nil, nil
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = "line " + strconv.Itoa(l)
	}
	return &output.Result{Fields: []output.Field{
		{Key: "detectedClass", Value: string(in.sc.Class)},
		{Key: "statements", Value: strings.Join(parts, "; ")},
	}}, output.NewError(output.CodeBlocked, "the script sets STATISTICS XML, which plan actual runs under itself",
		"Remove STATISTICS XML from those SET statements; plan actual sets it itself.")
}

// countingSink is plan actual's RowSink: it keeps each result set's columns
// and row count, not its rows.
type countingSink struct {
	sets []countedSet
}

type countedSet struct {
	columns []string
	rows    int
}

func (c *countingSink) BeginSet(columns []string) error {
	c.sets = append(c.sets, countedSet{columns: slices.Clone(columns)})
	return nil
}

func (c *countingSink) Row([]string, []bool) error {
	c.sets[len(c.sets)-1].rows++
	return nil
}

func (c *countingSink) EndSet(int) error { return nil }

// set is the result_sets set: one row per set the script returned.
func (c *countingSink) set() output.Set {
	set := output.Set{Name: "result_sets", Columns: []output.Column{
		{Name: "n", Type: "int"}, {Name: "rows", Type: "bigint"}, {Name: "columns"},
	}}
	for i, s := range c.sets {
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(i + 1)), ptr(strconv.Itoa(s.rows)), ptr(strings.Join(s.columns, ", ")),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// hintPlanErrors gives the errors a plan command meets their own hint:
// sqlError's for a permission is about the classifier, which a plan command
// does not apply. planType is "estimated" or "actual".
func hintPlanErrors(res *output.Result, planType string) {
	for _, e := range res.Errors {
		if e.SQL == nil {
			continue
		}
		switch e.SQL.Number {
		case 208:
			if planType == "estimated" {
				e.Hint = "Nothing runs under SHOWPLAN_XML, so a #temp table (or any object) the script creates does not " +
					"exist yet: create it first in the database, or use the actual plan."
			}
		case 262:
			e.Hint = "The " + planType + " plan needs SHOWPLAN in the database: GRANT SHOWPLAN TO <user>."
		}
	}
}

// addPlan adds plan to res in ps's format and writes --plan-file.
func addPlan(res *output.Result, plan *showplan.Plan, ps planSettings) {
	res.Fields = append(res.Fields, output.Field{Key: "statementCount", Value: strconv.Itoa(len(plan.Statements))})
	switch ps.format {
	case planXML:
		res.Fields = append(res.Fields, output.Field{Key: "planXml", Value: plan.XML, Verbatim: true})
	case planTree:
		res.Sets = []output.Set{treeSet(plan)}
	default:
		res.Sets = []output.Set{statementsSet(plan), operatorsSet(plan, ps.top), missingIndexesSet(plan), planWarningsSet(plan)}
	}
	if ps.file == "" {
		return
	}
	if len(res.Errors) > 0 {
		planFileNotWritten(res, ps, "the command did not succeed")
		return
	}
	// The TUI's Save Execution Plan As writes the same bytes: the decoded
	// document as UTF-8 (app_panel_actions.go, savePlanPanel).
	if err := fileutil.WriteAtomic(ps.file, []byte(plan.XML), 0o644); err != nil {
		res.Errors = append(res.Errors, output.NewError(output.CodeGeneralError,
			fmt.Sprintf("writing --plan-file %s: %v", ps.file, err), ""))
		return
	}
	res.Fields = append(res.Fields, output.Field{Key: "planFile", Value: ps.file})
}

func planFileNotWritten(res *output.Result, ps planSettings, why string) {
	if ps.file != "" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("--plan-file %s was not written: %s.", ps.file, why))
	}
}

// The plan sets' cells.

func floatCell(f float64) *string { return ptr(strconv.FormatFloat(f, 'f', -1, 64)) }

func pctText(fraction float64) *string { return ptr(fmt.Sprintf("%.1f", fraction*100)) }

func textOrNull(s string) *string {
	if s == "" {
		return nil
	}
	return ptr(s)
}

// statementText is a statement's text on one line, cut to planTextWidth
// characters.
func statementText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= planTextWidth {
		return s
	}
	return string([]rune(s)[:planTextWidth-1]) + "…"
}

// rowSource is the operator whose actual rows are the statement's: its root,
// or, while that is a Compute Scalar with no runtime of its own, the one
// operator below it. The server folds such a Compute Scalar into its parent
// and reports no counters for it, though it passes every row through — live
// on 17, a GROUP BY with COUNT(*) has one at its root, and the statement's
// 100 rows read as NULL.
func rowSource(n *showplan.Node) *showplan.Node {
	for n != nil && n.Runtime == nil && n.PhysicalOp == "Compute Scalar" && len(n.Children) == 1 {
		n = n.Children[0]
	}
	return n
}

func statementsSet(plan *showplan.Plan) output.Set {
	set := output.Set{Name: "statements", Columns: []output.Column{
		{Name: "n", Type: "int"}, {Name: "type"}, {Name: "cost", Type: "float"}, {Name: "est_rows", Type: "float"},
		{Name: "actual_rows", Type: "bigint", Nullable: true}, {Name: "elapsed_ms", Type: "bigint", Nullable: true},
		{Name: "cpu_ms", Type: "bigint", Nullable: true}, {Name: "dop", Type: "int", Nullable: true},
		{Name: "grant_kb", Type: "bigint", Nullable: true}, {Name: "warnings", Type: "int"}, {Name: "text"},
	}}
	for i, st := range plan.Statements {
		var actual, elapsed, cpu, dop, grant *string
		if root := rowSource(st.Root); root != nil && root.Runtime != nil {
			actual = int64Cell(root.Runtime.Rows)
		}
		if st.TimeStats != nil {
			elapsed, cpu = int64Cell(st.TimeStats.ElapsedMS), int64Cell(st.TimeStats.CPUMS)
		}
		if st.DOP > 0 {
			dop = ptr(strconv.Itoa(st.DOP))
		}
		if st.MemoryGrant != nil {
			grant = int64Cell(st.MemoryGrant.GrantedKB)
		}
		warnings := len(st.Warnings)
		for _, n := range st.Nodes() {
			warnings += len(n.Warnings)
		}
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(i + 1)), ptr(st.Type), floatCell(st.SubTreeCost), floatCell(st.EstRows),
			actual, elapsed, cpu, dop, grant, ptr(strconv.Itoa(warnings)), ptr(statementText(st.Text)),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// operatorColumns is the columns an operator row has after its stmt and node
// (and, in the tree, depth and parent).
var operatorColumns = []output.Column{
	{Name: "physical"}, {Name: "logical"}, {Name: "object", Nullable: true}, {Name: "cost_pct", Type: "float"},
	{Name: "est_rows", Type: "float"}, {Name: "actual_rows", Type: "bigint", Nullable: true},
	{Name: "elapsed_ms", Type: "bigint", Nullable: true}, {Name: "parallel", Type: "bit"}, {Name: "mode", Nullable: true}, {Name: "warnings", Nullable: true},
	{Name: "misestimate", Type: "bit", Nullable: true},
}

// operatorCells is n's operatorColumns.
func operatorCells(n *showplan.Node, st *showplan.Statement) []*string {
	var object, actual, elapsed *string
	if !n.Object.IsZero() {
		object = ptr(n.Object.Short())
	}
	if n.Runtime != nil {
		actual, elapsed = int64Cell(n.Runtime.Rows), int64Cell(n.Runtime.ElapsedMS)
	}
	return []*string{
		ptr(n.PhysicalOp), ptr(n.LogicalOp), object, pctText(n.Cost(st.SubTreeCost)), floatCell(n.EstRows), actual,
		elapsed, bitCell(n.Parallel), textOrNull(n.ExecMode), textOrNull(strings.Join(n.Warnings, "; ")), misestimate(n),
	}
}

// misestimate flags an operator that ran whose actual rows, over all its
// executions, are more than misestimateRatio times its estimate for as many
// executions, or less than a tenth of it; NULL on an operator that did not
// run. Below one row counts as one, so 0 against 0.5 is no misestimate.
func misestimate(n *showplan.Node) *string {
	if n.Runtime == nil {
		return nil
	}
	est := n.EstRows * float64(max(n.Runtime.Executions, 1))
	act := float64(n.Runtime.Rows)
	lo, hi := max(min(est, act), 1), max(est, act, 1)
	return bitCell(hi/lo > misestimateRatio)
}

// operatorsSet is the top operators of plan by own cost across statements
// (showplan.Plan.Rank), every one when top is 0.
func operatorsSet(plan *showplan.Plan, top int) output.Set {
	set := output.Set{Name: "operators", Columns: append([]output.Column{
		{Name: "stmt", Type: "int"}, {Name: "node", Type: "int"},
	}, operatorColumns...)}
	ranked := plan.Rank(showplan.RankByCost)
	if top > 0 && len(ranked) > top {
		ranked = ranked[:top]
	}
	for _, r := range ranked {
		row := []*string{ptr(strconv.Itoa(r.Stmt + 1)), ptr(strconv.Itoa(r.Node.ID))}
		set.Rows = append(set.Rows, append(row, operatorCells(r.Node, plan.Statements[r.Stmt])...))
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// treeSet is every operator of plan in preorder, statement by statement,
// with its depth and its parent's node id: the text renderer keeps a row on
// one line, so indentation would not survive.
func treeSet(plan *showplan.Plan) output.Set {
	set := output.Set{Name: "tree", Columns: append([]output.Column{
		{Name: "stmt", Type: "int"}, {Name: "node", Type: "int"}, {Name: "depth", Type: "int"},
		{Name: "parent", Type: "int", Nullable: true},
	}, operatorColumns...)}
	for i, st := range plan.Statements {
		var walk func(n, parent *showplan.Node, depth int)
		walk = func(n, parent *showplan.Node, depth int) {
			var p *string
			if parent != nil {
				p = ptr(strconv.Itoa(parent.ID))
			}
			row := []*string{ptr(strconv.Itoa(i + 1)), ptr(strconv.Itoa(n.ID)), ptr(strconv.Itoa(depth)), p}
			set.Rows = append(set.Rows, append(row, operatorCells(n, st)...))
			for _, c := range n.Children {
				walk(c, n, depth+1)
			}
		}
		if st.Root != nil {
			walk(st.Root, nil, 0)
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

func missingIndexesSet(plan *showplan.Plan) output.Set {
	set := output.Set{Name: "missing_indexes", Columns: []output.Column{
		{Name: "stmt", Type: "int"}, {Name: "impact", Type: "float"}, {Name: "database", Nullable: true},
		{Name: "table"}, {Name: "equality", Nullable: true}, {Name: "inequality", Nullable: true},
		{Name: "include", Nullable: true}, {Name: "create_statement"},
	}}
	for i, st := range plan.Statements {
		for _, m := range st.MissingIndexes {
			set.Rows = append(set.Rows, []*string{
				ptr(strconv.Itoa(i + 1)), floatCell(m.Impact), textOrNull(m.Database), ptr(m.Schema + "." + m.Table),
				textOrNull(strings.Join(m.Equality, ", ")), textOrNull(strings.Join(m.Inequality, ", ")),
				textOrNull(strings.Join(m.Include, ", ")), ptr(m.CreateStatement()),
			})
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// planWarningsSet is one row per warning: a statement's with no node, then
// its operators' in preorder.
func planWarningsSet(plan *showplan.Plan) output.Set {
	set := output.Set{Name: "warnings", Columns: []output.Column{
		{Name: "stmt", Type: "int"}, {Name: "node", Type: "int", Nullable: true},
		{Name: "operator", Nullable: true}, {Name: "warning"},
	}}
	for i, st := range plan.Statements {
		stmt := ptr(strconv.Itoa(i + 1))
		for _, w := range st.Warnings {
			set.Rows = append(set.Rows, []*string{stmt, nil, nil, ptr(w)})
		}
		for _, n := range st.Nodes() {
			for _, w := range n.Warnings {
				set.Rows = append(set.Rows, []*string{stmt, ptr(strconv.Itoa(n.ID)), ptr(n.PhysicalOp), ptr(w)})
			}
		}
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
