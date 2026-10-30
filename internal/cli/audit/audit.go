// Package audit is the command line's audit log: one JSON line per call that
// reached a class above read, or was refused (docs/cli.md § Audit log). It is
// always on — no flag or environment variable turns it off, since an agent
// could set either — and it never holds T-SQL text or a secret: options
// whose values may carry them are marked in the command tree
// (spec.Option.Audit) and recorded as a SHA-256, or not at all.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/fileutil"
)

// FileName is the log's name in the configuration directory, beside
// config.json and gossms.log.
const FileName = "audit.jsonl"

// DefaultPath is the log's path: FileName in config.ConfigDir().
func DefaultPath() string { return filepath.Join(config.ConfigDir(), FileName) }

// Record is one line of the log. Field order is the line's.
type Record struct {
	TS        string   `json:"ts"` // UTC, RFC 3339 with milliseconds
	Profile   string   `json:"profile"`
	Server    string   `json:"server"`
	Database  string   `json:"database"`
	Login     string   `json:"login"`
	Command   string   `json:"command"`
	Class     string   `json:"class"` // reached; refused, the class detected
	DryRun    bool     `json:"dryRun"`
	Args      []string `json:"args"`
	SQLSha256 string   `json:"sqlSha256,omitempty"`
	Outcome   string   `json:"outcome"` // the envelope's status
	Exit      int      `json:"exit"`
}

// Logged reports whether a call is logged: it reached (or was refused at) a
// class above read, or it was refused.
func Logged(class spec.Class, blocked bool) bool {
	return blocked || (class != "" && class != spec.ClassRead)
}

// Check reports whether the log at path can be appended to, creating it
// (and its directory) when missing. A command calls it before it changes
// anything: a change the log cannot record is not made.
func Check(path string) error {
	f, err := open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// Append writes r as one line at the end of the log at path, under the
// file's lock, so calls running at once never interleave their lines.
func Append(path string, r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	// The lock file lives beside the log: its directory first, or a refusal
	// on a fresh machine — no Check before it — could never be logged.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fileutil.WithLock(path, func() error {
		f, err := open(path)
		if err != nil {
			return err
		}
		if _, err := f.Write(line); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
}

// open opens the log for appending, owner-only as gossms.log is, creating
// its directory.
func open(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}

// Timestamp is t as a record's ts.
func Timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Hash is the hex SHA-256 of s, as the log records a hashed value.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Args is inv's command line as the log records it: the positional
// arguments, then every option given (on the command line or by its
// environment variable) as --name=value in the command's option order, then
// the globals'. A bool is --name or --no-name; a list is one comma-joined
// value; a repeated option is one token per value. An AuditHash option's
// value is "sha256:<hex>" (each value of a repeated one), an AuditOmit
// option is left out. Rebuilt from the parse rather than copied from argv,
// so a response file, a --name=value spelling or a short alias cannot slip a
// value past its mark.
func Args(tree *spec.Tree, inv *spec.Invocation) []string {
	out := slices.Clone(inv.Args)
	if out == nil {
		out = []string{}
	}
	for _, o := range slices.Concat(inv.Command.Options, tree.Globals) {
		if !inv.Given[o.Name] || o.Audit == spec.AuditOmit {
			continue
		}
		val := func(v string) string {
			if o.Audit == spec.AuditHash {
				return "sha256:" + Hash(v)
			}
			return v
		}
		switch v := inv.Values[o.Name].(type) {
		case bool:
			if v {
				out = append(out, "--"+o.Name)
			} else {
				out = append(out, "--no-"+o.Name)
			}
		case int:
			out = append(out, "--"+o.Name+"="+strconv.Itoa(v))
		case string:
			out = append(out, "--"+o.Name+"="+val(v))
		case []string:
			if o.Type == spec.TypeRepeat || o.Audit == spec.AuditHash {
				for _, item := range v {
					out = append(out, "--"+o.Name+"="+val(item))
				}
			} else {
				out = append(out, "--"+o.Name+"="+strings.Join(v, ","))
			}
		default:
			out = append(out, "--"+o.Name+"="+val(fmt.Sprint(v)))
		}
	}
	return out
}
