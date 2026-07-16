// Package showplan parses SQL Server ShowPlanXML documents (execution
// plans, estimated or actual) into a navigable operator tree. It is a pure
// data package: no TUI or database dependencies. Input may be UTF-8 or
// UTF-16 (SSMS saves .sqlplan files as UTF-16LE with a BOM).
package showplan
