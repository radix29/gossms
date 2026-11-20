package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/controls"
)

// A Select row whose list begins with a synthetic "(None)"/"<all databases>"
// entry must be built by searching that whole list, not the bare names with a
// 1+ offset. With the offset, indexOf's not-found 0 becomes 1 — the first
// *real* item — so an alert whose category has since been deleted displays as
// though it belonged to whichever category sorts first, and applying the page
// silently moves it there.
//
// The six pages that used this shape now build the row with propsheet.SelectPreserving
// instead, which shows the deleted category's own name (see
// prop_select_preserving_pages_test.go). This stays because indexOf still
// builds the fixed-vocabulary rows and the New-X dialogs' defaults, where the
// value is one of the list by construction.
func TestIndexOfSentinelListFallsBackToSentinel(t *testing.T) {
	names := []string{"Alpha", "Beta", "Gamma"}
	items := append([]string{noneItem}, names...)

	t.Run("value the list does not contain selects the sentinel", func(t *testing.T) {
		if got := indexOf(items, "deleted-category"); got != 0 {
			t.Errorf("indexOf(items, %q) = %d (%q), want 0 (%q)",
				"deleted-category", got, items[got], noneItem)
		}
	})

	t.Run("empty value selects the sentinel", func(t *testing.T) {
		if got := indexOf(items, ""); got != 0 {
			t.Errorf("indexOf(items, \"\") = %d (%q), want 0 (%q)", got, items[got], noneItem)
		}
	})

	// The offset form this replaced: 1 + indexOf(names, missing) == 1, which
	// is "Alpha". Pinned so the old shape can't come back unnoticed.
	t.Run("the offset form it replaced picks the wrong item", func(t *testing.T) {
		if buggy := 1 + indexOf(names, "deleted-category"); items[buggy] != "Alpha" {
			t.Fatalf("expected the 1+offset form to land on Alpha, got %q", items[buggy])
		}
	})

	t.Run("a real value still resolves to itself", func(t *testing.T) {
		for i, want := range items {
			if got := indexOf(items, want); got != i {
				t.Errorf("indexOf(items, %q) = %d, want %d", want, got, i)
			}
		}
	})
}

// allDatabasesItem heads the alert Database row the same way noneItem heads
// the category rows, so it needs the same fallback.
func TestIndexOfAllDatabasesSentinel(t *testing.T) {
	items := append([]string{allDatabasesItem}, "master", "msdb")
	if got := indexOf(items, "dropped-db"); got != 0 {
		t.Errorf("indexOf(items, %q) = %d (%q), want 0 (%q)",
			"dropped-db", got, items[got], allDatabasesItem)
	}
	if got := indexOf(items, "msdb"); got != 2 {
		t.Errorf("indexOf(items, \"msdb\") = %d, want 2", got)
	}
}

// indexOfOK is for a list with no sentinel to absorb a miss, where indexOf's
// 0 would render the first real option as though the server had reported it.
func TestIndexOfOKReportsAMiss(t *testing.T) {
	items := []string{"NONE", "ROW", "PAGE"}

	if i, ok := indexOfOK(items, "COLUMNSTORE"); ok || i != 0 {
		t.Errorf(`indexOfOK(items, "COLUMNSTORE") = %d, %v; want 0, false`, i, ok)
	}
	if i, ok := indexOfOK(items, ""); ok || i != 0 {
		t.Errorf(`indexOfOK(items, "") = %d, %v; want 0, false`, i, ok)
	}
	for want, v := range items {
		if i, ok := indexOfOK(items, v); !ok || i != want {
			t.Errorf("indexOfOK(items, %q) = %d, %v; want %d, true", v, i, ok, want)
		}
	}
}

// A database at a level the dropdown doesn't list must show its real level,
// not fall back to 100 — which is itself a valid level and so reads as fact.
func TestCompatItemsForInsertsAnUnlistedLevel(t *testing.T) {
	t.Run("a level below the list is inserted first", func(t *testing.T) {
		items := compatItemsFor(90, 0)
		if items[0] != "90" {
			t.Fatalf("compatItemsFor(90, 0) = %v, want 90 first", items)
		}
		if got := indexOf(items, "90"); got != 0 {
			t.Errorf("indexOf(items, \"90\") = %d, want 0", got)
		}
	})

	t.Run("a level above the list is inserted last", func(t *testing.T) {
		items := compatItemsFor(180, 0)
		if items[len(items)-1] != "180" {
			t.Fatalf("compatItemsFor(180, 0) = %v, want 180 last", items)
		}
	})

	t.Run("a listed level leaves the list alone", func(t *testing.T) {
		for _, level := range []int{100, 150, 170} {
			if got := compatItemsFor(level, 0); len(got) != len(compatLevelItems) {
				t.Errorf("compatItemsFor(%d, 0) = %v, want the base list unchanged", level, got)
			}
		}
	})

	t.Run("an unpopulated level adds nothing", func(t *testing.T) {
		if got := compatItemsFor(0, 0); len(got) != len(compatLevelItems) {
			t.Errorf("compatItemsFor(0, 0) = %v, want the base list unchanged", got)
		}
	})

	// The base list must not be mutated by an insert — both call sites read it.
	t.Run("the base list is never mutated", func(t *testing.T) {
		compatItemsFor(90, 0)
		compatItemsFor(180, 0)
		if compatLevelItems[0] != "100" || len(compatLevelItems) != 8 {
			t.Fatalf("compatLevelItems was mutated: %v", compatLevelItems)
		}
	})
}

// A6: the dropdown must not offer a level the server rejects. On 2017 the
// fixed list offered 150, 160 and 170, and the server answered "Valid values
// of the database compatibility level are 100, 110, 120, 130 or 140. (15048)"
// on both New Database and Database Properties.
func TestCompatItemsForCapsTheListAtTheServerMajor(t *testing.T) {
	t.Run("each major caps at its own level", func(t *testing.T) {
		for major, want := range map[int]string{13: "130", 14: "140", 15: "150", 16: "160", 17: "170"} {
			items := compatItemsFor(0, major)
			if got := items[len(items)-1]; got != want {
				t.Errorf("major %d offers up to %q, want %q (%v)", major, got, want, items)
			}
		}
	})

	// The one case the cap must not swallow: a database restored from a newer
	// instance still has to display the level it really is, even though the
	// server cannot be asked to set it.
	t.Run("a current level above the cap is still shown", func(t *testing.T) {
		items := compatItemsFor(160, 14)
		if items[len(items)-1] != "160" {
			t.Fatalf("compatItemsFor(160, 14) = %v, want 160 present and last", items)
		}
		if got := indexOf(items, "160"); got != len(items)-1 {
			t.Errorf("indexOf(items, \"160\") = %d, want %d", got, len(items)-1)
		}
	})

	// An unread version is treated as newest, the convention gosmo's own
	// version gates follow — degrading to the oldest list would hide levels a
	// modern server does accept.
	t.Run("an unknown major is uncapped", func(t *testing.T) {
		for _, major := range []int{0, 99} {
			if got := compatItemsFor(0, major); len(got) != len(compatLevelItems) {
				t.Errorf("major %d = %v, want the full list", major, got)
			}
		}
	})

	t.Run("the base list is never mutated by a cap", func(t *testing.T) {
		compatItemsFor(160, 13)
		if compatLevelItems[0] != "100" || len(compatLevelItems) != 8 {
			t.Fatalf("compatLevelItems was mutated: %v", compatLevelItems)
		}
	})
}

// A grid the user has dragged a column on keeps that width across redrawGrid.
// DataGrid.SetSource clears the overrides — right for a query grid handed a
// new result set, wrong for a Properties page redrawing its own fixed columns,
// where the drag simply vanishes on the next toggle or refresh.
func TestRedrawGridKeepsDraggedColumnWidths(t *testing.T) {
	headers := []string{"Attached", "Name", "Enabled"}
	rows := [][]string{{"[x]", "Nightly", "True"}, {"[ ]", "Weekly", "False"}}

	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	grid.SetBounds(0, 0, 80, 10)
	grid.SetData(headers, rows)

	grid.SetColumnWidth(1, 40)
	widths := grid.ColumnWidthOverrides()
	if widths[1] != 40 {
		t.Fatalf("column 1 override = %d before the redraw, want 40", widths[1])
	}

	rows[1][0] = "[x]"
	redrawGrid(grid, headers, rows)

	if got := grid.ColumnWidthOverrides(); got[1] != 40 {
		t.Errorf("column 1 override = %d after the redraw, want 40", got[1])
	}
	// The columns the user did not touch stay on their computed default, so a
	// redraw cannot freeze the whole grid at whatever it happened to be.
	if got := grid.ColumnWidthOverrides(); got[0] != 0 || got[2] != 0 {
		t.Errorf("untouched columns picked up overrides: %v", got)
	}
}

// A redraw that reshapes the grid starts clean rather than applying the old
// widths to new columns.
func TestRedrawGridDropsWidthsPastTheNewColumnCount(t *testing.T) {
	grid := controls.NewDataGrid()
	grid.SetBounds(0, 0, 80, 10)
	grid.SetData([]string{"A", "B", "C"}, [][]string{{"1", "2", "3"}})
	grid.SetColumnWidth(2, 30)

	redrawGrid(grid, []string{"A", "B"}, [][]string{{"1", "2"}})

	if got := grid.ColumnWidthOverrides(); len(got) != 2 {
		t.Fatalf("overrides = %v, want two columns' worth", got)
	}
}

// The Platform rows on Server Properties > General and the Object Explorer
// server Details pane must render ServerInfo.Platform, never OSVersion — the
// latter is @@VERSION verbatim, whose first line alone overflows the row and
// clips to a banner fragment ("Microsoft SQL Server 2025 (RTM-GDR) (KB51023").
// Both pages shipped that way.
func TestPlatformTextUsesPlatformNotTheVersionBanner(t *testing.T) {
	banner := "Microsoft SQL Server 2025 (RTM-GDR) (KB5102333) - 17.0.1125.2 (X64)\n" +
		"\tJul 10 2026 ...\n\ton Windows 10 Pro 10.0 <X64>"
	info := &gosmo.ServerInfo{OSVersion: banner, Platform: "Windows"}
	if got := platformText(info); got != "Windows" {
		t.Fatalf("platformText = %q, want %q", got, "Windows")
	}
	info = &gosmo.ServerInfo{OSVersion: banner}
	if got := platformText(info); got != "Unknown" {
		t.Fatalf("platformText with no Platform = %q, want %q", got, "Unknown")
	}
}

// The banner keeps its content but loses its line breaks and tabs: a raw
// newline breaks a DataGrid row, and the Details pane's Show Value popup shows
// whatever the cell holds.
func TestVersionBannerCollapsesToOneLine(t *testing.T) {
	info := &gosmo.ServerInfo{OSVersion: "Microsoft SQL Server 2025 (RTM-GDR) - 17.0.1125.2 (X64) \n\tJun 18 2026 14:38:44 \n\tCopyright (C) 2025 Microsoft Corporation\n\tEnterprise Developer Edition (64-bit) on Windows 10 Pro"}
	got := versionBanner(info)
	if strings.ContainsAny(got, "\n\t\r") {
		t.Errorf("the banner still carries line breaks, which break a grid row: %q", got)
	}
	if !strings.Contains(got, "17.0.1125.2 (X64) Jun 18 2026") {
		t.Errorf("collapsing lost or mangled the text: %q", got)
	}
	if !strings.HasSuffix(got, "on Windows 10 Pro") {
		t.Errorf("the tail naming the host OS was dropped: %q", got)
	}
	if got := versionBanner(&gosmo.ServerInfo{}); got != "Unknown" {
		t.Errorf("an empty banner renders as %q, want %q", got, "Unknown")
	}
}

// mustPropertyRowIndex is what keeps the Details pane's async backfills on the
// rows they name; adding a row above one used to redirect it silently.
func TestMustPropertyRowIndexFindsRowsByLabel(t *testing.T) {
	rows := [][]string{{"Server", "a"}, {"Version", "b"}, {"NUMA Nodes", "Loading..."}}
	if got := mustPropertyRowIndex(rows, "NUMA Nodes"); got != 2 {
		t.Errorf("index = %d, want 2", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("a missing label returned quietly instead of panicking")
		}
	}()
	mustPropertyRowIndex(rows, "Nope")
}

// The two rules the audit-specification pages now share. The page tests at
// both scopes (audit_specification_props_page_test.go,
// database_audit_specification_props_page_test.go) assert the same two
// behaviours through the real loaders; these pin the helpers themselves, so a
// drift shows here rather than as two page failures with no common cause.
func TestAuditSelectRowStandsInForAMissingAudit(t *testing.T) {
	row := auditSelectRow([]string{"HIPAA", "Rollover"}, "HIPAA")
	if got := row.Value(); got != "HIPAA" {
		t.Errorf("a bound audit selects %q", got)
	}

	// An orphan: the bound name is in no list, and the first real audit must
	// not stand in for it — a stray Apply would rebind the specification.
	row = auditSelectRow([]string{"HIPAA", "Rollover"}, "")
	if got := row.Value(); got != missingAuditItem {
		t.Errorf("an orphan selects %q, want the stand-in", got)
	}
	if got := row.Items()[0]; got != missingAuditItem {
		t.Errorf("the stand-in is not the first item: %v", row.Items())
	}
}

func TestUnionSortedKeepsARecordedValueTheServerNoLongerOffers(t *testing.T) {
	got := unionSorted(
		[]string{"SCHEMA_OBJECT_ACCESS_GROUP", "BACKUP_RESTORE_GROUP"},
		[]string{"DATABASE_ROLE_MEMBER_CHANGE_GROUP", "BACKUP_RESTORE_GROUP"})
	want := []string{"BACKUP_RESTORE_GROUP", "DATABASE_ROLE_MEMBER_CHANGE_GROUP", "SCHEMA_OBJECT_ACCESS_GROUP"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v — sorted, with no duplicate", got, want)
	}
}
