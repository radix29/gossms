package safety

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/spec"
)

const (
	read  = spec.ClassRead
	write = spec.ClassWrite
	ddl   = spec.ClassDDL
	admin = spec.ClassAdmin
)

// One script's class, per rule of docs/cli.md § Safety (comm-params §6.1).
func TestClassifyScriptClass(t *testing.T) {
	for _, tt := range []struct {
		script string
		want   spec.Class
	}{
		// read
		{"SELECT * FROM sys.objects", read},
		{"SELECT a, b FROM t WITH (NOLOCK) WHERE x = 1 ORDER BY a", read},
		{"SELECT 1 UNION ALL SELECT 2 EXCEPT SELECT 3", read},
		{"(SELECT 1) UNION (SELECT 2)", read},
		{"WITH c AS (SELECT 1 AS n) SELECT n FROM c", read},
		{"WITH XMLNAMESPACES ('urn:x' AS x) SELECT 1", read},
		{"USE master", read},
		{"SET NOCOUNT ON", read},
		{"SET TRANSACTION ISOLATION LEVEL SNAPSHOT", read},
		{"DECLARE @x int = 1; SET @x = (SELECT COUNT(*) FROM t); PRINT @x", read},
		{"IF 1 = 1 BEGIN PRINT 'a' END ELSE PRINT 'b'", read},
		{"BEGIN TRY SELECT 1/0 END TRY BEGIN CATCH THROW; END CATCH", read},
		{"RAISERROR('x', 10, 1) WITH NOWAIT", read},
		{"THROW 50000, 'x', 1", read},
		{"WAITFOR DELAY '00:00:01'", read},
		{"DECLARE c CURSOR FOR SELECT a FROM t FOR UPDATE OF a\nOPEN c\nFETCH NEXT FROM c INTO @a\nCLOSE c\nDEALLOCATE c", read},
		{"retry: PRINT 1", read},
		{"EXECUTE AS USER = 'u'; SELECT 1; REVERT", read},
		{"EXEC sp_who2", read},
		{"EXEC dbo.sp_help 'dbo.t'", read},
		{"EXEC master..sp_who", read},
		{"EXEC [sys].[sp_spaceused]", read},

		// xp_ names are not sp_ ones: dbo.xp_msver outside master is the
		// database's own procedure (probed live); master.dbo still runs the
		// system one even beside a user master.dbo.xp_fixeddrives.
		{"EXEC dbo.xp_msver", write},
		{"EXEC userdb.dbo.xp_readerrorlog", write},
		{"EXEC srv.userdb.dbo.xp_fixeddrives", write},
		{"INSERT #t EXEC dbo.xp_msver", write},
		{"EXEC evil.xp_msver", write},
		{"EXEC xp_msver", read},
		{"EXEC sys.xp_msver", read},
		{"EXEC db..xp_msver", read},
		{"EXEC master..xp_fixeddrives", read},
		{"EXEC master.dbo.xp_fixeddrives", read},
		{"EXEC [MASTER].[DBO].[XP_READERRORLOG]", read},
		{"sp_helptext 'dbo.p'", read},
		{"DBCC SHOW_STATISTICS ('dbo.t', ix)", read},
		{"DBCC SQLPERF(LOGSPACE)", read},
		{"DBCC OPENTRAN", read},
		{"DBCC TRACESTATUS(-1)", read},
		{"DBCC INPUTBUFFER(52)", read},
		{"SELECT col FROM t OPTION (MERGE JOIN)", read},
		{"SELECT 1 FROM a INNER MERGE JOIN b ON a.i = b.i", read},
		{"SELECT 'DELETE FROM t' AS s, [drop] FROM t -- UPDATE t\n/* KILL 1 */", read},
		{"SELECT \"insert\" FROM t", read},
		{"SELECT @update, #delete.x FROM #delete", read},
		{"SELECT login FROM app.users", read},
		{"SELECT next, value FROM t FOR XML PATH", read},
		{"SELECT [next] value FOR XML PATH", read},
		{"SELECT 'NEXT VALUE FOR s' -- NEXT VALUE FOR s", read},
		{"FETCH NEXT FROM c INTO @v", read},
		// Words that begin a verb phrase stay read as names.
		{"SELECT send, move, get, [add], add_date FROM t", read},
		{"SELECT 1 AS [end] ORDER BY send", read},
		{"IF 1 = 1 SELECT send FROM t ELSE SELECT get FROM t", read},
		{"", read},
		{"-- only a comment", read},

		// write
		{"INSERT INTO t VALUES (1)", write},
		{"INSERT t SELECT 1", write},
		{"INSERT #t EXEC sp_who2", write},
		{"UPDATE t SET a = 1 WHERE b = 2", write},
		{"UPDATE TOP (10) t SET a = 1", write},
		{"DELETE FROM t WHERE a = 1", write},
		{"DELETE t", write},
		{"MERGE INTO t USING s ON t.i = s.i WHEN MATCHED THEN UPDATE SET a = 1 WHEN NOT MATCHED THEN INSERT VALUES (1);", write},
		{"WITH c AS (SELECT * FROM t) DELETE FROM c", write},
		{"WITH c AS (SELECT * FROM t) UPDATE c SET a = 1", write},
		{"UPDATE t SET a = 1 OUTPUT inserted.a INTO @log", write},
		{"BULK INSERT t FROM 'c:\\x.csv'", write},
		{"BEGIN TRAN", write},
		{"BEGIN TRANSACTION t1", write},
		{"BEGIN DISTRIBUTED TRANSACTION", write},
		{"COMMIT", write},
		{"COMMIT TRANSACTION", write},
		{"ROLLBACK TRAN", write},
		{"SAVE TRAN s1", write},
		{"IF @@TRANCOUNT > 0 ROLLBACK", write},
		{"SET IMPLICIT_TRANSACTIONS ON", write},
		{"EXEC dbo.usp_unknown 1", write},
		{"EXEC @rc = dbo.usp_unknown", write},
		{"usp_unknown 1, 2", write},
		{"[dbo].[usp_unknown] 1", write},
		{"EXEC (@sql)", write},
		{"EXEC ('SELECT 1')", write},
		{"EXEC @procName", write},
		{"EXEC sp_executesql N'SELECT 1'", write},
		{"EXEC ('SELECT 1') AT linked", write},
		{"SELECT * FROM OPENQUERY(linked, 'SELECT 1')", write},
		{"SELECT * FROM OPENROWSET('SQLNCLI', 'x', 'SELECT 1')", write},
		{"SELECT NEXT VALUE FOR dbo.seq", write},
		{"select next   value\nfor [dbo].[seq]", write},
		{"SELECT NEXT VALUE FOR s OVER (ORDER BY name) FROM sys.objects", write},
		{"DECLARE @n bigint = NEXT VALUE FOR s", write},
		{"SET @n = NEXT VALUE FOR s", write},
		{"RECEIVE TOP (1) * FROM q", write},
		{"BEGIN DIALOG @h FROM SERVICE s TO SERVICE 't'", write},
		{"FOO BAR", write}, // unknown at batch start: an implicit EXEC of FOO
		{"SELECT 1; FOO BAR", write},

		// ddl
		{"SELECT * INTO #t FROM sys.objects", ddl},
		{"SELECT a INTO dbo.copy FROM t UNION SELECT b FROM u", ddl},
		{"WITH c AS (SELECT 1 AS n) SELECT n INTO #t FROM c", ddl},
		{"CREATE TABLE t (id int REFERENCES p ON DELETE CASCADE ON UPDATE NO ACTION)", ddl},
		{"CREATE UNIQUE CLUSTERED INDEX ix ON t(a)", ddl},
		{"ALTER TABLE t ADD c int", ddl},
		{"ALTER TABLE t DROP CONSTRAINT ck", ddl},
		{"DROP TABLE IF EXISTS t", ddl},
		{"DROP PROCEDURE dbo.p", ddl},
		{"TRUNCATE TABLE t", ddl},
		{"CREATE SCHEMA s", ddl},
		{"CREATE PROCEDURE p AS SELECT 1", ddl},
		{"CREATE OR ALTER PROC dbo.p AS BEGIN SET NOCOUNT ON; DELETE t; COMMIT; EXEC (@x) END", ddl},
		{"ALTER VIEW v AS SELECT 1 AS a", ddl},
		{"CREATE FUNCTION f() RETURNS int AS BEGIN RETURN 1 END", ddl},
		{"CREATE TRIGGER tr ON t AFTER INSERT, UPDATE AS IF UPDATE(a) ROLLBACK", ddl},
		{"DISABLE TRIGGER tr ON t", ddl},
		{"ENABLE TRIGGER ALL ON t", ddl},
		{"UPDATE STATISTICS t", ddl},
		{"update statistics [dbo].[t] ix_a WITH FULLSCAN", ddl},
		{"IF 1 = 1 UPDATE STATISTICS t WITH SAMPLE 10 PERCENT", ddl},
		{"BEGIN TRY UPDATE STATISTICS t END TRY BEGIN CATCH END CATCH", ddl},
		{"ADD SIGNATURE TO p BY CERTIFICATE c", ddl},

		// admin
		{"KILL 53", admin},
		{"SHUTDOWN WITH NOWAIT", admin},
		{"BACKUP DATABASE d TO DISK = 'x'", admin},
		{"BACKUP LOG d TO DISK = 'x'", admin},
		{"RESTORE DATABASE d FROM DISK = 'x' WITH REPLACE", admin},
		{"RESTORE VERIFYONLY FROM DISK = 'x'", admin},
		{"DBCC CHECKDB", admin},
		{"DBCC TRACEON (7745, -1)", admin},
		{"DBCC FREEPROCCACHE", admin},
		{"DBCC SQLPERF('sys.dm_os_wait_stats', CLEAR)", admin},
		{"ALTER DATABASE d SET RECOVERY FULL", admin},
		{"ALTER DATABASE SCOPED CONFIGURATION SET MAXDOP = 4", admin},
		{"CREATE DATABASE d", admin},
		{"DROP DATABASE d", admin},
		{"ALTER SERVER CONFIGURATION SET PROCESS AFFINITY CPU = AUTO", admin},
		{"EXEC sp_configure 'max degree of parallelism', 4", admin},
		{"EXEC sys.sp_updatestats @resample = 'resample'", ddl},
		{"sp_updatestats", ddl},
		{"EXEC [master].[sys].[sp_createstats] 'indexonly'", ddl},
		{"sp_configure 'show advanced options', 1", admin},
		{"RECONFIGURE", admin},
		{"RECONFIGURE WITH OVERRIDE", admin},
		{"CREATE LOGIN l WITH PASSWORD = 'x'", admin},
		{"ALTER LOGIN l DISABLE", admin},
		{"DROP USER u", admin},
		{"CREATE USER u FOR LOGIN l", admin},
		{"ALTER ROLE db_datareader ADD MEMBER u", admin},
		{"CREATE SERVER ROLE r", admin},
		{"GRANT SELECT ON SCHEMA::dbo TO u", admin},
		{"GRANT SELECT, INSERT, UPDATE, DELETE ON t TO u WITH GRANT OPTION", admin},
		{"DENY DELETE ON t TO u", admin},
		{"REVOKE EXECUTE ON p FROM u", admin},
		{"ALTER AUTHORIZATION ON DATABASE::d TO sa", admin},
		{"CREATE MASTER KEY ENCRYPTION BY PASSWORD = 'x'", admin},
		{"CREATE CERTIFICATE c WITH SUBJECT = 'x'", admin},
		{"ALTER AVAILABILITY GROUP ag FAILOVER", admin},
		{"CREATE EVENT SESSION s ON SERVER ADD EVENT sqlserver.error_reported", admin},
		{"EXEC msdb.dbo.sp_start_job @job_name = 'j'", admin},
		{"EXEC msdb.dbo.sp_add_jobstep @job_name = 'j'", admin},
		{"EXEC xp_cmdshell 'dir'", admin},
		{"EXEC master.dbo.xp_regwrite 'x'", admin},
		{"EXEC sp_trace_setstatus 2, 0", admin},
		{"CHECKPOINT", admin},
		// R1 (review 2026-11-13): verbs the splitter does not cut at keep their
		// class nested behind IF, ELSE, WHILE and BEGIN.
		{"IF 1=1 SETUSER 'x'", admin},
		{"IF 1=1 CHECKPOINT", admin},
		{"IF 1=1 SEND ON CONVERSATION @h ('x')", write},
		{"IF 1=1 MOVE CONVERSATION @a TO @b", write},
		{"IF 1=1 ADD SIGNATURE TO dbo.p BY CERTIFICATE c", ddl},
		{"IF 1=1 ADD COUNTER SIGNATURE TO dbo.p BY CERTIFICATE c", ddl},
		{"IF 1=1 GET CONVERSATION GROUP @g FROM q", write},
		{"WAITFOR (GET CONVERSATION GROUP @g FROM q)", write},
		{"IF 1=1 END CONVERSATION @h", write},
		{"IF 1=1 PRINT 1 ELSE SEND ON CONVERSATION @h", write},
		{"WHILE 1=1 SETUSER", admin},
		{"BEGIN TRY MOVE CONVERSATION @a TO @b END TRY BEGIN CATCH END CATCH", write},
		{"SEND ON CONVERSATION @h ('x')", write},
		{"CREATE TRIGGER tr ON ALL SERVER FOR CREATE_DATABASE AS PRINT 1", admin},
		{"INSERT #t EXEC sp_configure", admin},
	} {
		if got := Classify(tt.script).Class; got != tt.want {
			t.Errorf("Classify(%q) = %s, want %s\n%+v", tt.script, got, tt.want, Classify(tt.script).Statements)
		}
	}
}

// Read allow-list procedures stay read with their arguments, but an Agent
// procedure that only reads (sp_help_job) is read too: msdb's admin rule
// applies after the allow-list.
func TestClassifyMsdbReadProcedure(t *testing.T) {
	if got := Classify("EXEC msdb.dbo.sp_help_jobhistory @job_name = 'j'").Class; got != read {
		t.Errorf("sp_help_jobhistory = %s, want read", got)
	}
}

// A read allow-list name is read only where the server resolves it to the
// system procedure: a system one with no schema, dbo or sys; an Agent one as
// msdb.dbo. Anywhere else it is the user's own procedure, and sp_WhoIsActive,
// sp_block and sp_monitor are not reads at all. sp_spaceused asking for
// @updateusage runs DBCC UPDATEUSAGE.
func TestClassifyReadProcedureQualification(t *testing.T) {
	for _, tt := range []struct {
		script string
		want   spec.Class
	}{
		{"EXEC evil.sp_who", write},
		{"EXEC db.evil.sp_who", write},
		{"EXEC [evil].[sp_help]", write},
		{"EXEC srv.db.evil.sp_who", write},
		{"INSERT #t EXEC evil.sp_who2", write},
		{"EXEC sp_who", read},
		{"EXEC dbo.sp_who", read},
		{"EXEC db.dbo.sp_who", read},
		{"EXEC ..sp_who", read},
		{"EXEC sys.sp_who", read},
		{"EXEC srv.master.sys.sp_who", read},
		{"EXEC [sys].[sp_spaceused]", read},

		// xp_ names are not sp_ ones: dbo.xp_msver outside master is the
		// database's own procedure (probed live); master.dbo still runs the
		// system one even beside a user master.dbo.xp_fixeddrives.
		{"EXEC dbo.xp_msver", write},
		{"EXEC userdb.dbo.xp_readerrorlog", write},
		{"EXEC srv.userdb.dbo.xp_fixeddrives", write},
		{"INSERT #t EXEC dbo.xp_msver", write},
		{"EXEC evil.xp_msver", write},
		{"EXEC xp_msver", read},
		{"EXEC sys.xp_msver", read},
		{"EXEC db..xp_msver", read},
		{"EXEC master..xp_fixeddrives", read},
		{"EXEC master.dbo.xp_fixeddrives", read},
		{"EXEC [MASTER].[DBO].[XP_READERRORLOG]", read},

		{"EXEC sp_help_job", write},
		{"EXEC dbo.sp_help_job", write},
		{"EXEC userdb.dbo.sp_help_job", write},
		{"EXEC sys.sp_help_job", write},
		{"EXEC msdb.dbo.sp_help_job", read},
		{"EXEC msdb..sp_help_job", read},
		{"EXEC [MSDB].[DBO].[SP_HELP_JOB]", read},
		{"EXEC msdb.evil.sp_help_job", admin}, // every other msdb sp_ is admin

		{"sp_whoisactive", write},
		{"EXEC sp_WhoIsActive @destination_table = 'log'", write},
		{"EXEC sp_block", write},
		{"EXEC sp_monitor", write},

		{"EXEC sp_spaceused", read},
		{"EXEC sp_spaceused N't'", read},
		{"EXEC sp_spaceused N't', 'false'", read},
		{"EXEC sp_spaceused N't', N'FALSE'", read},
		{"EXEC sp_spaceused @updateusage = 'false'", read},
		{"EXEC sp_spaceused @objname = N't', @mode = 'ALL'", read},
		{"EXEC sp_spaceused @updateusage = 'true'", admin},
		{"EXEC sp_spaceused N't', 'true'", admin},
		{"EXEC sp_spaceused @updateusage = @x", admin},
		{"EXEC sp_spaceused @UpdateUsage = N'TRUE'", admin},
		{"INSERT #t EXEC sp_spaceused N't', 'true'", admin},
	} {
		if got := Classify(tt.script).Class; got != tt.want {
			t.Errorf("Classify(%q) = %s, want %s\n%+v", tt.script, got, tt.want, Classify(tt.script).Statements)
		}
	}
	for _, s := range []string{"EXEC evil.sp_who", "sp_whoisactive", "EXEC sp_help_job"} {
		if got := Classify(s).Flags; !got.Has(FlagExec) {
			t.Errorf("Classify(%q).Flags = %v, want exec", s, got.Names())
		}
	}
}

// P2-9: StatementCutter cuts only at its leader verbs, so a statement led by
// another word is merged into the one before it. The keyword floor keeps the
// merged statement from hiding behind a read leader.
func TestClassifyMergedStatementsAreFloored(t *testing.T) {
	for _, tt := range []struct {
		script, word string
		want         spec.Class
	}{
		{"SELECT 1\nDISABLE TRIGGER tr ON t", "DISABLE", ddl},
		{"SELECT 1\nENABLE TRIGGER tr ON t", "ENABLE", ddl},
		{"PRINT 'x'\nWRITETEXT t.c @p 'x'", "WRITETEXT", write},
		{"SELECT 1\nUPDATETEXT t.c @p 0 0 'x'", "UPDATETEXT", write},
		{"SET NOCOUNT ON\nDISABLE TRIGGER tr ON ALL SERVER", "DISABLE", ddl},
		{"PRINT 1 THROW 50000, 'x', 1", "", read},
		{"IF UPDATE(a) PRINT 1", "", read},
		// INTO in a subquery is no SELECT … INTO, but still floored.
		{"SELECT 1 FROM (SELECT 2 INTO #x FROM y) d", "INTO", write},
		// A CREATE/ALTER/DROP the splitter did not cut at (after AS) still makes
		// an administration object admin.
		{"SELECT 1 AS\nDROP DATABASE d", "", admin},
		// UPDATE STATISTICS is ddl wherever it stands, not UPDATE's write.
		{"SELECT 1 AS\nUPDATE STATISTICS t", "UPDATE STATISTICS", ddl},
		{"SELECT kill FROM t", "", admin}, // KILL is a cut: "KILL FROM t" is its own statement
	} {
		sc := Classify(tt.script)
		if sc.Class != tt.want {
			t.Errorf("Classify(%q) = %s, want %s: %+v", tt.script, sc.Class, tt.want, sc.Statements)
			continue
		}
		if tt.word != "" && !strings.Contains(sc.Statements[0].Reason, "keyword "+tt.word+" (line 2)") &&
			!strings.Contains(sc.Statements[0].Reason, "keyword "+tt.word+" (line 1)") {
			t.Errorf("Classify(%q) reason = %q, want it to name %s and its line", tt.script, sc.Statements[0].Reason, tt.word)
		}
	}
	// The flag survives the merge too.
	if sc := Classify("SET NOCOUNT ON\nDISABLE TRIGGER tr ON t"); !sc.Flags.Has(FlagNoCount) {
		t.Errorf("NOCOUNT flag lost to a merged statement: %v", sc.Flags.Names())
	}
}

// SQL Server ends a "--" comment at a lone CR as well as at LF (verified live:
// "SELECT 1 AS a -- c<CR>SELECT 2 AS b" returns two result sets), so the
// statement after the CR is code. Read as comment text, it passed --allow read.
func TestClassifyLineCommentEndsAtCR(t *testing.T) {
	for _, tt := range []struct {
		script  string
		want    spec.Class
		leaders []string
	}{
		{"SELECT 1 -- c\rDROP TABLE t", ddl, []string{"SELECT", "DROP TABLE"}},
		{"DECLARE @t TABLE(a int) -- c\rINSERT INTO @t VALUES(1)", write, []string{"DECLARE", "INSERT"}},
	} {
		sc := Classify(tt.script)
		var leaders []string
		for _, st := range sc.Statements {
			leaders = append(leaders, st.Leader)
		}
		if sc.Class != tt.want || !slices.Equal(leaders, tt.leaders) {
			t.Errorf("Classify(%q) = %s %q, want %s %q", tt.script, sc.Class, leaders, tt.want, tt.leaders)
		}
	}
}

// Statement boundaries, lines, batches and repeat counts.
func TestClassifyStatementsAndLines(t *testing.T) {
	script := "SELECT 1\n\nUPDATE Sales.Customer SET a = 1\nGO\n-- note\nDELETE FROM [dbo].[t]; INSERT INTO t VALUES (1)\nGO 3\nEXEC sp_who2"
	sc := Classify(script)
	type got struct {
		Batch, Repeat, Line int
		Class               spec.Class
		Leader, Object      string
	}
	var have []got
	for _, s := range sc.Statements {
		have = append(have, got{s.Batch, s.Repeat, s.Line, s.Class, s.Leader, s.Object})
	}
	want := []got{
		{1, 1, 1, read, "SELECT", ""},
		{1, 1, 3, write, "UPDATE", "Sales.Customer"},
		{2, 3, 6, write, "DELETE", "[dbo].[t]"},
		{2, 3, 6, write, "INSERT", "t"},
		{3, 1, 8, read, "EXEC", "sp_who2"},
	}
	if !slices.Equal(have, want) {
		t.Errorf("statements\n got %+v\nwant %+v", have, want)
	}
	if sc.Class != write {
		t.Errorf("script class = %s, want write", sc.Class)
	}
	for script, want := range map[string]string{
		"DROP TABLE IF EXISTS dbo.t":                         "dbo.t",
		"CREATE OR ALTER VIEW [s].[v] AS SELECT 1 AS a":      "[s].[v]",
		"UPDATE TOP (5) PERCENT x.y SET a = 1":               "x.y",
		"DELETE TOP 3 FROM t":                                "t",
		"INSERT INTO #t VALUES (1)":                          "#t",
		"ALTER DATABASE SCOPED CONFIGURATION SET MAXDOP = 4": "",
		"ALTER DATABASE [AdventureWorks] SET RECOVERY FULL":  "[AdventureWorks]",
	} {
		if got := Classify(script).Statements[0].Object; got != want {
			t.Errorf("Classify(%q) object = %q, want %q", script, got, want)
		}
	}
}

// A line inside a multi-line literal or comment still counts.
func TestClassifyLineCountsThroughLiterals(t *testing.T) {
	sc := Classify("PRINT 'a\nb\nc'\n/* x\ny */ DELETE t")
	if n := len(sc.Statements); n != 2 || sc.Statements[1].Line != 5 {
		t.Errorf("statements = %+v, want DELETE on line 5", sc.Statements)
	}
}

// P2-10: a module batch is one ddl statement; its body's COMMIT, NOCOUNT or
// dynamic SQL raise no flag. A statement before it in the batch is its own.
func TestClassifyModuleBatch(t *testing.T) {
	sc := Classify("PRINT 1\nCREATE PROCEDURE dbo.p AS\nBEGIN\n  SET NOCOUNT ON\n  BEGIN TRAN\n  DELETE t\n  EXEC (@sql)\n  COMMIT\nEND\nGO\nEXEC dbo.p")
	if n := len(sc.Statements); n != 3 {
		t.Fatalf("statements = %d, want 3: %+v", n, sc.Statements)
	}
	m := sc.Statements[1]
	if m.Class != ddl || m.Leader != "CREATE PROCEDURE" || m.Object != "dbo.p" || m.Flags != 0 || m.Line != 2 {
		t.Errorf("module = %+v, want one ddl CREATE PROCEDURE dbo.p on line 2, no flags", m)
	}
	if call := sc.Statements[2]; call.Class != write || !call.Flags.Has(FlagExec) {
		t.Errorf("call = %+v, want write with FlagExec (the body is unseen)", call)
	}
}

func TestClassifyFlags(t *testing.T) {
	for _, tt := range []struct {
		script string
		want   Flag
	}{
		{"BEGIN TRAN", FlagTranControl},
		{"COMMIT", FlagTranControl},
		{"ROLLBACK", FlagTranControl},
		{"SAVE TRANSACTION s", FlagTranControl},
		{"SET IMPLICIT_TRANSACTIONS ON", FlagTranControl},
		{"ALTER DATABASE d SET ONLINE", FlagNotInTransaction},
		{"CREATE DATABASE d", FlagNotInTransaction},
		{"DROP DATABASE IF EXISTS d", FlagNotInTransaction},
		{"BACKUP DATABASE d TO DISK = 'x'", FlagNotInTransaction},
		{"RESTORE DATABASE d FROM DISK = 'x'", FlagNotInTransaction},
		{"RECONFIGURE", FlagNotInTransaction},
		{"CREATE FULLTEXT CATALOG c", FlagNotInTransaction},
		{"SET NOCOUNT ON", FlagNoCount},
		{"SET XACT_ABORT, NOCOUNT ON", FlagNoCount},
		{"EXEC (@sql)", FlagDynamic},
		{"EXEC sp_executesql N'x'", FlagDynamic},
		{"INSERT t EXEC sp_executesql N'x'", FlagDynamic},
		{"INSERT #t EXEC dbo.p", FlagExec},
		{"SELECT * FROM OPENQUERY(l, 'x')", FlagDynamic},
		{"EXEC dbo.p", FlagExec},
		{"SET PARSEONLY ON", FlagModeOption},
		{"SET NOEXEC ON", FlagModeOption},
		{"SET SHOWPLAN_XML ON", FlagModeOption},
		{"SET SHOWPLAN_ALL ON", FlagModeOption},
		{"UPDATE t SET a = 1 OUTPUT inserted.a", FlagOutput},
		{"DELETE t OUTPUT deleted.*", FlagOutput},
		{"SELECT NEXT VALUE FOR dbo.seq", FlagSequence},
		{"INSERT t (id) VALUES (NEXT VALUE FOR s)", FlagSequence},
	} {
		if got := Classify(tt.script).Flags; !got.Has(tt.want) {
			t.Errorf("Classify(%q).Flags = %v, want %v among them", tt.script, got.Names(), tt.want.Names())
		}
	}
	for _, tt := range []struct {
		script string
		want   Flag
	}{
		{"SET NOCOUNT OFF", 0},
		{"SET NOEXEC OFF", 0},
		{"SELECT 1", 0},
		{"SELECT next, value FROM t FOR XML PATH", 0},
		{"EXEC sp_who2", 0},
		{"BEGIN TRY SELECT 1 END TRY BEGIN CATCH END CATCH", 0},
		{"ALTER TABLE t ADD c int", 0},
		{"SELECT 'BEGIN TRAN'", 0},
		{"EXEC p @x OUTPUT", FlagExec}, // an output parameter is no OUTPUT clause
	} {
		if got := Classify(tt.script).Flags; got != tt.want {
			t.Errorf("Classify(%q).Flags = %v, want %v", tt.script, got.Names(), tt.want.Names())
		}
	}
}

// The reason names why a statement is above its leader's class.
func TestClassifyReasons(t *testing.T) {
	for _, tt := range []struct{ script, want string }{
		{"SELECT * INTO #t FROM t", "SELECT … INTO creates a table"},
		{"EXEC dbo.p", "procedure dbo.p is not on the read allow-list"},
		{"EXEC xp_cmdshell 'x'", "procedure xp_cmdshell is administration"},
		{"DBCC CHECKDB", "DBCC CHECKDB is not on the read allow-list"},
		{"DROP LOGIN l", "DROP LOGIN is administration"},
		{"EXEC (@x)", "dynamic SQL is not classified"},
		{"SELECT 1\nDISABLE TRIGGER t ON x", "keyword DISABLE (line 2)"},
		{"INSERT #t EXEC sp_configure", "procedure sp_configure is administration (line 1)"},
		{"EXEC evil.sp_who", "procedure evil.sp_who is not on the read allow-list"},
		{"EXEC dbo.xp_msver", "procedure dbo.xp_msver is not on the read allow-list: xp_msver is read only unqualified, as sys.xp_msver or as master.dbo.xp_msver"},
		{"EXEC sp_help_job", "procedure sp_help_job is read only as msdb.dbo.sp_help_job: elsewhere the name is the current database's own procedure"},
		{"EXEC sp_spaceused @updateusage = 'true'", "sp_spaceused @updateusage runs DBCC UPDATEUSAGE"},
	} {
		if got := Classify(tt.script).Statements[0].Reason; got != tt.want {
			t.Errorf("Classify(%q) reason = %q, want %q", tt.script, got, tt.want)
		}
	}
}

func TestRank(t *testing.T) {
	if !(Rank(read) < Rank(write) && Rank(write) < Rank(ddl) && Rank(ddl) < Rank(admin)) || Rank("nope") != -1 {
		t.Error("Rank does not order read < write < ddl < admin")
	}
}

// TestUnquote pins a quoted identifier token's name, unterminated ones (a
// script that ends inside one) included: those keep what follows the opener.
func TestUnquote(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"[t]", "t"},
		{"[a]]b]", "a]b"},
		{"[a]]]", "a]"},
		{`"a""b"`, `a"b`},
		{`"a]b"`, "a]b"},
		{"[a b", "a b"},
		{"[a]]", "a]"},
		{"[", ""},
		{`"a""`, `a"`},
	} {
		if got := unquote(c.raw); got != c.want {
			t.Errorf("unquote(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// leaderSamples is, per verb leaderRule's switch names, a statement it opens
// whose class is above read. TestClassifyNestedVerbsKeepTheirClass checks that
// every case has one, so a verb added to the switch is covered.
var leaderSamples = map[string]string{
	"SELECT": "SELECT a INTO t FROM s", "WITH": "WITH c AS (SELECT 1 AS n) DELETE FROM c",
	"INSERT": "INSERT t VALUES (1)", "UPDATE": "UPDATE t SET a = 1", "DELETE": "DELETE t",
	"MERGE": "MERGE t USING s ON 1 = 1 WHEN MATCHED THEN DELETE;",
	"BULK":  "BULK INSERT t FROM 'x'", "CREATE": "CREATE TABLE t (a int)",
	"ALTER": "ALTER TABLE t ADD c int", "DROP": "DROP TABLE t", "TRUNCATE": "TRUNCATE TABLE t",
	"DISABLE": "DISABLE TRIGGER tr ON t", "ENABLE": "ENABLE TRIGGER tr ON t",
	"BACKUP": "BACKUP DATABASE d TO DISK = 'x'", "RESTORE": "RESTORE DATABASE d FROM DISK = 'x'",
	"RECONFIGURE": "RECONFIGURE", "DBCC": "DBCC CHECKDB", "EXEC": "EXEC dbo.p",
	"EXECUTE": "EXECUTE dbo.p", "BEGIN": "BEGIN TRAN", "COMMIT": "COMMIT",
	"ROLLBACK": "ROLLBACK", "SAVE": "SAVE TRAN s", "END": "END CONVERSATION @h",
	"SET": "SET IMPLICIT_TRANSACTIONS ON",
}

// R1 of the 2026-11-13 review: a verb only the leader rule knew (SETUSER, SEND
// ON CONVERSATION, ADD SIGNATURE, …) classified as read behind IF, ELSE, WHILE
// or BEGIN, which the splitter does not cut at, so --allow read sent it. Every
// leader-rule verb and every verbs entry keeps its class nested.
func TestClassifyNestedVerbsKeepTheirClass(t *testing.T) {
	samples := map[string]string{}
	for name := range verbs {
		samples[name] = name + " x"
	}
	for _, name := range leaderSwitchCases(t) {
		s, ok := leaderSamples[name]
		if !ok {
			t.Errorf("leaderRule case %q has no leaderSamples entry", name)
			continue
		}
		samples[name] = s
	}
	for name, s := range samples {
		want := Classify(s).Class
		if want == read {
			t.Errorf("sample for %s, %q, is read: give one above read", name, s)
			continue
		}
		for _, nested := range []string{
			"IF 1 = 1 " + s, "IF 1 = 1 PRINT 1 ELSE " + s, "WHILE 1 = 1 " + s,
			"BEGIN " + s + " END", "BEGIN TRY " + s + " END TRY BEGIN CATCH END CATCH",
			"IF 1 = 1 BEGIN " + s + " END",
		} {
			if got := Classify(nested).Class; Rank(got) < Rank(want) {
				t.Errorf("Classify(%q) = %s, below %s alone (%s)", nested, got, s, want)
			}
		}
	}
}

// leaderSwitchCases reads the verbs leaderRule's `switch w := t.word` names
// from classify.go.
func leaderSwitchCases(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(gotoken.NewFileSet(), "classify.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "leaderRule" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || sw.Init == nil {
				return true
			}
			if as, ok := sw.Init.(*ast.AssignStmt); !ok || as.Lhs[0].(*ast.Ident).Name != "w" {
				return true
			}
			for _, c := range sw.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					if lit, ok := e.(*ast.BasicLit); ok {
						s, _ := strconv.Unquote(lit.Value)
						out = append(out, s)
					}
				}
			}
			return false
		})
	}
	if len(out) < 10 {
		t.Fatalf("found %d leaderRule cases; has the switch changed shape?", len(out))
	}
	return out
}
