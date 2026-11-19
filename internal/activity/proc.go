package activity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
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
// In master it keeps its sp_ name, as a hand-installed copy has. In tempdb it
// has no sp_ prefix: an sp_ name falls back to master when the current database
// lacks it, so in tempdb CREATE OR ALTER dbo.sp_block alters master's copy and
// fails with "Invalid object name", and DROP PROCEDURE IF EXISTS deletes
// master's copy (verified live).
type Proc struct {
	// MasterName is the unqualified name in master, sp_-prefixed.
	MasterName string
	// TempDBName is the unqualified name in tempdb, never sp_-prefixed.
	TempDBName string

	// Options are the switches the procedure's tab offers, in toolbar order;
	// each is off until the user turns it on.
	Options []ProcOption

	// Fixed are arguments every run passes, ahead of the options; only Param
	// and Value apply.
	Fixed []ProcOption

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

// ProcOption is one switch a procedure tab offers. Off, the procedure runs
// with the parameter's own default (the parameter isn't passed); on, Exec
// passes Param = Value.
type ProcOption struct {
	// Label names the switch on the toolbar.
	Label string
	// Key is the switch's letter shortcut on the tab, lower case; the
	// toolbar has no keyboard route of its own.
	Key rune
	// Param is the procedure's parameter, with its @.
	Param string
	// Value is the T-SQL literal passed when the switch is on. A constant,
	// never user input: it is spliced into the batch.
	Value string
}

// Exec is the batch that runs the procedure with its Fixed arguments and the
// options that are on, empty for ProcNone. Always database-qualified, as neither name resolves
// unqualified from an arbitrary database.
func (p *Proc) Exec(l ProcLocation, on ...ProcOption) string {
	q := p.Qualified(l)
	if q == "" {
		return ""
	}
	var args []string
	for _, o := range slices.Concat(p.Fixed, on) {
		args = append(args, o.Param+" = "+o.Value)
	}
	if len(args) == 0 {
		return "exec " + q
	}
	return "exec " + q + " " + strings.Join(args, ", ")
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

// Install creates or replaces the procedure at l (not ProcNone). The script is a
// parameter to the three-part db..sp_executesql, which runs it in the target
// database without USE (the pooled connection's database must stay unchanged)
// and avoids quoting the body.
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

// Current reports whether the copy at l (not ProcNone) is the one Install
// writes, by its stored definition — false for an earlier gossms's body, or when
// the definition can't be read (no VIEW DEFINITION). Keeping the copy current
// carries a fix to a server that already has one; Find alone stops at "it exists".
func (p *Proc) Current(ctx context.Context, db *sql.DB, l ProcLocation) (bool, error) {
	database := l.Database()
	if database == "" {
		return false, fmt.Errorf("check %s: no target database", p.MasterName)
	}
	q := "select definition from " + database + ".sys.sql_modules where object_id = object_id(@p1, 'P')"
	var def sql.NullString
	err := db.QueryRowContext(ctx, q, p.Qualified(l)).Scan(&def)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check %s: %w", p.Qualified(l), err)
	}
	return def.Valid && storedHead(def.String) == storedHead(p.Script(l)), nil
}

// procHead matches a procedure's CREATE head in either form.
var procHead = regexp.MustCompile(`(?i)create\s+(or\s+alter\s+)?procedure`)

// storedHead puts every CREATE head in one form. The server stores CREATE OR
// ALTER's as "create   procedure", so a stored definition otherwise never equals
// the script that made it. Applies to every occurrence on both sides:
// whoisactive.sql's leading comment quotes the head too, and the server leaves
// that one alone.
func storedHead(def string) string {
	return procHead.ReplaceAllString(def, "create procedure")
}
