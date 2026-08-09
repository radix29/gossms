/*
    HealthClinic — additional sample data
    -------------------------------------
    Appends realistic, referentially-consistent rows to the existing
    HealthClinic schema. It does NOT delete or modify anything already in the
    database, and it does not roll back.

    Re-runnable: every run appends another batch. Uniqueness on
    Doctors.Email / Doctors.LicenseNumber is guaranteed by seeding the
    generated values from the current MAX(DoctorID), so a second run never
    collides with the first. Reference data (Specializations, Medications) is
    inserted with NOT EXISTS guards, so it is added once and only once.

    Volume is controlled by the three variables at the top of the batch.
    Everything runs in a single batch (no GO) so the variables stay in scope
    and so the whole thing can be pasted into one goSSMS query panel.

    Statuses used for Appointments: Scheduled, Completed, Cancelled, No-Show.
    The first three already exist in the table; No-Show is new (there is no
    CHECK constraint on the column).

    Row shape produced per run at the default sizes:
        Doctors           +40
        Patients          +500
        Appointments      +2000   (past 18 months .. next 60 days)
        MedicalRecords    one per Completed appointment (~71%)
        Prescriptions     1-3 per medical record (~2800)
        Invoices          one per Completed or No-Show appointment (~1550)
*/

USE HealthClinic;
SET NOCOUNT ON;

DECLARE @NewDoctors      int = 40;
DECLARE @NewPatients     int = 500;
DECLARE @NewAppointments int = 2000;

-- #temp tables live for the whole session, not the batch: a run that failed
-- part way would otherwise make the next run fail on "already exists".
DROP TABLE IF EXISTS #DocSeq, #SpecPool, #PatSeq, #PatPool, #DocPool,
                     #ApptSeq, #RecSeq, #MedPool, #PrescSeq, #InvSeq;

-------------------------------------------------------------------------------
-- 1. Reference data: Specializations
-------------------------------------------------------------------------------
DECLARE @Spec TABLE (Name nvarchar(150) PRIMARY KEY);
INSERT INTO @Spec (Name) VALUES
    (N'Cardiology'), (N'Dermatology'), (N'Endocrinology'), (N'Gastroenterology'),
    (N'General Practice'), (N'Geriatrics'), (N'Haematology'), (N'Infectious Diseases'),
    (N'Internal Medicine'), (N'Nephrology'), (N'Neurology'), (N'Obstetrics and Gynaecology'),
    (N'Oncology'), (N'Ophthalmology'), (N'Orthopaedics'), (N'Otolaryngology'),
    (N'Paediatrics'), (N'Physical Medicine and Rehabilitation'), (N'Psychiatry'),
    (N'Pulmonology'), (N'Radiology'), (N'Rheumatology'), (N'Urology'),
    (N'Emergency Medicine'), (N'Anaesthesiology');

INSERT INTO dbo.Specializations (SpecializationName)
SELECT s.Name
FROM   @Spec s
WHERE  NOT EXISTS (SELECT 1 FROM dbo.Specializations x WHERE x.SpecializationName = s.Name);

-------------------------------------------------------------------------------
-- 2. Reference data: Medications
-------------------------------------------------------------------------------
DECLARE @Med TABLE (
    MedicationName nvarchar(200) PRIMARY KEY,
    GenericName    nvarchar(200),
    DosageForm     nvarchar(80),
    Strength       nvarchar(50));

INSERT INTO @Med (MedicationName, GenericName, DosageForm, Strength) VALUES
    (N'Lipitor',    N'Atorvastatin',            N'Tablet',      N'20 mg'),
    (N'Zocor',      N'Simvastatin',             N'Tablet',      N'40 mg'),
    (N'Glucophage', N'Metformin',               N'Tablet',      N'500 mg'),
    (N'Januvia',    N'Sitagliptin',             N'Tablet',      N'100 mg'),
    (N'Lantus',     N'Insulin Glargine',        N'Injection',   N'100 units/mL'),
    (N'Norvasc',    N'Amlodipine',              N'Tablet',      N'5 mg'),
    (N'Zestril',    N'Lisinopril',              N'Tablet',      N'10 mg'),
    (N'Cozaar',     N'Losartan',                N'Tablet',      N'50 mg'),
    (N'Tenormin',   N'Atenolol',                N'Tablet',      N'50 mg'),
    (N'Lasix',      N'Furosemide',              N'Tablet',      N'40 mg'),
    (N'Prinivil',   N'Hydrochlorothiazide',     N'Tablet',      N'25 mg'),
    (N'Coumadin',   N'Warfarin',                N'Tablet',      N'5 mg'),
    (N'Plavix',     N'Clopidogrel',             N'Tablet',      N'75 mg'),
    (N'Eliquis',    N'Apixaban',                N'Tablet',      N'5 mg'),
    (N'Amoxil',     N'Amoxicillin',             N'Capsule',     N'500 mg'),
    (N'Augmentin',  N'Amoxicillin/Clavulanate', N'Tablet',      N'875 mg'),
    (N'Zithromax',  N'Azithromycin',            N'Tablet',      N'250 mg'),
    (N'Cipro',      N'Ciprofloxacin',           N'Tablet',      N'500 mg'),
    (N'Keflex',     N'Cephalexin',              N'Capsule',     N'500 mg'),
    (N'Flagyl',     N'Metronidazole',           N'Tablet',      N'400 mg'),
    (N'Tylenol',    N'Paracetamol',             N'Tablet',      N'500 mg'),
    (N'Advil',      N'Ibuprofen',               N'Tablet',      N'400 mg'),
    (N'Naprosyn',   N'Naproxen',                N'Tablet',      N'500 mg'),
    (N'Celebrex',   N'Celecoxib',               N'Capsule',     N'200 mg'),
    (N'Ultram',     N'Tramadol',                N'Tablet',      N'50 mg'),
    (N'Prilosec',   N'Omeprazole',              N'Capsule',     N'20 mg'),
    (N'Nexium',     N'Esomeprazole',            N'Capsule',     N'40 mg'),
    (N'Zantac',     N'Ranitidine',              N'Tablet',      N'150 mg'),
    (N'Zofran',     N'Ondansetron',             N'Tablet',      N'4 mg'),
    (N'Ventolin',   N'Salbutamol',              N'Inhaler',     N'100 mcg/dose'),
    (N'Symbicort',  N'Budesonide/Formoterol',   N'Inhaler',     N'160/4.5 mcg'),
    (N'Singulair',  N'Montelukast',             N'Tablet',      N'10 mg'),
    (N'Zyrtec',     N'Cetirizine',              N'Tablet',      N'10 mg'),
    (N'Deltasone',  N'Prednisone',              N'Tablet',      N'20 mg'),
    (N'Synthroid',  N'Levothyroxine',           N'Tablet',      N'75 mcg'),
    (N'Zoloft',     N'Sertraline',              N'Tablet',      N'50 mg'),
    (N'Lexapro',    N'Escitalopram',            N'Tablet',      N'10 mg'),
    (N'Xanax',      N'Alprazolam',              N'Tablet',      N'0.5 mg'),
    (N'Neurontin',  N'Gabapentin',              N'Capsule',     N'300 mg'),
    (N'Ambien',     N'Zolpidem',                N'Tablet',      N'10 mg'),
    (N'Fosamax',    N'Alendronate',             N'Tablet',      N'70 mg'),
    (N'Valtrex',    N'Valaciclovir',            N'Tablet',      N'500 mg'),
    (N'Diflucan',   N'Fluconazole',             N'Capsule',     N'150 mg'),
    (N'Voltaren',   N'Diclofenac',              N'Gel',         N'1%'),
    (N'Betnovate',  N'Betamethasone',           N'Cream',       N'0.1%');

INSERT INTO dbo.Medications (MedicationName, GenericName, DosageForm, Strength)
SELECT m.MedicationName, m.GenericName, m.DosageForm, m.Strength
FROM   @Med m
WHERE  NOT EXISTS (SELECT 1 FROM dbo.Medications x WHERE x.MedicationName = m.MedicationName);

-------------------------------------------------------------------------------
-- 3. Name pools, shared by Doctors and Patients
-------------------------------------------------------------------------------
DECLARE @First TABLE (ID int IDENTITY(1,1) PRIMARY KEY, Name nvarchar(50), Gender char(1));
INSERT INTO @First (Name, Gender) VALUES
    (N'Aaron','M'),(N'Adrian','M'),(N'Alan','M'),(N'Andrei','M'),(N'Anthony','M'),
    (N'Ben','M'),(N'Bogdan','M'),(N'Carl','M'),(N'Cristian','M'),(N'Daniel','M'),
    (N'David','M'),(N'Dmitri','M'),(N'Edward','M'),(N'Emil','M'),(N'Frank','M'),
    (N'George','M'),(N'Hassan','M'),(N'Henry','M'),(N'Ian','M'),(N'Ivan','M'),
    (N'Jacob','M'),(N'James','M'),(N'John','M'),(N'Jonas','M'),(N'Kevin','M'),
    (N'Lucas','M'),(N'Marcus','M'),(N'Martin','M'),(N'Michael','M'),(N'Nikolai','M'),
    (N'Oliver','M'),(N'Omar','M'),(N'Patrick','M'),(N'Paul','M'),(N'Peter','M'),
    (N'Radu','M'),(N'Raj','M'),(N'Robert','M'),(N'Samuel','M'),(N'Sebastian','M'),
    (N'Stefan','M'),(N'Thomas','M'),(N'Tobias','M'),(N'Victor','M'),(N'William','M'),
    (N'Alexandra','F'),(N'Alice','F'),(N'Amelia','F'),(N'Ana','F'),(N'Anna','F'),
    (N'Beatrice','F'),(N'Camelia','F'),(N'Carmen','F'),(N'Caroline','F'),(N'Chloe','F'),
    (N'Clara','F'),(N'Daniela','F'),(N'Diana','F'),(N'Elena','F'),(N'Elisabeth','F'),
    (N'Emma','F'),(N'Eva','F'),(N'Fatima','F'),(N'Gabriela','F'),(N'Hannah','F'),
    (N'Helena','F'),(N'Ioana','F'),(N'Irina','F'),(N'Isabel','F'),(N'Julia','F'),
    (N'Karin','F'),(N'Laura','F'),(N'Lena','F'),(N'Lucia','F'),(N'Maria','F'),
    (N'Marta','F'),(N'Monica','F'),(N'Nadia','F'),(N'Natalia','F'),(N'Olivia','F'),
    (N'Paula','F'),(N'Priya','F'),(N'Rebecca','F'),(N'Sarah','F'),(N'Simona','F'),
    (N'Sofia','F'),(N'Tatiana','F'),(N'Teresa','F'),(N'Valentina','F'),(N'Zoe','F');

DECLARE @Last TABLE (ID int IDENTITY(1,1) PRIMARY KEY, Name nvarchar(50));
INSERT INTO @Last (Name) VALUES
    (N'Abbott'),(N'Adams'),(N'Albrecht'),(N'Andersson'),(N'Antonescu'),(N'Bailey'),
    (N'Bakker'),(N'Barnes'),(N'Becker'),(N'Bennett'),(N'Bergmann'),(N'Blake'),
    (N'Bouchard'),(N'Brennan'),(N'Brooks'),(N'Campbell'),(N'Carter'),(N'Chen'),
    (N'Clarke'),(N'Colceriu'),(N'Constantin'),(N'Cooper'),(N'Dalton'),(N'Diaconu'),
    (N'Dimitrov'),(N'Dubois'),(N'Eriksen'),(N'Farrell'),(N'Fischer'),(N'Fleming'),
    (N'Garcia'),(N'Gibson'),(N'Grant'),(N'Gruber'),(N'Hamilton'),(N'Hansen'),
    (N'Hoffmann'),(N'Holloway'),(N'Ionescu'),(N'Jansen'),(N'Jensen'),(N'Kaufmann'),
    (N'Keller'),(N'Kovacs'),(N'Kowalski'),(N'Lambert'),(N'Larsson'),(N'Lawson'),
    (N'Leclerc'),(N'Lindqvist'),(N'Lopez'),(N'Marchetti'),(N'Marinescu'),(N'Mendez'),
    (N'Mihai'),(N'Moreau'),(N'Morgan'),(N'Novak'),(N'Nowak'),(N'Okafor'),
    (N'Olsen'),(N'Osborne'),(N'Pavlov'),(N'Pearson'),(N'Petrov'),(N'Popescu'),
    (N'Quinn'),(N'Ramirez'),(N'Reeves'),(N'Richter'),(N'Rossi'),(N'Sanchez'),
    (N'Schmidt'),(N'Schneider'),(N'Sharma'),(N'Silva'),(N'Simmons'),(N'Stanciu'),
    (N'Sullivan'),(N'Tanaka'),(N'Thompson'),(N'Torres'),(N'Vasquez'),(N'Vlad'),
    (N'Wagner'),(N'Walsh'),(N'Weber'),(N'Whitfield'),(N'Wright'),(N'Zielinski');

DECLARE @FirstCount int = (SELECT COUNT(*) FROM @First);
DECLARE @LastCount  int = (SELECT COUNT(*) FROM @Last);

DECLARE @Street TABLE (ID int IDENTITY(1,1) PRIMARY KEY, Name nvarchar(60));
INSERT INTO @Street (Name) VALUES
    (N'Alder Street'),(N'Beech Avenue'),(N'Birch Lane'),(N'Cedar Court'),
    (N'Chestnut Road'),(N'Elm Street'),(N'Hawthorn Way'),(N'Juniper Close'),
    (N'Linden Boulevard'),(N'Maple Drive'),(N'Oak Terrace'),(N'Poplar Road'),
    (N'Rowan Street'),(N'Spruce Avenue'),(N'Willow Lane');

DECLARE @City TABLE (ID int IDENTITY(1,1) PRIMARY KEY, Name nvarchar(60), Postcode nvarchar(10));
INSERT INTO @City (Name, Postcode) VALUES
    (N'Riverton',N'RV1 4AB'),(N'Ashford',N'AS2 7CD'),(N'Northgate',N'NG3 1EF'),
    (N'Millbrook',N'MB5 9GH'),(N'Fairview',N'FV8 2JK'),(N'Westbury',N'WB6 3LM');

DECLARE @StreetCount int = (SELECT COUNT(*) FROM @Street);
DECLARE @CityCount   int = (SELECT COUNT(*) FROM @City);

/*  Every random choice below is materialized into a #temp table with SELECT
    INTO before it is joined to a lookup pool. Do not "simplify" this into
    CROSS APPLY (SELECT TOP 1 ... ORDER BY NEWID()): that subquery is not
    correlated to the outer row, so the optimizer evaluates it ONCE and every
    generated row gets the same value — the first live run of this script
    produced 500 patients all named "Jacob Marinescu" at the same address, and
    every prescription pointed at the same medication.  */

-------------------------------------------------------------------------------
-- 4. Doctors
-- Email / LicenseNumber are seeded from MAX(DoctorID) so repeat runs of this
-- script never violate UQ_Doctors_Email / UQ_Doctors_LicenseNumber.
-------------------------------------------------------------------------------
DECLARE @DocSeed int = (SELECT ISNULL(MAX(DoctorID), 0) FROM dbo.Doctors);
DECLARE @SpecCount int = (SELECT COUNT(*) FROM dbo.Specializations);

SELECT  s.n,
        ABS(CHECKSUM(NEWID())) % @FirstCount + 1 AS FirstIdx,
        ABS(CHECKSUM(NEWID())) % @LastCount  + 1 AS LastIdx,
        ABS(CHECKSUM(NEWID())) % @SpecCount      AS SpecOffset,
        ABS(CHECKSUM(NEWID())) % 10000           AS PhoneTail,
        ABS(CHECKSUM(NEWID())) % 10              AS AvailRoll
INTO    #DocSeq
FROM   (SELECT TOP (@NewDoctors) ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS n
        FROM sys.all_objects a CROSS JOIN sys.all_objects b) s;

-- Specializations have gaps in their IDs, so pick by ordinal position.
SELECT  ROW_NUMBER() OVER (ORDER BY SpecializationID) - 1 AS Ord, SpecializationID
INTO    #SpecPool
FROM    dbo.Specializations;

INSERT INTO dbo.Doctors (SpecializationID, FirstName, LastName, Email, Phone, LicenseNumber, IsAvailable)
SELECT  sp.SpecializationID,
        f.Name,
        l.Name,
        LOWER(f.Name) + N'.' + LOWER(l.Name)
            + CAST(@DocSeed + d.n AS nvarchar(10)) + N'@healthclinic.example.com',
        N'+44 20 7' + RIGHT(N'000' + CAST(100 + (@DocSeed + d.n) % 900 AS nvarchar(4)), 3)
            + N' ' + RIGHT(N'0000' + CAST(d.PhoneTail AS nvarchar(5)), 4),
        N'MD-' + RIGHT(N'000000' + CAST(100000 + @DocSeed + d.n AS nvarchar(10)), 6),
        CASE WHEN d.AvailRoll < 8 THEN 1 ELSE 0 END
FROM    #DocSeq d
JOIN    @First    f  ON f.ID  = d.FirstIdx
JOIN    @Last     l  ON l.ID  = d.LastIdx
JOIN    #SpecPool sp ON sp.Ord = d.SpecOffset;

-------------------------------------------------------------------------------
-- 5. Patients
-------------------------------------------------------------------------------
DECLARE @PatSeed int = (SELECT ISNULL(MAX(PatientID), 0) FROM dbo.Patients);

SELECT  s.n,
        ABS(CHECKSUM(NEWID())) % @FirstCount  + 1 AS FirstIdx,
        ABS(CHECKSUM(NEWID())) % @LastCount   + 1 AS LastIdx,
        ABS(CHECKSUM(NEWID())) % @StreetCount + 1 AS StreetIdx,
        ABS(CHECKSUM(NEWID())) % @CityCount   + 1 AS CityIdx,
        ABS(CHECKSUM(NEWID())) % 250          + 1 AS HouseNo,
        ABS(CHECKSUM(NEWID())) % 1000             AS PhoneA,
        ABS(CHECKSUM(NEWID())) % 1000000          AS PhoneB,
        -- roughly 1 to 95 years old
        DATEADD(day, -(ABS(CHECKSUM(NEWID())) % 34700 + 400), CAST(SYSDATETIME() AS date)) AS DateOfBirth,
        -- registered somewhere in the last 6 years
        DATEADD(minute, -(ABS(CHECKSUM(NEWID())) % 3153600), SYSDATETIME()) AS RegisteredAt
INTO    #PatSeq
FROM   (SELECT TOP (@NewPatients) ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS n
        FROM sys.all_objects a CROSS JOIN sys.all_objects b) s;

INSERT INTO dbo.Patients (FirstName, LastName, DateOfBirth, Gender, Email, Phone, Address, RegisteredAt)
SELECT  f.Name,
        l.Name,
        p.DateOfBirth,
        f.Gender,
        LOWER(f.Name) + N'.' + LOWER(l.Name)
            + CAST(@PatSeed + p.n AS nvarchar(10)) + N'@example.com',
        N'+44 7' + RIGHT(N'000' + CAST(p.PhoneA AS nvarchar(4)), 3)
            + N' ' + RIGHT(N'000000' + CAST(p.PhoneB AS nvarchar(7)), 6),
        CAST(p.HouseNo AS nvarchar(5)) + N' ' + st.Name
            + N', ' + c.Name + N' ' + c.Postcode,
        p.RegisteredAt
FROM    #PatSeq p
JOIN    @First  f  ON f.ID  = p.FirstIdx
JOIN    @Last   l  ON l.ID  = p.LastIdx
JOIN    @Street st ON st.ID = p.StreetIdx
JOIN    @City   c  ON c.ID  = p.CityIdx;

-------------------------------------------------------------------------------
-- 6. Appointments
-- Spread from 18 months ago to 60 days out. Anything in the future is
-- Scheduled; anything in the past is Completed / Cancelled / No-Show.
-------------------------------------------------------------------------------
DECLARE @Reason TABLE (ID int IDENTITY(1,1) PRIMARY KEY, Text nvarchar(200));
INSERT INTO @Reason (Text) VALUES
    (N'Annual physical examination'),(N'Follow-up on blood pressure'),
    (N'Persistent cough for two weeks'),(N'Lower back pain'),
    (N'Medication review'),(N'Diabetes management check'),
    (N'Skin rash assessment'),(N'Headaches and dizziness'),
    (N'Post-operative review'),(N'Chest pain investigation'),
    (N'Routine vaccination'),(N'Abdominal pain'),
    (N'Joint pain in knees'),(N'Fatigue and weight loss'),
    (N'Anxiety and sleep problems'),(N'Allergy consultation'),
    (N'Blood test results discussion'),(N'Referral for imaging'),
    (N'Ear infection'),(N'Pre-employment health check');

DECLARE @NewApptStart int = (SELECT ISNULL(MAX(AppointmentID), 0) FROM dbo.Appointments);
DECLARE @ReasonCount  int = (SELECT COUNT(*) FROM @Reason);
DECLARE @PatCount     int = (SELECT COUNT(*) FROM dbo.Patients);
DECLARE @DocCount     int = (SELECT COUNT(*) FROM dbo.Doctors);

SELECT  ROW_NUMBER() OVER (ORDER BY PatientID) - 1 AS Ord, PatientID INTO #PatPool FROM dbo.Patients;
SELECT  ROW_NUMBER() OVER (ORDER BY DoctorID)  - 1 AS Ord, DoctorID  INTO #DocPool FROM dbo.Doctors;

SELECT  s.n,
        ABS(CHECKSUM(NEWID())) % @PatCount    AS PatOrd,
        ABS(CHECKSUM(NEWID())) % @DocCount    AS DocOrd,
        ABS(CHECKSUM(NEWID())) % @ReasonCount + 1 AS ReasonIdx,
        ABS(CHECKSUM(NEWID())) % 100          AS StatusRoll,
        CASE ABS(CHECKSUM(NEWID())) % 5 WHEN 0 THEN 15 WHEN 1 THEN 20
             WHEN 2 THEN 30 WHEN 3 THEN 45 ELSE 60 END AS DurationMinutes,
        -- day offset -547 .. +60, clinic hours 08:00-17:45 on quarter hours
        DATEADD(minute, 8 * 60 + (ABS(CHECKSUM(NEWID())) % 40) * 15,
                CAST(DATEADD(day, ABS(CHECKSUM(NEWID())) % 608 - 547,
                             CAST(SYSDATETIME() AS date)) AS datetime2(3))) AS ScheduledAt
INTO    #ApptSeq
FROM   (SELECT TOP (@NewAppointments) ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS n
        FROM sys.all_objects a CROSS JOIN sys.all_objects b) s;

INSERT INTO dbo.Appointments (PatientID, DoctorID, ScheduledAt, DurationMinutes, Status, Reason)
SELECT  pp.PatientID,
        dp.DoctorID,
        s.ScheduledAt,
        s.DurationMinutes,
        CASE
            WHEN s.ScheduledAt > SYSDATETIME() THEN N'Scheduled'
            WHEN s.StatusRoll < 78 THEN N'Completed'
            WHEN s.StatusRoll < 91 THEN N'Cancelled'
            ELSE N'No-Show'
        END,
        r.Text
FROM    #ApptSeq s
JOIN    #PatPool pp ON pp.Ord = s.PatOrd
JOIN    #DocPool dp ON dp.Ord = s.DocOrd
JOIN    @Reason  r  ON r.ID   = s.ReasonIdx;

-------------------------------------------------------------------------------
-- 7. MedicalRecords — one per newly created Completed appointment
-------------------------------------------------------------------------------
DECLARE @Dx TABLE (ID int IDENTITY(1,1) PRIMARY KEY,
                   Diagnosis nvarchar(200), Treatment nvarchar(400));
INSERT INTO @Dx (Diagnosis, Treatment) VALUES
    (N'Essential hypertension',        N'Start ACE inhibitor; low-sodium diet; review in 8 weeks'),
    (N'Type 2 diabetes mellitus',      N'Metformin titration; dietitian referral; HbA1c in 3 months'),
    (N'Acute upper respiratory infection', N'Supportive care; fluids and rest; safety-net advice'),
    (N'Community-acquired pneumonia',  N'Oral antibiotics for 7 days; chest X-ray follow-up'),
    (N'Iron deficiency anaemia',       N'Oral iron replacement; repeat full blood count in 6 weeks'),
    (N'Mechanical low back pain',      N'Physiotherapy referral; NSAIDs as required; stay active'),
    (N'Osteoarthritis of the knee',    N'Weight management; topical NSAID; orthopaedic referral if no change'),
    (N'Gastro-oesophageal reflux',     N'Proton pump inhibitor for 8 weeks; lifestyle advice'),
    (N'Allergic rhinitis',             N'Antihistamine; nasal corticosteroid spray'),
    (N'Asthma, well controlled',       N'Continue inhaled corticosteroid; inhaler technique reviewed'),
    (N'Migraine without aura',         N'Trigger diary; acute treatment reviewed; prophylaxis discussed'),
    (N'Generalised anxiety disorder',  N'CBT referral; SSRI initiated; review in 4 weeks'),
    (N'Hypothyroidism',                N'Levothyroxine dose adjusted; TSH in 8 weeks'),
    (N'Urinary tract infection',       N'Short-course antibiotics; increase fluid intake'),
    (N'Vitamin D deficiency',          N'Loading dose then maintenance supplementation'),
    (N'Hyperlipidaemia',               N'Statin started; repeat lipid profile in 3 months'),
    (N'Atrial fibrillation',           N'Rate control; anticoagulation started; cardiology referral'),
    (N'Contact dermatitis',            N'Topical corticosteroid; avoid identified irritant'),
    (N'Routine health check — normal', N'No abnormality detected; routine screening up to date'),
    (N'Post-operative recovery',       N'Wound healing well; sutures removed; return if signs of infection');

DECLARE @DxCount int = (SELECT COUNT(*) FROM @Dx);

SELECT  a.AppointmentID,
        DATEADD(minute, a.DurationMinutes, a.ScheduledAt) AS CreatedAt,
        ABS(CHECKSUM(NEWID())) % @DxCount + 1 AS DxIdx,
        ABS(CHECKSUM(NEWID())) % 4            AS NoteRoll
INTO    #RecSeq
FROM    dbo.Appointments a
WHERE   a.AppointmentID > @NewApptStart
  AND   a.Status = N'Completed';

INSERT INTO dbo.MedicalRecords (AppointmentID, Diagnosis, Treatment, Notes, CreatedAt)
SELECT  r.AppointmentID,
        d.Diagnosis,
        d.Treatment,
        CASE r.NoteRoll
            WHEN 0 THEN N'Patient reports symptoms improving since last visit.'
            WHEN 1 THEN N'Observations within normal limits. No red flags elicited.'
            WHEN 2 THEN N'Discussed treatment options; patient agreed to the plan above.'
            ELSE        N'Safety-net advice given; patient to return if symptoms worsen.'
        END,
        r.CreatedAt
FROM    #RecSeq r
JOIN    @Dx d ON d.ID = r.DxIdx;

-------------------------------------------------------------------------------
-- 8. Prescriptions — 1 to 3 per new medical record
-------------------------------------------------------------------------------
DECLARE @MedCount int = (SELECT COUNT(*) FROM dbo.Medications);
SELECT  ROW_NUMBER() OVER (ORDER BY MedicationID) - 1 AS Ord, MedicationID
INTO    #MedPool FROM dbo.Medications;

SELECT  r.RecordID,
        v.Line,
        ABS(CHECKSUM(NEWID())) % @MedCount AS MedOrd,
        ABS(CHECKSUM(NEWID())) % 4         AS DoseRoll,
        ABS(CHECKSUM(NEWID())) % 3 + 1     AS FrequencyPerDay,
        ABS(CHECKSUM(NEWID())) % 5         AS DurationRoll,
        ABS(CHECKSUM(NEWID())) % 5         AS InstrRoll
INTO    #PrescSeq
FROM   (SELECT r.RecordID, ABS(CHECKSUM(NEWID())) % 3 + 1 AS LineCount
        FROM   dbo.MedicalRecords r
        JOIN   dbo.Appointments a ON a.AppointmentID = r.AppointmentID
        WHERE  a.AppointmentID > @NewApptStart) r
JOIN   (VALUES (1),(2),(3)) v(Line) ON v.Line <= r.LineCount;

INSERT INTO dbo.Prescriptions (RecordID, MedicationID, Dosage, FrequencyPerDay, DurationDays, Instructions)
SELECT  p.RecordID,
        mp.MedicationID,
        CASE p.DoseRoll
            WHEN 0 THEN N'1 tablet'
            WHEN 1 THEN N'2 tablets'
            WHEN 2 THEN N'5 mL'
            ELSE        N'1 puff'
        END,
        p.FrequencyPerDay,
        CASE p.DurationRoll
            WHEN 0 THEN 5 WHEN 1 THEN 7 WHEN 2 THEN 14 WHEN 3 THEN 28 ELSE 90 END,
        CASE p.InstrRoll
            WHEN 0 THEN N'Take with food.'
            WHEN 1 THEN N'Take in the morning with water.'
            WHEN 2 THEN N'Take at night before bed.'
            WHEN 3 THEN N'Complete the full course even if symptoms improve.'
            ELSE        N'Do not exceed the stated dose.'
        END
FROM    #PrescSeq p
JOIN    #MedPool mp ON mp.Ord = p.MedOrd;

-------------------------------------------------------------------------------
-- 9. Invoices — for new Completed and No-Show appointments
-- No-Shows are billed a flat fee; completed visits scale with duration.
-------------------------------------------------------------------------------
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
WHERE   a.AppointmentID > @NewApptStart
  AND   a.Status IN (N'Completed', N'No-Show');

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
                    END AS IsPaid) p;

DROP TABLE #DocSeq, #SpecPool, #PatSeq, #PatPool, #DocPool, #ApptSeq,
           #RecSeq, #MedPool, #PrescSeq, #InvSeq;

-------------------------------------------------------------------------------
-- 10. Summary
-------------------------------------------------------------------------------
SELECT 'Specializations' AS TableName, COUNT(*) AS TotalRows FROM dbo.Specializations
UNION ALL SELECT 'Medications',    COUNT(*) FROM dbo.Medications
UNION ALL SELECT 'Doctors',        COUNT(*) FROM dbo.Doctors
UNION ALL SELECT 'Patients',       COUNT(*) FROM dbo.Patients
UNION ALL SELECT 'Appointments',   COUNT(*) FROM dbo.Appointments
UNION ALL SELECT 'MedicalRecords', COUNT(*) FROM dbo.MedicalRecords
UNION ALL SELECT 'Prescriptions',  COUNT(*) FROM dbo.Prescriptions
UNION ALL SELECT 'Invoices',       COUNT(*) FROM dbo.Invoices;
