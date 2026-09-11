// Package showplan parses SQL Server ShowPlanXML (estimated or actual) into an
// operator tree. Pure data: no TUI or database dependencies. Input may be UTF-8
// or UTF-16 (SSMS saves .sqlplan as UTF-16LE with BOM).
package showplan
