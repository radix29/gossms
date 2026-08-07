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
	// ProcMaster means the master copy exists and is preferred: a procedure
	// in master survives a restart, a tempdb one does not.
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

// Proc is a helper stored procedure goSSMS installs to back one Activity
// Monitor tab, under a different name in each of the two databases it can
// live in.
//
// In master the procedure keeps its sp_ name: that is the name a copy already
// installed by hand has, so it is the name that has to be recognised. In
// tempdb the name is deliberately without the sp_ prefix — an sp_-prefixed
// name falls back to master when the current database has no such procedure,
// which turns ordinary DDL into something else entirely on exactly the path
// that installs one. Both verified live: CREATE OR ALTER dbo.sp_block in
// tempdb finds master's copy, decides it is altering that, and fails with
// "Invalid object name"; DROP PROCEDURE IF EXISTS dbo.sp_block in tempdb
// deletes *master's* copy. A name without the prefix is resolved in the
// database it was issued against, like any other object, so the tempdb copy
// is reachable by plain DDL and can be replaced in place.
type Proc struct {
	// MasterName is the unqualified name in master, sp_-prefixed.
	MasterName string
	// TempDBName is the unqualified name in tempdb, never sp_-prefixed.
	TempDBName string

	// script builds the CREATE OR ALTER for one unqualified name: one batch,
	// no USE and no GO, so it can be sent to sp_executesql in the target
	// database's context.
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

// Exec is the batch that runs the procedure, empty for ProcNone. The name is
// always qualified with its database, since neither name is reachable
// unqualified from an arbitrary database context.
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

// Find reports where the procedure already exists, preferring master. One
// round trip, since the answer gates a whole tab.
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

// Install creates or replaces the procedure at l, which must not be ProcNone.
//
// The script goes to sp_executesql as a parameter rather than being
// concatenated into a batch: the bodies are full of single quotes, and the
// three-part sp_executesql name is what puts the CREATE in the target
// database's context without a USE — this runs on a pooled connection whose
// database must be left as it was found (verified live with DB_NAME()
// afterward).
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
