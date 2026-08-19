package showplan

import (
	"strings"
	"testing"
)

const missingIndexPlan = `<ShowPlanXML Version="1.599" Build="17.0.1125.2">
<BatchSequence><Batch><Statements>
<StmtSimple StatementText="SELECT ...">
<QueryPlan>
<MissingIndexes>
  <MissingIndexGroup Impact="96.4054">
    <MissingIndex Database="[HealthClinic]" Schema="[dbo]" Table="[Appointments]">
      <ColumnGroup Usage="EQUALITY"><Column Name="[DoctorID]" ColumnId="3"/></ColumnGroup>
      <ColumnGroup Usage="INEQUALITY"><Column Name="[ScheduledAt]" ColumnId="4"/></ColumnGroup>
      <ColumnGroup Usage="INCLUDE"><Column Name="[Status]" ColumnId="6"/></ColumnGroup>
    </MissingIndex>
  </MissingIndexGroup>
</MissingIndexes>
<RelOp NodeId="0" PhysicalOp="Table Scan" LogicalOp="Table Scan"/>
</QueryPlan></StmtSimple>
</Statements></Batch></BatchSequence></ShowPlanXML>`

func parseMissingIndex(t *testing.T) MissingIndex {
	t.Helper()
	plan, err := Parse([]byte(missingIndexPlan))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	mi := plan.Statements[0].MissingIndexes
	if len(mi) != 1 {
		t.Fatalf("MissingIndexes = %d, want 1", len(mi))
	}
	return mi[0]
}

// An INEQUALITY column is a key column, but not an equality one: it has to
// come after every equality column or the index can't seek on them.
func TestParseMissingIndexSeparatesInequalityColumns(t *testing.T) {
	m := parseMissingIndex(t)
	if got := strings.Join(m.Equality, ","); got != "DoctorID" {
		t.Errorf("Equality = %q, want DoctorID", got)
	}
	if got := strings.Join(m.Inequality, ","); got != "ScheduledAt" {
		t.Errorf("Inequality = %q, want ScheduledAt", got)
	}
	if got := strings.Join(m.Include, ","); got != "Status" {
		t.Errorf("Include = %q, want Status", got)
	}
	if got := strings.Join(m.Keys(), ","); got != "DoctorID,ScheduledAt" {
		t.Errorf("Keys() = %q, want the equality column first", got)
	}
}

func TestMissingIndexCreateStatement(t *testing.T) {
	m := parseMissingIndex(t)
	want := "CREATE NONCLUSTERED INDEX [<Name of Missing Index, sysname,>] " +
		"ON [dbo].[Appointments] ([DoctorID],[ScheduledAt]) INCLUDE ([Status])"
	if got := m.CreateStatement(); got != want {
		t.Errorf("CreateStatement()\n got %q\nwant %q", got, want)
	}
}

// An index with no INCLUDE columns must not emit an empty INCLUDE (), which
// doesn't parse.
func TestMissingIndexCreateStatementWithoutIncludes(t *testing.T) {
	m := MissingIndex{Schema: "dbo", Table: "T", Equality: []string{"a"}}
	if got := m.CreateStatement(); strings.Contains(got, "INCLUDE") {
		t.Errorf("CreateStatement() = %q, want no INCLUDE clause", got)
	}
}

// The script names the database in a USE, never in the CREATE — a
// three-part name is not valid there — and leaves the DDL commented out,
// as SSMS does, since the index name is still a placeholder.
func TestMissingIndexScript(t *testing.T) {
	got := MissingIndexScript([]MissingIndex{parseMissingIndex(t)})
	for _, want := range []string{
		"improve the query cost by 96.4054%",
		"USE [HealthClinic]",
		"ON [dbo].[Appointments] ([DoctorID],[ScheduledAt])",
		"INCLUDE ([Status])",
		"GO\n*/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[HealthClinic].[dbo]") {
		t.Errorf("CREATE names the database:\n%s", got)
	}
}
