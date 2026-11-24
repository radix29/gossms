package tui

import (
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// explorerFeatures describes engine support, not login permissions or runtime
// state. It is local to Object Explorer, its Details folders and its menus.
type explorerFeatures uint64

const (
	oeBackupDevices explorerFeatures = 1 << iota
	oeEndpoints
	oeLinkedServers
	oeServerTriggers
	oeAgent
	oeReplication
	oeAlwaysOn
	oeResourceGovernor
	oeServerXEvents
	oeDatabaseXEvents
	oeServerLogs
	oeDatabaseMail
	oeServerCredentials
	oeCryptographicProviders
	oeServerAudits
	oeQueryStore
	oeServiceBroker
	oeStorage
	oeExternalFileFormats
	oeExternalLibraries
	oeDatabaseTriggers
	oeCLR
	oeTypes
	oeRules
	oeDefaults
	oePlanGuides
	oeSequences
	oeSynonyms
	oeExternalFunctions
	oeAsymmetricKeys
	oeCertificates
	oeSymmetricKeys
	oeDatabaseAudits
	oeDatabaseCredentials
	oeSecurityPolicies
	oeAlwaysEncrypted
	oeSystemTables
	oeFileTables
	oeExternalTables
	oeGraphTables
	oeCheckConstraints
	oeTableTriggers
	oeTableIndexes
	oeViewTriggers
	oeIndexedViews
	oeFullText
	oeBackup
	oeRestore
	oeBackupHistory
	oeDatabaseRename
	oeDatabaseDrop
	oeCreateUser
	oeShrink
	oeActivityMonitor
	oeCreateServerRole
	oeFeatureEnd
)

const (
	oeServerObjectFeatures = oeBackupDevices | oeEndpoints | oeLinkedServers | oeServerTriggers
	oeManagementFeatures   = oeResourceGovernor | oeServerXEvents | oeServerLogs | oeDatabaseMail
	oeExternalResources    = oeExternalFileFormats | oeExternalLibraries

	oeInstanceFeatures = oeServerObjectFeatures | oeManagementFeatures | oeAgent |
		oeReplication | oeAlwaysOn | oeServerCredentials | oeCryptographicProviders |
		oeServerAudits | oeBackup | oeRestore | oeBackupHistory | oeActivityMonitor
)

func (f explorerFeatures) has(required explorerFeatures) bool { return f&required == required }

// explorerSupport uses cached server metadata only. In particular, Azure SQL
// Database is identified by EngineEdition, never by its hostname or DSN.
// Unknown metadata retains the existing fail-open folder/menu policy.
func explorerSupport(sc *db.ServerConn) explorerFeatures {
	f := (oeFeatureEnd - 1) &^ (oeExternalFunctions | oeDatabaseXEvents)
	var info *gosmo.ServerInfo
	if sc != nil && sc.Server != nil {
		info = sc.Server.Info()
	}
	if sc.IsFabricDW() {
		f &^= oeInstanceFeatures | oeQueryStore | oeServiceBroker | oeStorage |
			oeExternalResources | oeDatabaseTriggers | oeCLR | oeTypes | oeRules |
			oeDefaults | oePlanGuides | oeSequences | oeSynonyms | oeAsymmetricKeys |
			oeCertificates | oeSymmetricKeys | oeDatabaseAudits | oeDatabaseCredentials |
			oeSecurityPolicies | oeAlwaysEncrypted | oeSystemTables | oeFileTables |
			oeExternalTables | oeGraphTables | oeCheckConstraints | oeTableTriggers |
			oeTableIndexes | oeViewTriggers | oeIndexedViews | oeFullText | oeDatabaseRename |
			oeDatabaseDrop | oeCreateUser | oeShrink
		f |= oeExternalFunctions
	}
	if info.IsDatabaseScoped() {
		f &^= oeResourceGovernor | oeDatabaseMail | oeReplication | oeServerXEvents
		f |= oeDatabaseXEvents
	}
	if info != nil && info.EngineEdition == gosmo.EngineAzureSQLDatabase {
		// Azure SQL retains ordinary database objects (including CHECK/DML/DDL
		// triggers), but has no CLR, FileTables, ML libraries or SQL Server Audit.
		f &^= oeInstanceFeatures | oeCreateServerRole | oeServiceBroker | oeCLR |
			oeFileTables | oeExternalLibraries | oeDatabaseAudits
	}
	if !info.AtLeast(gosmo.SQLServer2017) {
		f &^= oeGraphTables | oeExternalLibraries
	}
	return f
}

type explorerFolder struct {
	label    string
	typ      NodeType
	requires explorerFeatures
}

func (l loaderCtx) folders(dbName string, specs ...explorerFolder) []*explorerNode {
	features := explorerSupport(l.sc)
	out := make([]*explorerNode, 0, len(specs))
	for _, spec := range specs {
		if features.has(spec.requires) {
			out = append(out, l.node(spec.label, spec.typ, "", "", dbName))
		}
	}
	return out
}
