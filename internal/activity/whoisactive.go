package activity

import (
	_ "embed"
	"strings"
)

// whoIsActiveScript is Adam Machanic's sp_WhoIsActive, GPL-3.0, carried
// verbatim except for the two changes its own header records. The licence
// text sits beside it as LICENSE.sp_whoisactive.
//
//go:embed whoisactive.sql
var whoIsActiveScript string

// whoIsActiveProcHeader is the upstream ALTER PROC line, the one place the
// script names itself. Rewriting it is what lets the tempdb copy carry a name
// without the sp_ prefix — see Proc for why that matters.
const whoIsActiveProcHeader = "ALTER PROC dbo.sp_WhoIsActive"

// WhoIsActiveAuthor, WhoIsActiveRepo and WhoIsActiveLicense credit the
// procedure wherever goSSMS shows it: the Sessions tab's own header line and
// Help > About.
const (
	WhoIsActiveAuthor  = "Adam Machanic"
	WhoIsActiveRepo    = "https://github.com/amachanic/sp_whoisactive"
	WhoIsActiveLicense = "GPL-3.0"
)

// WhoIsActiveProc is sp_WhoIsActive, the procedure behind the Sessions tab.
//
// Unlike BlockProc's body, the script is not written here: it is the upstream
// release script with its own copyright header intact, so the installed
// procedure carries the attribution too.
var WhoIsActiveProc = &Proc{
	MasterName: "sp_WhoIsActive",
	TempDBName: "usp_WhoIsActive",
	script: func(name string) string {
		return strings.Replace(whoIsActiveScript, whoIsActiveProcHeader,
			"create or alter procedure dbo."+name, 1)
	},
}

// WhoIsActiveVersion is the upstream version string, read out of the embedded
// script's header so it can never disagree with what is actually installed.
// Empty if the header ever stops carrying one.
func WhoIsActiveVersion() string {
	const marker = "Who Is Active? "
	i := strings.Index(whoIsActiveScript, marker)
	if i < 0 {
		return ""
	}
	rest := whoIsActiveScript[i+len(marker):]
	if j := strings.IndexAny(rest, "\r\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
