package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/safety"
)

// checkWriteTargets is --max-rows-affected's check on the server, after the
// offline one and before the script is sent: the server's row count covers a
// write's own target only, so a target with an enabled DML trigger (whose SET
// NOCOUNT ON hides what it changes) or referenced by an enabled foreign key
// with an ON DELETE/ON UPDATE action the statement can fire (whose changes are
// never counted) could commit far more than the limit. Each such write is
// refused, as is one whose target is a view (its base tables are unknowable),
// a synonym for a linked server's table, or a name found nowhere the script
// may run — a name the login cannot see included: the check fails closed
// (docs/cli.md § Transactions). A table variable or temporary table has
// neither, and is skipped.
//
// A target is looked for in the run's database and in every database a USE in
// the script names, since which one is current when the write runs is not
// known before it runs; a three-part name only in its own. A one-part name is
// looked for in every schema. Both over-approximate: a same-named table with a
// trigger elsewhere refuses a write that would not have reached it.
//
// The refusal is nil when every target is clear; err is a read that failed.
func checkWriteTargets(ctx context.Context, srv *gosmo.Server, sc *safety.Script, script string, p safety.Policy) (*safety.Blocked, error) {
	var writes []safety.Write
	for _, w := range sc.Writes() {
		if !w.Local() {
			writes = append(writes, w)
		}
	}
	if len(writes) == 0 {
		return nil, nil
	}
	run, err := srv.CurrentDatabase(ctx)
	if err != nil {
		return nil, err
	}
	c := &targetCheck{srv: srv, dbs: map[string]*gosmo.Database{}, seen: map[string][]string{}}
	runDBs := []string{run}
	for _, name := range safety.UseDatabases(script) {
		if !slices.ContainsFunc(runDBs, func(d string) bool { return strings.EqualFold(d, name) }) {
			runDBs = append(runDBs, name)
		}
	}
	var refused []safety.Statement
	for _, w := range writes {
		reasons, err := c.write(ctx, w, runDBs)
		if err != nil {
			return nil, err
		}
		if len(reasons) > 0 {
			refused = append(refused, w.Describe(strings.Join(reasons, "; ")))
		}
	}
	return p.RefuseUncounted(sc, refused), nil
}

// targetCheck is one run's check: the databases read so far, and each
// target's reasons by verb and name, so a table written ten times is read
// once.
type targetCheck struct {
	srv  *gosmo.Server
	dbs  map[string]*gosmo.Database // by lower-cased name; nil: no such database
	seen map[string][]string
}

// database is the database name, nil when the server has none by that name
// (a USE of a database this server lacks fails when it runs).
func (c *targetCheck) database(ctx context.Context, name string) (*gosmo.Database, error) {
	key := strings.ToLower(name)
	if d, ok := c.dbs[key]; ok {
		return d, nil
	}
	d, err := c.srv.DatabaseByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		d, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.dbs[key] = d
	return d, nil
}

// write is why w's target fails the check, nil when it passes. runDBs is the
// databases a name without a database part may be in.
func (c *targetCheck) write(ctx context.Context, w safety.Write, runDBs []string) ([]string, error) {
	key := w.Verb + "\x00" + strings.ToLower(strings.Join(w.Parts, "\x00"))
	if r, ok := c.seen[key]; ok {
		return r, nil
	}
	r, err := c.name(ctx, w.Verb, w.Parts, runDBs, true)
	if err == nil {
		c.seen[key] = r
	}
	return r, err
}

// name checks the table that parts (one to four, unquoted) names for verb.
// synonymOK is false when parts is a synonym's base, which may not be a
// synonym again.
func (c *targetCheck) name(ctx context.Context, verb string, parts []string, runDBs []string, synonymOK bool) ([]string, error) {
	dbs, schema, name := runDBs, "", parts[len(parts)-1]
	switch len(parts) {
	case 2:
		schema = parts[0]
	case 3:
		schema = parts[1]
		if parts[0] != "" {
			dbs = []string{parts[0]}
		}
	case 4:
		return []string{"a synonym for a linked server's table, which the check cannot read"}, nil
	}
	var reasons []string
	found := false
	for _, dbName := range dbs {
		d, err := c.database(ctx, dbName)
		if err != nil {
			return nil, err
		}
		if d == nil {
			continue
		}
		objs, err := objectsNamed(ctx, d, schema, name)
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			found = true
			r, err := c.object(ctx, verb, d, o, synonymOK)
			if err != nil {
				return nil, err
			}
			reasons = append(reasons, r...)
		}
	}
	if !found {
		return []string{fmt.Sprintf("no table by that name in %s that the login can see", strings.Join(dbs, ", "))}, nil
	}
	return reasons, nil
}

// objectsNamed is the objects schema.name names in d: one at most, or for an
// empty schema (a one-part name, db..name) every schema's.
func objectsNamed(ctx context.Context, d *gosmo.Database, schema, name string) ([]*gosmo.ObjectInfo, error) {
	if schema == "" {
		return d.ObjectsNamed(ctx, name)
	}
	o, err := d.ObjectByName(ctx, schema, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []*gosmo.ObjectInfo{o}, nil
}

// object checks one object a write's name resolved to.
func (c *targetCheck) object(ctx context.Context, verb string, d *gosmo.Database, o *gosmo.ObjectInfo, synonymOK bool) ([]string, error) {
	qn := gosmo.QualifiedName(o.Schema, o.Name)
	switch o.Type {
	case "U":
		return tableReasons(ctx, verb, d, o)
	case "SN":
		if !synonymOK {
			return []string{fmt.Sprintf("synonym %s names another synonym", qn)}, nil
		}
		syn, err := d.SynonymByName(ctx, o.Schema, o.Name)
		if err != nil {
			return nil, err
		}
		parts, err := gosmo.SplitName(syn.BaseObject)
		if err != nil {
			return []string{fmt.Sprintf("synonym %s's base object %s cannot be read", qn, syn.BaseObject)}, nil
		}
		return c.name(ctx, verb, parts, []string{d.Name}, false)
	case "V":
		return []string{fmt.Sprintf("%s is a view, whose base tables the check cannot see", qn)}, nil
	}
	return []string{fmt.Sprintf("%s is a %s, not a table", qn, strings.ToLower(strings.ReplaceAll(o.TypeDesc, "_", " ")))}, nil
}

// tableReasons is what in table o changes rows verb's count leaves out: each
// enabled DML trigger verb fires, and each enabled foreign key referencing o
// whose action for verb is not NO ACTION.
func tableReasons(ctx context.Context, verb string, d *gosmo.Database, o *gosmo.ObjectInfo) ([]string, error) {
	t, err := d.TableByName(ctx, o.Schema, o.Name)
	if err != nil {
		return nil, err
	}
	triggers, err := t.Triggers(ctx)
	if err != nil {
		return nil, err
	}
	var reasons []string
	for _, tr := range triggers {
		if tr.IsEnabled && (verb == "MERGE" || slices.Contains(tr.Events, verb)) {
			reasons = append(reasons, fmt.Sprintf("%s has trigger %s", t.FullName(), gosmo.QualifiedName(tr.Schema, tr.Name)))
		}
	}
	if verb == "INSERT" {
		return reasons, nil
	}
	keys, err := t.ReferencingForeignKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, fk := range keys {
		if fk.IsDisabled {
			continue
		}
		for _, a := range []struct{ on, action string }{{"DELETE", fk.DeleteAction}, {"UPDATE", fk.UpdateAction}} {
			if (verb == "MERGE" || verb == a.on) && a.action != "" && a.action != "NO_ACTION" {
				reasons = append(reasons, fmt.Sprintf("foreign key %s on %s is ON %s %s", gosmo.QuoteName(fk.Name),
					gosmo.QualifiedName(fk.Schema, fk.TableName), a.on, strings.ReplaceAll(a.action, "_", " ")))
			}
		}
	}
	return reasons, nil
}
