package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// sampleJobStep has every field set, so a dropped field shows as zero.
func sampleJobStep() *gosmo.JobStep {
	return &gosmo.JobStep{
		StepID: 2, Name: "Rebuild indexes", Subsystem: tsqlSubsystem,
		Command: "EXEC dbo.usp_reindex", Database: "AdventureWorks",
		OnSuccessAction: 3, OnSuccessStepID: 0,
		OnFailAction: 4, OnFailStepID: 7,
		RetryAttempts: 3, RetryInterval: 5,
		OutputFileName: `C:\logs\reindex.txt`,
	}
}

// A loaded, untouched step must be unchanged, or apply rewrites it on every OK.
func TestJobStepEditFromStepIsUnchangedAndCarriesEveryField(t *testing.T) {
	s := sampleJobStep()
	e := jobStepEditFromStep(s)

	if e.changed() {
		t.Error("a freshly loaded step reports changed() — every OK would rewrite it")
	}
	if e.isNew || e.pendingRemove {
		t.Error("a loaded step must be neither new nor pending removal")
	}
	if e.orig != s {
		t.Error("orig must point at the loaded step — Update and Delete both need it")
	}

	want := gosmo.JobStepRequest{
		Name: s.Name, Subsystem: s.Subsystem, Command: s.Command, Database: s.Database,
		OnSuccessAction: s.OnSuccessAction, OnSuccessStepID: s.OnSuccessStepID,
		OnFailAction: s.OnFailAction, OnFailStepID: s.OnFailStepID,
		RetryAttempts: s.RetryAttempts, RetryInterval: s.RetryInterval,
		OutputFileName: s.OutputFileName,
	}
	if got := e.request(); got != want {
		t.Errorf("request() =\n %+v\nwant %+v", got, want)
	}
}

// A field request() sends but changed() ignores can be edited but never saved.
// Also checked by reflection: every field with an orig<Name> mirror must be
// compared.
func TestJobStepChangedWatchesEveryMirroredField(t *testing.T) {
	edits := map[string]func(*jobStepEdit){
		"name":            func(e *jobStepEdit) { e.name = "other" },
		"database":        func(e *jobStepEdit) { e.database = "msdb" },
		"command":         func(e *jobStepEdit) { e.command = "SELECT 1" },
		"onSuccessAction": func(e *jobStepEdit) { e.onSuccessAction = 1 },
		"onSuccessStepID": func(e *jobStepEdit) { e.onSuccessStepID = 9 },
		"onFailAction":    func(e *jobStepEdit) { e.onFailAction = 2 },
		"onFailStepID":    func(e *jobStepEdit) { e.onFailStepID = 9 },
		"retryAttempts":   func(e *jobStepEdit) { e.retryAttempts = 99 },
		"retryInterval":   func(e *jobStepEdit) { e.retryInterval = 99 },
		"outputFileName":  func(e *jobStepEdit) { e.outputFileName = "" },
	}

	// Every mirrored field must be in the table.
	v := reflect.ValueOf(jobStepEdit{})
	mirrored := map[string]bool{}
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if strings.HasPrefix(name, "orig") {
			continue
		}
		if _, ok := v.Type().FieldByName("orig" + strings.ToUpper(name[:1]) + name[1:]); ok {
			mirrored[name] = true
			if _, covered := edits[name]; !covered {
				t.Errorf("%s has an orig mirror but no case here — extend this test and check changed() compares it", name)
			}
		}
	}
	for name := range edits {
		if !mirrored[name] {
			t.Errorf("%s has no orig mirror — changed() cannot compare it, so it is written on every OK", name)
		}
	}

	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			e := jobStepEditFromStep(sampleJobStep())
			edit(e)
			if !e.changed() {
				t.Errorf("editing %s left changed() false — the edit is shown on the page but never written", name)
			}
		})
	}
}

// Blanking the output file is a change (gosmo sends @output_file_name so empty
// clears it); an empty Database means "leave it". The two empties mean opposite
// things.
func TestJobStepBlankingTheOutputFileIsAChange(t *testing.T) {
	e := jobStepEditFromStep(sampleJobStep())
	e.outputFileName = ""
	if !e.changed() {
		t.Fatal("clearing the output file did not register as a change — the step would keep writing to the old path")
	}
	if got := e.request().OutputFileName; got != "" {
		t.Errorf("request().OutputFileName = %q, want empty so gosmo nulls the column", got)
	}
}

// editable keeps non-T-SQL steps out of updates; writing them back would run
// their command as T-SQL.
func TestJobStepEditableIsTSQLOrNew(t *testing.T) {
	for _, tc := range []struct {
		subsystem string
		isNew     bool
		want      bool
	}{
		{tsqlSubsystem, false, true},
		{"CmdExec", false, false},
		{"PowerShell", false, false},
		{"SSIS", false, false},
		// New steps are T-SQL and editable.
		{"", true, true},
		{"CmdExec", true, true},
	} {
		e := &jobStepEdit{subsystem: tc.subsystem, isNew: tc.isNew}
		if got := e.editable(); got != tc.want {
			t.Errorf("editable() for subsystem %q isNew=%v = %v, want %v", tc.subsystem, tc.isNew, got, tc.want)
		}
	}
}

// A New step has no subsystem and sp_add_jobstep rejects an empty one, so
// request() defaults it; a loaded step's subsystem must survive.
func TestJobStepRequestSubsystemDefaultsToTSQLOnly(t *testing.T) {
	if got := (&jobStepEdit{isNew: true}).request().Subsystem; got != tsqlSubsystem {
		t.Errorf("a new step's request Subsystem = %q, want %q", got, tsqlSubsystem)
	}
	if got := (&jobStepEdit{subsystem: "CmdExec"}).request().Subsystem; got != "CmdExec" {
		t.Errorf("request() rewrote an existing subsystem to %q — an update would change the step's type", got)
	}
}

func TestStepNumberText(t *testing.T) {
	if got := stepNumberText(&jobStepEdit{isNew: true}); got != "New" {
		t.Errorf("a pending step shows %q, want \"New\" — its step_id is not assigned until Apply", got)
	}
	if got := stepNumberText(&jobStepEdit{stepID: 3}); got != "3" {
		t.Errorf("stepNumberText = %q, want \"3\"", got)
	}
}

// jobStepOnActionItems' index is the action code minus one. The two halves
// cancel in a round trip, so a reordered list would store the wrong action
// silently; naming the pairs catches it. Codes per
// gosmo.JobStepRequest.OnSuccessAction.
func TestJobStepOnActionLabelsMatchTheirCodes(t *testing.T) {
	want := []struct {
		label string
		code  int
	}{
		{"Quit the job reporting success", 1},
		{"Quit the job reporting failure", 2},
		{"Go to the next step", 3},
		{"Go to step...", 4},
	}
	if len(jobStepOnActionItems) != len(want) {
		t.Fatalf("jobStepOnActionItems has %d entries, want %d — the index-to-code mapping is positional", len(jobStepOnActionItems), len(want))
	}
	for i, w := range want {
		if jobStepOnActionItems[i] != w.label {
			t.Errorf("index %d is %q, want %q", i, jobStepOnActionItems[i], w.label)
		}
		// The page's arithmetic.
		if code := i + 1; code != w.code {
			t.Errorf("%q sits at index %d, so it writes action code %d, want %d", w.label, i, code, w.code)
		}
	}
}

// The sentinels are matched by value and mean different things, so they must
// differ.
func TestDatabaseSentinelsAreDistinct(t *testing.T) {
	if unchangedDatabaseItem == defaultDatabaseItem {
		t.Fatal("the two database sentinels are identical — a Steps page edit and a New Job step would take the same branch")
	}
}

// loadedStep is an existing, unmodified step.
func loadedStep(stepID int, name string) *jobStepEdit {
	return jobStepEditFromStep(&gosmo.JobStep{
		StepID: stepID, Name: name, Subsystem: tsqlSubsystem, Command: "SELECT 1",
	})
}

func stepNames(edits []*jobStepEdit) []string {
	out := make([]string, len(edits))
	for i, e := range edits {
		out[i] = e.name
	}
	return out
}

// Deletes run in descending step_id order: sp_delete_jobstep renumbers later
// steps, so ascending would fail or delete the wrong step (apply re-fetches by
// ID, so the wrong-step case is real).
func TestJobStepDeletesRunHighestStepIDFirst(t *testing.T) {
	one, two, three, four := loadedStep(1, "one"), loadedStep(2, "two"), loadedStep(3, "three"), loadedStep(4, "four")
	// Marked out of order, as arbitrary clicks leave them.
	two.pendingRemove = true
	four.pendingRemove = true
	one.pendingRemove = true

	plan := planJobStepWrites([]*jobStepEdit{one, two, three, four})

	if got, want := stepNames(plan.deletes), []string{"four", "two", "one"}; !slices.Equal(got, want) {
		t.Errorf("delete order = %v, want %v (descending step_id)", got, want)
	}
}

// The three passes are disjoint and each edit lands in exactly one.
func TestPlanJobStepWritesSortsEveryEditIntoOnePass(t *testing.T) {
	unchanged := loadedStep(1, "unchanged")

	edited := loadedStep(2, "edited")
	edited.command = "SELECT 2"

	removed := loadedStep(3, "removed")
	removed.pendingRemove = true

	// A non-T-SQL step isn't changed() anyway; the plan refuses it regardless.
	readOnly := jobStepEditFromStep(&gosmo.JobStep{StepID: 4, Name: "ps", Subsystem: "PowerShell", Command: "Get-Date"})
	readOnly.command = "rm -rf /"

	added := &jobStepEdit{isNew: true, subsystem: tsqlSubsystem, name: "added"}

	// Added then deleted: nothing to add or delete.
	addedThenRemoved := &jobStepEdit{isNew: true, pendingRemove: true, subsystem: tsqlSubsystem, name: "transient"}

	plan := planJobStepWrites([]*jobStepEdit{unchanged, edited, removed, readOnly, added, addedThenRemoved})

	if got, want := stepNames(plan.updates), []string{"edited"}; !slices.Equal(got, want) {
		t.Errorf("updates = %v, want %v", got, want)
	}
	if got, want := stepNames(plan.deletes), []string{"removed"}; !slices.Equal(got, want) {
		t.Errorf("deletes = %v, want %v", got, want)
	}
	if got, want := stepNames(plan.adds), []string{"added"}; !slices.Equal(got, want) {
		t.Errorf("adds = %v, want %v", got, want)
	}
}

// A changed() read-only step must still be refused; the guard covers the day
// commitCurrent stops preventing it.
func TestPlanJobStepWritesRefusesAChangedNonTSQLStep(t *testing.T) {
	e := jobStepEditFromStep(&gosmo.JobStep{StepID: 1, Name: "ssis", Subsystem: "SSIS", Command: "pkg.dtsx"})
	e.command = "DROP TABLE dbo.orders"
	if !e.changed() {
		t.Fatal("setup: the step should report changed()")
	}
	if plan := planJobStepWrites([]*jobStepEdit{e}); len(plan.updates) != 0 {
		t.Error("a non-T-SQL step reached the update pass — sp_update_jobstep would hand its command to the query processor as T-SQL")
	}
}

// Nothing pending means no statements.
func TestPlanJobStepWritesIsEmptyWhenNothingChanged(t *testing.T) {
	plan := planJobStepWrites([]*jobStepEdit{loadedStep(1, "one"), loadedStep(2, "two")})
	if n := len(plan.updates) + len(plan.deletes) + len(plan.adds); n != 0 {
		t.Errorf("%d pending writes for an untouched page, want 0", n)
	}
}
