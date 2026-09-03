// Package dialogs provides the ModalDialog base type and generic re-usable
// dialog implementations (AlertDialog, ConfirmDialog, TypedConfirmDialog,
// PromptDialog, PropertiesDialog, FileDialog).
//
// Every dialog embeds ModalDialog which:
//   - Fades the underlying UI in place (keeping it visible) before its own box
//   - Intercepts all mouse clicks outside its border (focus trap)
//   - Cannot lose focus until explicitly dismissed
//
// Application-specific dialogs (ConnectDialog, HelpDialog, etc.) live in
// the tui package and embed ModalDialog from here.
//
//   - modal.go              — ModalDialog base type
//   - common.go             — helpers shared across more than one dialog file
//   - field_gesture.go      — FieldGesture, the text-field drag latch every
//     dialog with an InputField drives from HandleMouse
//   - prompt_dialog.go      — PromptDialog (single-line text prompt)
//   - file_system.go        — the FileSystem abstraction FileDialog browses
//   - properties_dialog.go  — PropertiesDialog (generic key/value viewer)
//   - alert_dialog.go       — AlertDialog (single-button info message)
//   - confirm_dialog.go     — ConfirmDialog (two-button yes/no)
//   - typed_confirm_dialog.go — TypedConfirmDialog (retype-to-confirm)
//   - file_dialog.go        — FileDialog (Open/Save file picker); split
//     across file_dialog_draw.go (rendering), file_dialog_input.go
//     (key/mouse handling), and file_dialog_complete.go (path completion)
package dialogs
