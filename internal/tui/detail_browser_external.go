package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_external.go is the Detail Browser's view of the External
// Resources families — external data sources, file formats and libraries.
// Its own file for the reason explorer_external.go is: these three are
// PolyBase and Machine Learning Services objects, a sibling of Views rather
// than anything under Programmability.
//
// Like every other detail view here, each leaf reuses the finder its
// Properties page uses.

// externalFolderDetail lists one of the three External Resources folders.
func externalFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	n := node.data
	d, err := sc.Server.DatabaseByNameContext(ctx, n.DBName)
	if err != nil {
		return nil, nil, err
	}

	switch n.Type {
	case NodeExternalDataSources:
		srcs, err := d.ExternalDataSourcesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		srcs = filterObjects(n.Filter, srcs, func(s *gosmo.ExternalDataSource) nodeData {
			return nodeData{Name: s.Name}
		})
		rows := make([][]string, 0, len(srcs))
		for _, s := range srcs {
			rows = append(rows, []string{s.Name, s.Type, s.Location, s.Credential, s.DatabaseName})
			*objs = append(*objs, nodeData{Type: NodeExternalDataSource, DBName: n.DBName, Name: s.Name})
		}
		return []string{"Name", "Type", "Location", "Credential", "Database"}, rows, nil

	case NodeExternalFileFormats:
		fmts, err := d.ExternalFileFormatsContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		fmts = filterObjects(n.Filter, fmts, func(f *gosmo.ExternalFileFormat) nodeData {
			return nodeData{Name: f.Name}
		})
		rows := make([][]string, 0, len(fmts))
		for _, f := range fmts {
			rows = append(rows, []string{f.Name, f.FormatType, f.FieldTerminator, f.Encoding, f.DataCompression})
			*objs = append(*objs, nodeData{Type: NodeExternalFileFormat, DBName: n.DBName, Name: f.Name})
		}
		return []string{"Name", "Format", "Field Terminator", "Encoding", "Compression"}, rows, nil

	default: // NodeExternalLibraries
		libs, err := d.ExternalLibrariesContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		libs = filterObjects(n.Filter, libs, func(l *gosmo.ExternalLibrary) nodeData {
			return nodeData{Name: l.Name}
		})
		rows := make([][]string, 0, len(libs))
		for _, l := range libs {
			rows = append(rows, []string{l.Name, l.Language, l.Scope, l.Owner})
			*objs = append(*objs, nodeData{Type: NodeExternalLibrary, DBName: n.DBName, Name: l.Name})
		}
		return []string{"Name", "Language", "Scope", "Owner"}, rows, nil
	}
}

// externalDetail is the Property/Value view of one external resource leaf.
func externalDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	n := node.data
	switch n.Type {
	case NodeExternalDataSource:
		s, err := findExternalDataSource(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		// ConnectionOptions and PushdownEnabled are 2019+; gosmo reads them
		// as empty and false on an older instance rather than failing, so
		// they are shown unconditionally and read as "not set" there.
		return propertyRows(
			"Name", s.Name,
			"Type", s.Type,
			"Location", s.Location,
			"Credential", s.Credential,
			"Resource manager", s.ResourceManagerLocation,
			"Database", s.DatabaseName,
			"Shard map", s.ShardMapName,
			"Connection options", s.ConnectionOptions,
			"Pushdown enabled", boolStr(s.PushdownEnabled),
		)

	case NodeExternalFileFormat:
		f, err := findExternalFileFormat(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", f.Name},
			{"Format type", f.FormatType},
			{"Field terminator", f.FieldTerminator},
			{"String delimiter", f.StringDelimiter},
			{"Row terminator", f.RowTerminator},
			{"Date format", f.DateFormat},
			{"Use type default", boolStr(f.UseTypeDefault)},
			{"Encoding", f.Encoding},
			{"Data compression", f.DataCompression},
			{"SerDe method", f.SerDeMethod},
		}
		// FirstRow and ParserVersion are 2019+ and read as zero/empty below
		// it — shown only when the instance actually has them, since a
		// "First row 0" line on a 2017 instance describes the read, not the
		// format.
		if f.FirstRow > 0 {
			rows = append(rows, []string{"First row", strconv.Itoa(f.FirstRow)})
		}
		if f.ParserVersion != "" {
			rows = append(rows, []string{"Parser version", f.ParserVersion})
		}
		return propertyValueColumns, rows, nil

	default: // NodeExternalLibrary
		l, err := findExternalLibrary(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", l.Name,
			"Language", l.Language,
			"Scope", l.Scope,
			"Owner", l.Owner,
		)
	}
}
