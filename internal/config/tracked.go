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
	"strings"
	"sync"

	"github.com/radix29/gossms/internal/fileutil"
)

// tracked.go holds the Query Store panel's tracked-query sets: per server, per
// database, the query ids the user has pinned to the Tracked Queries view.
//
// Its own file rather than a field in config.json, for two reasons. config.json
// is connection profiles and application settings, written by the Options and
// Connect dialogs — a set of query ids is neither, and every save of one would
// rewrite the file holding the encrypted passwords. And a tracked-query file
// that goes bad costs the user a list they can rebuild in four keystrokes,
// where config.json's does not.

// trackedFileName is the file, beside config.json.
const trackedFileName = "tracked_queries.json"

// TrackedQueries is the tracked-query sets, keyed by server and then database.
// Safe for concurrent use: the Query Store panel reads it on the UI goroutine
// and the Detail Browser's loaders read it on their own.
type TrackedQueries struct {
	mu   sync.Mutex
	path string
	sets map[string]map[string][]int64

	// unreadable is the error Load hit on an existing file, if any. It makes
	// this set write-protected for the same reason Config.unreadable does: the
	// file's contents are missing from it, so writing it back would replace a
	// list that is very likely still intact with an empty one.
	unreadable error
}

// trackedFile is the on-disk shape. One named field rather than a bare map, so
// the format can gain a sibling without every older file failing to parse.
type trackedFile struct {
	Tracked map[string]map[string][]int64 `json:"tracked"`
}

var (
	trackedOnce sync.Once
	trackedSet  *TrackedQueries
)

// Tracked returns the process-wide tracked-query sets, read from disk on first
// use. One instance rather than one per caller: the Query Store panel and the
// Detail Browser's Tracked Queries grid must show the same list, and each holds
// its own connection.
func Tracked() *TrackedQueries {
	trackedOnce.Do(func() {
		trackedSet = LoadTrackedQueriesFrom(filepath.Join(filepath.Dir(configPath()), trackedFileName))
	})
	return trackedSet
}

// UseTrackedQueries replaces the process-wide set, and is how a test points it
// at a temp directory: Tracked() reads the real user's file, and a test that
// toggled a query through it would edit the list of whoever ran it.
func UseTrackedQueries(t *TrackedQueries) {
	trackedOnce.Do(func() {}) // so a later Tracked() does not load over it
	trackedSet = t
}

// LoadTrackedQueriesFrom reads one tracked-query file. Exported for a test,
// which must not touch the user's own.
func LoadTrackedQueriesFrom(path string) *TrackedQueries {
	t := &TrackedQueries{path: path, sets: map[string]map[string][]int64{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("tracked queries: %s exists but could not be read (%v); "+
				"starting with none and refusing to overwrite it", path, err)
			t.unreadable = err
		}
		return t
	}
	var f trackedFile
	if err := json.Unmarshal(data, &f); err != nil {
		// Same treatment config.json gets: keep the bytes under a .corrupt
		// name and start empty, rather than silently discarding the only copy.
		_ = fileutil.WriteAtomic(path+".corrupt", data, 0o600)
		log.Printf("tracked queries: %s did not parse (%v); kept as %s.corrupt", path, err, path)
		return t
	}
	for server, dbs := range f.Tracked {
		for database, ids := range dbs {
			t.set(server, database, ids)
		}
	}
	return t
}

// serverKey folds a server address to one spelling. Addresses are
// case-insensitive and the same instance is reached by whatever the user typed
// into Connect, so a set pinned as HOST\SQL2022 must come back for host\sql2022.
//
// The database half of the key is deliberately *not* folded, and the asymmetry
// is the point: a server address is free text the user types, while every
// database name reaching this file comes from sys.databases by way of an
// Object Explorer node, in the server's own spelling. Folding it would merge
// two genuinely different databases on a case-sensitive server collation,
// where Sales and sales both exist — which is what SQL Server compares
// database names with. TestServerIsFoldedAndDatabaseIsNot pins it.
func serverKey(server string) string { return strings.ToLower(strings.TrimSpace(server)) }

// SameServer reports whether two addresses name the same instance for
// tracked-query purposes. Exported so a caller deciding which views a toggle
// made stale asks the same question the sets are keyed by, rather than
// comparing the two strings itself and missing HOST\SQL2022 against
// host\sql2022.
func SameServer(a, b string) bool { return serverKey(a) == serverKey(b) }

// set stores ids for one database, sorted and de-duplicated, or drops the entry
// when the list is empty — an empty set and no set are the same thing, and
// leaving the empty one behind grows the file with every database ever visited.
func (t *TrackedQueries) set(server, database string, ids []int64) {
	key := serverKey(server)
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		if dbs := t.sets[key]; dbs != nil {
			delete(dbs, database)
			if len(dbs) == 0 {
				delete(t.sets, key)
			}
		}
		return
	}
	if t.sets[key] == nil {
		t.sets[key] = map[string][]int64{}
	}
	t.sets[key][database] = ids
}

// IDs returns the query ids tracked in one database, ascending. The result is a
// copy — the caller passes it to a report as a query parameter list.
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

// Toggle adds a query to the set or removes it, saves the file, and reports
// which way it went. The set is written on every change rather than at exit:
// there is no other moment this application saves at, and a list of ids is a
// few hundred bytes.
//
// The error is the *save's*: the in-memory set is updated either way, so a
// failed write costs the user the list at next start rather than the toggle
// they just made.
func (t *TrackedQueries) Toggle(server, database string, id int64) (tracked bool, err error) {
	if t == nil {
		// The readers above tolerate a nil set; a writer must not answer
		// "pinned" for a pin it did not record.
		return false, errors.New("tracked queries: no set loaded")
	}
	t.mu.Lock()
	ids := slices.Clone(t.sets[serverKey(server)][database])
	if i := slices.Index(ids, id); i >= 0 {
		ids = slices.Delete(ids, i, i+1)
		tracked = false
	} else {
		ids = append(ids, id)
		tracked = true
	}
	t.set(server, database, ids)
	t.mu.Unlock()
	return tracked, t.Save()
}

// Save writes the file. Refuses to write over a file that could not be read —
// see the unreadable field.
func (t *TrackedQueries) Save() error {
	if t == nil {
		return errors.New("tracked queries: no set loaded")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.unreadable != nil {
		return fmt.Errorf("tracked queries: not saving over %s — it could not be read at startup: %w",
			t.path, t.unreadable)
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(trackedFile{Tracked: t.sets}, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteAtomic(t.path, append(data, '\n'), 0o600)
}
