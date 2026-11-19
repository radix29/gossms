package activity

import (
	_ "embed"
	"strings"
)

// whoIsActiveScript is Adam Machanic's sp_WhoIsActive (GPL-3.0), verbatim
// except the two changes its header records. Licence: LICENSE.sp_whoisactive.
//
//go:embed whoisactive.sql
var whoIsActiveScript string

// whoIsActiveProcHeader is the upstream ALTER PROC line, the one place the
// script names itself; rewriting it gives the tempdb copy its non-sp_ name (see
// Proc).
const whoIsActiveProcHeader = "ALTER PROC dbo.sp_WhoIsActive"

// WhoIsActiveAuthor, WhoIsActiveRepo and WhoIsActiveLicense credit the
// procedure in the Sessions tab header and Help > About.
const (
	WhoIsActiveAuthor  = "Adam Machanic"
	WhoIsActiveRepo    = "https://github.com/amachanic/sp_whoisactive"
	WhoIsActiveLicense = "GPL-3.0"
)

// WhoIsActiveProc is sp_WhoIsActive, behind the Sessions tab. The script is the
// upstream release with its copyright header, so the installed procedure
// carries attribution.
var WhoIsActiveProc = &Proc{
	MasterName: "sp_WhoIsActive",
	TempDBName: "usp_WhoIsActive",
	// 2 is the fixed-width-font format: numbers right-justified to one width.
	// The default (1), meant for proportional fonts, pads by digit count alone
	// (commas uncounted), so a terminal grid shows them ragged.
	Fixed: []ProcOption{{Param: "@format_output", Value: "2"}},
	Options: []ProcOption{
		// 2 lists every sleeping session; the default (1) only those holding
		// an open transaction.
		{Label: "Sleeping Sessions", Key: 's', Param: "@show_sleeping_spids", Value: "2"},
		// Adds the additional_info XML column: SET options, isolation level,
		// SQL Agent job details and the like.
		{Label: "Additional Info", Key: 'a', Param: "@get_additional_info", Value: "1"},
	},
	script: func(name string) string {
		return strings.Replace(whoIsActiveScript, whoIsActiveProcHeader,
			"create or alter procedure dbo."+name, 1)
	},
}

// WhoIsActiveVersion is the upstream version from the embedded script's header,
// so it matches what's installed. Empty if the header lacks one.
func WhoIsActiveVersion() string {
	const marker = "Who Is Active? "
	_, rest, ok := strings.Cut(whoIsActiveScript, marker)
	if !ok {
		return ""
	}
	if j := strings.IndexAny(rest, "\r\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
