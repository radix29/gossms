package safety

import (
	"slices"
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
