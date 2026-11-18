package tui

import (
	"reflect"

	"github.com/gdamore/tcell/v3"

	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/dialogs"
)

// ---------------------------------------------------------------------------
// Lazy dialog slots
// ---------------------------------------------------------------------------

// dialogOf returns a's one dialog of type T, building it with mk and
// registering it in a.modals on first use. Every dialog App owns is reached
// through one of the accessor methods below, so a dialog is never built for a
// session that does not open it, and none can be built without being
// registered: a shown but unregistered dialog is never drawn and never gets
// input (dialogs.Stack.Register). Registering on first use is safe for z-order
// because Stack.Sync stacks dialogs that open together by when they were
// shown, not by registration order.
//
// One slot per type: two accessors may not return the same type (they would
// share one dialog). TestEveryDialogAccessorIsInEveryDialog checks that.
//
// UI goroutine only, like every dialog method; a background goroutine posts
// (postAndWake) before touching a dialog. statusHistoryDialog is the one
// exception, fed from loader goroutines through logStatus, so it is an
// ordinary App field built in buildUI.
func dialogOf[T Dialog](a *App, mk func(*App) T) T {
	key := reflect.TypeFor[T]()
	if d, ok := a.dialogSlots[key]; ok {
		return d.(T)
	}
	d := mk(a)
	if a.dialogSlots == nil {
		a.dialogSlots = make(map[reflect.Type]Dialog)
	}
	a.dialogSlots[key] = d
	a.modals.Register(d)
	return d
}

// setScreen makes s the screen the UI draws on and every dialog centres on and
// sizes against: a.modals hands it to each dialog as dialogOf registers it, and
// to those already registered now. Dialog constructors take no screen.
// Dialogs a test installs without registering (installDialog) get it too.
func (a *App) setScreen(s tcell.Screen) {
	a.screen = s
	a.modals.SetScreen(s)
	for _, d := range a.dialogSlots {
		if u, ok := d.(interface{ SetScreen(tcell.Screen) }); ok {
			u.SetScreen(s)
		}
	}
}

// builtDialog returns a's dialog of type T if one has been built, for code
// that only asks about an open dialog (is Key Diagnostics recording?) and must
// not build one to get the answer.
func builtDialog[T Dialog](a *App) (T, bool) {
	d, ok := a.dialogSlots[reflect.TypeFor[T]()].(T)
	return d, ok
}

// appless adapts a constructor that takes nothing (tuikit's own dialogs) to
// dialogOf. No dialog constructor takes the screen: registering hands it the
// one a.modals holds (setScreen).
func appless[T Dialog](mk func() T) func(*App) T {
	return func(*App) T { return mk() }
}

// newAppFileDialog is the File dialog, confirming an overwrite through
// confirmDialog.
func newAppFileDialog(a *App) *dialogs.FileDialog {
	d := dialogs.NewFileDialog()
	d.OnConfirmOverwrite = func(path string, proceed func()) {
		// gosmo.ServerPathBase, not filepath.Base: the path uses the SQL Server
		// host's separator, so filepath.Base of `C:\Backup\db.bak` on Linux is
		// the whole string.
		a.confirmDialog().ShowConfirm("Confirm Save As",
			gosmo.ServerPathBase(path)+" already exists. Overwrite it?",
			func(confirmed bool) {
				if confirmed {
					proceed()
				}
			})
	}
	return d
}

// newAppProgressDialog is the progress dialog runWithProgress drives.
func newAppProgressDialog(a *App) *dialogs.ProgressDialog {
	d := dialogs.NewProgressDialog()
	d.RevealDelay = progressRevealDelay
	d.CancellingText = "Cancelling — waiting for the server to stop"
	return d
}

// The accessors, one per dialog type, each the only handle on its dialog. A
// new one also goes in everyDialog (app_dialogs_test.go).
func (a *App) connectDialog() *ConnectDialog             { return dialogOf(a, NewConnectDialog) }
func (a *App) findDialog() *FindReplaceDialog            { return dialogOf(a, NewFindReplaceDialog) }
func (a *App) helpDialog() *HelpDialog                   { return dialogOf(a, NewHelpDialog) }
func (a *App) keyDiagDialog() *KeyDiagnosticsDialog      { return dialogOf(a, NewKeyDiagnosticsDialog) }
func (a *App) updateDialog() *UpdateDialog               { return dialogOf(a, NewUpdateDialog) }
func (a *App) propsDialog() *PropertiesDialog            { return dialogOf(a, NewPropertiesDialog) }
func (a *App) propDialog() *PropDialog                   { return dialogOf(a, NewPropDialog) }
func (a *App) newDatabaseDialog() *NewDatabaseDialog     { return dialogOf(a, NewNewDatabaseDialog) }
func (a *App) newLoginDialog() *NewLoginDialog           { return dialogOf(a, NewNewLoginDialog) }
func (a *App) newJobDialog() *NewJobDialog               { return dialogOf(a, NewNewJobDialog) }
func (a *App) newScheduleDialog() *NewScheduleDialog     { return dialogOf(a, NewNewScheduleDialog) }
func (a *App) newAlertDialog() *NewAlertDialog           { return dialogOf(a, NewNewAlertDialog) }
func (a *App) newOperatorDialog() *NewOperatorDialog     { return dialogOf(a, NewNewOperatorDialog) }
func (a *App) newCredentialDialog() *NewCredentialDialog { return dialogOf(a, NewNewCredentialDialog) }
func (a *App) sendTestMailDialog() *SendTestMailDialog   { return dialogOf(a, NewSendTestMailDialog) }
func (a *App) generateScriptsDialog() *GenerateScriptsDialog {
	return dialogOf(a, NewGenerateScriptsDialog)
}
func (a *App) newBackupDeviceDialog() *NewBackupDeviceDialog {
	return dialogOf(a, NewNewBackupDeviceDialog)
}
func (a *App) newAuditDialog() *NewAuditDialog         { return dialogOf(a, NewNewAuditDialog) }
func (a *App) newXESessionDialog() *NewXESessionDialog { return dialogOf(a, NewNewXESessionDialog) }
func (a *App) newAuditSpecificationDialog() *NewAuditSpecificationDialog {
	return dialogOf(a, NewNewAuditSpecificationDialog)
}
func (a *App) newDBAuditSpecDialog() *NewDatabaseAuditSpecificationDialog {
	return dialogOf(a, NewNewDatabaseAuditSpecificationDialog)
}
func (a *App) newDBScopedCredDialog() *NewDatabaseScopedCredentialDialog {
	return dialogOf(a, NewNewDatabaseScopedCredentialDialog)
}
func (a *App) newIndexDialog() *NewIndexDialog           { return dialogOf(a, NewNewIndexDialog) }
func (a *App) newStatisticsDialog() *NewStatisticsDialog { return dialogOf(a, NewNewStatisticsDialog) }
func (a *App) newCMKDialog() *NewColumnMasterKeyDialog {
	return dialogOf(a, NewNewColumnMasterKeyDialog)
}
func (a *App) newCEKDialog() *NewColumnEncryptionKeyDialog {
	return dialogOf(a, NewNewColumnEncryptionKeyDialog)
}
func (a *App) newFullTextCatalogDialog() *NewFullTextCatalogDialog {
	return dialogOf(a, NewNewFullTextCatalogDialog)
}
func (a *App) newFullTextStoplistDialog() *NewFullTextStoplistDialog {
	return dialogOf(a, NewNewFullTextStoplistDialog)
}
func (a *App) newSearchPropListDialog() *NewSearchPropertyListDialog {
	return dialogOf(a, NewNewSearchPropertyListDialog)
}
func (a *App) newFullTextIndexDialog() *NewFullTextIndexDialog {
	return dialogOf(a, NewNewFullTextIndexDialog)
}
func (a *App) agAddDatabaseDialog() *AGAddDatabaseDialog { return dialogOf(a, NewAGAddDatabaseDialog) }
func (a *App) agAddListenerDialog() *AGAddListenerDialog { return dialogOf(a, NewAGAddListenerDialog) }
func (a *App) agAddReplicaDialog() *AGAddReplicaDialog   { return dialogOf(a, NewAGAddReplicaDialog) }
func (a *App) newAGDialog() *NewAGDialog                 { return dialogOf(a, NewNewAGDialog) }
func (a *App) newEndpointDialog() *NewEndpointDialog     { return dialogOf(a, NewNewEndpointDialog) }
func (a *App) newCertificateDialog() *NewCertificateDialog {
	return dialogOf(a, NewNewCertificateDialog)
}
func (a *App) newUserDialog() *NewUserDialog             { return dialogOf(a, NewNewUserDialog) }
func (a *App) newSchemaDialog() *NewSchemaDialog         { return dialogOf(a, NewNewSchemaDialog) }
func (a *App) newRoleDialog() *NewRoleDialog             { return dialogOf(a, NewNewRoleDialog) }
func (a *App) newServerRoleDialog() *NewServerRoleDialog { return dialogOf(a, NewNewServerRoleDialog) }
func (a *App) newApplicationRoleDialog() *NewApplicationRoleDialog {
	return dialogOf(a, NewNewApplicationRoleDialog)
}
func (a *App) newSequenceDialog() *NewSequenceDialog { return dialogOf(a, NewNewSequenceDialog) }
func (a *App) newSynonymDialog() *NewSynonymDialog   { return dialogOf(a, NewNewSynonymDialog) }
func (a *App) newAsymmetricKeyDialog() *NewAsymmetricKeyDialog {
	return dialogOf(a, NewNewAsymmetricKeyDialog)
}
func (a *App) newSymmetricKeyDialog() *NewSymmetricKeyDialog {
	return dialogOf(a, NewNewSymmetricKeyDialog)
}
func (a *App) backupCertificateDialog() *BackupCertificateDialog {
	return dialogOf(a, NewBackupCertificateDialog)
}
func (a *App) backupMasterKeyDialog() *BackupMasterKeyDialog {
	return dialogOf(a, NewBackupMasterKeyDialog)
}
func (a *App) regenerateMasterKeyDialog() *RegenerateMasterKeyDialog {
	return dialogOf(a, NewRegenerateMasterKeyDialog)
}
func (a *App) detachDatabaseDialog() *DetachDatabaseDialog {
	return dialogOf(a, NewDetachDatabaseDialog)
}
func (a *App) shrinkDatabaseDialog() *ShrinkDatabaseDialog {
	return dialogOf(a, NewShrinkDatabaseDialog)
}
func (a *App) shrinkFilesDialog() *ShrinkFilesDialog { return dialogOf(a, NewShrinkFilesDialog) }
func (a *App) attachDatabaseDialog() *AttachDatabaseDialog {
	return dialogOf(a, NewAttachDatabaseDialog)
}
func (a *App) newSnapshotDialog() *NewSnapshotDialog { return dialogOf(a, NewNewSnapshotDialog) }
func (a *App) fileDialog() *dialogs.FileDialog       { return dialogOf(a, newAppFileDialog) }
func (a *App) queryListDialog() *QueryListDialog     { return dialogOf(a, NewQueryListDialog) }
func (a *App) optionsDialog() *OptionsDialog         { return dialogOf(a, NewOptionsDialog) }
func (a *App) filterDialog() *FilterDialog           { return dialogOf(a, NewFilterDialog) }
func (a *App) logSearchDialog() *LogSearchDialog     { return dialogOf(a, NewLogSearchDialog) }
func (a *App) promptDialog() *dialogs.PromptDialog {
	return dialogOf(a, appless(dialogs.NewPromptDialog))
}
func (a *App) profileDialog() *ProfileDialog { return dialogOf(a, appless(NewProfileDialog)) }
func (a *App) tasksDialog() *TasksDialog     { return dialogOf(a, NewTasksDialog) }
func (a *App) confirmDialog() *dialogs.ConfirmDialog {
	return dialogOf(a, appless(dialogs.NewConfirmDialog))
}
func (a *App) confirmTypedDialog() *dialogs.TypedConfirmDialog {
	return dialogOf(a, appless(dialogs.NewTypedConfirmDialog))
}
func (a *App) alertDialog() *dialogs.AlertDialog {
	return dialogOf(a, appless(dialogs.NewAlertDialog))
}
func (a *App) progressDialog() *dialogs.ProgressDialog { return dialogOf(a, newAppProgressDialog) }
func (a *App) deviceCodeDialog() *DeviceCodeDialog     { return dialogOf(a, NewDeviceCodeDialog) }
func (a *App) backupDialog() *BackupDialog             { return dialogOf(a, NewBackupDialog) }
func (a *App) restoreDialog() *RestoreDialog           { return dialogOf(a, NewRestoreDialog) }
