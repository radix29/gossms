package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newAuditSpecGroups is the pick list both New dialogs are built with. The
// test ticks the second and third, never the first, so a dialog that read the
// grid against the wrong slice, or ignored it, sends the wrong groups.
var newAuditSpecGroups = []string{"BACKUP_RESTORE_GROUP", "DATABASE_CHANGE_GROUP", "SCHEMA_OBJECT_ACCESS_GROUP"}

// scriptNewAuditSpec ticks two groups on a built New dialog and returns the
// one statement its apply collects.
func scriptNewAuditSpec(t *testing.T, f *propsheet.Form, preflight func() error, apply propApply) string {
	t.Helper()
	editText(t, f, "Name", "spec1")
	grid := toggleGrid(t, f)
	toggleByName(t, grid, "DATABASE_CHANGE_GROUP", 0)
	toggleByName(t, grid, "SCHEMA_OBJECT_ACCESS_GROUP", 0)
	if err := preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	if err := apply(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := script.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	return stmts[0]
}

// checkTickedGroups asserts stmt adds exactly the two ticked groups.
func checkTickedGroups(t *testing.T, stmt string) {
	t.Helper()
	for _, g := range []string{"DATABASE_CHANGE_GROUP", "SCHEMA_OBJECT_ACCESS_GROUP"} {
		if !strings.Contains(stmt, "ADD ("+g+")") {
			t.Errorf("the ticked %s is missing:\n%s", g, stmt)
		}
	}
	if strings.Contains(stmt, "BACKUP_RESTORE_GROUP") {
		t.Errorf("the unticked BACKUP_RESTORE_GROUP was sent:\n%s", stmt)
	}
}

func TestNewServerAuditSpecificationSendsTheTickedGroups(t *testing.T) {
	sc, _ := newFakeConn(t)
	d := &NewAuditSpecificationDialog{}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&nauditSpecPrefetch{
		existingNames: newNameSet(""),
		auditNames:    []string{"audit1"},
		actionGroups:  newAuditSpecGroups,
	})
	stmt := scriptNewAuditSpec(t, d.forms[0], d.preflight, d.applyFns[0])
	if !strings.Contains(stmt, "CREATE SERVER AUDIT SPECIFICATION [spec1]") {
		t.Errorf("not a server specification create:\n%s", stmt)
	}
	checkTickedGroups(t, stmt)
}

func TestNewDatabaseAuditSpecificationSendsTheTickedGroups(t *testing.T) {
	sc, _ := newFakeConn(t)
	d := &NewDatabaseAuditSpecificationDialog{dbName: "Sales"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&ndbAuditSpecPrefetch{
		existingNames: newNameSet(""),
		auditNames:    []string{"audit1"},
		actionGroups:  newAuditSpecGroups,
	})
	stmt := scriptNewAuditSpec(t, d.forms[0], d.preflight, d.applyFns[0])
	if !strings.Contains(stmt, "CREATE DATABASE AUDIT SPECIFICATION [spec1]") {
		t.Errorf("not a database specification create:\n%s", stmt)
	}
	checkTickedGroups(t, stmt)
}

// TestNewServerAuditSpecificationOffersOnlyFreeAudits pins T6 for the New
// dialog: audits another server specification holds are not offered, and
// when every audit is held the dialog says so instead of claiming the server
// has none.
func TestNewServerAuditSpecificationOffersOnlyFreeAudits(t *testing.T) {
	sc, _ := newFakeConn(t, specRows(), auditRows(), actionGroupRows())
	pf, err := fetchNewAuditSpecPrefetch(t.Context(), sc)
	if err != nil {
		t.Fatal(err)
	}
	// AppSpec holds AppLogAudit and HIPAA_spec holds HIPAA; the orphan holds none.
	if !slices.Equal(pf.auditNames, []string{"Rollover"}) || pf.audits != 3 {
		t.Errorf("free audits %q of %d, want [Rollover] of 3", pf.auditNames, pf.audits)
	}

	d := &NewAuditSpecificationDialog{}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&nauditSpecPrefetch{existingNames: newNameSet(""), audits: 2, actionGroups: newAuditSpecGroups})
	if !strings.Contains(formNotes(d.forms[0]), "already has a server audit specification") {
		t.Error("with every audit held, the dialog does not say so")
	}
	editText(t, d.forms[0], "Name", "spec1")
	if err := d.preflight(); err == nil {
		t.Error("preflight passed with no audit free")
	}
}
