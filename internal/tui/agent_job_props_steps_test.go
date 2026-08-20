package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// sampleJobStep is a fully-populated T-SQL step, so a field dropped
// anywhere in the edit/request path shows up as a zero rather than
// coinciding with the sample's value.
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

// jobStepEditFromStep seeds both the live fields and their orig mirrors, so
// a step loaded and not touched must report itself unchanged — the Steps
// page's apply skips on !changed(), and a step that reported itself dirty
// on load would be rewritten through sp_update_jobstep on every OK.
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

// changed() gates the whole update pass, so a field that request() sends
// but changed() doesn't watch is one the user can edit and never save —
// silently, with the page showing the new value and the server keeping the
// old. The pairing is checked by reflection as well as by the table: every
// field of jobStepEdit that has an orig<Name> mirror is one changed() is
// meant to compare, so adding a field and its mirror without extending
// changed() fails here rather than in production.
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

	// Every mirrored field must be in the table above.
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

// Blanking the output file is a real edit, not a no-op: gosmo sends
// @output_file_name on every update precisely so an empty value clears the
// column. Database is the opposite — empty means "leave the step's own
// alone" — which is why the dropdown's sentinel maps to "" rather than to a
// database name. The two empty strings mean opposite things, so the one
// that must still register as a change is pinned on its own.
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

// editable is what keeps a non-T-SQL step out of the update pass entirely.
// JobStepRequest carries a subsystem, so writing a CmdExec or PowerShell
// step back through this page's T-SQL-only form would hand its old command
// text to the query processor as T-SQL.
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
		// New steps this page creates are always T-SQL, and are editable
		// before their subsystem field is consulted at all.
		{"", true, true},
		{"CmdExec", true, true},
	} {
		e := &jobStepEdit{subsystem: tc.subsystem, isNew: tc.isNew}
		if got := e.editable(); got != tc.want {
			t.Errorf("editable() for subsystem %q isNew=%v = %v, want %v", tc.subsystem, tc.isNew, got, tc.want)
		}
	}
}

// A step created by the New button carries no subsystem of its own, and
// sp_add_jobstep would reject an empty @subsystem. request() supplies the
// default; a loaded step's own subsystem must survive untouched, or an
// update would rewrite it.
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

// jobStepOnActionItems is a label list whose *index* is the msdb action
// code minus one — commitCurrent writes Selected()+1 and
// syncFieldsFromSelection reads back SetSelected(action-1). Nothing else
// records the mapping, and the two halves cancel each other out, so a
// reordered list round-trips cleanly while every dropdown reads wrong: the
// user picks "Quit the job reporting success" and the step is stored as
// "quit reporting failure". Naming the pairs is the only thing that catches
// it. Codes are sp_update_jobstep's, documented on
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
		// The page's own arithmetic, stated once here.
		if code := i + 1; code != w.code {
			t.Errorf("%q sits at index %d, so it writes action code %d, want %d", w.label, i, code, w.code)
		}
	}
}

// The two sentinels are load-bearing text, matched by value in
// pickedDatabase (Steps) and its New Job counterpart. They are also what
// the user sees, and they mean different things, so they must not collide.
func TestDatabaseSentinelsAreDistinct(t *testing.T) {
	if unchangedDatabaseItem == defaultDatabaseItem {
		t.Fatal("the two database sentinels are identical — a Steps page edit and a New Job step would take the same branch")
	}
}

// loadedStep is an existing, unmodified step at stepID.
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

// The delete pass runs in descending step_id order because sp_delete_jobstep
// renumbers every later step down by one. Ascending order is the bug: delete
// step 2 of {1,2,3,4} and the old step 4 becomes 3, so the next delete aimed
// at 4 either fails with "not found" or — once a later step has slid into
// that number — succeeds against a step the user never selected. apply
// re-fetches the step list by ID, so the wrong-step case is real rather than
// hypothetical.
func TestJobStepDeletesRunHighestStepIDFirst(t *testing.T) {
	one, two, three, four := loadedStep(1, "one"), loadedStep(2, "two"), loadedStep(3, "three"), loadedStep(4, "four")
	// Marked for removal out of order, the way clicking Delete on rows the
	// user happened to pick leaves them.
	two.pendingRemove = true
	four.pendingRemove = true
	one.pendingRemove = true

	plan := planJobStepWrites([]*jobStepEdit{one, two, three, four})

	if got, want := stepNames(plan.deletes), []string{"four", "two", "one"}; !slices.Equal(got, want) {
		t.Errorf("delete order = %v, want %v (descending step_id)", got, want)
	}
}

// The three passes are disjoint and each edit lands in exactly one, so the
// classification is worth stating whole rather than one predicate at a time.
func TestPlanJobStepWritesSortsEveryEditIntoOnePass(t *testing.T) {
	unchanged := loadedStep(1, "unchanged")

	edited := loadedStep(2, "edited")
	edited.command = "SELECT 2"

	removed := loadedStep(3, "removed")
	removed.pendingRemove = true

	// A non-T-SQL step cannot be written back through this page at all —
	// commitCurrent never copies the form onto it, so it should not be
	// changed() either; the plan refuses it regardless, which is the point
	// of restating !editable here.
	readOnly := jobStepEditFromStep(&gosmo.JobStep{StepID: 4, Name: "ps", Subsystem: "PowerShell", Command: "Get-Date"})
	readOnly.command = "rm -rf /"

	added := &jobStepEdit{isNew: true, subsystem: tsqlSubsystem, name: "added"}

	// Added and then deleted in the same sitting: never reached the server,
	// so there is nothing to add and nothing to delete.
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

// A read-only step that somehow reports changed() must still be refused.
// This is the case the !editable guard exists for: it is implied today by
// commitCurrent never writing to such a step, and stops being implied the
// moment anything else does.
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

// Nothing pending means no statements at all, so opening the Steps page and
// pressing OK is a no-op rather than a rewrite of every step.
func TestPlanJobStepWritesIsEmptyWhenNothingChanged(t *testing.T) {
	plan := planJobStepWrites([]*jobStepEdit{loadedStep(1, "one"), loadedStep(2, "two")})
	if n := len(plan.updates) + len(plan.deletes) + len(plan.adds); n != 0 {
		t.Errorf("%d pending writes for an untouched page, want 0", n)
	}
}
