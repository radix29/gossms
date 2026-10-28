package output

// Code is an error code: the name of a process exit code (docs/cli.md § Exit
// codes). A Result's first error decides the exit code.
type Code string

const (
	CodeOK               Code = "OK"
	CodeGeneralError     Code = "GENERAL_ERROR"
	CodeUsageError       Code = "USAGE_ERROR"
	CodeConnectionFailed Code = "CONNECTION_FAILED"
	CodeAuthFailed       Code = "AUTH_FAILED"
	CodeSQLError         Code = "SQL_ERROR"
	CodeTimeout          Code = "TIMEOUT"
	CodeBlocked          Code = "BLOCKED"
	CodeNotFound         Code = "NOT_FOUND"
	CodePermissionDenied Code = "PERMISSION_DENIED"
	CodePartial          Code = "PARTIAL"
	CodeUnsupported      Code = "UNSUPPORTED"
	CodeCancelled        Code = "CANCELLED"
)

// ExitCode is one row of the exit-code table.
type ExitCode struct {
	Exit    int
	Code    Code
	Meaning string
}

// ExitCodes is every exit code, in number order. All are defined although
// today's commands produce only some (docs/cli.md § Exit codes): `agent
// schema` will list this table, and an agent's handling must not depend on
// which phase it met.
var ExitCodes = []ExitCode{
	{0, CodeOK, "Success (output may still be truncated; check truncated)"},
	{1, CodeGeneralError, "Unexpected or internal error"},
	{2, CodeUsageError, "Invalid or unknown arguments"},
	{3, CodeConnectionFailed, "Server unreachable, TLS or network error"},
	{4, CodeAuthFailed, "Login failed or credentials are missing"},
	{5, CodeSQLError, "The server returned an error (severity 11 or higher)"},
	{6, CodeTimeout, "Command or connect timeout"},
	{7, CodeBlocked, "Refused by the safety model or a missing --confirm"},
	{8, CodeNotFound, "Profile, database or object does not exist"},
	{9, CodePermissionDenied, "The login lacks the required permission"},
	{10, CodePartial, "Some batches succeeded and some failed"},
	{11, CodeUnsupported, "The server's version or edition does not support the operation"},
	{130, CodeCancelled, "Ctrl+C or SIGINT"},
}

// Exit is code's process exit code; 1 (GENERAL_ERROR) for a code not in the
// table.
func (c Code) Exit() int {
	for _, e := range ExitCodes {
		if e.Code == c {
			return e.Exit
		}
	}
	return 1
}

// Error is one entry of a Result's errors, and an error a handler can return
// to choose the code.
type Error struct {
	Code Code
	// SQL is the SQL Server error behind a SQL_ERROR (and the codes a SQL
	// error maps to); nil for every other error.
	SQL     *SQLError
	Message string
	Hint    string // the next thing to try; empty when there is none
}

// SQLError is a SQL Server error's structured fields, as the driver reports
// them.
type SQLError struct {
	Number    int32
	Severity  uint8
	State     uint8
	Line      int    // the script line; the module's own line inside a procedure
	Procedure string // empty outside a module
}

// NewError is an *Error for code.
func NewError(code Code, message, hint string) *Error {
	return &Error{Code: code, Message: message, Hint: hint}
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Exit is the process exit code for e.
func (e *Error) Exit() int { return e.Code.Exit() }
