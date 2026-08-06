-- Activity Monitor load generator.
--
-- Puts enough work through a SQL Server instance to make every panel of the
-- Activity Monitor's History and Sample dashboards move: batch requests,
-- transactions, index searches, key lookups, forwarded records, page reads
-- and writes, log flushes, checkpoints, lock waits, and data-file I/O.
--
-- Run it against the server the Activity Monitor is watching, from a second
-- session, and watch the dashboards:
--
--   sqlcmd -S host,1433 -U sa -P <password> -C -i cmd/amdemo/loadtest.sql
--
-- One session fills every panel except Locking: a lock wait needs somebody
-- to wait on. Run two copies at once for that.
--
-- The database is created if it doesn't exist and is NEVER dropped, so the
-- script can be run repeatedly and the data it leaves behind keeps the
-- buffer pool and the file-I/O counters interesting. Drop it by hand when
-- you're done with it:
--
--   DROP DATABASE gossms_amload
--
-- Nothing here touches a pre-existing database. Everything lives in
-- gossms_amload.

SET NOCOUNT ON

IF DB_ID('gossms_amload') IS NULL
BEGIN
    PRINT 'Creating gossms_amload'
    CREATE DATABASE gossms_amload
END
GO

USE gossms_amload
GO

-- SIMPLE recovery keeps the log from growing without bound across repeated
-- runs. Log flushes and log waits still register — every committed
-- transaction flushes — it is only the retention that changes.
IF (SELECT recovery_model FROM sys.databases WHERE name = 'gossms_amload') <> 3
    ALTER DATABASE gossms_amload SET RECOVERY SIMPLE
GO

-- Load is the write target: a wide row so inserts move real pages, an
-- indexed lookup column, and a varchar column left short so UPDATEs that
-- lengthen it produce forwarded records on the heap table below.
IF OBJECT_ID('dbo.Load') IS NULL
BEGIN
    CREATE TABLE dbo.Load
    (
        id      INT IDENTITY(1,1) NOT NULL PRIMARY KEY,
        batch   INT NOT NULL,
        val     INT NOT NULL,
        note    VARCHAR(400) NOT NULL,
        pad     CHAR(2000) NOT NULL,
        created DATETIME2 NOT NULL CONSTRAINT DF_Load_created DEFAULT SYSDATETIME()
    )
    -- Nonclustered on val only: a lookup on val that also selects note has
    -- to go back to the clustered index for it, which is what shows up as
    -- key-lookup activity in the "Key lookups / Forwarded recs" panel.
    CREATE NONCLUSTERED INDEX IX_Load_val ON dbo.Load (val)
END
GO

-- A heap, so the UPDATEs that grow a row in place produce forwarded records
-- rather than page splits.
IF OBJECT_ID('dbo.LoadHeap') IS NULL
BEGIN
    CREATE TABLE dbo.LoadHeap
    (
        id   INT IDENTITY(1,1) NOT NULL,
        val  INT NOT NULL,
        note VARCHAR(2000) NOT NULL
    )
END
GO

DECLARE @iterations INT = 4000       -- raise for a longer run
DECLARE @batch INT = ABS(CHECKSUM(NEWID())) % 100000
DECLARE @i INT = 0
DECLARE @rows INT
DECLARE @note VARCHAR(400)

PRINT 'Load batch ' + CAST(@batch AS VARCHAR(20)) + ': ' + CAST(@iterations AS VARCHAR(20)) + ' iterations'

WHILE @i < @iterations
BEGIN
    -- Writes: one wide row per iteration. Every implicit transaction commits,
    -- so this is also the log-flush generator.
    INSERT dbo.Load (batch, val, note, pad)
    VALUES (@batch, @i % 500, 'x', REPLICATE('p', 2000))

    INSERT dbo.LoadHeap (val, note) VALUES (@i % 500, 'x')

    -- Index search plus key lookup: the seek is on val, note comes from the
    -- clustered index.
    SELECT @note = MAX(note) FROM dbo.Load WHERE val = @i % 500

    -- Every 10th iteration: grow a heap row in place, which forwards it.
    IF @i % 10 = 0
        UPDATE TOP (20) dbo.LoadHeap SET note = REPLICATE('f', 1800) WHERE val = @i % 500 AND LEN(note) < 100

    -- Every 25th: a scan wide enough to pull pages back off disk once the
    -- table outgrows the buffer pool — this is what moves Page reads/sec and
    -- the DATABASE IO latency panel.
    IF @i % 25 = 0
        SELECT @rows = COUNT(*) FROM dbo.Load WHERE pad LIKE '%p%' AND batch = @batch

    -- Every 50th: hold a lock long enough to register as a lock wait against
    -- any other session touching the same rows.
    IF @i % 50 = 0
    BEGIN
        BEGIN TRANSACTION
            UPDATE TOP (50) dbo.Load SET note = 'locked' WHERE batch = @batch
            WAITFOR DELAY '00:00:00.050'
        COMMIT TRANSACTION
    END

    -- Every 200th: force the checkpoint the CHECKPOINTS / LAZY WRITES panel
    -- plots, rather than waiting for the automatic one.
    IF @i % 200 = 0
        CHECKPOINT

    IF @i % 500 = 0
        RAISERROR('  %d of %d', 0, 1, @i, @iterations) WITH NOWAIT

    SET @i = @i + 1
END

CHECKPOINT
PRINT 'Done. gossms_amload left in place; DROP DATABASE gossms_amload to remove it.'
GO
