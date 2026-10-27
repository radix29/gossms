package tui

// tree_node_kinds.go is what each NodeType intrinsically is: folder or
// object, whether it has children, its display name and its glyphs. One row
// per type, indexed by the type itself, so a new NodeType is a missing row
// that TestEveryNodeTypeHasAKind names — not a default case. Three switches
// with a default answered "Object" for most leaves in the Details pane before
// this table replaced them. What a type *does* (loaders, menus, rights,
// scripting) stays in its feature file.

// nodeClass is a node type's place in the tree. The zero value is no class,
// so a row left out of nodeKinds reads as missing rather than as a leaf.
type nodeClass uint8

const (
	// kindLeaf is an object with no children.
	kindLeaf nodeClass = iota + 1
	// kindExpandable is an object that has children (a Database, a Table).
	kindExpandable
	// kindFolder is a grouping node ("Tables", "Views"): the folder glyph,
	// and always children.
	kindFolder
	// kindPlaceholder is NodeLoading and NodeError: not objects, no children.
	kindPlaceholder
)

// nodeKind is one NodeType's row. name is SSMS's noun: singular for an
// object, the folder's tree label for a folder. emoji and symbol are the
// object glyphs of the Emoji and Symbols styles (Portable derives from
// Symbols, see objectIconPortable); a folder has none, as nodeIcon draws
// every folder with folderIcon.
type nodeKind struct {
	class  nodeClass
	name   string
	emoji  rune
	symbol rune
}

// kindOf returns t's row, or the zero row for a value outside the enum.
func kindOf(t NodeType) nodeKind {
	if t < 0 || t >= nodeTypeCount {
		return nodeKind{}
	}
	return nodeKinds[t]
}

// isContainerNode reports whether t is a grouping ("folder") node such as
// "Tables" or "Views", rather than a concrete SQL Server object.
func isContainerNode(t NodeType) bool { return kindOf(t).class == kindFolder }

// hasChildren reports whether this node type can ever have children.
func hasChildren(t NodeType) bool {
	c := kindOf(t).class
	return c == kindFolder || c == kindExpandable
}

// groupedFolderTypes are the folders Group by Schema regroups into one
// NodeSchemaGroup per schema: the main object families only. The System *
// folders, Sequences, Types and the rest stay flat (decided scope).
var groupedFolderTypes = map[NodeType]bool{
	NodeTables:           true,
	NodeViews:            true,
	NodeSynonyms:         true,
	NodeStoredProcedures: true,
	NodeFunctions:        true,
}

// nodeTypeName returns the node type's display name.
func nodeTypeName(t NodeType) string { return kindOf(t).name }

// nodeDisplayType is the Details pane's "Type" for one node: nodeTypeName,
// refined where the node knows more than its type. A NodeKey is a primary or
// a unique key (foreign keys are NodeForeignKey), as its Properties dialog
// title says.
func nodeDisplayType(d nodeData) string {
	if d.Type == NodeKey {
		if d.IsPrimaryKey {
			return "Primary Key"
		}
		return "Unique Key"
	}
	return nodeTypeName(d.Type)
}

var nodeKinds = [nodeTypeCount]nodeKind{
	NodeServer:                      {kindExpandable, "Server", '🖥', '◉'},
	NodeDatabases:                   {kindFolder, "Databases", 0, 0},
	NodeSystemDatabases:             {kindFolder, "System Databases", 0, 0},
	NodeDatabaseSnapshots:           {kindFolder, "Database Snapshots", 0, 0},
	NodeDatabaseSnapshot:            {kindExpandable, "Database Snapshot", '📸', '◍'},
	NodeDatabase:                    {kindExpandable, "Database", '🛢', '⬢'},
	NodeTables:                      {kindFolder, "Tables", 0, 0},
	NodeSystemTables:                {kindFolder, "System Tables", 0, 0},
	NodeFileTables:                  {kindFolder, "FileTables", 0, 0},
	NodeExternalTables:              {kindFolder, "External Tables", 0, 0},
	NodeGraphTables:                 {kindFolder, "Graph Tables", 0, 0},
	NodeTable:                       {kindExpandable, "Table", '▤', '▦'},
	NodeColumns:                     {kindFolder, "Columns", 0, 0},
	NodeColumn:                      {kindLeaf, "Column", '🏷', '⁞'},
	NodeKeys:                        {kindFolder, "Keys", 0, 0},
	NodeKey:                         {kindLeaf, "Key", '🗝', '⚿'},
	NodeIndexes:                     {kindFolder, "Indexes", 0, 0},
	NodeIndex:                       {kindLeaf, "Index", '📇', '⌗'},
	NodeStatistics:                  {kindFolder, "Statistics", 0, 0},
	NodeStatistic:                   {kindLeaf, "Statistic", '📊', '▥'},
	NodeViews:                       {kindFolder, "Views", 0, 0},
	NodeView:                        {kindExpandable, "View", '👁', '◫'},
	NodeSystemViews:                 {kindFolder, "System Views", 0, 0},
	NodeStoredProcedures:            {kindFolder, "Stored Procedures", 0, 0},
	NodeStoredProcedure:             {kindLeaf, "Stored Procedure", '⚙', '⚙'},
	NodeSystemProcedures:            {kindFolder, "System Procedures", 0, 0},
	NodeFunctions:                   {kindFolder, "Functions", 0, 0},
	NodeFunction:                    {kindLeaf, "Function", 'ƒ', 'λ'},
	NodeSystemFunctions:             {kindFolder, "System Functions", 0, 0},
	NodeSecurity:                    {kindFolder, "Security", 0, 0},
	NodeLogins:                      {kindFolder, "Logins", 0, 0},
	NodeLogin:                       {kindLeaf, "Login", '🔐', '⚿'},
	NodeServerRoles:                 {kindFolder, "Server Roles", 0, 0},
	NodeServerRole:                  {kindLeaf, "Server Role", '🎭', '▣'},
	NodeCredentials:                 {kindFolder, "Credentials", 0, 0},
	NodeCredential:                  {kindLeaf, "Credential", '🪪', '⊞'},
	NodeCryptographicProviders:      {kindFolder, "Cryptographic Providers", 0, 0},
	NodeCryptographicProvider:       {kindLeaf, "Cryptographic Provider", '🔒', '⊗'},
	NodeAudits:                      {kindFolder, "Audits", 0, 0},
	NodeAudit:                       {kindLeaf, "Audit", '📋', '⊟'},
	NodeServerAuditSpecifications:   {kindFolder, "Server Audit Specifications", 0, 0},
	NodeServerAuditSpecification:    {kindLeaf, "Server Audit Specification", '📑', '⊡'},
	NodeServerObjects:               {kindFolder, "Server Objects", 0, 0},
	NodeBackupDevices:               {kindFolder, "Backup Devices", 0, 0},
	NodeBackupDevice:                {kindLeaf, "Backup Device", '💾', '⛁'},
	NodeServerTriggers:              {kindFolder, "Triggers", 0, 0},
	NodeServerTrigger:               {kindLeaf, "Server Trigger", '⚡', '⚡'},
	NodeEndpoints:                   {kindFolder, "Endpoints", 0, 0},
	NodeEndpoint:                    {kindLeaf, "Endpoint", '🔌', '⊸'},
	NodeManagement:                  {kindFolder, "Management", 0, 0},
	NodeSQLServerLogs:               {kindFolder, "SQL Server Logs", 0, 0},
	NodeSQLServerLog:                {kindLeaf, "SQL Server Log", '📄', '▤'},
	NodeAgentJobs:                   {kindFolder, "SQL Server Agent", 0, 0},
	NodeAgentJobsFolder:             {kindFolder, "Jobs", 0, 0},
	NodeAgentUserJobs:               {kindFolder, "User Jobs", 0, 0},
	NodeAgentSystemJobs:             {kindFolder, "System / Internal Jobs", 0, 0},
	NodeAgentJob:                    {kindLeaf, "Job", '⏱', '▶'},
	NodeAgentJobActivity:            {kindLeaf, "Job Activity", '📈', '▲'},
	NodeAgentJobHistory:             {kindLeaf, "Job History", '🕒', '↺'},
	NodeAgentJobCategories:          {kindLeaf, "Job Categories", '🗂', '▨'},
	NodeAgentSchedules:              {kindFolder, "Schedules", 0, 0},
	NodeAgentSchedule:               {kindLeaf, "Schedule", '📅', '◷'},
	NodeAgentAlerts:                 {kindFolder, "Alerts", 0, 0},
	NodeAgentEventAlerts:            {kindFolder, "SQL Server Event Alerts", 0, 0},
	NodeAgentAlert:                  {kindLeaf, "Alert", '🔔', '◈'},
	NodeAgentAlertCategories:        {kindLeaf, "Alert Categories", '🗂', '▨'},
	NodeAgentOperators:              {kindFolder, "Operators", 0, 0},
	NodeAgentOperator:               {kindLeaf, "Operator", '📞', '☏'},
	NodeAgentAdmin:                  {kindFolder, "SQL-only administration", 0, 0},
	NodeAgentReport:                 {kindLeaf, "SQL Server Agent Report", '📋', '≡'},
	NodeAgentErrorLogs:              {kindFolder, "Error Logs", 0, 0},
	NodeAgentErrorLog:               {kindLeaf, "SQL Server Agent Error Log", '📄', '▤'},
	NodeLinkedServers:               {kindFolder, "Linked Servers", 0, 0},
	NodeLinkedServer:                {kindLeaf, "Linked Server", '🔗', '⇄'},
	NodeAlwaysOn:                    {kindFolder, "Always On High Availability", 0, 0},
	NodeAvailabilityGroups:          {kindFolder, "Availability Groups", 0, 0},
	NodeAvailabilityGroup:           {kindExpandable, "Availability Group", '🔄', '↻'},
	NodeAvailabilityReplicas:        {kindFolder, "Availability Replicas", 0, 0},
	NodeAvailabilityReplica:         {kindLeaf, "Availability Replica", '🖧', '⧉'},
	NodeAvailabilityDatabases:       {kindFolder, "Availability Databases", 0, 0},
	NodeAvailabilityDatabase:        {kindLeaf, "Availability Database", '🛢', '⬢'},
	NodeAGListeners:                 {kindFolder, "Availability Group Listeners", 0, 0},
	NodeAGListener:                  {kindLeaf, "Availability Group Listener", '📡', '◎'},
	NodeDatabaseSecurity:            {kindFolder, "Security", 0, 0},
	NodeUsers:                       {kindFolder, "Users", 0, 0},
	NodeUser:                        {kindLeaf, "User", '👤', '◇'},
	NodeDatabaseRoles:               {kindFolder, "Roles", 0, 0},
	NodeDatabaseRole:                {kindLeaf, "Database Role", '🎭', '▣'},
	NodeSchemas:                     {kindFolder, "Schemas", 0, 0},
	NodeSchema:                      {kindLeaf, "Schema", '🧩', '▧'},
	NodeTriggers:                    {kindFolder, "Triggers", 0, 0},
	NodeTrigger:                     {kindLeaf, "Trigger", '⚡', '⚡'},
	NodeProgrammability:             {kindFolder, "Programmability", 0, 0},
	NodeDatabaseTriggers:            {kindFolder, "Database Triggers", 0, 0},
	NodeDatabaseTrigger:             {kindLeaf, "Database Trigger", '⚡', '⚡'},
	NodeSequences:                   {kindFolder, "Sequences", 0, 0},
	NodeSequence:                    {kindLeaf, "Sequence", '🔢', '↑'},
	NodeSynonyms:                    {kindFolder, "Synonyms", 0, 0},
	NodeSynonym:                     {kindLeaf, "Synonym", '🔖', '≈'},
	NodeTypes:                       {kindFolder, "Types", 0, 0},
	NodeSystemDataTypes:             {kindFolder, "System Data Types", 0, 0},
	NodeSystemDataType:              {kindLeaf, "System Data Type", '🔡', '◧'},
	NodeUserDefinedDataTypes:        {kindFolder, "User-Defined Data Types", 0, 0},
	NodeUserDefinedDataType:         {kindLeaf, "User-Defined Data Type", '🔡', '◨'},
	NodeUserDefinedTableTypes:       {kindFolder, "User-Defined Table Types", 0, 0},
	NodeUserDefinedTableType:        {kindLeaf, "User-Defined Table Type", '🧾', '◩'},
	NodeUserDefinedTypes:            {kindFolder, "User-Defined Types", 0, 0},
	NodeUserDefinedType:             {kindLeaf, "User-Defined Type", '🧬', '◪'},
	NodeXMLSchemaCollections:        {kindFolder, "XML Schema Collections", 0, 0},
	NodeXMLSchemaCollection:         {kindLeaf, "XML Schema Collection", '📜', '⊏'},
	NodeAssemblies:                  {kindFolder, "Assemblies", 0, 0},
	NodeAssembly:                    {kindLeaf, "Assembly", '📦', '⊛'},
	NodeRules:                       {kindFolder, "Rules", 0, 0},
	NodeRule:                        {kindLeaf, "Rule", '📏', '⊓'},
	NodeDefaults:                    {kindFolder, "Defaults", 0, 0},
	NodeDefault:                     {kindLeaf, "Default", '🧷', '⊔'},
	NodePlanGuides:                  {kindFolder, "Plan Guides", 0, 0},
	NodePlanGuide:                   {kindLeaf, "Plan Guide", '🧭', '⊚'},
	NodeServiceBroker:               {kindFolder, "Service Broker", 0, 0},
	NodeMessageTypes:                {kindFolder, "Message Types", 0, 0},
	NodeMessageType:                 {kindLeaf, "Message Type", '✉', '✉'},
	NodeContracts:                   {kindFolder, "Contracts", 0, 0},
	NodeContract:                    {kindLeaf, "Contract", '🤝', '⋈'},
	NodeBrokerQueues:                {kindFolder, "Queues", 0, 0},
	NodeBrokerQueue:                 {kindLeaf, "Queue", '📥', '⊐'},
	NodeBrokerServices:              {kindFolder, "Services", 0, 0},
	NodeBrokerService:               {kindLeaf, "Service", '🛎', '⊕'},
	NodeRoutes:                      {kindFolder, "Routes", 0, 0},
	NodeRoute:                       {kindLeaf, "Route", '🛣', '⇉'},
	NodeRemoteServiceBindings:       {kindFolder, "Remote Service Bindings", 0, 0},
	NodeRemoteServiceBinding:        {kindLeaf, "Remote Service Binding", '🪢', '⊶'},
	NodeBrokerPriorities:            {kindFolder, "Broker Priorities", 0, 0},
	NodeBrokerPriority:              {kindLeaf, "Broker Priority", '🎚', '⇕'},
	NodeExternalResources:           {kindFolder, "External Resources", 0, 0},
	NodeExternalDataSources:         {kindFolder, "External Data Sources", 0, 0},
	NodeExternalDataSource:          {kindLeaf, "External Data Source", '🌐', '⊙'},
	NodeExternalFileFormats:         {kindFolder, "External File Formats", 0, 0},
	NodeExternalFileFormat:          {kindLeaf, "External File Format", '📃', '⊘'},
	NodeExternalLibraries:           {kindFolder, "External Libraries", 0, 0},
	NodeExternalLibrary:             {kindLeaf, "External Library", '📚', '❖'},
	NodeForeignKey:                  {kindLeaf, "Foreign Key", '🔗', '⛓'},
	NodeChecks:                      {kindFolder, "Constraints", 0, 0},
	NodeCheck:                       {kindLeaf, "Check Constraint", '✔', '✓'},
	NodeStorage:                     {kindFolder, "Storage", 0, 0},
	NodePartitionFunctions:          {kindFolder, "Partition Functions", 0, 0},
	NodePartitionFunction:           {kindLeaf, "Partition Function", '📐', '∫'},
	NodePartitionSchemes:            {kindFolder, "Partition Schemes", 0, 0},
	NodePartitionScheme:             {kindLeaf, "Partition Scheme", '🗄', '▩'},
	NodeDatabaseAuditSpecifications: {kindFolder, "Database Audit Specifications", 0, 0},
	NodeDatabaseAuditSpecification:  {kindLeaf, "Database Audit Specification", '📑', '⊡'},
	NodeDatabaseScopedCredentials:   {kindFolder, "Database Scoped Credentials", 0, 0},
	NodeDatabaseScopedCredential:    {kindLeaf, "Database Scoped Credential", '🪪', '⊞'},
	NodeAsymmetricKeys:              {kindFolder, "Asymmetric Keys", 0, 0},
	NodeAsymmetricKey:               {kindLeaf, "Asymmetric Key", '🗝', '⚷'},
	NodeCertificates:                {kindFolder, "Certificates", 0, 0},
	NodeCertificate:                 {kindLeaf, "Certificate", '🏅', '✪'},
	NodeSymmetricKeys:               {kindFolder, "Symmetric Keys", 0, 0},
	NodeSymmetricKey:                {kindLeaf, "Symmetric Key", '🔏', '⚵'},
	NodeMasterKey:                   {kindLeaf, "Database Master Key", '🔐', '⚿'},
	NodeSecurityPolicies:            {kindFolder, "Security Policies", 0, 0},
	NodeSecurityPolicy:              {kindLeaf, "Security Policy", '🛡', '⛨'},
	NodeAlwaysEncryptedKeys:         {kindFolder, "Always Encrypted Keys", 0, 0},
	NodeQueryStore:                  {kindFolder, "Query Store", 0, 0},
	NodeQueryStoreReport:            {kindLeaf, "Query Store Report", '📋', '≡'},
	NodeColumnMasterKeys:            {kindFolder, "Column Master Keys", 0, 0},
	NodeColumnMasterKey:             {kindLeaf, "Column Master Key", '🔑', '⚿'},
	NodeColumnEncryptionKeys:        {kindFolder, "Column Encryption Keys", 0, 0},
	NodeColumnEncryptionKey:         {kindLeaf, "Column Encryption Key", '🔑', '⚿'},
	NodeExtendedEvents:              {kindFolder, "Extended Events", 0, 0},
	NodeEventSessions:               {kindFolder, "Sessions", 0, 0},
	NodeEventSession:                {kindExpandable, "Event Session", '🎬', '✦'},
	NodeEventTarget:                 {kindLeaf, "Event Session Target", '🎯', '◎'},
	NodeXEventProfiler:              {kindFolder, "XEvent Profiler", 0, 0},
	NodeXEventProfilerSession:       {kindLeaf, "XEvent Profiler Session", '🔭', '⌁'},
	NodeResourceGovernor:            {kindExpandable, "Resource Governor", '🚦', '⚖'},
	NodeResourcePools:               {kindFolder, "Resource Pools", 0, 0},
	NodeResourcePool:                {kindExpandable, "Resource Pool", '🪣', '◒'},
	NodeWorkloadGroups:              {kindFolder, "Workload Groups", 0, 0},
	NodeWorkloadGroup:               {kindLeaf, "Workload Group", '👥', '⁂'},
	NodeExternalResourcePools:       {kindFolder, "External Resource Pools", 0, 0},
	NodeExternalResourcePool:        {kindLeaf, "External Resource Pool", '🧪', '◓'},
	NodeDatabaseMail:                {kindLeaf, "Database Mail", '📮', '✆'},
	NodeReplication:                 {kindFolder, "Replication", 0, 0},
	NodeLocalPublications:           {kindFolder, "Local Publications", 0, 0},
	NodePublication:                 {kindLeaf, "Publication", '📤', '⇶'},
	NodeLocalSubscriptions:          {kindFolder, "Local Subscriptions", 0, 0},
	NodeLocalSubscription:           {kindLeaf, "Subscription", '📬', '⇲'},
	NodeFullTextCatalogs:            {kindFolder, "Full Text Catalogs", 0, 0},
	NodeFullTextCatalog:             {kindLeaf, "Full-Text Catalog", '🔎', '⌕'},
	NodeFullTextStoplists:           {kindFolder, "Full Text Stoplists", 0, 0},
	NodeFullTextStoplist:            {kindLeaf, "Full-Text Stoplist", '🚫', '∅'},
	NodeSearchPropertyLists:         {kindFolder, "Search Property Lists", 0, 0},
	NodeSearchPropertyList:          {kindLeaf, "Search Property List", '🗒', '≔'},
	NodeViewColumns:                 {kindFolder, "Columns", 0, 0},
	NodeViewColumn:                  {kindLeaf, "View Column", '🏷', '⁞'},
	NodeSchemaGroup:                 {kindFolder, "Schema Group", 0, 0},
	NodeLoading:                     {kindPlaceholder, "Loading", '⏳', '…'},
	NodeError:                       {kindPlaceholder, "Error", '⚠', '✗'},
}
