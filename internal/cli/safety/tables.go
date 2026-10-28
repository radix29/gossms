package safety

import "github.com/radix29/gossms/internal/cli/spec"

// readLeaders are statement leaders that change nothing: control flow, local
// variables, cursors over a query (the query itself is floored), messages,
// context switches.
var readLeaders = map[string]bool{
	"USE": true, "DECLARE": true, "PRINT": true, "RAISERROR": true, "THROW": true,
	"IF": true, "ELSE": true, "WHILE": true, "BREAK": true, "CONTINUE": true,
	"RETURN": true, "GOTO": true, "WAITFOR": true, "TRY": true, "CATCH": true,
	"OPEN": true, "CLOSE": true, "FETCH": true, "DEALLOCATE": true,
	"REVERT": true, "READTEXT": true,
}

// floorWords raise a statement containing them, anywhere outside literals,
// comments and quoted identifiers, to their class (docs/cli.md § Safety). A
// column named after one over-classifies; the reason names the word.
//
// LOGIN is not a floor word on its own: CREATE/ALTER/DROP before an
// administration object (adminObjects) raises to admin wherever it stands,
// and a bare login is a common column name.
var floorWords = map[string]spec.Class{
	"INSERT": spec.ClassWrite, "UPDATE": spec.ClassWrite, "DELETE": spec.ClassWrite,
	"MERGE": spec.ClassWrite, "INTO": spec.ClassWrite, "RECEIVE": spec.ClassWrite,
	"WRITETEXT": spec.ClassWrite, "UPDATETEXT": spec.ClassWrite,

	"CREATE": spec.ClassDDL, "ALTER": spec.ClassDDL, "DROP": spec.ClassDDL,
	"TRUNCATE": spec.ClassDDL, "DISABLE": spec.ClassDDL, "ENABLE": spec.ClassDDL,

	"KILL": spec.ClassAdmin, "SHUTDOWN": spec.ClassAdmin, "BACKUP": spec.ClassAdmin,
	"RESTORE": spec.ClassAdmin, "GRANT": spec.ClassAdmin, "DENY": spec.ClassAdmin,
	"REVOKE": spec.ClassAdmin, "RECONFIGURE": spec.ClassAdmin,
}

// moduleTypes are the objects CREATE/ALTER of which must be alone in their
// batch: everything after the header is the body.
var moduleTypes = map[string]bool{
	"PROCEDURE": true, "PROC": true, "FUNCTION": true, "TRIGGER": true, "VIEW": true,
}

// indexModifiers stand between CREATE and INDEX.
var indexModifiers = map[string]bool{
	"UNIQUE": true, "CLUSTERED": true, "NONCLUSTERED": true, "COLUMNSTORE": true,
	"PRIMARY": true, "SPATIAL": true,
}

// objectWords continue an object type word into a longer one, keyed by the
// type so far: ALTER DATABASE SCOPED CONFIGURATION, CREATE MASTER KEY.
var objectWords = map[string]bool{
	"DATABASE SCOPED": true, "DATABASE SCOPED CONFIGURATION": true, "DATABASE SCOPED CREDENTIAL": true,
	"DATABASE AUDIT": true, "DATABASE ENCRYPTION": true, "DATABASE ENCRYPTION KEY": true,
	"SERVER CONFIGURATION": true, "SERVER AUDIT": true, "SERVER ROLE": true,
	"AVAILABILITY GROUP": true, "APPLICATION ROLE": true, "MASTER KEY": true,
	"SYMMETRIC KEY": true, "ASYMMETRIC KEY": true, "EVENT SESSION": true,
	"RESOURCE POOL": true, "RESOURCE GOVERNOR": true, "WORKLOAD GROUP": true,
	"FULLTEXT CATALOG": true, "FULLTEXT INDEX": true, "FULLTEXT STOPLIST": true,
	"CRYPTOGRAPHIC PROVIDER": true, "EXTERNAL RESOURCE": true, "EXTERNAL RESOURCE POOL": true,
}

// unnamedObjects are the object types that name no object: ALTER SERVER
// CONFIGURATION SET …, not an object called SET.
var unnamedObjects = map[string]bool{
	"DATABASE SCOPED CONFIGURATION": true, "SERVER CONFIGURATION": true, "RESOURCE GOVERNOR": true,
}

// adminObjects make CREATE/ALTER/DROP of them admin: databases, the server,
// security principals and secrets, high availability, resource governance and
// event sessions.
var adminObjects = map[string]bool{
	"DATABASE": true, "SERVER": true, "LOGIN": true, "USER": true, "ROLE": true,
	"APPLICATION": true, "AUTHORIZATION": true, "CERTIFICATE": true, "CREDENTIAL": true,
	"MASTER": true, "SYMMETRIC": true, "ASYMMETRIC": true, "CRYPTOGRAPHIC": true,
	"ENDPOINT": true, "AVAILABILITY": true, "RESOURCE": true, "WORKLOAD": true,
	"EVENT": true,
}

// notInTransactionObjects are the CREATE/ALTER/DROP objects SQL Server refuses
// inside a user transaction. Every DATABASE form is listed, scoped
// configuration included: a refusal is cheaper than a partial commit.
var notInTransactionObjects = map[string]bool{
	"DATABASE": true, "FULLTEXT": true,
}

// dbccRead is the DBCC read allow-list; every other DBCC command is admin.
var dbccRead = map[string]bool{
	"SHOW_STATISTICS": true, "SQLPERF": true, "TRACESTATUS": true, "OPENTRAN": true,
	"INPUTBUFFER": true, "USEROPTIONS": true,
}

// readProcs are the system procedures that only read: catalog and help
// procedures, session and lock lists, the error log, Agent job history.
// Names are lower-cased, schema and database ignored.
var readProcs = map[string]bool{
	"sp_help": true, "sp_helptext": true, "sp_helpindex": true, "sp_helpdb": true,
	"sp_helpfile": true, "sp_helpfilegroup": true, "sp_helpconstraint": true,
	"sp_helprotect": true, "sp_helpuser": true, "sp_helplogins": true, "sp_helprole": true,
	"sp_helprolemember": true, "sp_helpsrvrole": true, "sp_helpsrvrolemember": true,
	"sp_helpserver": true, "sp_helpstats": true, "sp_helptrigger": true, "sp_helpsort": true,
	"sp_helpdevice": true, "sp_spaceused": true, "sp_who": true, "sp_who2": true,
	"sp_whoisactive": true, "sp_lock": true, "sp_block": true, "sp_columns": true,
	"sp_tables": true, "sp_stored_procedures": true, "sp_fkeys": true, "sp_pkeys": true,
	"sp_statistics": true, "sp_server_info": true, "sp_databases": true, "sp_depends": true,
	"sp_describe_first_result_set": true, "sp_describe_undeclared_parameters": true,
	"sp_monitor": true, "sp_readerrorlog": true, "sp_server_diagnostics": true,
	"sp_help_job": true, "sp_help_jobhistory": true, "sp_help_jobstep": true,
	"sp_help_schedule": true, "sp_help_jobactivity": true, "sp_help_jobschedule": true,
	"xp_readerrorlog": true, "xp_fixeddrives": true, "xp_msver": true,
}

// adminProcs are system procedures that change the server, its security or its
// databases. Every xp_ procedure not in readProcs, every sp_trace_ and sp_OA
// procedure, and every msdb sp_ procedure (Agent) is admin too.
var adminProcs = map[string]bool{
	"sp_configure": true, "sp_addlogin": true, "sp_droplogin": true, "sp_password": true,
	"sp_grantlogin": true, "sp_revokelogin": true, "sp_denylogin": true,
	"sp_addsrvrolemember": true, "sp_dropsrvrolemember": true, "sp_addrolemember": true,
	"sp_droprolemember": true, "sp_addrole": true, "sp_droprole": true, "sp_adduser": true,
	"sp_dropuser": true, "sp_grantdbaccess": true, "sp_revokedbaccess": true,
	"sp_changedbowner": true, "sp_change_users_login": true, "sp_addlinkedserver": true,
	"sp_dropserver": true, "sp_addlinkedsrvlogin": true, "sp_droplinkedsrvlogin": true,
	"sp_serveroption": true, "sp_dboption": true, "sp_detach_db": true, "sp_attach_db": true,
	"sp_attach_single_file_db": true, "sp_renamedb": true, "sp_add_job": true,
	"sp_start_job": true, "sp_stop_job": true, "sp_delete_job": true, "sp_update_job": true,
	"sp_add_jobstep": true, "sp_add_jobserver": true, "sp_add_schedule": true,
	"sp_attach_schedule": true, "sp_procoption": true, "sp_addumpdevice": true,
	"sp_dropdevice": true, "sp_cycle_errorlog": true, "sp_setapprole": true,
}
