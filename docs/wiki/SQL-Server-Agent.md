[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# SQL Server Agent

Jobs, steps, schedules, alerts, operators, error logs and the Agent reports —
under **SQL Server Agent** in Object Explorer, beside Server Objects.

The subtree holds **Jobs** (User and System), **Schedules**, **Alerts**,
**Operators**, **Error Logs** and a folder of **SQL-only administration reports**.
The log files themselves open in the
[Log File Viewer](https://github.com/radix29/gossms/wiki/Log-File-Viewer).

*(Screenshots for this section are not captured yet.)*

## Jobs

**New Job…** on the Jobs folder, or **Job Properties** on a job. The context menu
carries **Start Job**, **Stop Job**, **Enable Job** / **Disable Job**, **View
History**, **Delete** and **Properties**, and every action that writes names the job
in its confirmation and offers the statement instead of running it. Start and Stop
are reflected in the tree while they are in flight.

**Job Properties** pages: **General** (name, owner, category, enabled state, target
server), **Steps**, **Schedules**, **Alerts** and **Notifications**, **History**.
Each page keeps its own pending edits, so a step added and a schedule unticked are
one Apply.

**New Job** offers four of them — General, Steps, Schedules, Notifications. Alerts,
Targets and History are left out on purpose: alert-to-job linking lives on Job
Properties' Alerts page, a target has nothing to configure before the job exists,
and History is empty for a job that has never run.

### Steps

The step editor is one panel shared by New Job and Job Properties, because both map
the same ten fields onto `sp_add_jobstep` and a mapping wrong in one copy writes
wrong settings.

- **Type** — **T-SQL steps are the ones goSSMS creates and edits.** Steps of other
  subsystems are listed with their subsystem and database, and open read-only with
  that stated on the page: the server holds them fine, but a CmdExec, PowerShell or
  SSIS step is not something this client authors.
- **On success / On failure** — Quit reporting success, quit reporting failure, go
  to the next step, or go to step *N* (the adjacent number is used only when the
  action says "go to step"). These are `sp_add_jobstep`'s own actions, so a script
  goSSMS writes is a script SSMS would have written.
- **Database** — the database the step runs in; `(default)` lets the server pick,
  and on an existing step "(unchanged)" leaves it alone.
- Plus the **Command** text in an editor of its own, **retry attempts** and the
  retry interval, and an **Output file name** — a file the Agent service account
  writes on the server.
- A duplicate step name does not create a second step: the existing row is selected
  and re-synced instead.

### Schedules

**Schedule Properties** and the New Schedule dialog share one frequency form:
occurs once, daily, weekly, monthly or relative to a frequency interval, with the
daily interval and the sub-daily interval, the hours window, and the start and end
date. A schedule's **Jobs** page lists the jobs it is attached to, read-only — the
attachment itself is made from the job's Schedules page.

A schedule still attached to a job cannot be deleted, and goSSMS says that instead
of letting `sp_delete_schedule` explain it.

### Alerts and operators

**Alerts** are SQL Server event alerts: the condition (severity or message ID, in
or not in a list, optionally scoped to a database), its enabled state, the
**Response** — notify operators, run a job — on the alert's own two pages. A SQL
Server event alert responds to exactly one job (`sysalerts.job_id` is a single
relationship, not a list), and the job's Alerts page shows that relationship as a
grid cell rather than pretending it is a set.

**Operators** are the notification targets: name, e-mail address, category, and the
read-only **Notifications** page listing the alerts and jobs that notify them.
Pager and Net Send addresses are excluded with the rest of the SQL-only surface, and
**New Operator…** creates one.

An **alert's** and an **operator's** context menu carries New, Delete (with
confirmation) and Properties, as jobs and schedules do.

### History

Job History is a read-only pair: the run-level rows (one per execution, with date,
duration, status, the message Agent wrote, and who invoked it) and the selected run's
detail. **Ctrl+C** on the message copies it. **View History** on a job — and the
Agent report of the same name — is the same query, which is why they cannot disagree.

## Reports

Under the Agent's reports folder: **Agent Metadata Summary**, **Job Execution
Summary**, **Failed Job Runs**, **Disabled Jobs**, **Jobs Without Schedules**. Each
is a report rendered in the details pane over the same readings the Agent's own
reports use.

## What is not here

Three deliberate limits, each stated on screen where you meet it:

- **Steps of other subsystems are read-only** — goSSMS creates and edits T-SQL
  steps and lists the rest as they are.
- **Alerts are SQL Server event alerts** — there is no performance-alert or
  job-alert authoring.
- **Operators are e-mail only** — the pager and Net Send addresses of the full
  surface are excluded with the rest of the SQL-only scope.

Everything else on the Agent subtree is browsed, edited, run and scripted from
here.

## See also

- [Log File Viewer](https://github.com/radix29/gossms/wiki/Log-File-Viewer) — the Agent error logs
- [Database Mail](https://github.com/radix29/gossms/wiki/Database-Mail) — the mail an operator notification depends on
- [Scripting](https://github.com/radix29/gossms/wiki/Scripting)

[← Home](https://github.com/radix29/gossms/wiki)
