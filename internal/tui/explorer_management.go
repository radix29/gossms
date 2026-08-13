package tui

import (
	"fmt"

	gosmo "github.com/radix29/gosmo"
)

// loadServerObjectsChildren returns the Server Objects folder's children:
// Linked Servers. SQL Server Agent lives one level up, as a sibling of
// Server Objects itself — see loadServerChildren. SQL Server Agent's own
// loaders live in agent_explorer.go.
func loadServerObjectsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Linked Servers", NodeLinkedServers, "", "", ""),
	}, nil
}

// loadManagementChildren returns the Management folder's children: SQL
// Server Logs. SSMS hangs a good deal more here (Policy Management, Data
// Collection, Maintenance Plans, Database Mail, …); this folder exists for
// the parts goSSMS implements.
func loadManagementChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("SQL Server Logs", NodeSQLServerLogs, "", "", ""),
	}, nil
}

// loadSQLServerLogsChildren lists the instance's error log files, current
// first — one leaf per file, each opening the log viewer on it.
func loadSQLServerLogsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return loadErrorLogChildren(l, gosmo.ErrorLogSQLServer, NodeSQLServerLog)
}

// loadAgentErrorLogsChildren is loadSQLServerLogsChildren for the Agent's
// own error logs — SQLAGENT.OUT and its archives.
func loadAgentErrorLogsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return loadErrorLogChildren(l, gosmo.ErrorLogAgent, NodeAgentErrorLog)
}

// loadErrorLogChildren builds one leaf per log file of the given family,
// labelled the way SSMS's Log File Viewer names them ("Current — <date>",
// "Archive #1 — <date>").
func loadErrorLogChildren(l loaderCtx, logType gosmo.ErrorLogType, leaf NodeType) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.ErrorLogFile, error) {
		return l.sc.Server.EnumErrorLogsContext(l.ctx, logType)
	}, func(f *gosmo.ErrorLogFile) *explorerNode {
		n := l.node(errorLogFileLabel(f), leaf, "", errorLogFileLabel(f), "")
		n.data.LogType = logType
		n.data.LogNumber = f.Number
		return n
	})
}

// errorLogFileLabel names one log file for the tree and the viewer's file
// selector. The date comes from LastWritten when it parsed and from the
// server's own string when it didn't, so an unrecognized locale format
// still shows something rather than a zero date.
func errorLogFileLabel(f *gosmo.ErrorLogFile) string {
	name := fmt.Sprintf("Archive #%d", f.Number)
	if f.Number == 0 {
		name = "Current"
	}
	when := f.Date
	if !f.LastWritten.IsZero() {
		// Minute precision, because that is all sp_enumerrorlogs reports —
		// formatSQLDate's ":00" seconds would be invented.
		when = f.LastWritten.Format("2006-01-02 15:04")
	}
	if when == "" {
		return name
	}
	return name + " — " + when
}

func loadLinkedServersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.LinkedServer, error) { return l.sc.Server.LinkedServersContext(l.ctx) },
		func(ls *gosmo.LinkedServer) *explorerNode {
			return l.node(ls.Name, NodeLinkedServer, "", ls.Name, "")
		})
}
