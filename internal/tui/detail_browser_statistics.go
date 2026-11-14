package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

func statisticDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	if !sc.IsFabricDW {
		return []string{"Property", "Value"}, basicDetailRows(node), nil
	}

	n := node.data
	d := sc.Server.DatabaseRef(n.DBName)
	var st *gosmo.Statistic
	if n.OnView {
		st = d.ViewRef(n.Schema, n.TableName).StatisticRef(n.Name)
	} else {
		st = d.TableRef(n.Schema, n.TableName).StatisticRef(n.Name)
	}
	cols, rows := []string{"Property", "Value"}, basicDetailRows(node)
	rows = append(rows, []string{"Object", gosmo.QualifiedName(n.Schema, n.TableName)})
	hdr, err := st.Header(ctx)
	if err != nil {
		if se, ok := gosmo.AsSQLError(err); ok && se.Number == 16542 && n.Name == "ACE-Cardinality" {
			rows = append(rows, []string{"Note",
				"Fabric Warehouse does not expose a statistics header for ACE-Cardinality (error 16542)."})
			return cols, rows, nil
		}
		return nil, nil, err
	}

	samplingRate := "n/a"
	if hdr.Rows > 0 {
		samplingRate = fmt.Sprintf("%.1f %%", float64(hdr.RowsSampled)/float64(hdr.Rows)*100)
	}
	rows = append(rows,
		[]string{"Last updated", orDefault(hdr.Updated, "Never")},
		[]string{"Rows", strconv.FormatInt(hdr.Rows, 10)},
		[]string{"Rows sampled", strconv.FormatInt(hdr.RowsSampled, 10)},
		[]string{"Sampling rate", samplingRate},
		[]string{"Steps", strconv.Itoa(hdr.Steps)},
		[]string{"Density", fmt.Sprintf("%.8f", hdr.Density)},
		[]string{"Average key length", fmt.Sprintf("%.1f", hdr.AverageKeyLength)},
		[]string{"String index", orDefault(hdr.StringIndex, "n/a")},
		[]string{"Filter expression", orDefault(hdr.FilterExpression, "n/a")},
		[]string{"Unfiltered rows", strconv.FormatInt(hdr.UnfilteredRows, 10)},
	)
	return cols, rows, nil
}
