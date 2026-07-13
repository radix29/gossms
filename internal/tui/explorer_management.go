package tui

import gosmo "github.com/radix29/gosmo"

// loadManagementChildren returns the Server Objects folder's children:
// SQL Server Agent (jobs) and Linked Servers.
func loadManagementChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("SQL Server Agent", NodeAgentJobs, "", "", ""),
		l.node("Linked Servers", NodeLinkedServers, "", "", ""),
	}, nil
}

func loadAgentJobsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Job, error) { return l.sc.Server.JobsContext(l.ctx) },
		func(j *gosmo.Job) *explorerNode {
			return l.node(j.Name, NodeAgentJob, "", j.Name, "")
		})
}

func loadLinkedServersChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.LinkedServer, error) { return l.sc.Server.LinkedServersContext(l.ctx) },
		func(ls *gosmo.LinkedServer) *explorerNode {
			return l.node(ls.Name, NodeLinkedServer, "", ls.Name, "")
		})
}
