package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/tuikit/dialogs"
)

// installDialog puts d in a's slot for its type, replacing whatever dialogOf
// built, for a test that needs a fresh dialog or one it built itself
// (dialogs.Replace). It does not register d, as assigning the old App field did
// not: a test that routes input through a.modals registers it itself.
// Never reset a.modals (a.modals = dialogs.Stack{}) after installing: the
// Stack holds the slots too, so the next accessor quietly builds a fresh,
// unconfigured dialog in place of the installed one.
func installDialog[T Dialog](a *App, d T) T {
	return dialogs.Replace(&a.modals, d)
}

// withScreen gives d the screen registering it would (ModalDialog.SetScreen),
// for a test that shows a tuikit dialog or sheet outside a.modals.
func withScreen[T interface{ SetScreen(tcell.Screen) }](d T, s tcell.Screen) T {
	d.SetScreen(s)
	return d
}

// newAppWithEveryDialog is an App after buildUI with every dialog built, for
// a test that walks a.modals.Registered() to check all of them.
func newAppWithEveryDialog() *App {
	a := &App{cfg: &config.Config{}}
	a.buildUI()
	everyDialog(a)
	return a
}

// everyDialog builds every dialog App owns through its accessor.
// TestEveryDialogAccessorIsInEveryDialog keeps it complete.
func everyDialog(a *App) []Dialog {
	return []Dialog{
		a.statusHistoryDialog,
		a.connectDialog(),
		a.findDialog(),
		a.helpDialog(),
		a.keyDiagDialog(),
		a.updateDialog(),
		a.propsDialog(),
		a.propDialog(),
		a.newDatabaseDialog(),
		a.newLoginDialog(),
		a.newJobDialog(),
		a.newScheduleDialog(),
		a.newAlertDialog(),
		a.newOperatorDialog(),
		a.newCredentialDialog(),
		a.sendTestMailDialog(),
		a.generateScriptsDialog(),
		a.newBackupDeviceDialog(),
		a.newAuditDialog(),
		a.newXESessionDialog(),
		a.newAuditSpecificationDialog(),
		a.newDBAuditSpecDialog(),
		a.newDBScopedCredDialog(),
		a.newIndexDialog(),
		a.newStatisticsDialog(),
		a.newCMKDialog(),
		a.newCEKDialog(),
		a.newFullTextCatalogDialog(),
		a.newFullTextStoplistDialog(),
		a.newSearchPropListDialog(),
		a.newFullTextIndexDialog(),
		a.agAddDatabaseDialog(),
		a.agAddListenerDialog(),
		a.agAddReplicaDialog(),
		a.newAGDialog(),
		a.newEndpointDialog(),
		a.newCertificateDialog(),
		a.newUserDialog(),
		a.newSchemaDialog(),
		a.newRoleDialog(),
		a.newServerRoleDialog(),
		a.newApplicationRoleDialog(),
		a.newSequenceDialog(),
		a.newSynonymDialog(),
		a.newAsymmetricKeyDialog(),
		a.newSymmetricKeyDialog(),
		a.backupCertificateDialog(),
		a.backupMasterKeyDialog(),
		a.regenerateMasterKeyDialog(),
		a.detachDatabaseDialog(),
		a.shrinkDatabaseDialog(),
		a.shrinkFilesDialog(),
		a.attachDatabaseDialog(),
		a.newSnapshotDialog(),
		a.fileDialog(),
		a.queryListDialog(),
		a.optionsDialog(),
		a.filterDialog(),
		a.logSearchDialog(),
		a.promptDialog(),
		a.profileDialog(),
		a.tasksDialog(),
		a.confirmDialog(),
		a.confirmTypedDialog(),
		a.alertDialog(),
		a.progressDialog(),
		a.deviceCodeDialog(),
		a.backupDialog(),
		a.restoreDialog(),
	}
}

// A dialog missing from everyDialog escapes every test that walks all of them
// (clipboard ownership, field gestures, clipboard tokens), so the list is
// checked against the accessors app_dialogs.go declares. Two accessors of one
// type would share a slot, and so one dialog: also checked. And every dialog
// built is registered, or it would never draw or take input.
func TestEveryDialogAccessorIsInEveryDialog(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "app_dialogs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	accessors := 0
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "dialogOf" {
					accessors++
				}
			}
			return true
		})
	}
	if accessors == 0 {
		t.Fatal("found no dialogOf accessor in app_dialogs.go — this test has stopped checking anything")
	}

	a := &App{cfg: &config.Config{}}
	a.buildUI()
	all := everyDialog(a)
	if got := len(all) - 1; got != accessors { // - statusHistoryDialog
		t.Errorf("everyDialog lists %d accessors, app_dialogs.go declares %d", got, accessors)
	}

	types := map[reflect.Type]bool{}
	for _, d := range all {
		if types[reflect.TypeOf(d)] {
			t.Errorf("two accessors return %T: they share one dialog", d)
		}
		types[reflect.TypeOf(d)] = true
	}

	registered := map[Dialog]bool{}
	for _, d := range a.modals.Registered() {
		if registered[d] {
			t.Errorf("%T is registered twice", d)
		}
		registered[d] = true
	}
	for _, d := range all {
		if !registered[d] {
			t.Errorf("%T was built but not registered: it would never draw or take input", d)
		}
	}
	if len(registered) != len(all) {
		t.Errorf("%d dialogs registered, everyDialog has %d", len(registered), len(all))
	}
}

// A dialog is built on first use, once: buildUI builds none but the status
// history, and asking twice returns the same one.
func TestDialogsAreBuiltOnFirstUseOnly(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()
	if n := len(a.modals.Registered()); n != 1 {
		t.Errorf("buildUI registered %d dialogs, want 1 (status history)", n)
	}
	if _, ok := builtDialog[*HelpDialog](a); ok {
		t.Error("builtDialog found a Help dialog nothing asked for")
	}
	h := a.helpDialog()
	if a.helpDialog() != h {
		t.Error("a second helpDialog() built another dialog")
	}
	if got, ok := builtDialog[*HelpDialog](a); !ok || got != h {
		t.Errorf("builtDialog after helpDialog() = %v, %v; want it, true", got, ok)
	}
	if n := len(a.modals.Registered()); n != 2 {
		t.Errorf("after helpDialog(): %d registered, want 2", n)
	}
}
