package tui

import (
	"database/sql/driver"
	"testing"
)

// External Resources Properties, driven through fakedb_test.go.
//
// The version-gated fields are what these tests are mostly about. gosmo reads
// connection_options, pushdown, first_row and parser_version as empty or zero
// on an instance whose catalog lacks the column, rather than failing the read
// — so a page showing them unconditionally would report "Pushdown enabled:
// False" on SQL Server 2017, stating positively that a source has pushdown
// off when the instance cannot express the setting at all.

func externalDataSourceResponse(connOptions string, pushdown bool) fakeResponse {
	return fakeResponse{match: "FROM   sys.external_data_sources s", cols: 10, rows: [][]driver.Value{{
		"HadoopCluster", int64(65536), "hdfs://nn:8020", "HADOOP",
		"nn:8050", "HadoopCred", "", "", connOptions, pushdown,
	}}}
}

func TestExternalDataSourceGeneralShowsItsConnection(t *testing.T) {
	sc, inst := newTypePropConn(t, externalDataSourceResponse("", false))
	form, apply := loadPage(t, externalDataSourcePropPages(sc, propTypeDB, "HadoopCluster")[0], inst)

	if got := staticValue(t, form, "Location"); got != "hdfs://nn:8020" {
		t.Errorf("Location is %q", got)
	}
	if got := staticValue(t, form, "Credential"); got != "HadoopCred" {
		t.Errorf("Credential is %q", got)
	}
	if got := staticValue(t, form, "Database"); got != "(none)" {
		t.Errorf("Database is %q — a HADOOP source names none, and blank reads as a failed load", got)
	}
	if apply != nil {
		t.Error("the page has an apply, but CREATE EXTERNAL DATA SOURCE has no ALTER")
	}
}

// Below 2019 the two columns are absent from the catalog. The section has to
// be absent from the page too — not present and empty.
func TestExternalDataSourceHidesThe2019FieldsWhereTheyDoNotExist(t *testing.T) {
	sc, inst := newTypePropConn(t, externalDataSourceResponse("", false))
	form, _ := loadPage(t, externalDataSourcePropPages(sc, propTypeDB, "HadoopCluster")[0], inst)

	if hasStatic(form, "Pushdown enabled") {
		t.Error("the page shows Pushdown enabled on an instance whose catalog has no pushdown column")
	}
}

func TestExternalDataSourceShowsThe2019FieldsWhenSet(t *testing.T) {
	sc, inst := newTypePropConn(t, externalDataSourceResponse("driver='ODBC Driver 17'", true))
	form, _ := loadPage(t, externalDataSourcePropPages(sc, propTypeDB, "HadoopCluster")[0], inst)

	if got := staticValue(t, form, "Connection options"); got != "driver='ODBC Driver 17'" {
		t.Errorf("Connection options is %q", got)
	}
	if got := staticValue(t, form, "Pushdown enabled"); got != "True" {
		t.Errorf("Pushdown enabled is %q", got)
	}
	// hasStatic is what the two hiding tests above assert with; if it could
	// never find a row they would pass on a page that shows everything.
	if !hasStatic(form, "Pushdown enabled") {
		t.Error("hasStatic cannot see a row that is on the page")
	}
}

func externalFileFormatResponse(firstRow int64, parserVersion string) fakeResponse {
	return fakeResponse{match: "FROM   sys.external_file_formats f", cols: 13, rows: [][]driver.Value{{
		int64(65536), "CsvFormat", "DELIMITEDTEXT",
		",", "\"", "", false, "", "\\n", "UTF8", "", firstRow, parserVersion,
	}}}
}

func TestExternalFileFormatGeneralShowsItsTextOptions(t *testing.T) {
	sc, inst := newTypePropConn(t, externalFileFormatResponse(0, ""))
	form, apply := loadPage(t, externalFileFormatPropPages(sc, propTypeDB, "CsvFormat")[0], inst)

	if got := staticValue(t, form, "Format type"); got != "DELIMITEDTEXT" {
		t.Errorf("Format type is %q", got)
	}
	if got := staticValue(t, form, "Field terminator"); got != "," {
		t.Errorf("Field terminator is %q", got)
	}
	// Microsoft documents first_row as 2017; a 14.0.2130.4 instance has
	// neither it nor parser_version — measured on the instance, which is why
	// the page gates on the value rather than on the major.
	if hasStatic(form, "First row") {
		t.Error("the page shows First row on an instance whose catalog has no first_row column")
	}
	if apply != nil {
		t.Error("the page has an apply, but CREATE EXTERNAL FILE FORMAT has no ALTER")
	}
}

func TestExternalFileFormatShowsParsingWhereItExists(t *testing.T) {
	sc, inst := newTypePropConn(t, externalFileFormatResponse(2, "2.0"))
	form, _ := loadPage(t, externalFileFormatPropPages(sc, propTypeDB, "CsvFormat")[0], inst)

	if got := staticValue(t, form, "First row"); got != "2" {
		t.Errorf("First row is %q", got)
	}
	if got := staticValue(t, form, "Parser version"); got != "2.0" {
		t.Errorf("Parser version is %q", got)
	}
}

// sys.external_libraries has no platform column on any major that has the
// view, whatever the documentation says — so the page must not claim one.
func TestExternalLibraryGeneralShowsItsRuntime(t *testing.T) {
	sc, inst := newTypePropConn(t, fakeResponse{
		match: "FROM   sys.external_libraries l", cols: 5,
		rows: [][]driver.Value{{int64(1), "ggplot2", "dbo", "R", "PUBLIC"}},
	})
	form, apply := loadPage(t, externalLibraryPropPages(sc, propTypeDB, "ggplot2")[0], inst)

	if got := staticValue(t, form, "Language"); got != "R" {
		t.Errorf("Language is %q", got)
	}
	if got := staticValue(t, form, "Scope"); got != "PUBLIC" {
		t.Errorf("Scope is %q", got)
	}
	if hasStatic(form, "Platform") {
		t.Error("the page shows a Platform the catalog does not record")
	}
	if apply != nil {
		t.Error("the page has an apply, but ALTER EXTERNAL LIBRARY needs the new package binary")
	}
}
