package tui

import (
	"reflect"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

const mb = int64(1024) // one megabyte, in the KB units every field here uses

// sampleDataFile is a fully-populated ROWS file, so a field dropped anywhere
// in the edit path shows up as a zero rather than coinciding with the
// sample's value.
func sampleDataFile() *gosmo.DatabaseFileInfo {
	return &gosmo.DatabaseFileInfo{
		Name: "AdventureWorks_Data", Type: "ROWS", FileGroup: "PRIMARY",
		PhysicalName:  `C:\data\AdventureWorks.mdf`,
		SizeKB:        200 * mb,
		GrowthKB:      64 * mb,
		GrowthPercent: 0,
		MaxSizeKB:     -1,
	}
}

// A file loaded and not touched must report itself unchanged: the Files page
// asks changed() both for GridRow.DirtyFn and for whether to ALTER this
// particular file, so a file that reported itself dirty on load would be
// rewritten through ALTER DATABASE ... MODIFY FILE on every OK.
func TestFileEditFromInfoIsUnchanged(t *testing.T) {
	e := fileEditFromInfo(sampleDataFile())
	if e.changed() {
		t.Error("a freshly loaded file reports changed() — every OK would ALTER it")
	}
	if e.isNew || e.pendingRemove {
		t.Error("a loaded file must be neither new nor pending removal")
	}
	if e.origName != "AdventureWorks_Data" {
		t.Errorf("origName = %q — apply addresses the file by it, so a rename must not overwrite it", e.origName)
	}
	if got := e.modify(); got != (gosmo.FileModify{}) {
		t.Errorf("modify() = %+v for an untouched file, want the zero FileModify so gosmo emits no statement", got)
	}
}

// changed() decides both whether the page is dirty and whether this file is
// ALTERed, so a field it doesn't compare is one the user can edit and never
// save. The pairing is checked by reflection as well as by the table: every
// field with an orig<Name> mirror is one changed() is meant to watch.
func TestFileEditChangedWatchesEveryMirroredField(t *testing.T) {
	edits := map[string]func(*fileEdit){
		"name":            func(e *fileEdit) { e.name = "renamed" },
		"sizeKB":          func(e *fileEdit) { e.sizeKB = 500 * mb },
		"isPercentGrowth": func(e *fileEdit) { e.isPercentGrowth = true },
		"growthKB":        func(e *fileEdit) { e.growthKB = 128 * mb },
		"growthPercent":   func(e *fileEdit) { e.growthPercent = 10 },
		"maxSizeKB":       func(e *fileEdit) { e.maxSizeKB = 1000 * mb },
	}

	v := reflect.ValueOf(fileEdit{})
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
			t.Errorf("%s has no orig mirror — changed() cannot compare it", name)
		}
	}

	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			e := fileEditFromInfo(sampleDataFile())
			edit(e)
			if !e.changed() {
				t.Errorf("editing %s left changed() false — the edit is shown on the page but never written", name)
			}
		})
	}
}

// fileType, fileGroup and path have no orig mirror on purpose: MODIFY FILE
// cannot retype a file, move it between filegroups, or move it on disk. An
// edit to one of them is not something this page can write, and counting it
// as a change would send an ALTER carrying nothing — gosmo builds an empty
// statement and the user sees a successful OK that did nothing.
func TestFileEditIgnoresTheFieldsModifyFileCannotChange(t *testing.T) {
	for name, edit := range map[string]func(*fileEdit){
		"fileType":  func(e *fileEdit) { e.fileType = logFileType },
		"fileGroup": func(e *fileEdit) { e.fileGroup = "SECONDARY" },
		"path":      func(e *fileEdit) { e.path = `D:\elsewhere.mdf` },
	} {
		e := fileEditFromInfo(sampleDataFile())
		edit(e)
		if e.changed() {
			t.Errorf("editing %s reported a change — MODIFY FILE cannot write it, so the ALTER would carry nothing", name)
		}
	}
}

// modify() must carry only what changed. gosmo reads a zero field as "leave
// this property alone", so an unconditional assignment is not the harmless
// simplification it looks like — SIZE especially, which MODIFY FILE treats
// as a grow-to target and rejects below the file's current size.
func TestFileEditModifyCarriesOnlyWhatChanged(t *testing.T) {
	t.Run("a growth-only edit sends no SIZE", func(t *testing.T) {
		e := fileEditFromInfo(sampleDataFile())
		e.growthKB = 128 * mb

		m := e.modify()
		if m.SizeKB != 0 {
			t.Errorf("SizeKB = %d for a growth-only edit, want 0 — a resent SIZE fails once the file has grown past its recorded size", m.SizeKB)
		}
		if m.NewName != "" {
			t.Errorf("NewName = %q for a growth-only edit, want empty", m.NewName)
		}
		if m.MaxSizeKB != 0 {
			t.Errorf("MaxSizeKB = %d for a growth-only edit, want 0", m.MaxSizeKB)
		}
		if m.GrowthKB != 128*mb {
			t.Errorf("GrowthKB = %d, want %d", m.GrowthKB, 128*mb)
		}
	})

	// The other direction: a real size change must actually be sent, or
	// growing a file through this page does nothing.
	t.Run("a size change is carried as SIZE", func(t *testing.T) {
		e := fileEditFromInfo(sampleDataFile())
		e.sizeKB = 500 * mb

		m := e.modify()
		if m.SizeKB != 500*mb {
			t.Errorf("SizeKB = %d, want %d", m.SizeKB, 500*mb)
		}
		if m.NewName != "" || m.GrowthKB != 0 || m.GrowthPercent != 0 || m.MaxSizeKB != 0 {
			t.Errorf("a size-only edit carried more than SIZE: %+v", m)
		}
	})

	t.Run("a rename is carried as NEWNAME", func(t *testing.T) {
		e := fileEditFromInfo(sampleDataFile())
		e.name = "AW_Data"
		m := e.modify()
		if m.NewName != "AW_Data" {
			t.Errorf("NewName = %q, want AW_Data", m.NewName)
		}
		if m.SizeKB != 0 || m.GrowthKB != 0 || m.GrowthPercent != 0 || m.MaxSizeKB != 0 {
			t.Errorf("a rename-only edit carried more than NEWNAME: %+v", m)
		}
	})

	// The two growth fields are mutually exclusive and gosmo lets
	// GrowthPercent win when both are set, so switching the radio must send
	// the new kind alone — carrying the old kind's value too would leave the
	// losing half deciding nothing while still being sent.
	t.Run("switching to percent growth sends only the percent", func(t *testing.T) {
		e := fileEditFromInfo(sampleDataFile())
		e.isPercentGrowth, e.growthPercent = true, 10

		m := e.modify()
		if m.GrowthPercent != 10 {
			t.Errorf("GrowthPercent = %d, want 10", m.GrowthPercent)
		}
		if m.GrowthKB != 0 {
			t.Errorf("GrowthKB = %d alongside a percent growth, want 0", m.GrowthKB)
		}
	})

	t.Run("switching to megabyte growth sends only the KB", func(t *testing.T) {
		info := sampleDataFile()
		info.GrowthKB, info.GrowthPercent, info.IsPercentGrowth = 0, 10, true
		e := fileEditFromInfo(info)
		e.isPercentGrowth, e.growthKB = false, 64*mb

		m := e.modify()
		if m.GrowthKB != 64*mb {
			t.Errorf("GrowthKB = %d, want %d", m.GrowthKB, 64*mb)
		}
		if m.GrowthPercent != 0 {
			t.Errorf("GrowthPercent = %d alongside a KB growth, want 0 — gosmo lets the percent win", m.GrowthPercent)
		}
	})

	// -1 is gosmo's UNLIMITED, and it is a value rather than an absence, so
	// capping an unlimited file and uncapping a capped one both have to
	// survive.
	t.Run("unlimited round-trips as -1", func(t *testing.T) {
		info := sampleDataFile()
		info.MaxSizeKB = 500 * mb
		e := fileEditFromInfo(info)
		e.maxSizeKB = -1
		if m := e.modify(); m.MaxSizeKB != -1 {
			t.Errorf("MaxSizeKB = %d when lifting a cap, want -1 (UNLIMITED)", m.MaxSizeKB)
		}

		e = fileEditFromInfo(sampleDataFile()) // starts unlimited
		e.maxSizeKB = 500 * mb
		if m := e.modify(); m.MaxSizeKB != 500*mb {
			t.Errorf("MaxSizeKB = %d when capping, want %d", m.MaxSizeKB, 500*mb)
		}
	})
}

// spec() is the add side, where there is no previous value to leave alone,
// so every field is sent. The filegroup is the exception that matters: a LOG
// file belongs to none, and the page's dropdown shows "(not applicable)" for
// one rather than a real name.
func TestFileEditSpecForANewFile(t *testing.T) {
	t.Run("a data file carries its filegroup", func(t *testing.T) {
		e := &fileEdit{
			isNew: true, name: "AW_Data2", fileType: "ROWS", fileGroup: "SECONDARY",
			path: `D:\data\AW2.ndf`, sizeKB: 100 * mb, growthKB: 64 * mb, maxSizeKB: -1,
		}
		want := gosmo.DatabaseFileSpec{
			Name: "AW_Data2", Type: "ROWS", FileGroup: "SECONDARY", Path: `D:\data\AW2.ndf`,
			SizeKB: 100 * mb, GrowthKB: 64 * mb, MaxSizeKB: -1,
		}
		if got := e.spec(); got != want {
			t.Errorf("spec() =\n %+v\nwant %+v", got, want)
		}
	})

	t.Run("a log file carries no filegroup", func(t *testing.T) {
		e := &fileEdit{
			isNew: true, name: "AW_Log2", fileType: logFileType, fileGroup: "PRIMARY",
			path: `D:\log\AW2.ldf`, sizeKB: 50 * mb, growthKB: 32 * mb, maxSizeKB: -1,
		}
		if got := e.spec().FileGroup; got != "" {
			t.Errorf("spec().FileGroup = %q for a LOG file, want empty — a log file belongs to no filegroup", got)
		}
	})

	t.Run("percent growth excludes KB growth", func(t *testing.T) {
		e := &fileEdit{isNew: true, name: "x", fileType: "ROWS", isPercentGrowth: true, growthPercent: 10, growthKB: 64 * mb}
		spec := e.spec()
		if spec.GrowthPercent != 10 || spec.GrowthKB != 0 {
			t.Errorf("spec() growth = %d%% / %dKB, want 10%% and no KB", spec.GrowthPercent, spec.GrowthKB)
		}
	})
}

func TestGrowthAndMaxSizeText(t *testing.T) {
	for _, tc := range []struct {
		isPercent     bool
		growthKB      int64
		growthPercent int
		want          string
	}{
		{false, 64 * mb, 0, "64 MB"},
		{true, 0, 10, "10%"},
		// The losing half is still carried on the edit, so the text must be
		// chosen by the flag rather than by whichever value is non-zero.
		{true, 64 * mb, 10, "10%"},
		// Autogrowth off, in each unit — and the flag still decides which
		// zero is being read, so a stale losing half cannot make it look set.
		{false, 0, 10, "None"},
		{true, 64 * mb, 0, "None"},
	} {
		if got := growthText(tc.isPercent, tc.growthKB, tc.growthPercent); got != tc.want {
			t.Errorf("growthText(%v, %d, %d) = %q, want %q", tc.isPercent, tc.growthKB, tc.growthPercent, got, tc.want)
		}
	}

	for _, tc := range []struct {
		maxSizeKB int64
		want      string
	}{
		{-1, "Unlimited"},
		{500 * mb, "500 MB"},
		{0, "0 MB"},
	} {
		if got := maxSizeText(tc.maxSizeKB); got != tc.want {
			t.Errorf("maxSizeText(%d) = %q, want %q", tc.maxSizeKB, got, tc.want)
		}
	}
}

// TestTurningAutogrowthOffIsNotAZeroGrowthAmount is the whole point of
// fileEdit.growthOff and gosmo's FileModify.DisableGrowth. A growth of zero
// is how SQL Server records autogrowth-off, and it is also FileModify's
// "leave FILEGROWTH alone", so the two have to be told apart before the
// statement is built. They were not: the ALTER came out with no FILEGROWTH
// clause, and where growth was the only edit the whole statement collapsed to
// the bare identifying NAME — gosmo returns "" for that and AlterFileContext
// returns nil, so Apply reported success and the file went on growing.
//
// The statement itself is asserted a level up, in
// TestFilesPageWritesTheAutogrowthItWasGiven, which runs the page's real
// apply closure; this pins the request the page hands gosmo.
func TestTurningAutogrowthOffIsNotAZeroGrowthAmount(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*fileEdit)
	}{
		{"megabytes", func(e *fileEdit) { e.growthKB = 0 }},
		{"percent", func(e *fileEdit) {
			e.isPercentGrowth, e.growthPercent = true, 0
			e.origIsPercentGrowth, e.origGrowthPercent = true, 10
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := fileEditFromInfo(sampleDataFile())
			tc.edit(e)
			if !e.changed() {
				t.Fatal("growth set to zero is not reported as a change")
			}
			m := e.modify()
			if !m.DisableGrowth {
				t.Error("DisableGrowth is false: the ALTER will carry no FILEGROWTH clause")
			}
			if m.GrowthKB != 0 || m.GrowthPercent != 0 {
				t.Errorf("a growth amount is carried alongside DisableGrowth: %+v", m)
			}
		})
	}
}

// TestAGrowthAmountStillTravelsWhenItIsNotZero is the other half: growthOff
// must not swallow an ordinary edit. Reading every zero as "off" would write
// autogrowth off on a file whose growth the user never touched.
func TestAGrowthAmountStillTravelsWhenItIsNotZero(t *testing.T) {
	e := fileEditFromInfo(sampleDataFile())
	e.growthKB = 128 * mb
	m := e.modify()
	if m.DisableGrowth {
		t.Error("DisableGrowth is set for a non-zero growth")
	}
	if m.GrowthKB != 128*mb {
		t.Errorf("GrowthKB = %d, want %d", m.GrowthKB, 128*mb)
	}

	// An edit that leaves growth alone must carry neither — not the amount,
	// and not an accidental "off" read off the untouched zero of the other unit.
	resize := fileEditFromInfo(sampleDataFile())
	resize.sizeKB = 512 * mb
	if m := resize.modify(); m.DisableGrowth || m.GrowthKB != 0 || m.GrowthPercent != 0 {
		t.Errorf("a resize carried growth: %+v", m)
	}
}

// TestANewFileCanBeCreatedWithAutogrowthOff covers the ADD FILE half, where
// the same zero means the same thing and gosmo would otherwise omit the
// clause and let the server's default growth apply.
func TestANewFileCanBeCreatedWithAutogrowthOff(t *testing.T) {
	e := &fileEdit{isNew: true, name: "appdb_2", fileType: "ROWS", fileGroup: "PRIMARY", path: `C:\data\appdb_2.ndf`, sizeKB: 8 * mb, maxSizeKB: -1}
	if spec := e.spec(); !spec.DisableGrowth {
		t.Errorf("a new file with growth 0 did not ask for autogrowth off: %+v", spec)
	}
	e.growthKB = 64 * mb
	if spec := e.spec(); spec.DisableGrowth || spec.GrowthKB != 64*mb {
		t.Errorf("a new file with a real growth got DisableGrowth: %+v", spec)
	}
}
