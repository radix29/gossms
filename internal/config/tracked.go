package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/radix29/gossms/internal/fileutil"
)

// tracked.go holds the Query Store panel's tracked-query sets: per server and
// database, the query ids pinned to the Tracked Queries view.
//
// Kept out of config.json: it's neither a profile nor a setting, every save
// would rewrite the file holding encrypted passwords, and losing it only costs
// a rebuildable list.

// trackedFileName is the file, beside config.json.
const trackedFileName = "tracked_queries.json"

// TrackedQueries is the tracked-query sets, keyed by server then database. Safe
// for concurrent use (the Query Store panel and the Detail Browser's loaders).
type TrackedQueries struct {
	// mu guards everything below and is never held across file work, so a
	// save waiting on another instance's lock does not stall a reader.
	mu sync.Mutex
	// saveMu runs one Save at a time: each adopts the file it wrote and
	// drops the pending ops it replayed, by position.
	saveMu sync.Mutex

	path string
	sets map[string]map[string][]int64

	// unreadable is the error Load hit on an existing file. It write-protects
	// the set, as Config.unreadable does.
	unreadable error

	// pending is this process's Toggles not yet saved, which Save replays
	// onto the file as it is now — as Config.ops does, and for the same
	// reason: another gossms instance may have saved since this one loaded.
	pending []trackOp
}

// trackOp is one recorded Toggle, as the add or remove it resolved to.
type trackOp struct {
	server, database string
	id               int64
	add              bool
}

// trackedFile is the on-disk shape; a named field so the format can grow
// without breaking older files.
type trackedFile struct {
	Tracked map[string]map[string][]int64 `json:"tracked"`
}

var (
	trackedOnce sync.Once
	trackedSet  *TrackedQueries
)

// Tracked returns the process-wide sets, loaded on first use. Shared so the
// Query Store panel and Detail Browser show the same list.
func Tracked() *TrackedQueries {
	trackedOnce.Do(func() {
		trackedSet = LoadTrackedQueriesFrom(filepath.Join(filepath.Dir(configPath()), trackedFileName))
	})
	return trackedSet
}

// UseTrackedQueries replaces the process-wide set, so tests can use a temp
// directory instead of the user's file.
func UseTrackedQueries(t *TrackedQueries) {
	trackedOnce.Do(func() {}) // so a later Tracked() does not load over it
	trackedSet = t
}

// LoadTrackedQueriesFrom reads one tracked-query file. Exported for tests.
func LoadTrackedQueriesFrom(path string) *TrackedQueries {
	t := &TrackedQueries{path: path, sets: map[string]map[string][]int64{}}
	sets, err := readTrackedFile(path)
	if err != nil {
		log.Printf("tracked queries: %s exists but could not be read (%v); "+
			"starting with none and refusing to overwrite it", path, err)
		t.unreadable = err
		return t
	}
	t.sets = sets
	return t
}

// readTrackedFile reads path into a fresh set map. A missing file is an empty
// set; one that doesn't parse is kept as .corrupt and is empty too. Only a
// file that exists and can't be read is an error.
func readTrackedFile(path string) (map[string]map[string][]int64, error) {
	sets := map[string]map[string][]int64{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return sets, nil
	}
	if err != nil {
		return nil, err
	}
	var f trackedFile
	if err := json.Unmarshal(data, &f); err != nil {
		// As with config.json: keep the bytes as .corrupt and start empty.
		_ = fileutil.WriteAtomic(path+".corrupt", data, 0o600)
		log.Printf("tracked queries: %s did not parse (%v); kept as %s.corrupt", path, err, path)
		return sets, nil
	}
	// Merged, not assigned: keys are re-folded on every read, so a file
	// written under an older fold — ToLower(Server) — loads into today's keys,
	// and spellings that now name one instance ("host" and "host,1433") pool
	// their ids. The next Save writes the file back in the new keys.
	for server, dbs := range f.Tracked {
		for database, ids := range dbs {
			held := sets[serverKey(server)][database]
			setTracked(sets, server, database, append(slices.Clone(held), ids...))
		}
	}
	return sets, nil
}

// serverKey folds a server address by InstanceKey, the rule peers and saved
// credentials key by: HOST\SQL2022, host\sql2022 and host\sql2022,1500 share a
// set, as do "host" and "host,1433", while "host,55253" is another instance.
// Callers pass ConnectionAddress(opts), not opts.Server, which drops a
// Port-field port.
//
// The database is deliberately not folded: database names come from
// sys.databases in the server's spelling, and on a case-sensitive collation
// Sales and sales are different databases. TestServerIsFoldedAndDatabaseIsNot
// pins it.
func serverKey(server string) string { return InstanceKey(server) }

// SameServer reports whether two addresses name the same instance, by the same
// rule the sets are keyed by.
func SameServer(a, b string) bool { return serverKey(a) == serverKey(b) }

// setTracked stores ids for one database sorted and de-duplicated, or drops the
// entry when empty so the file doesn't grow with every database visited.
func setTracked(sets map[string]map[string][]int64, server, database string, ids []int64) {
	key := serverKey(server)
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		if dbs := sets[key]; dbs != nil {
			delete(dbs, database)
			if len(dbs) == 0 {
				delete(sets, key)
			}
		}
		return
	}
	if sets[key] == nil {
		sets[key] = map[string][]int64{}
	}
	sets[key][database] = ids
}

// apply makes op's change to sets: adding an id already there, or removing
// one that isn't, is a no-op.
func (op trackOp) apply(sets map[string]map[string][]int64) {
	ids := slices.Clone(sets[serverKey(op.server)][op.database])
	i := slices.Index(ids, op.id)
	switch {
	case op.add && i < 0:
		ids = append(ids, op.id)
	case !op.add && i >= 0:
		ids = slices.Delete(ids, i, i+1)
	default:
		return
	}
	setTracked(sets, op.server, op.database, ids)
}

// IDs returns a copy of one database's tracked query ids, ascending.
func (t *TrackedQueries) IDs(server, database string) []int64 {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.sets[serverKey(server)][database])
}

// IsTracked reports whether one query is in the set.
func (t *TrackedQueries) IsTracked(server, database string, id int64) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Contains(t.sets[serverKey(server)][database], id)
}

// Toggle adds or removes a query in memory and reports the new state. Save
// writes it; the Query Store panel calls that off the UI goroutine.
func (t *TrackedQueries) Toggle(server, database string, id int64) (tracked bool, err error) {
	if t == nil {
		// Readers tolerate a nil set; a writer must not claim a pin it didn't
		// record.
		return false, errors.New("tracked queries: no set loaded")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	op := trackOp{server: server, database: database, id: id,
		add: !slices.Contains(t.sets[serverKey(server)][database], id)}
	op.apply(t.sets)
	t.pending = append(t.pending, op)
	return op.add, nil
}

// Save writes the file, refusing to overwrite one that couldn't be read (see
// unreadable). It re-reads the file and replays this process's unsaved
// Toggles onto it, then adopts the result, so pins another gossms instance
// saved meanwhile are kept on disk and appear here. A Toggle made while the
// file is written stays pending for the next Save, replayed onto the adopted
// set so it is not lost from memory either.
func (t *TrackedQueries) Save() error {
	if t == nil {
		return errors.New("tracked queries: no set loaded")
	}
	t.saveMu.Lock()
	defer t.saveMu.Unlock()
	t.mu.Lock()
	unreadable, ops := t.unreadable, slices.Clone(t.pending)
	t.mu.Unlock()
	if unreadable != nil {
		return fmt.Errorf("tracked queries: not saving over %s — it could not be read at startup: %w",
			t.path, unreadable)
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	var sets map[string]map[string][]int64
	// Locked from the re-read to the write, as Config.Save.
	err := fileutil.WithLock(t.path, func() error {
		var err error
		sets, err = readTrackedFile(t.path)
		if err != nil {
			return fmt.Errorf("tracked queries: not saving over %s — it could not be re-read: %w", t.path, err)
		}
		for _, op := range ops {
			op.apply(sets)
		}
		data, err := json.MarshalIndent(trackedFile{Tracked: sets}, "", "  ")
		if err != nil {
			return err
		}
		return fileutil.WriteAtomic(t.path, append(data, '\n'), 0o600)
	})
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	later := slices.Clone(t.pending[len(ops):])
	for _, op := range later {
		op.apply(sets)
	}
	t.sets = sets
	t.pending = later
	return nil
}
