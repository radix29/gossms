package tui

import (
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// capabilitySet is the question-asking half of gosmo's *Capabilities and
// *DatabaseCapabilities, so one helper serves server- and database-scope pages.
// Both implementations are nil-safe.
type capabilitySet interface {
	Has(name string) bool
	Allows(name string) bool
}

// knownDenied reports whether the server actually answered "no" for a right,
// not merely that we could not establish a "yes".
//
// Every placeholder in this file is gated on this direction, deliberately the
// conservative one: an unprobed connection, a timed-out probe, and a permission
// this instance does not define all leave the value on screen as it renders
// today. Gating on Has instead would blank a sysadmin's page the moment a probe
// failed.
func knownDenied(c capabilitySet, right string) bool { return !c.Allows(right) }

// valueOrUnreadable renders value unless right is known-denied, in which case
// the value is a visibility artefact and unreadableValue is shown instead.
//
// sys.* catalog views are metadata-visibility filtered, so a count over one is
// not an error and not zero but a smaller number, indistinguishable from the
// truth. HealthClinic has 8 users; a login without VIEW DEFINITION counts 5 and
// the page states 5 as fact (measured on both test servers, 2026-08-25).
func valueOrUnreadable(c capabilitySet, right, value string) string {
	if knownDenied(c, right) {
		return unreadableValue
	}
	return value
}

// visibilityNote returns the note to append to a page that lists rows out of a
// visibility-filtered catalog view, or nil when the login can see all of them.
//
// A note rather than a placeholder because the defect is in the *list*: there is
// no cell to blank, only rows silently missing. Returned as a slice so a caller
// can append it unconditionally.
func visibilityNote(c capabilitySet, right, what string) []propsheet.Row {
	if !knownDenied(c, right) {
		return nil
	}
	return []propsheet.Row{propsheet.Note(
		"Only the " + what + " visible to this login are listed. Seeing all of them requires " + right + ".")}
}

// viewServerStateAdvice names the right a refused sys.dm_os_* read needs.
//
// Both halves are named because SQL Server 2022 split VIEW SERVER STATE in two,
// and every read these notes cover (sys.dm_os_sys_info, sys.dm_os_sys_memory,
// sys.dm_os_process_memory) falls in the performance half. The wide right fixes
// it on every version and is what an administrator on 2019 or earlier has to
// grant; the narrow one is the least privilege that works on 2022 and later and
// the one the server names in the refusal. Naming only the wide one asks for
// more than needed, in a feature whose point is least privilege.
const viewServerStateAdvice = "VIEW SERVER STATE (or VIEW SERVER PERFORMANCE STATE on SQL Server 2022 and later)"

// deniedReadNote is the note a page carries when one of its reads was refused
// and the values it fed render as N/A.
func deniedReadNote(right string) propsheet.Row {
	return propsheet.Note("Values shown as " + unreadableValue +
		" could not be read: they require " + right + ".")
}
