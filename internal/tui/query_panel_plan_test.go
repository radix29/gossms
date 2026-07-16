package tui

import (
	"errors"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
)

// testPlanXML is the small synthetic single-line ShowPlanXML fixture also
// used by internal/showplan's own tests (internal/showplan/testdata/
// estimated_plan.xml) — one statement, no runtime info, valid enough for
// planview.PlanView.SetPlanXML to parse successfully.
const testPlanXML = `<?xml version="1.0" encoding="utf-8"?><ShowPlanXML xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" Version="1.599" Build="17.0.4055.5" xmlns="http://schemas.microsoft.com/sqlserver/2004/07/showplan"><BatchSequence><Batch><Statements><StmtSimple StatementCompId="1" StatementEstRows="5" StatementId="1" StatementOptmLevel="FULL" StatementSubTreeCost="0.0065" StatementText="SELECT TOP (5) [DoctorID], [FirstName], [LastName] FROM [HealthClinic].[dbo].[Doctors] ORDER BY [LastName]" StatementType="SELECT" QueryHash="0x1122334455667788"><StatementSetOptions ANSI_NULLS="true" ANSI_PADDING="true" ANSI_WARNINGS="true" ARITHABORT="true" CONCAT_NULL_YIELDS_NULL="true" NUMERIC_ROUNDABORT="false" QUOTED_IDENTIFIER="true" /><QueryPlan DegreeOfParallelism="1" CachedPlanSize="32" CompileTime="4" CompileCPU="4" CompileMemory="256"><MemoryGrantInfo SerialRequiredMemory="512" SerialDesiredMemory="512" GrantedMemory="512" MaxUsedMemory="0" /><RelOp AvgRowSize="60" EstimateCPU="0.0000012" EstimateIO="0" EstimateRebinds="0" EstimateRewinds="0" EstimatedExecutionMode="Row" EstimateRows="5" LogicalOp="Top" NodeId="0" Parallel="false" PhysicalOp="Top" EstimatedTotalSubtreeCost="0.0065"><OutputList><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="DoctorID" /><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="FirstName" /><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="LastName" /></OutputList><Top RowCount="false" IsPercent="false" WithTies="false"><TopExpression><ScalarOperator ScalarString="(5)"><Const ConstValue="(5)" /></ScalarOperator></TopExpression><RelOp AvgRowSize="60" EstimateCPU="0.0001" EstimateIO="0.05" EstimateRebinds="0" EstimateRewinds="0" EstimatedExecutionMode="Row" EstimateRows="409" EstimatedRowsRead="409" LogicalOp="Clustered Index Scan" NodeId="1" Parallel="false" PhysicalOp="Clustered Index Scan" EstimatedTotalSubtreeCost="0.0064" TableCardinality="409"><OutputList><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="DoctorID" /><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="FirstName" /><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="LastName" /></OutputList><Warnings><ColumnsWithNoStatistics><ColumnReference Column="LastName" /></ColumnsWithNoStatistics></Warnings><IndexScan Ordered="true" ScanDirection="FORWARD" ForcedIndex="false" ForceScan="false" NoExpandHint="false" Storage="RowStore"><DefinedValues><DefinedValue><ColumnReference Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Alias="[Doctors]" Column="DoctorID" /></DefinedValue></DefinedValues><Object Database="[HealthClinic]" Schema="[dbo]" Table="[Doctors]" Index="[PK_Doctors]" Alias="[Doctors]" IndexKind="Clustered" Storage="RowStore" /></IndexScan></RelOp></Top></RelOp></QueryPlan></StmtSimple></Statements></Batch></BatchSequence></ShowPlanXML>`

// testPlanResult returns a successful query.ExecuteEstimatedPlan-shaped
// Result: one captured plan document, no errors.
func testPlanResult() *query.Result {
	return &query.Result{PlanXML: []string{testPlanXML}}
}

// TestSetEstimatedPlanSuccessInstallsPlan confirms a successful fetch
// replaces any Results tabs with exactly Execution Plan + Messages, selects
// the plan tab, and clears p.result.
func TestSetEstimatedPlanSuccessInstallsPlan(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setEstimatedPlan(testPlanResult(), false)

	if qp.result != nil {
		t.Errorf("result = %v, want nil after a successful plan fetch", qp.result)
	}
	if !qp.planTabActive() {
		t.Errorf("planTabActive() = false after a successful plan fetch, want true")
	}
	tabs := qp.resultTabs()
	if len(tabs) != 2 || tabs[0] != "Execution Plan" || tabs[1] != "Messages" {
		t.Errorf("resultTabs() = %v, want [Execution Plan Messages]", tabs)
	}
	if qp.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Execution Plan)", qp.activeTab)
	}
	if qp.planView.Plan() == nil {
		t.Errorf("planView.Plan() = nil, want the parsed plan")
	}
}

// TestSetEstimatedPlanReplacesExistingResults confirms an existing set of
// Results tabs is dropped once a plan is shown — "delete results if
// exists" is the explicit requirement this feature was built for.
func TestSetEstimatedPlanReplacesExistingResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setResult(newTestResult(2, false), false)
	if len(qp.resultTabs()) != 3 {
		t.Fatalf("setup: resultTabs() = %v, want 3 entries before the plan fetch", qp.resultTabs())
	}

	qp.setEstimatedPlan(testPlanResult(), false)

	if qp.result != nil {
		t.Errorf("result = %v, want nil — Results tabs should be dropped", qp.result)
	}
	tabs := qp.resultTabs()
	if len(tabs) != 2 || tabs[0] != "Execution Plan" || tabs[1] != "Messages" {
		t.Errorf("resultTabs() = %v, want [Execution Plan Messages]", tabs)
	}
}

// TestSetEstimatedPlanFetchErrorShowsInMessages confirms a capture failure
// (e.g. a syntax error surfaced through the driver) lands on the Messages
// tab with the same "Msg N, Level L..." formatting query.Execute itself
// uses, not the plan view — and that the Messages tab is now the panel's
// only tab (no stale/empty "Execution Plan" tab alongside it).
func TestSetEstimatedPlanFetchErrorShowsInMessages(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := &query.Result{Messages: query.ErrorMessages(mssql.Error{
		Number:  208,
		State:   1,
		Class:   16,
		LineNo:  4,
		Message: "Invalid object name 'foo'.",
	})}

	qp.setEstimatedPlan(res, false)

	if !qp.onMessagesTab() {
		t.Fatalf("onMessagesTab() = false after a fetch error, want true (Messages selected)")
	}
	tabs := qp.resultTabs()
	if len(tabs) != 1 || tabs[0] != "Messages" {
		t.Errorf("resultTabs() = %v, want [Messages] only — no stale Execution Plan tab", tabs)
	}
	if qp.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Messages, the only tab)", qp.activeTab)
	}
	if qp.planView != nil {
		t.Errorf("planView = %v, want nil after a fetch error", qp.planView)
	}
	if len(qp.messageErrorLines) != 2 {
		t.Fatalf("messageErrorLines = %v, want 2 entries", qp.messageErrorLines)
	}
	if want := "Msg 208, Level 16, State 1, Line 4"; qp.messages.Text() == "" || qp.messages.Text()[:len(want)] != want {
		t.Errorf("messages.Text() = %q, want it to start with %q", qp.messages.Text(), want)
	}
}

// TestSetEstimatedPlanCancelledShowsFriendlyMessage confirms a cancelled
// fetch shows "Query was cancelled by user." instead of the raw wrapped
// context error, on the (only) Messages tab.
func TestSetEstimatedPlanCancelledShowsFriendlyMessage(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	qp.setEstimatedPlan(&query.Result{}, true)

	if qp.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Messages, the only tab)", qp.activeTab)
	}
	if got := qp.messages.Text(); got != "Query was cancelled by user." {
		t.Errorf("messages.Text() = %q, want %q", got, "Query was cancelled by user.")
	}
	if a.statusText != "Query cancelled" {
		t.Errorf("status = %q, want %q", a.statusText, "Query cancelled")
	}
}

// TestSetEstimatedPlanSuccessDespiteCancelFlag is a regression test for the
// cancelled/success precedence: like query.Execute's own res/cancelled
// split, a plan that did come back must still be installed and shown even
// if the fetch happened to race a cancel signal.
func TestSetEstimatedPlanSuccessDespiteCancelFlag(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	qp.setEstimatedPlan(testPlanResult(), true)

	if !qp.planTabActive() {
		t.Errorf("planTabActive() = false, want true — a successful plan must still show even if cancelled raced true")
	}
	if qp.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Execution Plan)", qp.activeTab)
	}
}

// TestSetEstimatedPlanSuccessClearsStaleMessages confirms a successful
// fetch after a previous failure clears the old error out of Messages —
// otherwise switching to Messages afterward would show stale content that
// renderActiveTab (a no-op in plan mode) would never refresh.
func TestSetEstimatedPlanSuccessClearsStaleMessages(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setEstimatedPlan(&query.Result{Messages: query.ErrorMessages(errors.New("boom"))}, false)
	if qp.messages.Text() == "" {
		t.Fatalf("setup: messages.Text() empty after a fetch error")
	}

	qp.setEstimatedPlan(testPlanResult(), false)

	if qp.messages.Text() != "" {
		t.Errorf("messages.Text() = %q after a successful fetch, want empty", qp.messages.Text())
	}
	if len(qp.messageErrorLines) != 0 {
		t.Errorf("messageErrorLines = %v, want empty", qp.messageErrorLines)
	}
}

// TestSetEstimatedPlanReusesPlanView confirms repeated plan fetches reuse
// the same *planview.PlanView instance (so its OnStatus wiring survives)
// rather than recreating it every time.
func TestSetEstimatedPlanReusesPlanView(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setEstimatedPlan(testPlanResult(), false)
	first := qp.planView

	qp.setEstimatedPlan(testPlanResult(), false)

	if qp.planView != first {
		t.Errorf("planView pointer changed across two successful fetches, want the same instance reused")
	}
}

// TestSetResultAfterPlanRevertsToResultsTabs confirms a normal query
// execution after a plan was shown drops the plan and reverts the response
// area back to Results/Messages — the mutual-exclusion half of the
// contract (setEstimatedPlan clears p.result; setResult clears p.planView).
func TestSetResultAfterPlanRevertsToResultsTabs(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.setEstimatedPlan(testPlanResult(), false)
	if !qp.planTabActive() {
		t.Fatalf("setup: planTabActive() = false after a plan fetch")
	}

	qp.setResult(newTestResult(1, false), false)

	if qp.planView != nil {
		t.Errorf("planView = %v, want nil after a normal Execute", qp.planView)
	}
	tabs := qp.resultTabs()
	if len(tabs) != 2 || tabs[0] != "Results" || tabs[1] != "Messages" {
		t.Errorf("resultTabs() = %v, want [Results Messages]", tabs)
	}
}

// TestSetEstimatedPlanNoPlanReturnedShowsInMessages covers a fetch that
// reports no error but also returns no plan XML at all (e.g. a script that
// only contains non-SELECT statements SQL Server didn't showplan) — this
// must still land on a reachable Messages tab, not a dead end.
func TestSetEstimatedPlanNoPlanReturnedShowsInMessages(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	qp.setEstimatedPlan(&query.Result{}, false)

	if !qp.onMessagesTab() {
		t.Fatalf("onMessagesTab() = false, want true — an empty result must still be reachable")
	}
	if qp.planView != nil {
		t.Errorf("planView = %v, want nil when no plan was returned", qp.planView)
	}
	if qp.messages.Text() == "" {
		t.Errorf("messages.Text() empty, want an explanation for the missing plan")
	}
}

// TestRunEstimatedPlanGuards confirms the three early-return guards in
// runEstimatedPlan behave like runQuery's: empty text, no connection, and
// an already-executing panel all no-op instead of starting a fetch.
func TestRunEstimatedPlanGuards(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	qp.runEstimatedPlan("")
	if qp.resultsNotice != "No query to execute" {
		t.Errorf("resultsNotice = %q, want %q for empty text", qp.resultsNotice, "No query to execute")
	}

	qp.editor.SetText("SELECT 1")
	qp.runEstimatedPlan(qp.editor.Text())
	if qp.resultsNotice != "Not connected — use File > Connect" {
		t.Errorf("resultsNotice = %q, want the not-connected notice", qp.resultsNotice)
	}
	if qp.executing {
		t.Errorf("executing = true, want false when not connected")
	}

	qp.conn = &db.ServerConn{Opts: config.Connection{Server: "fake"}}
	qp.executing = true
	qp.runEstimatedPlan("SELECT 1")
	if a.statusText != "A query is already executing in this panel" {
		t.Errorf("status = %q, want the already-executing notice", a.statusText)
	}
}
