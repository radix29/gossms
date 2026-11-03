package tui

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// sequence_props.go is Sequence Properties: General (every ALTER SEQUENCE
// option, and Restart with), Permissions and Extended Properties — SSMS's
// pages. The name and schema are not edited here: Rename and Move to Schema are
// the tree's, so the tree label never changes under this dialog and it opens
// with plain show, not showReloading.
//
// General and Extended Properties are gated on ALTER on the sequence or what
// implies it (gate.ObjectWriteRights; probed 2026-11-03: ALTER or CONTROL on the
// sequence and ALTER on its schema alter it, UPDATE does not, and DENY ALTER on
// the sequence refuses it beside ALTER on the schema, Msg 15151). Permissions
// asks what Table Properties' does.

func findSequence(ctx context.Context, sc *db.ServerConn, dbName, schema, name string) (*gosmo.Sequence, error) {
	return inDBSchema(ctx, sc, dbName, schema, name, (*gosmo.Database).SequenceByName)
}

func sequencePropPages(sc *db.ServerConn, dbName, schema, name string) []propPage {
	w := gate.ObjectWriteRights()
	return []propPage{
		withRequiresOn(pageSequenceGeneral(sc, dbName, schema, name), dbName, schema, name, w...),
		withRequires(pageSequencePermissions(sc, dbName, schema, name), dbName, gate.ControlDB),
		withRequiresOn(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return sequencePropertyLevel(schema, name)
		}), dbName, schema, name, w...),
	}
}

// sequenceValue validates one value row: a whole number in bigint's range,
// gosmo.SequenceChanges' — a decimal(38, 0) sequence's wider range is shown but
// cannot be written back. blank allows an empty row (Restart with).
func sequenceValue(r *propsheet.TextRow, blank bool) {
	r.SetValidate(func(v string) error {
		v = strings.TrimSpace(v)
		if v == "" && blank {
			return nil
		}
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("%s must be a whole number from %d to %d", strings.ToLower(r.Label()), int64(math.MinInt64), int64(math.MaxInt64))
		}
		return nil
	})
}

// sequenceCacheItem is the Cache picker's entry for what the catalog holds.
func sequenceCacheItem(s *gosmo.Sequence) string {
	switch {
	case !s.IsCached:
		return nseqCacheNone
	case s.CacheSize == 0:
		return nseqCacheDefault
	}
	return nseqCacheSize
}

func pageSequenceGeneral(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := findSequence(ctx, sc, dbName, schema, name)
			if err != nil {
				return nil, nil, err
			}

			restart := propsheet.Text("Restart with", "", 42)
			sequenceValue(restart, true)
			increment := propsheet.Text("Increment by", s.Increment, 42)
			sequenceValue(increment, false)
			minRow := propsheet.Text("Minimum value", s.MinValue, 42)
			sequenceValue(minRow, false)
			maxRow := propsheet.Text("Maximum value", s.MaxValue, 42)
			sequenceValue(maxRow, false)
			cycle := propsheet.Check("Cycle", s.IsCycling)
			cache := propsheet.Select("Cache", nseqCacheItems, slices.Index(nseqCacheItems, sequenceCacheItem(s)))
			size := int64(s.CacheSize)
			if size == 0 {
				size = 50
			}
			cacheSize := propsheet.Int("Cache size", size, 1, math.MaxInt32, "")
			sync := func() { cacheSize.SetEnabled(cache.Value() == nseqCacheSize) }
			cache.SetOnChange(func(string) { sync() })
			sync()

			f := propsheet.NewForm(
				propsheet.Section("Sequence"),
				propsheet.Static("Name", s.Name),
				propsheet.Static("Schema", s.Schema),
				propsheet.Static("Data type", formatSequenceType(s)),
				propsheet.Section("Values"),
				propsheet.Static("Start value", s.StartValue),
				propsheet.Static("Current value", s.CurrentValue),
				restart,
				propsheet.Note("Restart with sets the next value handed out, and the start value with it. Leave it blank to keep the sequence where it is."),
				increment,
				minRow,
				maxRow,
				cycle,
				propsheet.Section("Cache"),
				cache,
				cacheSize,
			)

			apply := func(ctx context.Context) error {
				ch, err := sequenceChangesFrom(restart, increment, minRow, maxRow, cycle, cache, cacheSize)
				if err != nil {
					return err
				}
				// Every write addresses the sequence by name, so the handle is enough.
				return sc.Server.DatabaseRef(dbName).SequenceRef(schema, name).Alter(ctx, ch)
			}
			return f, apply, nil
		},
	}
}

// sequenceChangesFrom reads the General page's edited rows into one ALTER. A row
// left as loaded stays nil, so the statement names only what changed.
func sequenceChangesFrom(restart, increment, minRow, maxRow *propsheet.TextRow, cycle *propsheet.CheckRow,
	cache *propsheet.SelectRow, cacheSize *propsheet.TextRow) (gosmo.SequenceChanges, error) {
	var ch gosmo.SequenceChanges
	num := func(r *propsheet.TextRow) (*int64, error) {
		v := strings.TrimSpace(r.Value())
		if !r.Dirty() || v == "" {
			return nil, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", strings.ToLower(r.Label()), err)
		}
		return &n, nil
	}
	var err error
	if ch.RestartWith, err = num(restart); err != nil {
		return ch, err
	}
	if ch.Increment, err = num(increment); err != nil {
		return ch, err
	}
	if ch.MinValue, err = num(minRow); err != nil {
		return ch, err
	}
	if ch.MaxValue, err = num(maxRow); err != nil {
		return ch, err
	}
	if cycle.Dirty() {
		ch.Cycle = new(cycle.Checked())
	}
	if cache.Dirty() || cache.Value() == nseqCacheSize && cacheSize.Dirty() {
		switch cache.Value() {
		case nseqCacheNone:
			ch.Cache = &gosmo.SequenceCache{}
		case nseqCacheDefault:
			ch.Cache = &gosmo.SequenceCache{Enabled: true}
		default:
			n, err := cacheSize.IntValue()
			if err != nil {
				return ch, fmt.Errorf("cache size: %w", err)
			}
			ch.Cache = &gosmo.SequenceCache{Enabled: true, Size: int(n)}
		}
	}
	return ch, nil
}

func pageSequencePermissions(sc *db.ServerConn, dbName, schema, name string) propPage {
	return propPage{
		title: "Permissions",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			d, err := sc.Server.DatabaseByName(ctx, dbName)
			if err != nil {
				return nil, nil, err
			}
			perms, err := d.Permissions(ctx, schema, name)
			if err != nil {
				return nil, nil, err
			}
			users, err := d.Users(ctx)
			if err != nil {
				return nil, nil, err
			}
			roles, err := d.Roles(ctx)
			if err != nil {
				return nil, nil, err
			}
			f, apply := buildPermissionsMatrix(databasePermPrincipals(users, roles), gosmo.SecurableSequence.PermissionNames(),
				objectPermEntries(perms),
				securablePermApply(d, gosmo.Securable{Class: gosmo.SecurableSequence, Schema: schema, Name: name}))
			return f, apply, nil
		},
	}
}

func sequenceMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showSequencePropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
	})
}

// showSequencePropertiesFor opens Sequence Properties.
func (a *App) showSequencePropertiesFor(sc *db.ServerConn, dbName, schema, name string) {
	a.propDialog.show(sc, dbName, "Sequence Properties", "Sequence: "+gosmo.QualifiedName(schema, name), "Database: "+dbName,
		func() []propPage { return sequencePropPages(sc, dbName, schema, name) })
}
