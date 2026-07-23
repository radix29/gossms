package tui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// boolStr renders a bool as "True"/"False", the Static-row convention used
// throughout the Properties pages.
func boolStr(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// engineEditionNames maps SERVERPROPERTY('EngineEdition') (gosmo's
// ServerInfo.EngineEdition) to the name SSMS's General page shows.
var engineEditionNames = map[int]string{
	1:  "Personal/Desktop Engine",
	2:  "Standard",
	3:  "Enterprise",
	4:  "Express",
	5:  "SQL Database",
	6:  "SQL Data Warehouse",
	8:  "Managed Instance",
	9:  "SQL Edge",
	11: "Azure Synapse serverless SQL pool",
}

// engineEditionName renders an EngineEdition code as SSMS would, falling
// back to the raw number for a code this build doesn't recognize yet.
func engineEditionName(code int) string {
	if name, ok := engineEditionNames[code]; ok {
		return name
	}
	return strconv.Itoa(code)
}

// boolIdx maps a bool onto a two-item Select/Radio row's index — 1 for
// true, 0 for false — matching the [off, on] / [enabled, disabled] item
// ordering every such row on these pages uses.
func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

// indexOf returns the index of value within items, or 0 (the first item)
// if it isn't present — the fallback a Select/Radio row's "selected" index
// needs when the server reports a value outside the row's known options.
func indexOf(items []string, value string) int {
	for i, it := range items {
		if it == value {
			return i
		}
	}
	return 0
}

// orDefault returns s, or def if s is empty — for server fields that come
// back blank when unset but need a concrete default for indexOf/Select.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// credNames extracts the Name field of every credential, for building a
// Select row's item list.
func credNames(creds []*gosmo.Credential) []string {
	names := make([]string, len(creds))
	for i, c := range creds {
		names[i] = c.Name
	}
	return names
}

// buildFilterInfoForm builds the read-only Filter page shared by Index and
// Statistics Properties: SQL Server only accepts a filtered predicate at
// CREATE time, so on an existing index/statistic it's shown read-only here
// rather than editable, with Check Syntax/Estimate Rows running the real
// predicate against the table live via t's own CheckWhereSyntax/CountWhere.
func buildFilterInfoForm(d *PropDialog, t *gosmo.Table, hasFilter bool, filterDef string) *propsheet.Form {
	statusRow := propsheet.Static("Status", "Not checked")
	rowsRow := propsheet.Static("Estimated qualifying rows", "")

	checkBtn := d.asyncStatusButton("Check Syntax", statusRow, "Checking...", func(ctx context.Context) (string, error) {
		if filterDef == "" {
			return "", fmt.Errorf("no filter expression to check")
		}
		if err := t.CheckWhereSyntaxContext(ctx, filterDef); err != nil {
			return "", err
		}
		return "Valid", nil
	})
	estimateBtn := d.asyncStatusButton("Estimate Rows", rowsRow, "Estimating...", func(ctx context.Context) (string, error) {
		if filterDef == "" {
			return "", fmt.Errorf("no filter expression to estimate")
		}
		n, err := t.CountWhereContext(ctx, filterDef)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(n, 10), nil
	})

	return propsheet.NewForm(
		propsheet.Section("Filtered predicate"),
		propsheet.Static("Filtered", boolStr(hasFilter)),
		propsheet.Section("Filter expression"),
		propsheet.Static("Expression", orDefault(filterDef, "(none)")),
		propsheet.Section("Validation"),
		statusRow, rowsRow,
		propsheet.Buttons(checkBtn, estimateBtn),
		propsheet.Note("The predicate can only be set when the index or statistic is created — use Script Changes, or DROP + CREATE, to change it. Check Syntax and Estimate Rows run the expression against the live table."),
	)
}
