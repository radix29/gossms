package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/cli/spec"
)

func TestArgs(t *testing.T) {
	tree := &spec.Tree{Globals: []spec.Option{{Name: "output", Type: spec.TypeEnum, Global: true}}}
	c := &spec.Command{Path: []string{"g", "c"}, Options: []spec.Option{
		{Name: "sql", Type: spec.TypeString, Audit: spec.AuditHash},
		{Name: "param", Type: spec.TypeRepeat, Audit: spec.AuditHash},
		{Name: "secret", Type: spec.TypeString, Audit: spec.AuditOmit},
		{Name: "option", Type: spec.TypeRepeat},
		{Name: "type", Type: spec.TypeList},
		{Name: "flag", Type: spec.TypeBool},
		{Name: "off", Type: spec.TypeBool},
		{Name: "top", Type: spec.TypeInt},
		{Name: "unset", Type: spec.TypeString},
	}}
	inv := &spec.Invocation{
		Command: c,
		Args:    []string{"app"},
		Values: map[string]any{"sql": "SELECT 1", "param": []string{"a=1:int", "b=2:int"}, "secret": "pw",
			"option": []string{"A=1,2", "B=3"}, "type": []string{"x", "y"}, "flag": true, "off": false, "top": 5,
			"unset": "default", "output": "json"},
		Given: map[string]bool{"sql": true, "param": true, "secret": true, "option": true, "type": true,
			"flag": true, "off": true, "top": true, "output": true},
	}
	want := []string{"app", "--sql=sha256:" + Hash("SELECT 1"), "--param=sha256:" + Hash("a=1:int"),
		"--param=sha256:" + Hash("b=2:int"), "--option=A=1,2", "--option=B=3", "--type=x,y", "--flag", "--no-off",
		"--top=5", "--output=json"}
	if got := Args(tree, inv); !slices.Equal(got, want) {
		t.Errorf("Args:\n got %q\nwant %q", got, want)
	}
	if got := Hash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("Hash(abc) = %s, not SHA-256's test vector", got)
	}
}

func TestLogged(t *testing.T) {
	for _, c := range []struct {
		class   spec.Class
		blocked bool
		want    bool
	}{
		{spec.ClassRead, false, false},
		{"", false, false},
		{spec.ClassRead, true, true},
		{spec.ClassWrite, false, true},
		{spec.ClassDDL, false, true},
		{spec.ClassAdmin, false, true},
	} {
		if got := Logged(c.class, c.blocked); got != c.want {
			t.Errorf("Logged(%q, %v) = %v", c.class, c.blocked, got)
		}
	}
}

// TestAppendCreatesOwnerOnly: the log and its directory are made on first
// use — by Append alone too, as a refusal's line is, with no Check before
// it — the file owner-only, and each record is one line appended.
func TestAppendCreatesOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gossms", FileName)
	for i := range 2 {
		if err := Append(path, Record{Command: "c" + strconv.Itoa(i), Args: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); len(lines) != 2 ||
		!strings.HasPrefix(lines[1], `{"ts":`) || !strings.Contains(lines[1], `"command":"c1"`) {
		t.Errorf("log:\n%s", data)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func TestCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", FileName)
	if err := Check(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() != 0 {
		t.Errorf("Check left %v, %v; want an empty file", fi, err)
	}
}

func TestCheckFailsWhenUncreatable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(filepath.Join(file, FileName)); err == nil {
		t.Error("Check under a regular file succeeded")
	}
}

// childEnv names the log a re-run of this test binary appends to as one of
// TestAppendParallelProcesses's writers.
const childEnv = "GOSSMS_AUDIT_TEST_CHILD"

const (
	children     = 6
	perChild     = 40
	argBytesEach = 8 << 10 // past PIPE_BUF: an O_APPEND write this long may interleave without the lock
)

// TestAppendParallelProcesses runs several processes appending at once: every
// line must come out whole, none lost. On Linux one O_APPEND write is atomic
// even unlocked, so the test passes without the lock there; it fails once a
// line takes two writes unlocked (checked), and Windows gives no such
// guarantee — the lock is what holds there.
func TestAppendParallelProcesses(t *testing.T) {
	if path := os.Getenv(childEnv); path != "" {
		id := os.Getenv(childEnv + "_ID")
		big := strings.Repeat(id, argBytesEach)
		for i := range perChild {
			if err := Append(path, Record{Command: id + "-" + strconv.Itoa(i), Args: []string{big}}); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		return
	}
	if testing.Short() {
		t.Skip("spawns processes")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	var cmds []*exec.Cmd
	for i := range children {
		c := exec.Command(exe, "-test.run=^TestAppendParallelProcesses$")
		c.Env = append(os.Environ(), childEnv+"="+path, childEnv+"_ID="+string(rune('a'+i)))
		var stderr bytes.Buffer
		c.Stderr = &stderr
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("child: %v: %s", err, c.Stderr)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("a line is not one record (interleaved?): %v", err)
		}
		id, _, _ := strings.Cut(r.Command, "-")
		if len(r.Args) != 1 || r.Args[0] != strings.Repeat(id, argBytesEach) {
			t.Fatalf("record %s's args are damaged", r.Command)
		}
		seen[r.Command] = true
	}
	if len(seen) != children*perChild {
		t.Errorf("%d distinct records, want %d", len(seen), children*perChild)
	}
}
