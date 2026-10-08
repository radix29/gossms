package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// matrixParts pulls the two grids and the two filter boxes back out of a
// built permissions matrix, so a test can drive the page the way a user
// does — OnSelectRow to pick a principal, OnActivateCell to cycle a State,
// the filter row's own key handling to narrow a grid.
type matrixParts struct {
	principals, perms *controls.DataGrid
	principalFilter   *propsheet.TextRow
	permsRow          *propsheet.GridRow
}

func matrixPartsOf(t *testing.T, f *propsheet.Form) matrixParts {
	t.Helper()
	var grids []*propsheet.GridRow
	var texts []*propsheet.TextRow
	for _, r := range f.Rows() {
		switch v := r.(type) {
		case *propsheet.GridRow:
			grids = append(grids, v)
		case *propsheet.TextRow:
			texts = append(texts, v)
		}
	}
	if len(grids) != 2 || len(texts) != 2 {
		t.Fatalf("form has %d grids and %d text rows, want 2 and 2", len(grids), len(texts))
	}
	return matrixParts{
		principals:      grids[0].Grid,
		perms:           grids[1].Grid,
		principalFilter: texts[0],
		permsRow:        grids[1],
	}
}

// recorder is a permApplyFn that logs each statement it is asked to issue and
// can be made to fail on one of them.
type recorder struct {
	stmts  []string
	failOn string
	err    error
}

func (r *recorder) fn() permApplyFn {
	return func(_ context.Context, verb gosmo.PermissionVerb, opts gosmo.PermissionOptions, permission, principal string) error {
		s := fmt.Sprintf("%s %s -> %s%s", verb, permission, principal, modifiers(opts))
		if r.failOn != "" && principal == r.failOn {
			r.err = errors.New("boom")
			return r.err
		}
		r.stmts = append(r.stmts, s)
		return nil
	}
}

func modifiers(o gosmo.PermissionOptions) string {
	switch {
	case o.WithGrantOption:
		return " [WITH GRANT OPTION]"
	case o.GrantOptionOnly:
		return " [GRANT OPTION FOR]"
	case o.Cascade:
		return " [CASCADE]"
	}
	return ""
}

const (
	permA = "CONNECT SQL"
	permB = "VIEW ANY DATABASE"
)

// threePrincipalMatrix builds a matrix over principals a/b/c where "a"
// already holds permA WITH GRANT OPTION, then edits one cell on each
// principal: a is downgraded to a plain Grant (the transition that emits
// REVOKE GRANT OPTION FOR), b and c are granted from nothing.
func threePrincipalMatrix(t *testing.T, rec *recorder) (propApply, matrixParts) {
	t.Helper()
	principals := []permPrincipal{
		{Name: "a", Type: "SQL_LOGIN"},
		{Name: "b", Type: "SQL_LOGIN"},
		{Name: "c", Type: "SQL_LOGIN"},
	}
	entries := []permEntry{
		{Principal: "a", Permission: permA, State: permStateGrantWith},
	}
	f, apply := buildPermissionsMatrix(principals, []string{permA, permB}, entries, 8, 8, rec.fn())
	p := matrixPartsOf(t, f)

	// a: Grant With Grant -> Deny -> (none) -> Grant.
	for range 3 {
		p.perms.OnActivateCell(0, 1)
	}
	for _, row := range []int{1, 2} {
		p.principals.OnSelectRow(row)
		p.perms.OnActivateCell(0, 1) // (none) -> Grant
	}
	return apply, p
}

// A partial apply has to be reproducible: ranging the edits map put the
// statements in a different order every run, so which ones landed before a
// mid-run failure was luck, and Script Changes reordered its output between
// presses.
func TestPermissionsMatrixApplyOrderIsStable(t *testing.T) {
	want := []string{
		"REVOKE " + permA + " -> a [GRANT OPTION FOR]",
		"GRANT " + permA + " -> b",
		"GRANT " + permA + " -> c",
	}
	// Map iteration order is randomized per range, so one run could match by
	// chance; repeating makes an unordered implementation certain to fail.
	for i := range 20 {
		rec := &recorder{}
		apply, _ := threePrincipalMatrix(t, rec)
		if err := apply(context.Background()); err != nil {
			t.Fatalf("run %d: apply: %v", i, err)
		}
		if len(rec.stmts) != len(want) {
			t.Fatalf("run %d: issued %v, want %v", i, rec.stmts, want)
		}
		for j := range want {
			if rec.stmts[j] != want[j] {
				t.Fatalf("run %d: statement %d = %q, want %q (full: %v)", i, j, rec.stmts[j], want[j], rec.stmts)
			}
		}
	}
}

// The failure a stale baseline costs: undoing an edit that already landed.
// Apply downgrades a's grant option, grants b, and is refused on c, so the
// server holds a plain GRANT for a. Were the page kept as it was, putting a
// back to Grant With Grant — the state it loaded with — would read clean and
// send nothing, leaving the grid claiming a grant option the server lost.
//
// An apply closure may not move the cells' baselines itself (it runs off the
// UI goroutine, and under Script Changes), so the page is reloaded instead:
// its statements reached the server, which gosmo's statement observer sees.
func TestPermissionsMatrixPartlyAppliedReloads(t *testing.T) {
	sc, _ := newFakeConn(t, fakeResponse{match: "TO [c]", err: errRefused})
	loads := 0
	sheet := propsheet.NewPropertySheet(&fakeSizedScreen{w: 120, h: 40}, "Server Properties")
	d := &PropDialog{PropertySheet: sheet, app: &App{}, ctx: context.Background(), applyFn: map[int]propApply{}}
	d.SetPages([]string{"Permissions"})
	d.OnLoadPage = func(page, seq int) {
		loads++
		principals := []permPrincipal{{Name: "a", Type: "SQL_LOGIN"}, {Name: "b", Type: "SQL_LOGIN"}, {Name: "c", Type: "SQL_LOGIN"}}
		entries := []permEntry{{Principal: "a", Permission: permA, State: permStateGrantWith}}
		f, apply := buildPermissionsMatrix(principals, []string{permA, permB}, entries, 8, 8, serverPermApply(sc.Server))
		if d.SetPageForm(page, seq, f) {
			d.applyFn[page] = apply
		}
		if loads > 1 {
			return
		}
		p := matrixPartsOf(t, f)
		for range 3 {
			p.perms.OnActivateCell(0, 1) // a: Grant With Grant -> ... -> Grant
		}
		for _, row := range []int{1, 2} {
			p.principals.OnSelectRow(row)
			p.perms.OnActivateCell(0, 1) // (none) -> Grant
		}
	}
	d.Show()
	d.SelectPage(0)
	if !d.Dirty() {
		t.Fatal("setup: the page is not dirty")
	}

	runAndWait(t, d, func() { d.runApply(false) })

	if !strings.Contains(d.Message(), "refused") {
		t.Errorf("message = %q, want the refusal", d.Message())
	}
	if loads != 2 {
		t.Fatalf("the page loaded %d times, want a reload after the partial apply", loads)
	}
	if d.Dirty() {
		t.Error("the reloaded page is still dirty — it kept edits that are already on the server")
	}
}

// Script Changes runs the same closures with writes captured instead of
// executed. Committing the baseline there would mark the page clean, so the
// real Apply that follows would have nothing left to do.
func TestPermissionsMatrixScriptingLeavesPageDirty(t *testing.T) {
	rec := &recorder{}
	apply, p := threePrincipalMatrix(t, rec)

	scriptCtx, _ := gosmo.WithScript(context.Background())
	if err := apply(scriptCtx); err != nil {
		t.Fatalf("scripted apply: %v", err)
	}
	scripted := len(rec.stmts)
	if scripted != 3 {
		t.Fatalf("scripted apply issued %d statements, want 3", scripted)
	}
	if !p.permsRow.DirtyFn() {
		t.Error("the page went clean after Script Changes, so Apply would now do nothing")
	}

	rec.stmts = nil
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply after scripting: %v", err)
	}
	if len(rec.stmts) != scripted {
		t.Errorf("the real apply issued %d statements after scripting, want all %d", len(rec.stmts), scripted)
	}
}

// A filter matching nothing must clear the bottom grid. Left as it was, its
// cells stay editable for a principal the page no longer lists — and Apply
// then writes them.
func TestPermissionsMatrixEmptyFilterClearsSelection(t *testing.T) {
	rec := &recorder{}
	principals := []permPrincipal{{Name: "a", Type: "SQL_LOGIN"}}
	f, apply := buildPermissionsMatrix(principals, []string{permA, permB}, nil, 8, 8, rec.fn())
	p := matrixPartsOf(t, f)

	p.perms.OnActivateCell(0, 1) // (none) -> Grant on principal a
	if !p.permsRow.DirtyFn() {
		t.Fatal("the edit did not register — test premise is wrong")
	}

	// Paste rather than SetValue: it is an edit path, so it fires the
	// row's onChange the way typing does.
	p.principalFilter.Paste("zzz")
	if p.principals.SelectedRow() != -1 {
		t.Fatalf("principal grid still has rows after a filter matching nothing")
	}
	if p.perms.SelectedRow() != -1 {
		t.Error("the permissions grid still lists a principal the page no longer shows")
	}

	// Cycling the now-empty grid must not reach the edit behind it.
	p.perms.OnActivateCell(0, 1)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "GRANT " + permA + " -> a"
	if len(rec.stmts) != 1 || rec.stmts[0] != want {
		t.Errorf("apply issued %v, want just [%q] — the edit made before filtering, unchanged", rec.stmts, want)
	}
}
