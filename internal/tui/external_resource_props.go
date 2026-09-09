package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// external_resource_props.go is the read-only Properties for the three
// External Resources families: external data sources, external file formats
// and external libraries.
//
// None of the three writes. CREATE EXTERNAL DATA SOURCE and CREATE EXTERNAL
// FILE FORMAT have no ALTER at all on any supported major, and CREATE
// EXTERNAL LIBRARY's ALTER replaces the package *content* — an R or Python
// binary a form cannot supply, the same wall assembly_props.go hits. All
// pages here are named in prop_page_requires_test.go's pagesThatOnlyRead.
//
// Several fields below arrived in SQL Server 2019 and are simply absent from
// the catalog on 13 and 14, where gosmo reads them as empty or zero rather
// than failing — the pages say so rather than showing a blank that reads as
// an unset option. That gating was settled by reading the real catalog on 13,
// 14 and 17 rather than the documentation, which is wrong for first_row (see
// the note on the Parsing section below).

func findExternalDataSource(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.ExternalDataSource, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ExternalDataSourceByNameContext(ctx, name)
}

func findExternalFileFormat(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.ExternalFileFormat, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ExternalFileFormatByNameContext(ctx, name)
}

func findExternalLibrary(ctx context.Context, sc *db.ServerConn, dbName, name string) (*gosmo.ExternalLibrary, error) {
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	return d.ExternalLibraryByNameContext(ctx, name)
}

func externalDataSourcePropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			s, err := findExternalDataSource(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			// "External data source credential" is 31 columns and would clip
			// against propsheet.LabelWidth — the section heading above it
			// carries the "external data source" half instead.
			f := propsheet.NewForm(
				propsheet.Section("External data source"),
				propsheet.Static("Name", s.Name),
				propsheet.Static("Type", s.Type),
				propsheet.Static("Location", s.Location),
				propsheet.Section("Authentication"),
				propsheet.Static("Credential", boundOrNone(s.Credential)),
				propsheet.Section("Remote target"),
				propsheet.Static("Database", boundOrNone(s.DatabaseName)),
				propsheet.Static("Shard map", boundOrNone(s.ShardMapName)),
				propsheet.Static("Resource manager", boundOrNone(s.ResourceManagerLocation)),
			)
			// CONNECTION_OPTIONS and PUSHDOWN are 2019 columns, absent from
			// sys.external_data_sources on 13 and 14. Shown only where the
			// catalog has them: "Pushdown enabled: No" on a 2017 instance
			// would describe the read, not the source.
			if s.ConnectionOptions != "" || s.PushdownEnabled {
				f.Add(
					propsheet.Section("Connection"),
					propsheet.Static("Connection options", boundOrNone(s.ConnectionOptions)),
					propsheet.Static("Pushdown enabled", boolStr(s.PushdownEnabled)),
				)
			}
			f.Add(propsheet.Note("An external data source has no ALTER. Changing one means dropping it, which every external table built on it refuses, and creating it again."))
			return f, nil, nil
		},
	}}
}

func externalFileFormatPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			ff, err := findExternalFileFormat(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			f := propsheet.NewForm(
				propsheet.Section("External file format"),
				propsheet.Static("Name", ff.Name),
				propsheet.Static("Format type", ff.FormatType),
				propsheet.Section("Text options"),
				propsheet.Static("Field terminator", boundOrNone(ff.FieldTerminator)),
				propsheet.Static("String delimiter", boundOrNone(ff.StringDelimiter)),
				propsheet.Static("Row terminator", boundOrNone(ff.RowTerminator)),
				propsheet.Static("Date format", boundOrNone(ff.DateFormat)),
				propsheet.Static("Encoding", boundOrNone(ff.Encoding)),
				propsheet.Static("Use type default", boolStr(ff.UseTypeDefault)),
				propsheet.Section("Storage"),
				propsheet.Static("Data compression", boundOrNone(ff.DataCompression)),
				propsheet.Static("SerDe method", boundOrNone(ff.SerDeMethod)),
			)
			// FIRST_ROW and PARSER_VERSION are 2019 columns. Microsoft
			// documents FIRST_ROW as 2017 and a 14.0.2130.4 instance has
			// neither — measured, not taken from the documentation.
			if ff.FirstRow > 0 || ff.ParserVersion != "" {
				f.Add(
					propsheet.Section("Parsing"),
					propsheet.Static("First row", strconv.Itoa(ff.FirstRow)),
					propsheet.Static("Parser version", boundOrNone(ff.ParserVersion)),
				)
			}
			f.Add(propsheet.Note("An external file format has no ALTER. Every external table declaring it holds it in place until they are dropped."))
			return f, nil, nil
		},
	}}
}

func externalLibraryPropPages(sc *db.ServerConn, dbName, name string) []propPage {
	return []propPage{{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			l, err := findExternalLibrary(ctx, sc, dbName, name)
			if err != nil {
				return nil, nil, err
			}
			// No platform: sys.external_libraries has no platform column on
			// any major that has the view, whatever the documentation says.
			f := propsheet.NewForm(
				propsheet.Section("External library"),
				propsheet.Static("Name", l.Name),
				propsheet.Static("Owner", boundOrNone(l.Owner)),
				propsheet.Section("Runtime"),
				propsheet.Static("Language", l.Language),
				propsheet.Static("Scope", l.Scope),
				propsheet.Note("PUBLIC libraries load for every user; a PRIVATE one loads only for its owner. Replacing the package is an ALTER EXTERNAL LIBRARY, which needs the new R or Python binary."),
			)
			return f, nil, nil
		},
	}}
}
