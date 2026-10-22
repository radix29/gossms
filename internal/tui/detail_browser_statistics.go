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
		cols, rows := leafIdentityDetails(node)
		return cols, rows, nil
	}

	n := node.data
	d := sc.Server.DatabaseRef(n.DBName)
	var st *gosmo.Statistic
	if n.OnView {
		st = d.ViewRef(n.Schema, n.TableName).StatisticRef(n.Name)
	} else {
		st = d.TableRef(n.Schema, n.TableName).StatisticRef(n.Name)
	}
	hdr, err := st.Header(ctx)
	if err != nil {
		return nil, nil, err
	}

	samplingRate := "n/a"
	if hdr.Rows > 0 {
		samplingRate = fmt.Sprintf("%.1f %%", float64(hdr.RowsSampled)/float64(hdr.Rows)*100)
	}
	cols, rows := leafIdentityDetails(node)
	rows = append(rows,
		[]string{"Object", fqn(n.Schema, n.TableName)},
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
