package tui

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// The six server-level folders that offer a filter. Each is filtered twice —
// once by the tree's filterChildren, once by the Details pane's filterObjects
// over its own independent read — so a folder wired into only one of them
// lists what the other has filtered away.

// serverFolderFilterCase is one folder: the loader's scripted answer, the
// Details pane's loader for the same objects, and a Name criterion matching
// exactly one object that is not the first in the list.
type serverFolderFilterCase struct {
	name     string
	folder   NodeType
	resp     fakeResponse
	detail   func(context.Context, *db.ServerConn, *explorerNode, *[]nodeData) ([]string, [][]string, error)
	total    int
	value    string // a Name criterion value
	wantName string
	created  time.Time // zero when the folder offers no Creation Date
}

var serverFolderFilterCreated = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)

func serverFolderFilterCases() []serverFolderFilterCase {
	when := serverFolderFilterCreated
	return []serverFolderFilterCase{
		{
			name:   "credentials",
			folder: NodeCredentials,
			resp: fakeResponse{
				match: "FROM   sys.credentials",
				cols:  7,
				rows: [][]driver.Value{
					{int64(1), "aaa_first_cred", `DOMAIN\svc_first`, when, when, nil, nil},
					{int64(2), "app_cred", `DOMAIN\svc_app`, when, when, nil, nil},
					{int64(4), "ekm_cred", "ekm_user", when, when, nil, nil},
				},
			},
			detail: credentialsFolderDetail, total: 3, value: "app_", wantName: "app_cred", created: when,
		},
		{
			name:   "audits",
			folder: NodeAudits,
			resp:   auditRows(),
			detail: auditsFolderDetail, total: 3, value: "hipaa", wantName: "HIPAA", created: auditCreated,
		},
		{
			name:   "server audit specifications",
			folder: NodeServerAuditSpecifications,
			resp:   specRows(),
			detail: serverAuditSpecificationsFolderDetail, total: 3, value: "hipaa", wantName: "HIPAA_spec", created: auditCreated,
		},
		{
			name:   "backup devices",
			folder: NodeBackupDevices,
			resp: fakeResponse{
				match: "FROM   sys.backup_devices",
				cols:  3,
				rows: [][]driver.Value{
					{"AAA_First", "DISK", `C:\Backups\first.bak`},
					{"NightlyDev", "DISK", `C:\Backups\nightly.bak`},
					{"OldTape", "TAPE", `\\.\tape0`},
				},
			},
			detail: backupDevicesFolderDetail, total: 3, value: "nightly", wantName: "NightlyDev",
		},
		{
			name:   "server triggers",
			folder: NodeServerTriggers,
			resp:   serverTriggerRows(),
			detail: serverTriggersFolderDetail, total: 3, value: "ddl", wantName: "ddl_audit",
			created: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		},
		{
			name:   "endpoints",
			folder: NodeEndpoints,
			resp:   endpointRows(),
			detail: endpointsFolderDetail, total: 4, value: "agep", wantName: "AGEP",
		},
	}
}

// Both halves must narrow to the same single object. The criterion names an
// object that is neither first nor last, so a half that ignores the filter
// and hands back the whole folder — or one that keeps row 0 — fails.
func TestServerFolderFiltersNarrowBothHalves(t *testing.T) {
	for _, c := range serverFolderFilterCases() {
		t.Run(c.name, func(t *testing.T) {
			f := &nodeFilter{criteria: []filterCriterion{
				{prop: filterProps(c.folder)[0], op: opContains, value: c.value},
			}}

			// The tree: the loader's children, narrowed the way
			// fetchChildren narrows them.
			sc, _ := newFakeConn(t, c.resp)
			node := &explorerNode{data: nodeData{Type: c.folder, conn: sc}}
			children, err := childLoaders[c.folder](loaderCtx{ctx: t.Context(), sc: sc}, node)
			if err != nil {
				t.Fatalf("loader: %v", err)
			}
			if len(children) != c.total {
				t.Fatalf("loader returned %d children, want %d", len(children), c.total)
			}
			kept := filterChildren(f, children)
			if len(kept) != 1 || kept[0].data.Name != c.wantName {
				t.Errorf("tree kept %v, want [%s]", nodeNames(kept), c.wantName)
			}

			// The Details pane: its own read of the same objects.
			sc, _ = newFakeConn(t, c.resp)
			node = &explorerNode{data: nodeData{Type: c.folder, conn: sc, Filter: f}}
			var objs []nodeData
			_, rows, err := c.detail(context.Background(), sc, node, &objs)
			if err != nil {
				t.Fatalf("detail: %v", err)
			}
			if len(rows) != 1 || rows[0][0] != c.wantName {
				t.Errorf("pane kept %v, want one row for %s", rows, c.wantName)
			}
			if len(objs) != len(rows) {
				t.Errorf("got %d row objects for %d rows — the pane's Delete is withheld", len(objs), len(rows))
			}
		})
	}
}

// A Creation Date criterion is only usable where the loader populates
// nodeData.CreateDate: matchDate rejects a zero date outright, so a folder
// offering the property over an unpopulated field filters every object away
// and reads as a broken filter. Both halves are asked, because each builds
// its own nodeData.
func TestServerFolderCreationDateFiltersAreBacked(t *testing.T) {
	for _, c := range serverFolderFilterCases() {
		t.Run(c.name, func(t *testing.T) {
			prop, ok := creationDateProp(filterProps(c.folder))
			if !ok {
				if !c.created.IsZero() {
					t.Fatalf("%v carries a creation date but offers no Creation Date criterion", c.folder)
				}
				return
			}
			if c.created.IsZero() {
				t.Fatalf("%v offers Creation Date but nothing in the fixture carries one", c.folder)
			}
			on := &nodeFilter{criteria: []filterCriterion{
				{prop: prop, op: opOn, value: c.created.Format(filterDateLayout)},
			}}
			before := &nodeFilter{criteria: []filterCriterion{
				{prop: prop, op: opBefore, value: c.created.Format(filterDateLayout)},
			}}

			sc, _ := newFakeConn(t, c.resp)
			node := &explorerNode{data: nodeData{Type: c.folder, conn: sc}}
			children, err := childLoaders[c.folder](loaderCtx{ctx: t.Context(), sc: sc}, node)
			if err != nil {
				t.Fatalf("loader: %v", err)
			}
			if got := len(filterChildren(on, children)); got != c.total {
				t.Errorf("tree kept %d of %d on the objects' own creation date — the loader leaves CreateDate zero", got, c.total)
			}
			if got := len(filterChildren(before, children)); got != 0 {
				t.Errorf("tree kept %d children created before their own creation date", got)
			}

			for _, tc := range []struct {
				f    *nodeFilter
				want int
			}{{on, c.total}, {before, 0}} {
				sc, _ := newFakeConn(t, c.resp)
				node := &explorerNode{data: nodeData{Type: c.folder, conn: sc, Filter: tc.f}}
				var objs []nodeData
				_, rows, err := c.detail(context.Background(), sc, node, &objs)
				if err != nil {
					t.Fatalf("detail: %v", err)
				}
				if len(rows) != tc.want {
					t.Errorf("pane kept %d rows, want %d — its filterObjects key drops CreateDate", len(rows), tc.want)
				}
			}
		})
	}
}

func creationDateProp(props []filterProp) (filterProp, bool) {
	for _, p := range props {
		if p.id == fpCreationDate {
			return p, true
		}
	}
	return filterProp{}, false
}
