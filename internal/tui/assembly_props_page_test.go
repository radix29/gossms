package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
)

// Assembly Properties, driven through fakedb_test.go.
//
// The assembly under test is not the first row in sys.assemblies and is not
// the shipped one, so a page that ignored its argument and read whichever row
// sorts first cannot pass. The by-name read is scoped with arg: and placed
// before the list read, because both queries contain "FROM   sys.assemblies"
// and responses match by substring, in order.

const propAssembly = "GeoUtils"

func assemblyPropResponses() []fakeResponse {
	return []fakeResponse{
		{
			match: "FROM   sys.assemblies a",
			arg:   propAssembly,
			cols:  9,
			rows: [][]driver.Value{{
				propAssembly, int64(65536), "dbo",
				"GeoUtils, version=1.0.0.0, culture=neutral, publickeytoken=null",
				"EXTERNAL_ACCESS", true, true, propTypeDate, propTypeDate,
			}},
		},
		{
			match: "FROM   sys.assemblies a",
			cols:  9,
			rows: [][]driver.Value{
				{"Microsoft.SqlServer.Types", int64(1), "sys", "Microsoft.SqlServer.Types, version=14.0.0.0",
					"UNSAFE", true, false, propTypeDate, propTypeDate},
				{propAssembly, int64(65536), "dbo", "GeoUtils, version=1.0.0.0",
					"EXTERNAL_ACCESS", true, true, propTypeDate, propTypeDate},
			},
		},
		{match: "FROM   sys.assembly_files f", cols: 3, rows: [][]driver.Value{
			{"GeoUtils.dll", int64(1), int64(45056)},
			{"GeoUtils.pdb", int64(2), int64(102400)},
		}},
		{match: "FROM   sys.assembly_modules am", cols: 6, rows: [][]driver.Value{
			{int64(11), "dbo", "Distance", "FS", "GeoUtils.Point", "Distance"},
			{int64(12), "dbo", "Centroid", "AF", "GeoUtils.Centroid", ""},
		}},
	}
}

func TestAssemblyGeneralLoadsTheSelectedAssembly(t *testing.T) {
	sc, inst := newTypePropConn(t, assemblyPropResponses()...)
	form, apply := loadPage(t, pageAssemblyGeneral(sc, propTypeDB, propAssembly), inst)

	if got := staticValue(t, form, "Name"); got != propAssembly {
		t.Errorf("Name is %q, want the selected assembly's %q", got, propAssembly)
	}
	// The permission set is the whole security story of a CLR assembly:
	// EXTERNAL_ACCESS and UNSAFE reach outside the server, SAFE does not.
	if got := staticValue(t, form, "Permission set"); got != "EXTERNAL_ACCESS" {
		t.Errorf("Permission set is %q", got)
	}
	if got := staticValue(t, form, "Shipped with SQL Server"); got != "False" {
		t.Errorf("Shipped with SQL Server is %q — this row has is_user_defined set", got)
	}
	if apply != nil {
		t.Error("the General page has an apply, but ALTER ASSEMBLY needs a binary no form can supply")
	}
}

// The Files page is what says how big the assembly is and whether its source
// was registered beside it. It must not read the payload to do that — the
// size comes from DATALENGTH in the listing.
func TestAssemblyFilesPageListsTheRegisteredFiles(t *testing.T) {
	sc, inst := newTypePropConn(t, assemblyPropResponses()...)
	form, apply := loadPage(t, pageAssemblyFiles(sc, propTypeDB, propAssembly), inst)

	rows := gridRowsOf(t, form)
	if len(rows) != 2 {
		t.Fatalf("the Files page shows %d rows, want 2", len(rows))
	}
	if rows[0][0] != "1" || rows[0][1] != "GeoUtils.dll" {
		t.Errorf("row 1 is %v, want file 1 as the assembly binary", rows[0])
	}
	if rows[0][2] != "44.0 KB" {
		t.Errorf("row 1's size is %q, want 44.0 KB", rows[0][2])
	}
	if apply != nil {
		t.Error("the Files page has an apply")
	}

	// DATALENGTH, not the payload. Reads("f.content") returns every query
	// mentioning the column; each has to be the length read, not a SELECT of
	// a multi-megabyte binary the page shows nothing of.
	for _, q := range inst.Reads("f.content") {
		if !strings.Contains(q, "DATALENGTH") {
			t.Errorf("the Files page read an assembly payload:\n%s", q)
		}
	}
}

// The Routines page is what a user checks before deleting an assembly: an
// assembly bound to nothing is safe to drop, and one bound to a function is
// not. The sys.objects type codes are spelled out, since "AF" is not a thing
// a reader should have to remember.
func TestAssemblyRoutinesPageNamesWhatIsBoundToIt(t *testing.T) {
	sc, inst := newTypePropConn(t, assemblyPropResponses()...)
	form, apply := loadPage(t, pageAssemblyRoutines(sc, propTypeDB, propAssembly), inst)

	rows := gridRowsOf(t, form)
	if len(rows) != 2 {
		t.Fatalf("the Routines page shows %d rows, want 2", len(rows))
	}
	if rows[0][0] != "dbo.Distance" || rows[0][1] != "Scalar function" {
		t.Errorf("row 1 is %v, want dbo.Distance as a scalar function", rows[0])
	}
	if rows[1][1] != "Aggregate" {
		t.Errorf("row 2's kind is %q, want Aggregate — AF is not a kind a reader knows", rows[1][1])
	}
	if apply != nil {
		t.Error("the Routines page has an apply")
	}
}
