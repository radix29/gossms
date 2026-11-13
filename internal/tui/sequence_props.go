package tui

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/propsheet"
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
		withRequires(pageObjectPermissions(sc, dbName, gosmo.SecurableSequence, schema, name), dbName, gate.ControlDB),
		withRequiresOn(pageExtendedProperties(sc, dbName, func() gosmo.ExtendedPropertyLevel {
			return sequencePropertyLevel(schema, name)
		}), dbName, schema, name, w...),
	}
}

// sequenceRange is the range a sequence of type dt holds: the integer types',
// and ±(10^p − 1) for decimal and numeric at precision p (0 is the bare
// name's 18). ok is false for a user-defined alias type, whose base type the
// catalog read does not carry; the server then judges the range.
func sequenceRange(dt gosmo.DataType, precision int) (lo, hi *big.Int, ok bool) {
	switch dt {
	case gosmo.DataTypeTinyInt:
		return big.NewInt(0), big.NewInt(math.MaxUint8), true
	case gosmo.DataTypeSmallInt:
		return big.NewInt(math.MinInt16), big.NewInt(math.MaxInt16), true
	case gosmo.DataTypeInt:
		return big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32), true
	case gosmo.DataTypeBigInt:
		return big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64), true
	case gosmo.DataTypeDecimal, gosmo.DataTypeNumeric:
		if precision < 1 || precision > 38 {
			precision = 18
		}
		hi = new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
		hi.Sub(hi, big.NewInt(1))
		return new(big.Int).Neg(hi), hi, true
	}
	return nil, nil, false
}

// sequenceNumber parses a value row's text as gosmo takes it: a whole number
// of any size, as decimal digits. The digits go to gosmo as the row holds them
// (trimmed); gosmo re-renders them into the statement.
func sequenceNumber(r *propsheet.TextRow) (*big.Int, bool) {
	return new(big.Int).SetString(strings.TrimSpace(r.Value()), 10)
}

// sequenceValue validates one value row: a whole number within the range
// typeRange reports, the sequence's data type's. Values are carried as text end
// to end, so a decimal(38, 0) sequence's whole range can be written. blank
// allows an empty row (Restart with).
func sequenceValue(r *propsheet.TextRow, blank bool, typeRange func() (lo, hi *big.Int, ok bool)) {
	r.SetValidate(func(v string) error {
		v = strings.TrimSpace(v)
		if v == "" && blank {
			return nil
		}
		n, isNum := new(big.Int).SetString(v, 10)
		lo, hi, known := typeRange()
		switch {
		case known && (!isNum || n.Cmp(lo) < 0 || n.Cmp(hi) > 0):
			return fmt.Errorf("%s must be a whole number from %s to %s", strings.ToLower(r.Label()), lo, hi)
		case !isNum:
			return fmt.Errorf("%s must be a whole number", strings.ToLower(r.Label()))
		}
		return nil
	})
}

// sequenceLastUsed is the Last used value row's text: last_used_value, which
// tells "nothing handed out yet" apart from Current value's start value.
func sequenceLastUsed(s *gosmo.Sequence) string {
	switch {
	case !s.TracksLastUsedValue():
		return "(not tracked before SQL Server 2017)"
	case s.LastUsedValue == "":
		return "(none since created or restarted)"
	}
	return s.LastUsedValue
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

			// An alias type (DataTypeSchema not sys) has no range here; a hidden
			// one already reads as its base type.
			typeRange := func() (lo, hi *big.Int, ok bool) {
				if s.DataTypeSchema != "" && s.DataTypeSchema != "sys" {
					return nil, nil, false
				}
				return sequenceRange(s.DataType, s.Precision)
			}
			restart := propsheet.Text("Restart with", "", 42)
			sequenceValue(restart, true, typeRange)
			increment := propsheet.Text("Increment by", s.Increment, 42)
			sequenceValue(increment, false, typeRange)
			minRow := propsheet.Text("Minimum value", s.MinValue, 42)
			sequenceValue(minRow, false, typeRange)
			maxRow := propsheet.Text("Maximum value", s.MaxValue, 42)
			sequenceValue(maxRow, false, typeRange)
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
				propsheet.Static("Last used value", sequenceLastUsed(s)),
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
	// A row left as loaded is "", which SequenceChanges leaves as stored. The
	// rows' validators have run (Form.Validate), and gosmo refuses anything
	// but a whole number again.
	num := func(r *propsheet.TextRow) string {
		if !r.Dirty() {
			return ""
		}
		return strings.TrimSpace(r.Value())
	}
	ch.RestartWith = num(restart)
	ch.Increment = num(increment)
	ch.MinValue = num(minRow)
	ch.MaxValue = num(maxRow)
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
