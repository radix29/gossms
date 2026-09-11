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
	script: func(name string) string {
		return strings.Replace(whoIsActiveScript, whoIsActiveProcHeader,
			"create or alter procedure dbo."+name, 1)
	},
}

// WhoIsActiveVersion is the upstream version from the embedded script's header,
// so it matches what's installed. Empty if the header lacks one.
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
