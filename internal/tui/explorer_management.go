package tui

import (
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// loadServerObjectsChildren returns the Server Objects folder's children:
// Backup Devices, Endpoints, Linked Servers and Triggers, in SSMS's order. SQL Server Agent lives
// one level up, as a sibling of Server Objects itself — see
// loadServerChildren. SQL Server Agent's own loaders live in
// agent_explorer.go.
func loadServerObjectsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Backup Devices", NodeBackupDevices, "", "", ""),
		l.node("Endpoints", NodeEndpoints, "", "", ""),
		l.node("Linked Servers", NodeLinkedServers, "", "", ""),
		l.node("Triggers", NodeServerTriggers, "", "", ""),
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

func loadBackupDevicesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.BackupDevice, error) { return l.sc.Server.BackupDevicesContext(l.ctx) },
		func(d *gosmo.BackupDevice) *explorerNode {
			return l.node(d.Name, NodeBackupDevice, "", d.Name, "")
		})
}

// loadServerTriggersChildren lists the server-scope DDL and logon triggers.
// These are a different family from a database's Triggers folder, which lists
// DML triggers on a table.
func loadServerTriggersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.ServerTrigger, error) { return l.sc.Server.ServerTriggersContext(l.ctx) },
		func(t *gosmo.ServerTrigger) *explorerNode {
			// A disabled DDL trigger is inert, and nothing else in the row
			// says so — the security policies folder labels its own the same
			// way.
			label := t.Name
			if !t.IsEnabled {
				label += " (Disabled)"
			}
			n := l.node(label, NodeServerTrigger, "", t.Name, "")
			n.data.IsEnabled = t.IsEnabled
			return n
		})
}

// loadEndpointsChildren lists every endpoint on the server, the five built-in
// ones included — SSMS shows them too, and an endpoint missing from the folder
// is one whose port nothing explains. A built-in endpoint's node is marked
// IsSystem, which is what withholds Rename and Delete.
func loadEndpointsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Endpoint, error) { return l.sc.Server.EndpointsContext(l.ctx) },
		func(e *gosmo.Endpoint) *explorerNode {
			// A stopped or disabled endpoint accepts nothing, and the icon is
			// the same either way — the same reason a disabled server trigger
			// says so in its label.
			label := e.Name
			if e.State != "" && e.State != "STARTED" {
				label += " (" + endpointStateLabel(e.State) + ")"
			}
			n := l.node(label, NodeEndpoint, "", e.Name, "")
			n.data.IsSystem = e.IsSystem
			n.data.IsEnabled = e.State == "STARTED"
			return n
		})
}

// endpointStateLabel renders a state_desc the way the tree and the Details
// pane name it — STARTED/STOPPED/DISABLED as Started/Stopped/Disabled.
func endpointStateLabel(state string) string {
	if state == "" {
		return ""
	}
	return strings.ToUpper(state[:1]) + strings.ToLower(state[1:])
}
