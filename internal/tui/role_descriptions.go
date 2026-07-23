package tui

// fixedRoleDescriptions gives each fixed database role a short blurb for
// a Database User Properties' Membership page — matching SSMS's own
// well-known descriptions. User-defined roles have no entry (blank).
var fixedRoleDescriptions = map[string]string{
	"db_owner":          "Full control over the database",
	"db_accessadmin":    "Add or remove access for logins",
	"db_securityadmin":  "Manage role membership and permissions",
	"db_ddladmin":       "Run any DDL command",
	"db_backupoperator": "Back up the database",
	"db_datareader":     "Read all data from all user tables",
	"db_datawriter":     "Add, delete, or change data in all user tables",
	"db_denydatareader": "Deny SELECT on all user tables",
	"db_denydatawriter": "Deny INSERT/UPDATE/DELETE on all user tables",
}

// fixedServerRoleDescriptions and serverRoleImpact give each fixed server
// role a short blurb and an impact level for Login Properties' Server
// Roles page, matching the mockup's own impact classification.
// User-defined server roles have no entry (blank).
var fixedServerRoleDescriptions = map[string]string{
	"sysadmin":      "Full control over the SQL Server instance",
	"securityadmin": "Manage logins and their properties",
	"serveradmin":   "Configure server-wide settings",
	"setupadmin":    "Add and remove linked servers",
	"processadmin":  "Manage processes running in SQL Server",
	"diskadmin":     "Manage disk files",
	"dbcreator":     "Create, alter, drop, and restore databases",
	"bulkadmin":     "Run BULK INSERT statements",
}

var serverRoleImpact = map[string]string{
	"sysadmin":      "Critical",
	"securityadmin": "Critical",
	"serveradmin":   "Critical",
	"setupadmin":    "High",
	"processadmin":  "High",
	"diskadmin":     "High",
	"dbcreator":     "High",
	"bulkadmin":     "Medium",
}
