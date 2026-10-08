package tui

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/gate"
)

// refusal is what a failed statement lets us say about permissions.
type refusal struct {
	kind    gosmo.RefusalKind
	number  int32
	message string // the server's own sentence, verbatim
}

// classifyRefusal is gosmo.ClassifyRefusal kept as the pair advice() reads:
// the number that picks the pattern and the server's sentence it is read
// from. gosmo owns which numbers are refusals and why the first message is
// the one that counts.
func classifyRefusal(err error) refusal {
	kind, m := gosmo.ClassifyRefusal(err)
	if m == nil {
		return refusal{}
	}
	return refusal{kind: kind, number: m.Number, message: m.Message}
}

// -- turning a refusal into the right it needs -------------------------------

// The patterns below read identifiers out of SQL Server's own wording, all of
// it English, so a miss is not a failure: advice returns "" and the caller
// shows the server's sentence verbatim, the only correct thing left on a
// localized instance. The *numbers* are not localized, so classification is
// keyed on them and only the wording is parsed.
var (
	// 229/230: "The SELECT permission was denied on the object 'sysjobservers',
	// database 'msdb', schema 'dbo'."
	reObjectDenied = regexp.MustCompile(
		`The ([A-Z][A-Z ]*) permission was denied on the \w+ '([^']*)'(?:, database '([^']*)')?(?:, schema '([^']*)')?`)

	// 262: "CREATE TABLE permission denied in database 'HealthClinic'."
	// Also what a refused BACKUP produces: "BACKUP DATABASE permission denied
	// in database 'HealthClinic'."
	reInDatabaseDenied = regexp.MustCompile(`^([A-Z][A-Z ]*) permission denied in database '([^']*)'`)

	// 300: "VIEW SERVER PERFORMANCE STATE permission was denied on object
	// 'server', database 'master'."
	reServerDenied = regexp.MustCompile(`^([A-Z][A-Z ]*) permission was denied on object 'server'`)

	// 916: The server principal "user_dr" is not able to access the database
	// "backup_test" under the current security context.
	reNoDatabaseAccess = regexp.MustCompile(`is not able to access the database "([^"]*)"`)

	// 5011: "User does not have permission to alter database 'HealthClinic',
	// the database does not exist, or …"
	reAlterDatabase = regexp.MustCompile(`permission to alter database '([^']*)'`)

	// 3701/15151: "Cannot alter the login 'user_dbo', because it does not exist
	// or you do not have permission."
	reCannotBecause = regexp.MustCompile(
		`Cannot (\w+) the ([\w ]+) '([^']*)', because it does not exist or you do not have permission`)

	// 1088: `Cannot find the object "Invoices" because it does not exist or you
	// do not have permissions.` Three things separate it from the sentence
	// reCannotBecause reads, and all three are why it needs its own pattern:
	// the identifier is in double quotes, there is no comma before "because",
	// and "permissions" is plural. The name may or may not be schema-qualified.
	reCannotFindObject = regexp.MustCompile(
		`Cannot find the ([\w ]+) "([^"]*)" because it does not exist or you do not have permission`)
)

// advice renders the refusal as a sentence naming what the login would need, or
// "" when its wording could not be read (a localized server, or an unseen
// message shape).
//
// An ambiguous refusal keeps its ambiguity in the wording. SQL Server won't say
// whether the object is missing or merely invisible, and a sentence picking one
// sends the user to fix the wrong thing half the time.
//
// Msg 297 deliberately has no branch. Its text names nothing, and it is not
// only the follow-up to Msg 300 (a refused KILL, sp_readerrorlog and several
// other procedures raise it alone). Naming a right for it would invent one; the
// DMV case that looks like it needs a branch is handled by classifyRefusal
// reading the *first* message, the 300 that names the right.
func (r refusal) advice() string {
	switch r.number {
	case 229, 230:
		if m := reObjectDenied.FindStringSubmatch(r.message); m != nil {
			return "Requires " + m[1] + " on " + qualify(m[3], m[4], m[2]) + "."
		}
	case 262:
		if m := reInDatabaseDenied.FindStringSubmatch(r.message); m != nil {
			return "Requires " + m[1] + " in " + m[2] + "."
		}
	case 300:
		if m := reServerDenied.FindStringSubmatch(r.message); m != nil {
			return "Requires " + m[1] + "."
		}
	case 916:
		if m := reNoDatabaseAccess.FindStringSubmatch(r.message); m != nil {
			return "Requires CONNECT on " + m[1] + "."
		}
	case 5011:
		if m := reAlterDatabase.FindStringSubmatch(r.message); m != nil {
			return m[1] + " does not exist, or this login needs " + gate.AlterDatabase.String() + " on it."
		}
	case 3701, 15151:
		if m := reCannotBecause.FindStringSubmatch(r.message); m != nil {
			return "The " + m[2] + " " + m[3] + " does not exist, or this login cannot " + m[1] + " it."
		}
	case 1088:
		// Not phrased with the verb the way 3701/15151 are: the verb SQL
		// Server uses here is "find", and "this login cannot find it" reads as
		// a lookup failure rather than the refusal it is.
		if m := reCannotFindObject.FindStringSubmatch(r.message); m != nil {
			return "The " + m[1] + " " + m[2] + " does not exist, or this login has no permission on it."
		}
	}
	return ""
}

// qualify joins whichever of database, schema and object the server named into
// the longest name it supports. Each part is optional: Msg 229 carries all
// three, Msg 230 may carry fewer.
func qualify(database, schema, object string) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{database, schema, object} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}

// -- display -----------------------------------------------------------------

// accessDeniedLabel prefixes a *certain* refusal wherever one is shown in place
// of content — a tree node, a grid with no rows. Never used for an ambiguous
// one: "Access denied" is a claim the server did not make there.
const accessDeniedLabel = "Access denied — "

// accessDeniedText renders err as one readable line when it is a permission
// refusal, and returns "" otherwise, leaving the caller's existing display
// alone.
//
// Prefers the sentence naming the right the login needs, and falls back to the
// server's own words when that could not be derived. Never invents a right the
// server did not name: everything in advice() is read out of the message.
func accessDeniedText(err error) string {
	r := classifyRefusal(err)
	switch r.kind {
	case gosmo.PermissionDenied:
		if a := r.advice(); a != "" {
			return accessDeniedLabel + a
		}
		return accessDeniedLabel + r.message
	case gosmo.MissingOrDenied:
		if a := r.advice(); a != "" {
			return a
		}
		return r.message
	}
	return ""
}

// displayError is err reduced to the one sentence that names the missing right
// when it is a permission refusal, and err untouched otherwise. For the places
// that show an error where content would go — a grid with no rows, the detail
// pane — since the wrapped form there is mostly plumbing the user cannot act
// on.
func displayError(err error) error {
	if denied := accessDeniedText(err); denied != "" {
		return errors.New(denied)
	}
	return err
}

// withPermissionAdvice appends the right a refusal needs to err's own text, for
// places that report a *failed action* rather than replacing content (a status
// line, an alert). The server's words are kept: the full failure is what the
// user is owed, and the advice is what they do next. err stays wrapped, so
// errors.Is and gosmo.AsSQLError still see through the advice.
func withPermissionAdvice(err error) error {
	if err == nil {
		return nil
	}
	a := classifyRefusal(err).advice()
	if a == "" || strings.Contains(err.Error(), a) {
		return err
	}
	return fmt.Errorf("%w — %s", err, a)
}
