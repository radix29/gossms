/*
  generate_load.sql -- constant, moderate load for watching Activity Monitor.

  Target : win10cli, database Healthclinic (any SQL Server 2016+).
  Runs   : forever. Stop it with Cancel (Ctrl+C / Stop query) -- it is safe to
           cancel at any time; the transaction rolls back.
  Touches: only dbo.LoadGen_Scratch (created here, capped at @MaxRows rows).
           No Healthclinic table is read or modified.
  Cleanup: run the DROP at the bottom when finished.

  Per iteration (roughly @CycleMs long) it produces:
    - CPU          : a busy loop for @CpuMs           -> Processor Time %, Expensive Queries
    - Log + data IO: insert / update / delete batches  -> Data File I/O, Log Flush waits
    - Read IO      : scans + sort spilling to tempdb   -> Physical/Logical reads, tempdb I/O
    - Idle waits   : WAITFOR DELAY                     -> Waiting Tasks (WAITFOR)
  Duty cycle = @CpuMs / @CycleMs. Raise @CpuMs for more load; run the script in
  several query windows at once for more sessions / parallel load.
*/
USE Healthclinic;
SET NOCOUNT ON;

IF OBJECT_ID(N'dbo.LoadGen_Scratch') IS NULL
BEGIN
    CREATE TABLE dbo.LoadGen_Scratch
    (
        id      bigint IDENTITY(1,1) NOT NULL CONSTRAINT PK_LoadGen_Scratch PRIMARY KEY,
        grp     int            NOT NULL,
        val     int            NOT NULL,
        payload char(400)      NOT NULL CONSTRAINT DF_LoadGen_payload DEFAULT (REPLICATE('x', 400)),
        ts      datetime2(3)   NOT NULL CONSTRAINT DF_LoadGen_ts DEFAULT (SYSDATETIME())
    );
    CREATE INDEX IX_LoadGen_Scratch_grp ON dbo.LoadGen_Scratch (grp) INCLUDE (val);
END;

-- Tunables ------------------------------------------------------------------
DECLARE @CycleMs   int = 1000;     -- length of one iteration
DECLARE @CpuMs     int = 250;      -- CPU burn per iteration (~25% of one core)
DECLARE @BatchRows int = 500;      -- rows inserted per iteration
DECLARE @MaxRows   int = 200000;   -- scratch table size cap
-----------------------------------------------------------------------------

DECLARE @start datetime2(3), @until datetime2(3), @x float, @rows int, @waitMs int, @delay char(12);

WHILE 1 = 1
BEGIN
    SET @start = SYSDATETIME();

    -- 1. CPU burst
    SET @x = 1.0;
    SET @until = DATEADD(MILLISECOND, @CpuMs, @start);
    WHILE SYSDATETIME() < @until
        SET @x = SQRT(ABS(SIN(@x) * COS(@x + RAND()))) + 1.0;

    -- 2. Writes: insert a batch, update a random slice, trim old rows
    INSERT dbo.LoadGen_Scratch (grp, val)
    SELECT TOP (@BatchRows) ABS(CHECKSUM(NEWID())) % 100, ABS(CHECKSUM(NEWID())) % 100000
    FROM sys.all_columns AS a CROSS JOIN sys.all_columns AS b;

    UPDATE TOP (200) dbo.LoadGen_Scratch
       SET val = val + 1, ts = SYSDATETIME()
     WHERE grp = ABS(CHECKSUM(NEWID())) % 100;

    SELECT @rows = COUNT_BIG(*) FROM dbo.LoadGen_Scratch;
    IF @rows > @MaxRows
        DELETE TOP (@BatchRows * 2) FROM dbo.LoadGen_Scratch;

    -- 3. Reads: full scan with aggregate + sort on an unindexed column (tempdb grant)
    SELECT TOP (50) payload, val, ts
      FROM dbo.LoadGen_Scratch
     ORDER BY payload DESC, ts DESC;

    SELECT grp, COUNT_BIG(*) AS n, AVG(CAST(val AS bigint)) AS avg_val
      FROM dbo.LoadGen_Scratch
     GROUP BY grp
     ORDER BY n DESC;

    -- 4. Idle for whatever is left of the cycle (shows as WAITFOR)
    SET @waitMs = @CycleMs - DATEDIFF(MILLISECOND, @start, SYSDATETIME());
    IF @waitMs > 0
    BEGIN
        SET @delay = '00:00:' + RIGHT('0' + CAST(@waitMs / 1000 AS varchar(2)), 2)
                   + '.' + RIGHT('00' + CAST(@waitMs % 1000 AS varchar(3)), 3);
        WAITFOR DELAY @delay;
    END;
END;

/* Cleanup (run separately after stopping the load):
USE Healthclinic;
DROP TABLE IF EXISTS dbo.LoadGen_Scratch;
*/
