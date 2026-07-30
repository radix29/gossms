package tui

import "testing"

// A Select row whose list begins with a synthetic "(None)"/"<all databases>"
// entry must be built by searching that whole list, not the bare names with a
// 1+ offset. With the offset, indexOf's not-found 0 becomes 1 — the first
// *real* item — so an alert whose category has since been deleted displays as
// though it belonged to whichever category sorts first, and applying the page
// silently moves it there.
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
		items := compatItemsFor(90)
		if items[0] != "90" {
			t.Fatalf("compatItemsFor(90) = %v, want 90 first", items)
		}
		if got := indexOf(items, "90"); got != 0 {
			t.Errorf("indexOf(items, \"90\") = %d, want 0", got)
		}
	})

	t.Run("a level above the list is inserted last", func(t *testing.T) {
		items := compatItemsFor(180)
		if items[len(items)-1] != "180" {
			t.Fatalf("compatItemsFor(180) = %v, want 180 last", items)
		}
	})

	t.Run("a listed level leaves the list alone", func(t *testing.T) {
		for _, level := range []int{100, 150, 170} {
			if got := compatItemsFor(level); len(got) != len(compatLevelItems) {
				t.Errorf("compatItemsFor(%d) = %v, want the base list unchanged", level, got)
			}
		}
	})

	t.Run("an unpopulated level adds nothing", func(t *testing.T) {
		if got := compatItemsFor(0); len(got) != len(compatLevelItems) {
			t.Errorf("compatItemsFor(0) = %v, want the base list unchanged", got)
		}
	})

	// The base list must not be mutated by an insert — both call sites read it.
	t.Run("the base list is never mutated", func(t *testing.T) {
		compatItemsFor(90)
		compatItemsFor(180)
		if compatLevelItems[0] != "100" || len(compatLevelItems) != 8 {
			t.Fatalf("compatLevelItems was mutated: %v", compatLevelItems)
		}
	})
}
