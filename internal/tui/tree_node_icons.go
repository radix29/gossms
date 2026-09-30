package tui

import "github.com/radix29/gossms/internal/config"

// tree_node_icons.go is the Object Explorer's glyphs: nodeIcon, which picks
// one for a node from its type and state, and the per-style tables behind it
// (Emoji, Symbols, Portable). isContainerNode, which decides folder or
// object, stays with the NodeType enum in tree_node.go.

// nodeIcon returns the icon glyph for a node, in the given icon style.
// expanded only affects container ("folder") node types, which show a
// different glyph open vs. closed. style == config.IconStyleNone always
// returns 0 (no icon), which TreeView's Draw treats as "don't draw one".
// NodeAgentJobs (the "SQL Server Agent" node) gets a fixed stopwatch glyph
// rather than the generic folder icon its container status would give it.
// d.IsPrimaryKey overrides the normal NodeColumn glyph with the primary-key
// glyph, since that's per-column data, not something Type alone expresses.
func nodeIcon(d nodeData, style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleNone {
		return 0
	}
	if d.Type == NodeAgentJobs {
		return '⏱'
	}
	if isContainerNode(d.Type) {
		return folderIcon(style, expanded)
	}
	if d.Type == NodeColumn && d.IsPrimaryKey {
		return primaryKeyIcon(style)
	}
	if d.Type == NodeDatabase && d.IsOffline {
		return offlineDatabaseIcon(style)
	}
	if d.Type == NodeEventSession && !d.IsEnabled {
		return stoppedEventSessionIcon(style)
	}
	return objectIcon(d.Type, style)
}

// stoppedEventSessionIcon returns the glyph substituted for a NodeEventSession
// that is not running — the hollow twin of the running one, the same idiom as
// offlineDatabaseIcon. The label says nothing about the state, as SSMS's
// doesn't: a session list is mostly stopped sessions, and a suffix on each
// would crowd out the names.
func stoppedEventSessionIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '⏹'
	}
	return '✧'
}

// offlineDatabaseIcon returns the glyph substituted for a NodeDatabase
// that's currently offline — a hollow hexagon in the geometric styles
// (vs. the filled '⬢' an online database uses), a "powered off" glyph
// for Emoji.
func offlineDatabaseIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '📴'
	}
	return '⬡'
}

// primaryKeyIcon returns the glyph substituted for a primary-key column's
// normal NodeColumn icon — the 🗝/⚿ "Primary Key" glyph, shared with NodeKey
// (the same glyph a Keys-folder primary/unique key entry uses).
func primaryKeyIcon(style config.IconStyle) rune {
	if style == config.IconStyleEmoji {
		return '🗝'
	}
	return '⚿'
}

// folderIcon returns the container glyph for a style, open vs. closed.
func folderIcon(style config.IconStyle, expanded bool) rune {
	if style == config.IconStyleEmoji {
		if expanded {
			return '📂'
		}
		return '📁'
	}
	// Symbols and Portable share the same geometric folder glyphs.
	if expanded {
		return '▾'
	}
	return '▸'
}

// objectIcon returns the glyph for a concrete (non-container) node type.
func objectIcon(t NodeType, style config.IconStyle) rune {
	switch style {
	case config.IconStyleEmoji:
		return objectIconEmoji(t)
	case config.IconStylePortable:
		return objectIconPortable(t)
	default: // Symbols
		return objectIconSymbols(t)
	}
}

func objectIconEmoji(t NodeType) rune {
	switch t {
	case NodeServer:
		return '🖥'
	case NodeDatabase:
		return '🛢'
	case NodeDatabaseSnapshot:
		return '📸'
	case NodeTable:
		return '▤'
	case NodeColumn:
		return '🏷'
	case NodeIndex:
		return '📇'
	case NodeKey:
		return '🗝'
	case NodeStatistic:
		return '📊'
	case NodeView:
		return '👁'
	case NodeStoredProcedure:
		return '⚙'
	case NodeFunction:
		return 'ƒ'
	case NodeLogin:
		return '🔐'
	case NodeUser:
		return '👤'
	case NodeServerRole, NodeDatabaseRole:
		return '🎭'
	case NodeCredential, NodeDatabaseScopedCredential:
		return '🪪'
	case NodeCryptographicProvider:
		return '🔒'
	case NodeAudit:
		return '📋'
	case NodeServerAuditSpecification, NodeDatabaseAuditSpecification:
		return '📑'
	case NodeBackupDevice:
		return '💾'
	case NodeEndpoint:
		return '🔌'
	case NodeAgentJob:
		return '⏱'
	case NodeAgentSchedule:
		return '📅'
	case NodeAgentAlert:
		return '🔔'
	case NodeAgentOperator:
		return '📞'
	case NodeAgentJobActivity:
		return '📈'
	case NodeAgentJobHistory:
		return '🕒'
	case NodeAgentJobCategories, NodeAgentAlertCategories:
		return '🗂'
	case NodeAgentReport, NodeQueryStoreReport:
		return '📋'
	case NodeSQLServerLog, NodeAgentErrorLog:
		return '📄'
	case NodeLinkedServer:
		return '🔗'
	case NodeAvailabilityGroup:
		return '🔄'
	case NodeAvailabilityReplica:
		return '🖧'
	case NodeAvailabilityDatabase:
		return '🛢'
	case NodeAGListener:
		return '📡'
	case NodeTrigger, NodeServerTrigger, NodeDatabaseTrigger:
		return '⚡'
	case NodeSequence:
		return '🔢'
	case NodeSynonym:
		return '🔖'
	case NodeSystemDataType, NodeUserDefinedDataType:
		return '🔡'
	case NodeUserDefinedTableType:
		return '🧾'
	case NodeUserDefinedType:
		return '🧬'
	case NodeXMLSchemaCollection:
		return '📜'
	case NodeAssembly:
		return '📦'
	case NodeRule:
		return '📏'
	case NodeDefault:
		return '🧷'
	case NodePlanGuide:
		return '🧭'
	case NodeMessageType:
		return '✉'
	case NodeContract:
		return '🤝'
	case NodeBrokerQueue:
		return '📥'
	case NodeBrokerService:
		return '🛎'
	case NodeRoute:
		return '🛣'
	case NodeRemoteServiceBinding:
		return '🪢'
	case NodeBrokerPriority:
		return '🎚'
	case NodeExternalDataSource:
		return '🌐'
	case NodeExternalFileFormat:
		return '📃'
	case NodeExternalLibrary:
		return '📚'
	case NodeForeignKey:
		return '🔗'
	case NodeCheck:
		return '✔'
	case NodeSchema:
		return '🧩'
	case NodePartitionFunction:
		return '📐'
	case NodePartitionScheme:
		return '🗄'
	case NodeSecurityPolicy:
		return '🛡'
	case NodeAsymmetricKey:
		return '🗝'
	case NodeCertificate:
		return '🏅'
	case NodeSymmetricKey:
		return '🔏'
	case NodeMasterKey:
		return '🔐'
	case NodeColumnMasterKey, NodeColumnEncryptionKey:
		return '🔑'
	case NodeEventSession:
		return '🎬'
	case NodeEventTarget:
		return '🎯'
	case NodeXEventProfilerSession:
		return '🔭'
	case NodeResourceGovernor:
		return '🚦'
	case NodeResourcePool:
		return '🪣'
	case NodeWorkloadGroup:
		return '👥'
	case NodeExternalResourcePool:
		return '🧪'
	case NodeLoading:
		return '⏳'
	case NodeError:
		return '⚠'
	default:
		return '•'
	}
}

func objectIconSymbols(t NodeType) rune {
	switch t {
	case NodeServer:
		return '◉'
	case NodeDatabase:
		return '⬢'
	case NodeDatabaseSnapshot:
		return '◍'
	case NodeTable:
		return '▦'
	case NodeColumn:
		return '⁞'
	case NodeIndex:
		return '⌗'
	case NodeKey:
		return '⚿'
	case NodeStatistic:
		return '▥'
	case NodeView:
		return '◫'
	case NodeStoredProcedure:
		return '⚙'
	case NodeFunction:
		return 'λ'
	case NodeLogin:
		return '⚿'
	case NodeUser:
		return '◇'
	case NodeServerRole, NodeDatabaseRole:
		return '▣'
	case NodeCredential, NodeDatabaseScopedCredential:
		return '⊞'
	case NodeCryptographicProvider:
		return '⊗'
	case NodeAudit:
		return '⊟'
	case NodeServerAuditSpecification, NodeDatabaseAuditSpecification:
		return '⊡'
	case NodeBackupDevice:
		return '⛁'
	case NodeEndpoint:
		return '⊸'
	case NodeAgentJob:
		return '▶'
	case NodeAgentSchedule:
		return '◷'
	case NodeAgentAlert:
		return '◈'
	case NodeAgentOperator:
		return '☏'
	case NodeAgentJobActivity:
		return '▲'
	case NodeAgentJobHistory:
		return '↺'
	case NodeAgentJobCategories, NodeAgentAlertCategories:
		return '▨'
	case NodeAgentReport, NodeQueryStoreReport:
		return '≡'
	case NodeSQLServerLog, NodeAgentErrorLog:
		return '▤'
	case NodeLinkedServer:
		return '⇄'
	case NodeAvailabilityGroup:
		return '↻'
	case NodeAvailabilityReplica:
		return '⧉'
	case NodeAvailabilityDatabase:
		return '⬢'
	case NodeAGListener:
		return '◎'
	case NodeTrigger, NodeServerTrigger, NodeDatabaseTrigger:
		return '⚡'
	case NodeSequence:
		return '↑'
	case NodeSynonym:
		return '≈'
	case NodeSystemDataType:
		return '◧'
	case NodeUserDefinedDataType:
		return '◨'
	case NodeUserDefinedTableType:
		return '◩'
	case NodeUserDefinedType:
		return '◪'
	case NodeXMLSchemaCollection:
		return '⊏'
	case NodeAssembly:
		return '⊛'
	case NodeRule:
		return '⊓'
	case NodeDefault:
		return '⊔'
	case NodePlanGuide:
		return '⊚'
	case NodeMessageType:
		return '✉'
	case NodeContract:
		return '⋈'
	case NodeBrokerQueue:
		return '⊐'
	case NodeBrokerService:
		return '⊕'
	case NodeRoute:
		return '⇉'
	case NodeRemoteServiceBinding:
		return '⊶'
	case NodeBrokerPriority:
		return '⇕'
	case NodeExternalDataSource:
		return '⊙'
	case NodeExternalFileFormat:
		return '⊘'
	case NodeExternalLibrary:
		return '❖'
	case NodeForeignKey:
		return '⛓'
	case NodeCheck:
		return '✓'
	case NodeSchema:
		return '▧'
	case NodePartitionFunction:
		return '∫'
	case NodePartitionScheme:
		return '▩'
	case NodeSecurityPolicy:
		return '⛨'
	case NodeAsymmetricKey:
		return '⚷'
	case NodeCertificate:
		return '✪'
	case NodeSymmetricKey:
		return '⚵'
	case NodeMasterKey:
		return '⚿'
	case NodeColumnMasterKey, NodeColumnEncryptionKey:
		return '⚿'
	case NodeEventSession:
		return '✦'
	case NodeEventTarget:
		return '◎'
	case NodeXEventProfilerSession:
		return '⌁'
	case NodeResourceGovernor:
		return '⚖'
	case NodeResourcePool:
		return '◒'
	case NodeWorkloadGroup:
		return '⁂'
	case NodeExternalResourcePool:
		return '◓'
	case NodeLoading:
		return '…'
	case NodeError:
		return '✗'
	default:
		return '•'
	}
}

// objectIconPortable is the Symbols set with one substitution: Column uses
// the plain '•' bullet, since '⁞' isn't guaranteed to render everywhere.
func objectIconPortable(t NodeType) rune {
	if t == NodeColumn {
		return '•'
	}
	return objectIconSymbols(t)
}
