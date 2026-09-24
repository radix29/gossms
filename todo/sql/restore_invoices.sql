-- Rebuild dbo.Invoices in HealthClinic.
--
-- The table was dropped on 2026-08-27. The registered 2026-08-12 backup
-- (C:\temp\HealthClinic_full.bak) is no longer on disk, so this reconstructs
-- it instead from the two sources that survived: the column list and billing
-- logic in todo/healthclinic_sample_data.sql section 9, and dbo.vw_UnpaidInvoices,
-- which pins InvoiceID/InvoiceDate/Amount/IsPaid and the FK to Appointments.
--
-- The schema is exact. The per-row Amount and IsPaid values are regenerated to
-- the original distribution, not the original values -- those are not recoverable.
USE HealthClinic;
GO

IF OBJECT_ID('dbo.Invoices') IS NOT NULL
BEGIN
    RAISERROR('dbo.Invoices already exists; nothing done.', 16, 1);
    RETURN;
END
GO

CREATE TABLE dbo.Invoices (
    InvoiceID     int IDENTITY(1,1) NOT NULL
                      CONSTRAINT PK_Invoices PRIMARY KEY,
    AppointmentID int NOT NULL
                      CONSTRAINT FK_Invoices_Appointments
                      REFERENCES dbo.Appointments(AppointmentID),
    InvoiceDate   datetime2     NOT NULL,
    Amount        decimal(10,2) NOT NULL,
    IsPaid        bit           NOT NULL,
    PaidAt        datetime2     NULL
);
GO

-- Section 9 of healthclinic_sample_data.sql, over every billable appointment:
-- No-Shows are billed a flat fee, completed visits scale with duration.
SELECT  a.AppointmentID,
        a.Status,
        DATEADD(minute, a.DurationMinutes, a.ScheduledAt) AS InvoiceDate,
        CASE WHEN a.Status = N'No-Show' THEN CAST(35.00 AS decimal(10,2))
             ELSE CAST(45.00 + a.DurationMinutes * 2.5
                       + (ABS(CHECKSUM(NEWID())) % 4000) / 100.0 AS decimal(10,2))
        END AS Amount,
        ABS(CHECKSUM(NEWID())) % 100    AS PaidRoll,
        ABS(CHECKSUM(NEWID())) % 45 + 1 AS PayDelayDays
INTO    #InvSeq
FROM    dbo.Appointments a
WHERE   a.Status IN (N'Completed', N'No-Show');

INSERT INTO dbo.Invoices (AppointmentID, InvoiceDate, Amount, IsPaid, PaidAt)
SELECT  i.AppointmentID,
        i.InvoiceDate,
        i.Amount,
        p.IsPaid,
        CASE WHEN p.IsPaid = 1
             THEN DATEADD(day, i.PayDelayDays, i.InvoiceDate) END
FROM    #InvSeq i
CROSS APPLY (SELECT CASE WHEN i.Status = N'No-Show'
                         THEN CASE WHEN i.PaidRoll < 40 THEN 1 ELSE 0 END
                         ELSE CASE WHEN i.PaidRoll < 82 THEN 1 ELSE 0 END
                    END AS IsPaid) p
ORDER BY i.AppointmentID;

DROP TABLE #InvSeq;
GO

SELECT 'Invoices' AS TableName, COUNT(*) AS TotalRows,
       SUM(CAST(IsPaid AS int)) AS Paid
FROM dbo.Invoices;
SELECT TOP 5 * FROM dbo.vw_UnpaidInvoices;
GO
