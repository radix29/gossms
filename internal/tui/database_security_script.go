package tui

import (
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// database_security_script.go is Object Explorer's database-node "Script
// Database Security": a query window holding a generator script that, when
// the user runs it, returns the T-SQL recreating the database's security —
// owner, roles, users, schemas and their owners, role memberships and
// explicit permissions — as one ordered [Script] column. It is opened, never
// executed: running it is the user's call, and it only reads the catalog.

// scriptDatabaseSecurity opens the generator for dbName in a new query
// window connected to that database.
func (a *App) scriptDatabaseSecurity(sc *db.ServerConn, dbName string) {
	if !a.requireConn(sc) {
		return
	}
	a.openQueryWithText(sc, dbName, databaseSecurityScript(dbName))
}

// databaseSecurityScript is the generator for dbName. Every statement it
// emits is guarded or idempotent (IF DATABASE_PRINCIPAL_ID(...) IS NULL,
// IF SCHEMA_ID(...) IS NULL, ALTER ROLE ... ADD MEMBER), so its output can
// be re-run against a database that already holds part of it — the usual
// case after a restore onto another server. Contained users and application
// roles get @password as a placeholder: their real passwords are not
// readable, so it is gosmo.PasswordPlaceholder, as gosmo's scripter uses. It
// fails Windows complexity (no upper case, no digit) where the host enforces
// it; N'ChangeMe123!', used until 2026-10, passed. A host with no policy (a
// standalone workstation, live) accepts any literal, so the placeholder only
// keeps the output from planting a password that looks deliberate.
// It targets SQL Server 2016+ (authentication_type, ALTER ROLE ... ADD
// MEMBER) and Azure SQL Database, where EngineEdition 5 makes an Entra user
// FROM EXTERNAL PROVIDER even if a login of its SID were visible.
func databaseSecurityScript(dbName string) string {
	return "USE " + gosmo.QuoteName(dbName) + ";\nGO\n" + databaseSecurityScriptBody
}

var databaseSecurityScriptBody = `
-- Script Database Security: run this (F5) and copy the [Script] column.
-- It only reads the catalog; the statements it returns are not executed.

SET NOCOUNT ON;

-- Placeholder for contained users and application roles, whose passwords
-- cannot be read back. Replace it on each line marked "set the real
-- password" before running the output.
DECLARE @password nvarchar(128) = ` + gosmo.QuoteLiteral(gosmo.PasswordPlaceholder) + `;
DECLARE @azure bit = CASE WHEN SERVERPROPERTY('EngineEdition') = 5 THEN 1 ELSE 0 END;

IF OBJECT_ID('tempdb..#script') IS NOT NULL DROP TABLE #script;
IF OBJECT_ID('tempdb..#principals') IS NOT NULL DROP TABLE #principals;
CREATE TABLE #script (id int IDENTITY PRIMARY KEY, line nvarchar(max) NOT NULL);

-- The principals a CREATE can make: not public (0), dbo (1), guest (2),
-- INFORMATION_SCHEMA (3), sys (4), a fixed role, or a ##MS_...## principal.
SELECT dp.principal_id
INTO   #principals
FROM   sys.database_principals dp
WHERE  dp.principal_id > 4
  AND  dp.is_fixed_role = 0
  AND  dp.name NOT LIKE N'##%';

INSERT #script (line) VALUES
    (N'-- Security for database ' + QUOTENAME(DB_NAME()) + N' on ' + QUOTENAME(@@SERVERNAME)
        + N', scripted ' + CONVERT(nvarchar(19), SYSDATETIME(), 120)),
    (N'USE ' + QUOTENAME(DB_NAME()) + N';'),
    (N'GO');

-- Database owner ------------------------------------------------------------
INSERT #script (line) VALUES (N''), (N'-- Database owner');
INSERT #script (line)
SELECT CASE
           WHEN SUSER_SNAME(d.owner_sid) IS NULL
               THEN N'-- the owner''s login is not visible here (SID ' + CONVERT(nvarchar(200), d.owner_sid, 1) + N')'
           ELSE N'ALTER AUTHORIZATION ON DATABASE::' + QUOTENAME(d.name) + N' TO ' + QUOTENAME(SUSER_SNAME(d.owner_sid)) + N';'
       END
FROM   sys.databases d
WHERE  d.database_id = DB_ID();

-- Roles ---------------------------------------------------------------------
-- Created owned by dbo: without AUTHORIZATION the owner would be whoever runs
-- the output, e.g. a db_owner member who isn't dbo. A role owned by anyone
-- else is handed over in the ownership section, once every owner exists.
INSERT #script (line) VALUES (N''), (N'-- Database roles');
INSERT #script (line)
SELECT N'IF DATABASE_PRINCIPAL_ID(N' + QUOTENAME(dp.name, '''') + N') IS NULL CREATE ROLE ' + QUOTENAME(dp.name) + N' AUTHORIZATION [dbo];'
FROM   sys.database_principals dp
JOIN   #principals p ON p.principal_id = dp.principal_id
WHERE  dp.type = 'R'
ORDER  BY dp.name;

INSERT #script (line) VALUES (N''), (N'-- Application roles');
INSERT #script (line)
SELECT N'IF DATABASE_PRINCIPAL_ID(N' + QUOTENAME(dp.name, '''') + N') IS NULL CREATE APPLICATION ROLE ' + QUOTENAME(dp.name)
       + N' WITH PASSWORD = N' + QUOTENAME(@password, '''')
       + ISNULL(N', DEFAULT_SCHEMA = ' + QUOTENAME(dp.default_schema_name), N'')
       + N';  -- set the real password'
FROM   sys.database_principals dp
JOIN   #principals p ON p.principal_id = dp.principal_id
WHERE  dp.type = 'A'
ORDER  BY dp.name;

-- Users ---------------------------------------------------------------------
-- authentication_type: 0 NONE (WITHOUT LOGIN), 1 INSTANCE (a login),
-- 2 DATABASE (contained, password), 3 WINDOWS, 4 EXTERNAL (Entra).
-- SUSER_SNAME resolves a Windows SID through the OS even with no server
-- login, so a WINDOWS user named as its account gets the bare CREATE USER it
-- was made with, not FOR LOGIN naming a login that may not exist. (SQL 2017
-- accepts FOR LOGIN for a login-less Windows account too, so this restates
-- the source rather than avoiding an error seen live.)
INSERT #script (line) VALUES (N''), (N'-- Database users');
INSERT #script (line)
SELECT N'IF DATABASE_PRINCIPAL_ID(N' + QUOTENAME(dp.name, '''') + N') IS NULL CREATE USER ' + QUOTENAME(dp.name)
       + CASE
             WHEN dp.type = 'C' THEN N' FOR CERTIFICATE ' + QUOTENAME(c.name)
             WHEN dp.type = 'K' THEN N' FOR ASYMMETRIC KEY ' + QUOTENAME(ak.name)
             WHEN dp.authentication_type = 2 THEN N' WITH PASSWORD = N' + QUOTENAME(@password, '''')
             WHEN dp.authentication_type = 0 THEN N' WITHOUT LOGIN'
             WHEN dp.type IN ('E', 'X') AND (@azure = 1 OR l.login_name IS NULL) THEN N' FROM EXTERNAL PROVIDER'
             WHEN dp.authentication_type = 3 AND dp.name = l.login_name THEN N''
             ELSE N' FOR LOGIN ' + QUOTENAME(ISNULL(l.login_name, dp.name))
         END
       + CASE
             WHEN dp.type IN ('C', 'K') OR dp.default_schema_name IS NULL OR dp.default_schema_name = N'dbo' THEN N''
             WHEN dp.authentication_type = 2 THEN N', DEFAULT_SCHEMA = ' + QUOTENAME(dp.default_schema_name)
             ELSE N' WITH DEFAULT_SCHEMA = ' + QUOTENAME(dp.default_schema_name)
         END
       + N';'
       + CASE
             WHEN dp.authentication_type = 2 THEN N'  -- set the real password'
             WHEN dp.authentication_type = 1 AND l.login_name IS NULL THEN N'  -- no login with this user''s SID is visible here; check the login name'
             ELSE N''
         END
FROM   sys.database_principals dp
JOIN   #principals p ON p.principal_id = dp.principal_id
CROSS  APPLY (SELECT SUSER_SNAME(dp.sid) AS login_name) l
LEFT   JOIN sys.certificates c ON dp.type = 'C' AND c.sid = dp.sid
LEFT   JOIN sys.asymmetric_keys ak ON dp.type = 'K' AND ak.sid = dp.sid
WHERE  dp.type IN ('S', 'U', 'G', 'E', 'X', 'C', 'K')
ORDER  BY dp.name;

-- Schemas and ownership -----------------------------------------------------
-- A GRANT ... ON SCHEMA:: needs the schema, so each user schema is created
-- if missing (CREATE SCHEMA must start a batch, hence EXEC) or handed to its
-- owner if present.
INSERT #script (line) VALUES (N''), (N'-- Schemas');
INSERT #script (line)
SELECT N'IF SCHEMA_ID(N' + QUOTENAME(s.name, '''') + N') IS NULL EXEC (N'''
       + REPLACE(N'CREATE SCHEMA ' + QUOTENAME(s.name) + N' AUTHORIZATION ' + QUOTENAME(o.name), N'''', N'''''')
       + N''') ELSE ALTER AUTHORIZATION ON SCHEMA::' + QUOTENAME(s.name) + N' TO ' + QUOTENAME(o.name) + N';'
FROM   sys.schemas s
JOIN   sys.database_principals o ON o.principal_id = s.principal_id
WHERE  s.schema_id > 4 AND s.schema_id < 16384
ORDER  BY s.name;

INSERT #script (line) VALUES (N''), (N'-- Role ownership');
INSERT #script (line)
SELECT N'ALTER AUTHORIZATION ON ROLE::' + QUOTENAME(dp.name) + N' TO ' + QUOTENAME(o.name) + N';'
FROM   sys.database_principals dp
JOIN   #principals p ON p.principal_id = dp.principal_id
JOIN   sys.database_principals o ON o.principal_id = dp.owning_principal_id
WHERE  dp.type = 'R' AND dp.owning_principal_id <> 1
ORDER  BY dp.name;

-- Role memberships ----------------------------------------------------------
INSERT #script (line) VALUES (N''), (N'-- Role memberships');
INSERT #script (line)
SELECT N'ALTER ROLE ' + QUOTENAME(r.name) + N' ADD MEMBER ' + QUOTENAME(m.name) + N';'
FROM   sys.database_role_members rm
JOIN   sys.database_principals r ON r.principal_id = rm.role_principal_id
JOIN   sys.database_principals m ON m.principal_id = rm.member_principal_id
WHERE  m.principal_id IN (SELECT principal_id FROM #principals)
ORDER  BY r.name, m.name;

-- Permissions ---------------------------------------------------------------
-- Explicit GRANT/DENY to every grantee but dbo, INFORMATION_SCHEMA, sys and
-- the ##MS_...## principals (public and guest are kept: a grant to either is
-- a deliberate opening), minus grants on system objects, which the server
-- recreates itself. A class this script cannot name is listed as a comment.
INSERT #script (line) VALUES (N''), (N'-- Permissions');
INSERT #script (line)
SELECT CASE
           WHEN perm.class <> 0 AND sec.securable IS NULL
               THEN N'-- not scripted: ' + perm.state_desc COLLATE DATABASE_DEFAULT + N' ' + perm.permission_name COLLATE DATABASE_DEFAULT
                    + N' on ' + perm.class_desc COLLATE DATABASE_DEFAULT
                    + N' id ' + CONVERT(nvarchar(20), perm.major_id) + N' to ' + QUOTENAME(g.name)
           ELSE CASE perm.state WHEN 'D' THEN N'DENY ' WHEN 'R' THEN N'REVOKE ' ELSE N'GRANT ' END
                + perm.permission_name COLLATE DATABASE_DEFAULT
                + ISNULL(N' ON ' + sec.securable, N'')
                + N' TO ' + QUOTENAME(g.name)
                + CASE WHEN perm.state = 'W' THEN N' WITH GRANT OPTION' ELSE N'' END
                + N';'
       END
FROM   sys.database_permissions perm
JOIN   sys.database_principals g ON g.principal_id = perm.grantee_principal_id
CROSS  APPLY (SELECT CASE perm.class
    WHEN 1  THEN N'OBJECT::' + QUOTENAME(OBJECT_SCHEMA_NAME(perm.major_id)) + N'.' + QUOTENAME(OBJECT_NAME(perm.major_id))
                 + CASE WHEN perm.minor_id > 0 THEN N' (' + QUOTENAME(COL_NAME(perm.major_id, perm.minor_id)) + N')' ELSE N'' END
    WHEN 3  THEN N'SCHEMA::' + QUOTENAME(SCHEMA_NAME(perm.major_id))
    WHEN 4  THEN (SELECT CASE x.type WHEN 'R' THEN N'ROLE::' WHEN 'A' THEN N'APPLICATION ROLE::' ELSE N'USER::' END + QUOTENAME(x.name)
                  FROM sys.database_principals x WHERE x.principal_id = perm.major_id)
    WHEN 5  THEN (SELECT N'ASSEMBLY::' + QUOTENAME(x.name) FROM sys.assemblies x WHERE x.assembly_id = perm.major_id)
    WHEN 6  THEN (SELECT N'TYPE::' + QUOTENAME(SCHEMA_NAME(x.schema_id)) + N'.' + QUOTENAME(x.name) FROM sys.types x WHERE x.user_type_id = perm.major_id)
    WHEN 10 THEN (SELECT N'XML SCHEMA COLLECTION::' + QUOTENAME(SCHEMA_NAME(x.schema_id)) + N'.' + QUOTENAME(x.name)
                  FROM sys.xml_schema_collections x WHERE x.xml_collection_id = perm.major_id)
    WHEN 15 THEN (SELECT N'MESSAGE TYPE::' + QUOTENAME(x.name) COLLATE DATABASE_DEFAULT FROM sys.service_message_types x WHERE x.message_type_id = perm.major_id)
    WHEN 16 THEN (SELECT N'CONTRACT::' + QUOTENAME(x.name) COLLATE DATABASE_DEFAULT FROM sys.service_contracts x WHERE x.service_contract_id = perm.major_id)
    WHEN 17 THEN (SELECT N'SERVICE::' + QUOTENAME(x.name) COLLATE DATABASE_DEFAULT FROM sys.services x WHERE x.service_id = perm.major_id)
    WHEN 18 THEN (SELECT N'REMOTE SERVICE BINDING::' + QUOTENAME(x.name) COLLATE DATABASE_DEFAULT FROM sys.remote_service_bindings x WHERE x.remote_service_binding_id = perm.major_id)
    WHEN 19 THEN (SELECT N'ROUTE::' + QUOTENAME(x.name) COLLATE DATABASE_DEFAULT FROM sys.routes x WHERE x.route_id = perm.major_id)
    WHEN 23 THEN (SELECT N'FULLTEXT CATALOG::' + QUOTENAME(x.name) FROM sys.fulltext_catalogs x WHERE x.fulltext_catalog_id = perm.major_id)
    WHEN 24 THEN (SELECT N'SYMMETRIC KEY::' + QUOTENAME(x.name) FROM sys.symmetric_keys x WHERE x.symmetric_key_id = perm.major_id)
    WHEN 25 THEN (SELECT N'CERTIFICATE::' + QUOTENAME(x.name) FROM sys.certificates x WHERE x.certificate_id = perm.major_id)
    WHEN 26 THEN (SELECT N'ASYMMETRIC KEY::' + QUOTENAME(x.name) FROM sys.asymmetric_keys x WHERE x.asymmetric_key_id = perm.major_id)
    WHEN 29 THEN (SELECT N'FULLTEXT STOPLIST::' + QUOTENAME(x.name) FROM sys.fulltext_stoplists x WHERE x.stoplist_id = perm.major_id)
    WHEN 31 THEN (SELECT N'SEARCH PROPERTY LIST::' + QUOTENAME(x.name) FROM sys.registered_search_property_lists x WHERE x.property_list_id = perm.major_id)
END AS securable) sec
WHERE  perm.grantee_principal_id NOT IN (1, 3, 4)
  AND  g.name NOT LIKE N'##%'
  AND  NOT (perm.class = 1 AND (perm.major_id < 0 OR OBJECTPROPERTY(perm.major_id, 'IsMSShipped') = 1))
ORDER  BY g.name, perm.class, sec.securable, perm.permission_name;

-- Orphaned users ------------------------------------------------------------
INSERT #script (line) VALUES (N''), (N'-- Orphaned users (SQL users whose SID matches no visible login)');
INSERT #script (line)
SELECT N'-- ALTER USER ' + QUOTENAME(dp.name) + N' WITH LOGIN = [<login>];  -- SID ' + CONVERT(nvarchar(200), dp.sid, 1)
FROM   sys.database_principals dp
JOIN   #principals p ON p.principal_id = dp.principal_id
WHERE  dp.type = 'S' AND dp.authentication_type = 1 AND SUSER_SNAME(dp.sid) IS NULL
ORDER  BY dp.name;

SELECT line AS [Script] FROM #script ORDER BY id;

DROP TABLE #script;
DROP TABLE #principals;
`
