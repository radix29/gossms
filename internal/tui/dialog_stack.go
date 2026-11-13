package tui

import "github.com/radix29/tuikit/dialogs"

// Dialog is the shape every modal dialog has by embedding
// tuikit/dialogs.ModalDialog (see dialogs.Dialog). App keeps every dialog it
// owns in one dialogs.Stack (App.modals) instead of hand-enumerating dialog
// types wherever it needs "what's on top": adding a dialog costs one
// registerDialog call in buildUI, and draw order, input routing, resize
// handling and clipboard ownership follow from the stack. App adds only its
// gesture-overlay snapshot (overlaySnapshot), which compares Top() across a
// held press.
type Dialog = dialogs.Dialog
