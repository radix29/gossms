package tui

import (
	"errors"
	"regexp"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// refusalKind is how much a SQL Server error number lets us claim.
type refusalKind int

const (
	// notARefusal — the error says nothing about permissions.
	notARefusal refusalKind = iota

	// refusalCertain — the server stated a permission was denied.
	refusalCertain

	// refusalAmbiguous — the server said the object "does not exist or you do
	// not have permission", which it does deliberately: telling the two apart
	// would let an unprivileged login enumerate objects it cannot see. Nothing
	// downstream may narrow it to one or the other.
	refusalAmbiguous
)

// refusalNumbers classifies the SQL Server error numbers that mean a login was
// refused. Every one was captured from a live instance against the two
// least-privileged test logins, not taken from documentation — see
// docs/permissions-plan.md § 3.3.
//
//	229   The SELECT/EXECUTE permission was denied on the object '…'
//	230   The SELECT permission was denied on the column '…'
//	262   CREATE TABLE / BACKUP DATABASE / … permission denied in database '…'
//	297   The user does not have permission to perform this action
//	300   VIEW SERVER (PERFORMANCE|SECURITY) STATE permission was denied …
//	916   The server principal "…" is not able to access the database "…"
//	3701  Cannot drop the table '…', because it does not exist or …
//	5011  User does not have permission to alter database '…', the database
//	      does not exist, or …
//	15151 Cannot alter the login '…', because it does not exist or …
//	15247 User does not have permission to perform this action
//
// A number missing from this map only means the error keeps its raw text,
// which is what every one of them did before this existed.
var refusalNumbers = map[int32]refusalKind{
	229: refusalCertain, 230: refusalCertain, 262: refusalCertain,
	297: refusalCertain, 300: refusalCertain, 916: refusalCertain,

	3701: refusalAmbiguous, 5011: refusalAmbiguous,
	15151: refusalAmbiguous, 15247: refusalAmbiguous,
}

// refusal is what a failed statement lets us say about permissions.
type refusal struct {
	kind    refusalKind
	number  int32
	message string // the server's own sentence, verbatim
}

// classifyRefusal reports what err says about permissions, reading the *first*
// qualifying message rather than the last, because that is the one that names
// the right: a refused sys.dm_os_process_memory read sends "VIEW SERVER
// PERFORMANCE STATE permission was denied on object 'server'" (Msg 300)
// followed by the contentless "The user does not have permission to perform
// this action" (Msg 297), and database/sql surfaces only the second. A refused
// BACKUP sends Msg 262 then the contentless Msg 3013 the same way. Both
// live-captured 2026-08-25.
func classifyRefusal(err error) refusal {
	se, ok := gosmo.AsSQLError(err)
	if !ok {
		return refusal{}
	}
	all := se.All
	if len(all) == 0 {
		all = []gosmo.SQLError{*se}
	}
	for _, m := range all {
		if kind := refusalNumbers[m.Number]; kind != notARefusal && m.Class >= 11 && m.Message != "" {
			return refusal{kind: kind, number: m.Number, message: m.Message}
		}
	}
	return refusal{}
}

// -- turning a refusal into the right it needs -------------------------------

// The patterns below read the identifiers out of SQL Server's own wording, and
// every one of them is English. That is why a miss is not a failure: advice
// returns "" and the caller shows the server's sentence verbatim, which on a
// localized instance is the only correct thing left to say. The *numbers* are
// not localized, which is why classification is keyed on them and only the
// wording is parsed.
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
)

// advice renders the refusal as a sentence naming what the login would need,
// or "" when its wording could not be read (a localized server, or a message
// shape this build has not seen).
//
// An ambiguous refusal keeps its ambiguity in the wording. SQL Server will not
// say whether the object is missing or merely invisible, and a sentence that
// picks one sends the user to fix the wrong thing half the time.
//
// Msg 297 deliberately has no branch. Its text names nothing, and it is not
// only the follow-up to Msg 300 — a refused KILL, sp_readerrorlog and several
// other procedures raise it alone. Naming a right for it would be inventing
// one, which is what the whole file is written to avoid; the DMV case that
// looks like it needs a branch is already handled by classifyRefusal reading
// the *first* message, which is the 300 that names the right.
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
			return m[1] + " does not exist, or this login needs " + rightAlterDatabase.String() + " on it."
		}
	case 3701, 15151:
		if m := reCannotBecause.FindStringSubmatch(r.message); m != nil {
			return "The " + m[2] + " " + m[3] + " does not exist, or this login cannot " + m[1] + " it."
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
	case refusalCertain:
		if a := r.advice(); a != "" {
			return accessDeniedLabel + a
		}
		return accessDeniedLabel + r.message
	case refusalAmbiguous:
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

// withPermissionAdvice appends the right a refusal needs to err's own text,
// for the places that report a *failed action* rather than replacing content —
// a status line, an alert. The server's words are kept: the user asked for
// something and it did not happen, and the full failure is what they are owed;
// the advice is what they do next.
func withPermissionAdvice(err error) error {
	if err == nil {
		return nil
	}
	a := classifyRefusal(err).advice()
	if a == "" || strings.Contains(err.Error(), a) {
		return err
	}
	return errors.New(err.Error() + " — " + a)
}
