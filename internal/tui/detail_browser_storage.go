package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_storage.go is the Detail Browser's view of the five
// families that have no editable Properties: partition functions and
// schemes, row-level security policies, and the two Always Encrypted keys.
// Each reuses the finder its Properties page uses, so the pane and the
// dialog can't disagree about which object a node names.

// storageSecurityDetail dispatches on node type; the caller has already
// narrowed it to one of the five.
func storageSecurityDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	n := node.data
	switch n.Type {
	case NodePartitionFunction:
		pf, err := findPartitionFunction(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		side := "LEFT"
		if pf.IsRight {
			side = "RIGHT"
		}
		return propertyRows(
			"Name", pf.Name,
			"Input type", string(pf.InputType),
			"Range", side,
			"Boundary values", strconv.Itoa(pf.BoundaryCount),
			"Partitions", strconv.Itoa(pf.BoundaryCount+1),
			"Boundaries", strings.Join(pf.Boundaries, ", "),
		)

	case NodePartitionScheme:
		ps, err := findPartitionScheme(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", ps.Name,
			"Partition function", ps.FunctionName,
			"Filegroups", strings.Join(ps.FileGroups, ", "),
		)

	case NodeSecurityPolicy:
		p, err := findSecurityPolicy(ctx, sc, n.DBName, n.Schema, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", fqn(p.Schema, p.Name)},
			{"Enabled", boolStr(p.IsEnabled)},
			{"Schema bound", boolStr(p.IsSchemaBound)},
			{"Not for replication", boolStr(p.IsNotForReplication)},
			{"Predicates", strconv.Itoa(len(p.Predicates))},
		}
		for i, pred := range p.Predicates {
			label := fmt.Sprintf("%s predicate %d", pred.PredicateType, i+1)
			value := fmt.Sprintf("%s ON %s", pred.PredicateDefinition, fqn(pred.TargetSchema, pred.TargetTable))
			if pred.Operation != "" {
				value += " " + pred.Operation
			}
			rows = append(rows, []string{label, value})
		}
		return []string{"Property", "Value"}, rows, nil

	case NodeColumnMasterKey:
		k, err := findColumnMasterKey(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		return propertyRows(
			"Name", k.Name,
			"Key store provider", k.KeyStoreProviderName,
			"Key path", k.KeyPath,
			"Allow enclave computations", boolStr(k.AllowEnclaveComputations),
		)

	default: // NodeColumnEncryptionKey
		k, err := findColumnEncryptionKey(ctx, sc, n.DBName, n.Name)
		if err != nil {
			return nil, nil, err
		}
		rows := [][]string{
			{"Name", k.Name},
			{"Encrypted values", strconv.Itoa(len(k.Values))},
		}
		for i, v := range k.Values {
			rows = append(rows, []string{fmt.Sprintf("Value %d", i+1),
				fmt.Sprintf("%s, %s, %s", v.MasterKeyName, v.EncryptionAlgorithm, hexPreview(v.EncryptedValue))})
		}
		return []string{"Property", "Value"}, rows, nil
	}
}

// propertyRows builds a Property/Value grid from alternating label/value
// arguments — the shape every leaf detail view here has.
func propertyRows(pairs ...string) ([]string, [][]string, error) {
	rows := make([][]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		rows = append(rows, []string{pairs[i], pairs[i+1]})
	}
	return []string{"Property", "Value"}, rows, nil
}
