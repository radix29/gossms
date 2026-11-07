package cmd

import (
	"fmt"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/cli/output"
)

// TestScriptErrorHintNamesTheOperation: the same refused
// sql_expression_dependencies read is a DROP's schema-bound read for script
// object/db, but for script db --objects it is not a DROP at all, so that
// hint must not appear there.
func TestScriptErrorHintNamesTheOperation(t *testing.T) {
	denied := fmt.Errorf("gosmo: read: %w", mssql.Error{Number: 229, Class: 14,
		Message: "The SELECT permission was denied on the object 'sql_expression_dependencies'"})

	e := scriptError(denied, "[app]", false)
	if e.Code != output.CodePermissionDenied || !strings.Contains(e.Hint, "Scripting a DROP of [app]") {
		t.Errorf("single script: %s, hint %q", e.Code, e.Hint)
	}
	e = scriptError(denied, "[app]", true)
	if e.Code != output.CodePermissionDenied || strings.Contains(e.Hint, "DROP") || strings.Contains(e.Hint, "its definition") ||
		!strings.Contains(e.Hint, "the message names the object") {
		t.Errorf("objects script: %s, hint %q", e.Code, e.Hint)
	}
}
