Backup Database
┌──────────────────────────────────────────────────────────────────────────────┐
│ SQL Server TUI Manager                                      F1 Help  Esc Exit│
├──────────────────────────────────────────────────────────────────────────────┤
│ Backup Database                                                        Step 1│
├──────────────────────────────────────────────────────────────────────────────┤
│ Server:        localhost\SQLEXPRESS                                       │
│ Database:      AdventureWorks2022 ▼                                       │
│                                                                            │
│ Backup Type:   (*) Full    ( ) Differential    ( ) Transaction Log         │
│                                                                            │
│ Destination:                                                           │
│   /var/backups/AdventureWorks2022_full.bak                              │
│                                                                            │
│ Compression:   [✓] Enable                                                │
│ Verify Backup: [✓] After completion                                      │
│ Checksum:      [✓] Use backup checksum                                   │
│ Copy Only:     [ ] Copy-only backup                                      │
│                                                                            │
├──────────────────────────────────────────────────────────────────────────────┤
│ Status: Ready                                                             │
├──────────────────────────────────────────────────────────────────────────────┤
│ [ Start Backup ]   [ Validate ]   [ Cancel ]                              │
└──────────────────────────────────────────────────────────────────────────────┘

Backup Progress
┌──────────────────────────────────────────────────────────────────────────────┐
│ Backup Database - Progress                                                  │
├──────────────────────────────────────────────────────────────────────────────┤
│ Database : AdventureWorks2022                                              │
│ Type     : Full                                                            │
│ Target   : /var/backups/AdventureWorks2022_full.bak                        │
│                                                                            │
│ Progress:                                                                  │
│                                                                            │
│ ████████████████████████████████░░░░░░░░░░░░░░░░  68%                      │
│                                                                            │
│ Elapsed : 00:02:18                                                         │
│ Remaining: 00:01:04                                                        │
│ Speed    : 325 MB/s                                                        │
│                                                                            │
│ Current Operation: Writing backup pages...                                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ [ Hide ]                                                [ Cancel Backup ]   │
└──────────────────────────────────────────────────────────────────────────────┘

Restore Database
┌──────────────────────────────────────────────────────────────────────────────┐
│ Restore Database                                                     Step 1 │
├──────────────────────────────────────────────────────────────────────────────┤
│ Restore From:                                                              │
│   (*) Backup File                                                          │
│   ( ) Existing Backup History                                              │
│                                                                            │
│ Backup File:                                                               │
│   /var/backups/AdventureWorks2022_full.bak                                 │
│                                                                            │
│ Target Database:                                                           │
│   AdventureWorks2022_Restore                                               │
│                                                                            │
│ Recovery Options:                                                          │
│   (*) WITH RECOVERY                                                        │
│   ( ) WITH NORECOVERY                                                      │
│                                                                            │
│ Replace Existing Database: [ ]                                             │
│ Verify Backup Before Restore: [✓]                                          │
│ Close Existing Connections: [✓]                                            │
│                                                                            │
├──────────────────────────────────────────────────────────────────────────────┤
│ [ Analyze Backup ]   [ Start Restore ]   [ Cancel ]                        │
└──────────────────────────────────────────────────────────────────────────────┘

Backup File Inspection
┌──────────────────────────────────────────────────────────────────────────────┐
│ Backup Information                                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ File: AdventureWorks2022_full.bak                                          │
│                                                                            │
│ Database      : AdventureWorks2022                                         │
│ Backup Type   : Full                                                       │
│ Backup Date   : 2026-07-16 18:42:10                                        │
│ SQL Version   : SQL Server 2022                                            │
│ Size          : 5.8 GB                                                     │
│ Compressed    : Yes                                                        │
│ Checksum      : Valid                                                      │
│                                                                            │
├──────────────────────────────────────────────────────────────────────────────┤
│ Files Included                                                             │
│────────────────────────────────────────────────────────────────────────────│
│ AdventureWorks_Data     PRIMARY     5.1 GB                                 │
│ AdventureWorks_Log      LOG         0.7 GB                                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ [ Restore ]   [ Back ]                                                     │
└──────────────────────────────────────────────────────────────────────────────┘

Restore Progress
┌──────────────────────────────────────────────────────────────────────────────┐
│ Restore Database - Progress                                                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ Database : AdventureWorks2022_Restore                                      │
│ Source   : AdventureWorks2022_full.bak                                     │
│                                                                            │
│ ████████████████████████████████████████████░░░░░░░  84%                  │
│                                                                            │
│ Restoring data pages...                                                    │
│                                                                            │
│ Elapsed : 00:04:11                                                         │
│ Remaining: 00:00:47                                                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ [ View Log ]                                            [ Cancel ]          │
└──────────────────────────────────────────────────────────────────────────────┘
