package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_fulltext.go is the Detail Browser's view of a database's three
// full-text folders (explorer_fulltext.go) and their leaves: the catalogs with
// their population status, one catalog's indexed tables, the stoplists and one
// stoplist's words, the search property lists and one list's properties.
//
// Each leaf reuses the finder its Properties page uses (fulltext_props.go), and
// each folder applies the folder's filter the way the tree does.

func fullTextCatalogsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	d, err := sc.Server.DatabaseByName(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	cats, err := d.FullTextCatalogs(ctx)
	if err != nil {
		return nil, nil, err
	}
	cats = filterObjects(node.data.Filter, cats, func(c *gosmo.FullTextCatalog) nodeData {
		return nodeData{Name: c.Name}
	})
	rows := make([][]string, 0, len(cats))
	for _, c := range cats {
		rows = append(rows, []string{c.Name, boolStr(c.IsDefault), boolStr(c.AccentSensitive), c.Owner,
			strconv.Itoa(c.IndexCount), strconv.Itoa(c.ItemCount), strconv.Itoa(c.SizeMB),
			c.PopulateStatus.String(), fullTextDate(c.LastPopulated)})
		*objs = append(*objs, nodeData{Type: NodeFullTextCatalog, DBName: node.data.DBName, Name: c.Name})
	}
	return []string{"Name", "Default", "Accent Sensitive", "Owner", "Indexes", "Items", "Size (MB)",
		"Population Status", "Last Populated"}, rows, nil
}

// fullTextCatalogDetail is one catalog's tables: each full-text index it
// holds, with its columns and counters.
func fullTextCatalogDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	c, err := findFullTextCatalog(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	idx, err := c.Indexes(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(idx))
	for _, i := range idx {
		rows = append(rows, []string{articleObject(i.Schema, i.Table), i.KeyIndex, fullTextColumnNames(i), boolStr(i.IsEnabled),
			string(i.ChangeTracking), fullTextStoplistText(i), strconv.Itoa(i.ItemCount),
			strconv.Itoa(i.PendingChanges), i.PopulateStatus.String()})
	}
	return []string{"Table/View", "Unique Index", "Columns", "Enabled", "Change Tracking", "Stoplist",
		"Items", "Pending Changes", "Population Status"}, rows, nil
}

func fullTextStoplistsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	d, err := sc.Server.DatabaseByName(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	lists, err := d.FullTextStoplists(ctx)
	if err != nil {
		return nil, nil, err
	}
	lists = filterObjects(node.data.Filter, lists, func(l *gosmo.FullTextStoplist) nodeData {
		return nodeData{Name: l.Name}
	})
	rows := make([][]string, 0, len(lists))
	for _, l := range lists {
		rows = append(rows, []string{l.Name, l.Owner, formatSQLDate(l.CreateDate), formatSQLDate(l.ModifyDate)})
		*objs = append(*objs, nodeData{Type: NodeFullTextStoplist, DBName: node.data.DBName, Name: l.Name})
	}
	return []string{"Name", "Owner", "Created", "Modified"}, rows, nil
}

func fullTextStoplistDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	l, err := findFullTextStoplist(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	words, err := l.Stopwords(ctx)
	if err != nil {
		return nil, nil, err
	}
	return stopwordHeaders, stopwordRows(words), nil
}

func searchPropertyListsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	d, err := sc.Server.DatabaseByName(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	lists, err := d.SearchPropertyLists(ctx)
	if err != nil {
		return nil, nil, err
	}
	lists = filterObjects(node.data.Filter, lists, func(p *gosmo.SearchPropertyList) nodeData {
		return nodeData{Name: p.Name}
	})
	rows := make([][]string, 0, len(lists))
	for _, p := range lists {
		rows = append(rows, []string{p.Name, p.Owner, formatSQLDate(p.CreateDate), formatSQLDate(p.ModifyDate)})
		*objs = append(*objs, nodeData{Type: NodeSearchPropertyList, DBName: node.data.DBName, Name: p.Name})
	}
	return []string{"Name", "Owner", "Created", "Modified"}, rows, nil
}

func searchPropertyListDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	p, err := findSearchPropertyList(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	props, err := p.Properties(ctx)
	if err != nil {
		return nil, nil, err
	}
	return searchPropertyHeaders, searchPropertyRows(props), nil
}
