package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// A1: adding an Object Explorer family means touching a dozen switches and
// maps, and what pinned that was a hand-written slice of the leaves each
// batch added — the same act of memory the tests exist to replace. This is
// the exhaustive form: every NodeType from 0 to nodeTypeCount must declare
// one classification here, and the classification implies the wiring the
// type must have. A new NodeType fails this test by construction, because it
// has no entry; extending the table is the moment to notice the switch you
// have not touched yet.
//
// It does not check what the switches *say* — that a queue's glyph is a queue
// — only that each type is wired at all. TestNewFamilyLeavesAreNamed and its
// siblings pin the values for the families they cover.

type nodeClass int

const (
	// classObject is a node that stands for a SQL Server object: it draws an
	// object glyph, never the folder one. expandable says whether it also has
	// children (a Database, a Table), which must agree with hasChildren and
	// with having a childLoaders entry.
	classObject nodeClass = iota
	// classFolder is a container: the folder glyph, an expand arrow, and a
	// childLoaders entry. filterable says whether its Filter menu offers
	// anything, which is false for every folder of folders and for the few
	// object folders that deliberately offer no filter (Columns, Keys,
	// Indexes, Statistics, Checks, and the Agent and Always On folders).
	classFolder
	// classInternal is NodeLoading and NodeError — placeholders, not objects:
	// no name, no children, no loader.
	classInternal
)

type nodeWiring struct {
	class      nodeClass
	filterable bool
	expandable bool
	// unnamed records a type nodeTypeName has no case for, so it reads
	// "Object". It surfaces only in the Details pane's fallback Property/Value
	// grid (the "Type" row), which a type with a purpose-built detail view
	// never reaches — which is why these are tolerated rather than fixed here.
	// A *new* type is not on this list and must be named.
	unnamed bool
	// portableBullet records the one type whose Portable glyph is the shared
	// bullet on purpose: NodeColumn, because '⁞' is not guaranteed to render
	// everywhere (see objectIconPortable).
	portableBullet bool
}

var nodeTypeWiring = map[NodeType]nodeWiring{
	NodeServer:                      {class: classObject, expandable: true},
	NodeDatabases:                   {class: classFolder, filterable: true},
	NodeSystemDatabases:             {class: classFolder, filterable: true},
	NodeDatabaseSnapshots:           {class: classFolder, filterable: true},
	NodeDatabaseSnapshot:            {class: classObject, expandable: true},
	NodeDatabase:                    {class: classObject, expandable: true},
	NodeTables:                      {class: classFolder, filterable: true},
	NodeSystemTables:                {class: classFolder, filterable: true},
	NodeFileTables:                  {class: classFolder, filterable: true},
	NodeExternalTables:              {class: classFolder, filterable: true},
	NodeGraphTables:                 {class: classFolder, filterable: true},
	NodeTable:                       {class: classObject, expandable: true},
	NodeColumns:                     {class: classFolder},
	NodeColumn:                      {class: classObject, unnamed: true, portableBullet: true},
	NodeKeys:                        {class: classFolder},
	NodeKey:                         {class: classObject, unnamed: true},
	NodeIndexes:                     {class: classFolder},
	NodeIndex:                       {class: classObject, unnamed: true},
	NodeStatistics:                  {class: classFolder},
	NodeStatistic:                   {class: classObject, unnamed: true},
	NodeViews:                       {class: classFolder, filterable: true},
	NodeView:                        {class: classObject, expandable: true},
	NodeSystemViews:                 {class: classFolder, filterable: true},
	NodeStoredProcedures:            {class: classFolder, filterable: true},
	NodeStoredProcedure:             {class: classObject},
	NodeSystemProcedures:            {class: classFolder, filterable: true},
	NodeFunctions:                   {class: classFolder, filterable: true},
	NodeFunction:                    {class: classObject},
	NodeSystemFunctions:             {class: classFolder, filterable: true},
	NodeSecurity:                    {class: classFolder},
	NodeLogins:                      {class: classFolder, filterable: true},
	NodeLogin:                       {class: classObject},
	NodeServerRoles:                 {class: classFolder, filterable: true},
	NodeServerRole:                  {class: classObject, unnamed: true},
	NodeCredentials:                 {class: classFolder, filterable: true},
	NodeCredential:                  {class: classObject},
	NodeCryptographicProviders:      {class: classFolder},
	NodeCryptographicProvider:       {class: classObject},
	NodeAudits:                      {class: classFolder, filterable: true},
	NodeAudit:                       {class: classObject},
	NodeServerAuditSpecifications:   {class: classFolder, filterable: true},
	NodeServerAuditSpecification:    {class: classObject},
	NodeServerObjects:               {class: classFolder},
	NodeBackupDevices:               {class: classFolder, filterable: true},
	NodeBackupDevice:                {class: classObject},
	NodeServerTriggers:              {class: classFolder, filterable: true},
	NodeServerTrigger:               {class: classObject},
	NodeEndpoints:                   {class: classFolder, filterable: true},
	NodeEndpoint:                    {class: classObject},
	NodeManagement:                  {class: classFolder},
	NodeSQLServerLogs:               {class: classFolder},
	NodeSQLServerLog:                {class: classObject},
	NodeAgentJobs:                   {class: classFolder},
	NodeAgentJobsFolder:             {class: classFolder},
	NodeAgentUserJobs:               {class: classFolder},
	NodeAgentSystemJobs:             {class: classFolder},
	NodeAgentJob:                    {class: classObject, unnamed: true},
	NodeAgentJobActivity:            {class: classObject, unnamed: true},
	NodeAgentJobHistory:             {class: classObject, unnamed: true},
	NodeAgentJobCategories:          {class: classObject, unnamed: true},
	NodeAgentSchedules:              {class: classFolder},
	NodeAgentSchedule:               {class: classObject, unnamed: true},
	NodeAgentAlerts:                 {class: classFolder},
	NodeAgentEventAlerts:            {class: classFolder},
	NodeAgentAlert:                  {class: classObject, unnamed: true},
	NodeAgentAlertCategories:        {class: classObject, unnamed: true},
	NodeAgentOperators:              {class: classFolder},
	NodeAgentOperator:               {class: classObject, unnamed: true},
	NodeAgentAdmin:                  {class: classFolder},
	NodeAgentReport:                 {class: classObject, unnamed: true},
	NodeAgentErrorLogs:              {class: classFolder},
	NodeAgentErrorLog:               {class: classObject},
	NodeLinkedServers:               {class: classFolder},
	NodeLinkedServer:                {class: classObject, unnamed: true},
	NodeAlwaysOn:                    {class: classFolder},
	NodeAvailabilityGroups:          {class: classFolder},
	NodeAvailabilityGroup:           {class: classObject, expandable: true},
	NodeAvailabilityReplicas:        {class: classFolder},
	NodeAvailabilityReplica:         {class: classObject},
	NodeAvailabilityDatabases:       {class: classFolder},
	NodeAvailabilityDatabase:        {class: classObject},
	NodeAGListeners:                 {class: classFolder},
	NodeAGListener:                  {class: classObject},
	NodeDatabaseSecurity:            {class: classFolder},
	NodeUsers:                       {class: classFolder, filterable: true},
	NodeUser:                        {class: classObject},
	NodeDatabaseRoles:               {class: classFolder, filterable: true},
	NodeDatabaseRole:                {class: classObject, unnamed: true},
	NodeSchemas:                     {class: classFolder, filterable: true},
	NodeSchema:                      {class: classObject, unnamed: true},
	NodeTriggers:                    {class: classFolder, filterable: true},
	NodeTrigger:                     {class: classObject, unnamed: true},
	NodeProgrammability:             {class: classFolder},
	NodeDatabaseTriggers:            {class: classFolder, filterable: true},
	NodeDatabaseTrigger:             {class: classObject},
	NodeSequences:                   {class: classFolder, filterable: true},
	NodeSequence:                    {class: classObject, unnamed: true},
	NodeSynonyms:                    {class: classFolder, filterable: true},
	NodeSynonym:                     {class: classObject, unnamed: true},
	NodeTypes:                       {class: classFolder},
	NodeSystemDataTypes:             {class: classFolder, filterable: true},
	NodeSystemDataType:              {class: classObject},
	NodeUserDefinedDataTypes:        {class: classFolder, filterable: true},
	NodeUserDefinedDataType:         {class: classObject},
	NodeUserDefinedTableTypes:       {class: classFolder, filterable: true},
	NodeUserDefinedTableType:        {class: classObject},
	NodeUserDefinedTypes:            {class: classFolder, filterable: true},
	NodeUserDefinedType:             {class: classObject},
	NodeXmlSchemaCollections:        {class: classFolder, filterable: true},
	NodeXmlSchemaCollection:         {class: classObject},
	NodeAssemblies:                  {class: classFolder, filterable: true},
	NodeAssembly:                    {class: classObject},
	NodeRules:                       {class: classFolder, filterable: true},
	NodeRule:                        {class: classObject},
	NodeDefaults:                    {class: classFolder, filterable: true},
	NodeDefault:                     {class: classObject},
	NodePlanGuides:                  {class: classFolder, filterable: true},
	NodePlanGuide:                   {class: classObject},
	NodeServiceBroker:               {class: classFolder},
	NodeMessageTypes:                {class: classFolder, filterable: true},
	NodeMessageType:                 {class: classObject},
	NodeContracts:                   {class: classFolder, filterable: true},
	NodeContract:                    {class: classObject},
	NodeBrokerQueues:                {class: classFolder, filterable: true},
	NodeBrokerQueue:                 {class: classObject},
	NodeBrokerServices:              {class: classFolder, filterable: true},
	NodeBrokerService:               {class: classObject},
	NodeRoutes:                      {class: classFolder, filterable: true},
	NodeRoute:                       {class: classObject},
	NodeRemoteServiceBindings:       {class: classFolder, filterable: true},
	NodeRemoteServiceBinding:        {class: classObject},
	NodeBrokerPriorities:            {class: classFolder, filterable: true},
	NodeBrokerPriority:              {class: classObject},
	NodeExternalResources:           {class: classFolder},
	NodeExternalDataSources:         {class: classFolder, filterable: true},
	NodeExternalDataSource:          {class: classObject},
	NodeExternalFileFormats:         {class: classFolder, filterable: true},
	NodeExternalFileFormat:          {class: classObject},
	NodeExternalLibraries:           {class: classFolder, filterable: true},
	NodeExternalLibrary:             {class: classObject},
	NodeForeignKey:                  {class: classObject, unnamed: true},
	NodeChecks:                      {class: classFolder},
	NodeCheck:                       {class: classObject, unnamed: true},
	NodeStorage:                     {class: classFolder},
	NodePartitionFunctions:          {class: classFolder, filterable: true},
	NodePartitionFunction:           {class: classObject},
	NodePartitionSchemes:            {class: classFolder, filterable: true},
	NodePartitionScheme:             {class: classObject},
	NodeDatabaseAuditSpecifications: {class: classFolder, filterable: true},
	NodeDatabaseAuditSpecification:  {class: classObject},
	NodeDatabaseScopedCredentials:   {class: classFolder, filterable: true},
	NodeDatabaseScopedCredential:    {class: classObject},
	NodeSecurityPolicies:            {class: classFolder, filterable: true},
	NodeSecurityPolicy:              {class: classObject},
	NodeAlwaysEncryptedKeys:         {class: classFolder},
	NodeQueryStore:                  {class: classFolder},
	NodeQueryStoreReport:            {class: classObject},
	NodeColumnMasterKeys:            {class: classFolder, filterable: true},
	NodeColumnMasterKey:             {class: classObject},
	NodeColumnEncryptionKeys:        {class: classFolder, filterable: true},
	NodeColumnEncryptionKey:         {class: classObject},
	NodeLoading:                     {class: classInternal},
	NodeError:                       {class: classInternal},
}

// nodeTypeNames reads the constant names out of tree_node.go's iota block, in
// order, so a failure here names the type rather than its number. Parsed
// rather than listed: a hand-written list of 163 names is the very thing this
// test exists to stop relying on.
func nodeTypeNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "tree_node.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing tree_node.go: %v", err)
	}
	var names []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 {
				continue
			}
			if n := vs.Names[0].Name; n == "nodeTypeCount" {
				return names
			} else if len(names) > 0 || n == "NodeServer" {
				names = append(names, n)
			}
		}
	}
	return names
}

func TestEveryNodeTypeIsWired(t *testing.T) {
	if nodeTypeCount == 0 {
		t.Fatal("nodeTypeCount is 0; the sentinel is not last in the const block")
	}
	names := nodeTypeNames(t)
	if len(names) != int(nodeTypeCount) {
		t.Fatalf("tree_node.go declares %d NodeType constants before nodeTypeCount, which is %d — "+
			"the sentinel is not last, or a constant is declared elsewhere", len(names), nodeTypeCount)
	}
	// Safe to index: the count check above pins len(names) to nodeTypeCount.
	name := func(nt NodeType) string { return names[nt] }
	for nt := NodeType(0); nt < nodeTypeCount; nt++ {
		w, ok := nodeTypeWiring[nt]
		if !ok {
			t.Errorf("%s has no entry in nodeTypeWiring — a new type is wired nowhere "+
				"until it is classified here; add it, and check every switch the class implies", name(nt))
			continue
		}
		_, hasLoader := childLoaders[nt]
		switch w.class {
		case classFolder:
			if !isContainerNode(nt) {
				t.Errorf("%s is declared a folder but isContainerNode says no — it would "+
					"draw an object glyph and refuse to expand", name(nt))
			}
			if !hasChildren(nt) {
				t.Errorf("%s is a folder with hasChildren false — no expand arrow", name(nt))
			}
			if !hasLoader {
				t.Errorf("%s is a folder with no childLoaders entry — it expands to nothing", name(nt))
			}
			props := filterProps(nt)
			if w.filterable {
				if len(props) == 0 {
					t.Errorf("%s is declared filterable and declares no filter properties — "+
						"its Filter menu offers nothing", name(nt))
					break
				}
				var propNames []string
				for _, p := range props {
					propNames = append(propNames, p.name)
				}
				if !slices.Contains(propNames, "Name") {
					t.Errorf("%s filter properties = %v, with no Name", name(nt), propNames)
				}
			} else if len(props) != 0 {
				t.Errorf("%s declares filter properties but is not declared filterable — "+
					"flip filterable so the Name check applies to it", name(nt))
			}
		case classObject:
			if isContainerNode(nt) {
				t.Errorf("%s is declared an object but isContainerNode says yes — it would "+
					"draw a folder glyph", name(nt))
			}
			if hasChildren(nt) != w.expandable {
				t.Errorf("%s: hasChildren = %v, declared expandable = %v", name(nt),
					hasChildren(nt), w.expandable)
			}
			if hasLoader != w.expandable {
				t.Errorf("%s: childLoaders entry = %v, declared expandable = %v — an "+
					"expandable object with no loader shows an arrow that leads nowhere, and a "+
					"leaf with one contradicts hasChildren", name(nt), hasLoader, w.expandable)
			}
			assertGlyphs(t, name(nt), nt, w)
			if !w.unnamed && nodeTypeName(nt) == "Object" {
				t.Errorf("%s falls to nodeTypeName's \"Object\" default — the Details "+
					"pane would name it that", name(nt))
			}
		case classInternal:
			if isContainerNode(nt) || hasChildren(nt) || hasLoader {
				t.Errorf("%s is a placeholder but is wired as something expandable", name(nt))
			}
			assertGlyphs(t, name(nt), nt, w)
		}
	}
	for nt := range nodeTypeWiring {
		if nt >= nodeTypeCount {
			t.Errorf("nodeTypeWiring has an entry for %d, past nodeTypeCount (%d)", nt, nodeTypeCount)
		}
	}
}

// assertGlyphs checks all three icon switches answer for nt. They are three
// separate switches, so a type added to one draws the shared bullet in the
// others — which is a glyph, and so invisible to a nil check.
func assertGlyphs(t *testing.T, name string, nt NodeType, w nodeWiring) {
	t.Helper()
	styles := []struct {
		name   string
		s      config.IconStyle
		bullet bool // whether the shared bullet is allowed here
	}{
		{"Emoji", config.IconStyleEmoji, false},
		{"Symbols", config.IconStyleSymbols, false},
		{"Portable", config.IconStylePortable, w.portableBullet},
	}
	for _, style := range styles {
		got := objectIcon(nt, style.s)
		if got == 0 {
			t.Errorf("%s: %s has no glyph", style.name, name)
			continue
		}
		if got == '•' && !style.bullet {
			t.Errorf("%s: %s fell through to the default bullet", style.name, name)
		}
	}
}
