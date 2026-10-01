package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_database_mail.go is the Details pane for Management ▸
// Database Mail: status, queues, profiles, accounts and the latest failed
// items, in one Property/Value grid. The node is a leaf (docs/decisions.md), so
// its one view carries what folders would otherwise list; each section is a
// heading row with a count, then one indented row per object.
//
// Each section needs different rights (W8) and degrades alone: status needs
// DatabaseMailUserRole or more, queues VIEW SERVER STATE, profiles and
// accounts db_owner in msdb or CONTROL SERVER, and items are filtered by the
// server to the caller's own for a role-only member. A refused section says
// what it needs rather than failing the view.

// databaseMailFailedItems is how many of the newest failed items the view
// lists: enough to see a pattern, few enough not to bury the configuration.
const databaseMailFailedItems = 20

// Rights each refused section names. Configuration reads are plain msdb
// permission, not sysadmin (W8).
const (
	mailConfigNotVisible = "Not visible — db_owner in msdb or CONTROL SERVER is needed"
	mailQueueNotVisible  = "Not visible — VIEW SERVER STATE is needed"
	// mailQueueNoExecute is the queue read refused Msg 229 rather than 300:
	// EXECUTE on sysmail_help_queue_sp, which DatabaseMailUserRole lacks. A
	// db_owner past that still needs VIEW SERVER STATE (W8).
	mailQueueNoExecute  = "Not visible — CONTROL SERVER, or db_owner in msdb with VIEW SERVER STATE, is needed"
	mailItemsNotVisible = "Not visible — DatabaseMailUserRole in msdb is needed"
)

// databaseMailDetail is the Database Mail node's view.
func databaseMailDetail(ctx context.Context, sc *dbconn.ServerConn) ([]string, [][]string, error) {
	var pairs []string
	add := func(kv ...string) { pairs = append(pairs, kv...) }

	switch st, err := sc.Server.MailStatus(ctx); {
	case err == nil:
		add("Status", st.String())
		xps := "1"
		if st == gosmo.MailDisabled {
			xps = "0 — nothing is sent; enable it in Server Properties ▸ Advanced"
		}
		add("Database Mail XPs", xps)
	case isRefusal(err):
		// MailStatus reads 'Database Mail XPs' first and calls the refused
		// procedure only when it is 1, so a refusal says it is on.
		add("Status", mailItemsNotVisible, "Database Mail XPs", "1")
	default:
		return nil, nil, err
	}

	switch queues, err := sc.Server.MailQueues(ctx); {
	case err == nil:
		for _, q := range queues {
			add(mailQueueLabel(q.Type), mailQueueText(q))
		}
	case isRefusal(err):
		if classifyRefusal(err).number == 300 {
			add("Queues", mailQueueNotVisible)
		} else {
			add("Queues", mailQueueNoExecute)
		}
	default:
		return nil, nil, err
	}

	switch profiles, err := sc.Server.MailProfiles(ctx); {
	case err == nil:
		add("Profiles", strconv.Itoa(len(profiles)))
		for _, p := range profiles {
			add("  "+p.Name, mailProfileText(p))
		}
	case isRefusal(err):
		add("Profiles", mailConfigNotVisible)
	default:
		return nil, nil, err
	}

	switch accounts, err := sc.Server.MailAccounts(ctx); {
	case err == nil:
		add("Accounts", strconv.Itoa(len(accounts)))
		for _, a := range accounts {
			add("  "+a.Name, mailAccountText(a))
		}
	case isRefusal(err):
		add("Accounts", mailConfigNotVisible)
	default:
		return nil, nil, err
	}

	heading := fmt.Sprintf("Failed items (latest %d)", databaseMailFailedItems)
	if !mailVisibility(ctx, sc).AllItems {
		heading = fmt.Sprintf("Your failed items (latest %d)", databaseMailFailedItems)
	}
	switch items, err := sc.Server.MailItems(ctx, gosmo.MailItemFilter{Status: gosmo.MailFailed, Max: databaseMailFailedItems}); {
	case err == nil:
		add(heading, strconv.Itoa(len(items)))
		for _, m := range items {
			add(fmt.Sprintf("  #%d  %s", m.MailItemID, formatSQLDate(m.SendRequestDate)), mailItemText(m))
		}
	case isRefusal(err):
		add(heading, mailItemsNotVisible)
	default:
		return nil, nil, err
	}
	return propertyRows(pairs...)
}

// mailVisibility is how much of Database Mail's items and log sc's login
// reads: gosmo reads the base tables for a login that may (sysadmin, msdb
// db_owner or db_datareader, CONTROL SERVER), and the sysadmin-filtered views
// otherwise, which leave a DatabaseMailUserRole member its own items and their
// events (docs/decisions.md). A failed read is not presumed limited — the same answer as
// before the question was asked.
func mailVisibility(ctx context.Context, sc *dbconn.ServerConn) gosmo.MailVisibility {
	v, err := sc.Server.MailVisibility(ctx)
	if err != nil {
		return gosmo.MailVisibility{AllItems: true, AllEvents: true}
	}
	return v
}

// isRefusal reports whether err is the server refusing the login — what a
// section shows as "not visible" rather than failing the whole view.
func isRefusal(err error) bool { return classifyRefusal(err).kind != notARefusal }

// mailQueueLabel names one of sysmail_help_queue_sp's two queues.
func mailQueueLabel(typ string) string {
	switch strings.ToLower(typ) {
	case "mail":
		return "Mail queue"
	case "status":
		return "Status queue"
	}
	return "Queue " + typ
}

// mailQueueText is a queue's length and its monitor's state.
func mailQueueText(q *gosmo.MailQueue) string {
	if q.State == "" {
		return strconv.Itoa(q.Length)
	}
	return fmt.Sprintf("%d (%s)", q.Length, strings.ToLower(q.State))
}

// mailProfileText is a profile's accounts in failover order and its public
// grant. Private grants are Profile Security's to show.
func mailProfileText(p *gosmo.MailProfile) string {
	names := make([]string, 0, len(p.Accounts))
	for _, a := range p.Accounts {
		names = append(names, a.AccountName)
	}
	parts := []string{"no accounts"}
	if len(names) > 0 {
		parts = []string{strings.Join(names, ", ")}
	}
	switch {
	case p.IsDefault:
		parts = append(parts, "public, default")
	case p.IsPublic:
		parts = append(parts, "public")
	}
	return strings.Join(parts, " · ")
}

// mailAccountText is an account's address, SMTP server and authentication.
func mailAccountText(a *gosmo.MailAccount) string {
	parts := []string{a.EmailAddress, fmt.Sprintf("%s:%d", a.ServerName, a.Port)}
	if a.EnableSSL {
		parts = append(parts, "SSL")
	}
	auth := a.Authentication().String()
	if a.Authentication() == gosmo.MailAuthBasic && a.UserName != "" {
		auth += " (" + a.UserName + ")"
	}
	return strings.Join(append(parts, auth), " · ")
}

// mailItemText is a failed item's recipients and subject. Why it failed is in
// the Database Mail log, keyed by the item's id.
func mailItemText(m *gosmo.MailItem) string {
	return flattenLogText(m.Recipients) + " · " + flattenLogText(m.Subject)
}
