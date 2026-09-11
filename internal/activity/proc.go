package activity

import (
	"context"
	"database/sql"
	"fmt"
)

// ProcLocation says which database holds a helper procedure.
type ProcLocation int

const (
	// ProcNone means neither master nor tempdb has it.
	ProcNone ProcLocation = iota
	// ProcMaster means the master copy exists and is preferred: it survives
	// restarts, tempdb doesn't.
	ProcMaster
	// ProcTempDB means only the tempdb copy exists.
	ProcTempDB
)

// Database is the database this location names, empty for ProcNone.
func (l ProcLocation) Database() string {
	switch l {
	case ProcMaster:
		return "master"
	case ProcTempDB:
		return "tempdb"
	}
	return ""
}

// Proc is a helper stored procedure backing an Activity Monitor tab, named
// differently in each database it can live in.
//
// In master it keeps its sp_ name, the name a hand-installed copy has. In
// tempdb it has no sp_ prefix: an sp_ name falls back to master when the
// current database lacks it, so in tempdb CREATE OR ALTER dbo.sp_block alters
// master's copy and fails with "Invalid object name", and DROP PROCEDURE IF
// EXISTS deletes master's copy (both verified live). Without the prefix, the
// name resolves in tempdb like any object.
type Proc struct {
	// MasterName is the unqualified name in master, sp_-prefixed.
	MasterName string
	// TempDBName is the unqualified name in tempdb, never sp_-prefixed.
	TempDBName string

	// script builds the CREATE OR ALTER for an unqualified name as one batch
	// (no USE, no GO) for sp_executesql in the target database.
	script func(name string) string
}

// Name is the procedure's name in this location, empty for ProcNone.
func (p *Proc) Name(l ProcLocation) string {
	switch l {
	case ProcMaster:
		return p.MasterName
	case ProcTempDB:
		return p.TempDBName
	}
	return ""
}

// Qualified is the procedure's database-qualified name, empty for ProcNone.
func (p *Proc) Qualified(l ProcLocation) string {
	if db := l.Database(); db != "" {
		return db + ".dbo." + p.Name(l)
	}
	return ""
}

// Exec is the batch that runs the procedure, empty for ProcNone. Always
// database-qualified, as neither name resolves unqualified from an arbitrary
// database.
func (p *Proc) Exec(l ProcLocation) string {
	if q := p.Qualified(l); q != "" {
		return "exec " + q
	}
	return ""
}

// Script is the CREATE OR ALTER that installs the procedure at l, empty for
// ProcNone.
func (p *Proc) Script(l ProcLocation) string {
	name := p.Name(l)
	if name == "" {
		return ""
	}
	return p.script(name)
}

// Find reports where the procedure exists, preferring master, in one round
// trip.
func (p *Proc) Find(ctx context.Context, db *sql.DB) (ProcLocation, error) {
	q := `select
	case when object_id('master.dbo.` + p.MasterName + `', 'P') is not null then 1 else 0 end,
	case when object_id('tempdb.dbo.` + p.TempDBName + `', 'P') is not null then 1 else 0 end`
	var inMaster, inTempDB bool
	if err := db.QueryRowContext(ctx, q).Scan(&inMaster, &inTempDB); err != nil {
		return ProcNone, fmt.Errorf("look up %s: %w", p.MasterName, err)
	}
	switch {
	case inMaster:
		return ProcMaster, nil
	case inTempDB:
		return ProcTempDB, nil
	}
	return ProcNone, nil
}

// Install creates or replaces the procedure at l (not ProcNone). The script is
// a parameter to the three-part db..sp_executesql, which runs it in the target
// database without USE — the pooled connection's database must be left
// unchanged (verified live) — and avoids quoting the body.
func (p *Proc) Install(ctx context.Context, db *sql.DB, l ProcLocation) error {
	database := l.Database()
	if database == "" {
		return fmt.Errorf("install %s: no target database", p.MasterName)
	}
	stmt := "exec " + database + ".sys.sp_executesql @stmt"
	if _, err := db.ExecContext(ctx, stmt, sql.Named("stmt", p.Script(l))); err != nil {
		return fmt.Errorf("install %s: %w", p.Qualified(l), err)
	}
	return nil
}
