package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_security.go is the Detail Browser's view of the server-level
// Security families that are not logins — Credentials, Cryptographic
// Providers, Audits and Server Audit Specifications — plus a database's own
// Audit Specifications and Scoped Credentials, which share their rendering.
// The Logins folder has its own progressive loader
// (detail_browser_logins.go); everything here answers from a single round
// trip.

// credentialsFolderDetail lists every server-level credential. It reads gosmo
// independently of the tree, so the folder's filter is applied here too — over
// the gosmo objects, before the rows are built.
func credentialsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	creds, err := sc.Server.CredentialsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	creds = filterObjects(node.data.Filter, creds, func(c *gosmo.Credential) nodeData {
		return nodeData{Name: c.Name, CreateDate: c.CreateDate}
	})

	rows := make([][]string, 0, len(creds))
	out := make([]nodeData, 0, len(creds))
	for _, c := range creds {
		rows = append(rows, []string{
			c.Name, c.Identity, credentialKind(c),
			formatSQLDate(c.CreateDate), formatSQLDate(c.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeCredential, Name: c.Name})
	}
	*objs = out
	return []string{"Name", "Identity", "Type", "Created", "Modified"}, rows, nil
}

// credentialDetail is one credential's Property/Value view. The secret is not
// shown because it cannot be read — see gosmo's credential.go.
func credentialDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	c, err := sc.Server.CredentialByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	pairs := []string{
		"Name", c.Name,
		"Identity", c.Identity,
		"Type", credentialKind(c),
	}
	if c.CryptographicProvider != "" {
		pairs = append(pairs, "Provider", c.CryptographicProvider)
	}
	return propertyRows(append(pairs,
		"Created", formatSQLDate(c.CreateDate),
		"Modified", formatSQLDate(c.ModifyDate),
	)...)
}

// credentialKind names what a credential is bound to, in the wording SSMS
// uses. TargetType, not the resolved provider name: the name comes from a join
// onto sys.cryptographic_providers, which a login without rights on it reads
// as empty.
func credentialKind(c *gosmo.Credential) string {
	if c.TargetType == "" {
		return "Credential"
	}
	return "Cryptographic Provider"
}

// -- Cryptographic providers -------------------------------------------------------

// cryptographicProvidersFolderDetail lists every registered EKM provider. A
// server with none is the ordinary case and yields an empty grid, not an
// error.
//
// The folder declares no filter properties (explorer_filter.go), so there is
// no filterObjects call here — adding one without adding the folder there
// would filter the pane by a criterion the tree cannot express.
func cryptographicProvidersFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	providers, err := sc.Server.CryptographicProvidersContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(providers))
	out := make([]nodeData, 0, len(providers))
	for _, p := range providers {
		rows = append(rows, []string{p.Name, enabledText(p.IsEnabled), p.Version, p.DLLPath})
		out = append(out, nodeData{Type: NodeCryptographicProvider, Name: p.Name, IsEnabled: p.IsEnabled})
	}
	*objs = out
	return []string{"Name", "State", "Version", "DLL path"}, rows, nil
}

// cryptographicProviderDetail is one provider's Property/Value view. There is
// no by-name read in gosmo for a provider — sys.cryptographic_providers is
// listed whole — so the row is picked out of the list.
func cryptographicProviderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	providers, err := sc.Server.CryptographicProvidersContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range providers {
		if !strings.EqualFold(p.Name, node.data.Name) {
			continue
		}
		return propertyRows(
			"Name", p.Name,
			"State", enabledText(p.IsEnabled),
			"Provider ID", strconv.Itoa(p.ProviderID),
			"GUID", p.GUID,
			"Version", p.Version,
			"DLL path", p.DLLPath,
		)
	}
	return nil, nil, fmt.Errorf("cryptographic provider %q no longer exists", node.data.Name)
}

// -- Audits ----------------------------------------------------------------------

// auditsFolderDetail lists every server audit. As with credentials, the read
// is independent of the tree's, so the folder's filter is applied here too,
// over the gosmo objects before the rows are built.
func auditsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	audits, err := sc.Server.ServerAuditsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	audits = filterObjects(node.data.Filter, audits, func(a *gosmo.ServerAudit) nodeData {
		return nodeData{Name: a.Name, CreateDate: a.CreateDate}
	})

	rows := make([][]string, 0, len(audits))
	out := make([]nodeData, 0, len(audits))
	for _, a := range audits {
		rows = append(rows, []string{
			a.Name, a.Type, auditDestinationText(a), enabledText(a.IsEnabled),
			a.OnFailure, formatSQLDate(a.CreateDate), formatSQLDate(a.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeAudit, Name: a.Name, IsEnabled: a.IsEnabled})
	}
	*objs = out
	return []string{"Name", "Destination", "Target", "State", "On Failure", "Created", "Modified"}, rows, nil
}

// auditDetail is one audit's Property/Value view.
func auditDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	a, err := sc.Server.ServerAuditByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	pairs := []string{
		"Name", a.Name,
		"Destination", a.Type,
		"State", enabledText(a.IsEnabled),
		"On failure", a.OnFailure,
		"Queue delay (ms)", strconv.Itoa(a.QueueDelay),
	}
	if a.Type == gosmo.AuditToFile {
		pairs = append(pairs,
			"File path", a.LogFilePath,
			"File name", a.LogFileName,
			"Maximum file size", auditFileSizeText(a.MaxFileSize),
			"Files to retain", auditFileCountText(a),
			"Reserve disk space", yesNo(a.ReserveDiskSpace),
		)
	}
	if a.Predicate != "" {
		pairs = append(pairs, "Filter", a.Predicate)
	}
	return propertyRows(append(pairs,
		"Created", formatSQLDate(a.CreateDate),
		"Modified", formatSQLDate(a.ModifyDate),
	)...)
}

// auditDestinationText names where the audit writes, in one column: the file
// path for a FILE audit and nothing for the two Windows log targets, whose
// destination the Destination column already says in full.
func auditDestinationText(a *gosmo.ServerAudit) string {
	if a.Type != gosmo.AuditToFile {
		return ""
	}
	return strings.TrimRight(a.LogFilePath, `\/`)
}

func auditFileSizeText(mb int64) string {
	if mb <= 0 {
		return "Unlimited"
	}
	return strconv.FormatInt(mb, 10) + " MB"
}

// auditFileCountText reads the two mutually exclusive file-count settings.
// MAX_ROLLOVER_FILES stays at its UNLIMITED sentinel even when MAX_FILES was
// the one set, so a non-zero MaxFiles is what decides which is in force —
// reading rollover first would report "Unlimited" for an audit capped at 7.
func auditFileCountText(a *gosmo.ServerAudit) string {
	if a.MaxFiles > 0 {
		return strconv.Itoa(a.MaxFiles) + " (maximum files)"
	}
	if a.MaxRolloverFiles <= 0 || a.MaxRolloverFiles == gosmo.AuditUnlimited {
		return "Unlimited (rollover)"
	}
	return strconv.Itoa(a.MaxRolloverFiles) + " (rollover)"
}

// -- Server audit specifications -------------------------------------------------

func serverAuditSpecificationsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	specs, err := sc.Server.ServerAuditSpecificationsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	specs = filterObjects(node.data.Filter, specs, func(s *gosmo.ServerAuditSpecification) nodeData {
		return nodeData{Name: s.Name, CreateDate: s.CreateDate}
	})

	rows := make([][]string, 0, len(specs))
	out := make([]nodeData, 0, len(specs))
	for _, spec := range specs {
		rows = append(rows, []string{
			spec.Name, auditNameText(spec), enabledText(spec.IsEnabled),
			strconv.Itoa(len(spec.ActionGroups)),
			formatSQLDate(spec.CreateDate), formatSQLDate(spec.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeServerAuditSpecification, Name: spec.Name, IsEnabled: spec.IsEnabled})
	}
	*objs = out
	return []string{"Name", "Audit", "State", "Action Groups", "Created", "Modified"}, rows, nil
}

func serverAuditSpecificationDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	spec, err := sc.Server.ServerAuditSpecificationByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	pairs := []string{
		"Name", spec.Name,
		"Audit", auditNameText(spec),
		"State", enabledText(spec.IsEnabled),
	}
	for i, g := range spec.ActionGroups {
		label := "Action groups"
		if i > 0 {
			label = ""
		}
		pairs = append(pairs, label, g)
	}
	return propertyRows(append(pairs,
		"Created", formatSQLDate(spec.CreateDate),
		"Modified", formatSQLDate(spec.ModifyDate),
	)...)
}

// -- Database audit specifications ------------------------------------------------

func databaseAuditSpecificationsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	specs, err := dbObj.DatabaseAuditSpecificationsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	// The folder's filter is applied to the collection, before rows are
	// built — the pane queries gosmo independently of the tree.
	specs = filterObjects(node.data.Filter, specs, func(s *gosmo.DatabaseAuditSpecification) nodeData {
		return nodeData{Name: s.Name, CreateDate: s.CreateDate}
	})

	rows := make([][]string, 0, len(specs))
	out := make([]nodeData, 0, len(specs))
	for _, spec := range specs {
		rows = append(rows, []string{
			spec.Name, databaseAuditNameText(spec), enabledText(spec.IsEnabled),
			strconv.Itoa(len(spec.ActionGroups)), strconv.Itoa(len(spec.Actions)),
			formatSQLDate(spec.CreateDate), formatSQLDate(spec.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeDatabaseAuditSpecification, DBName: node.data.DBName,
			Name: spec.Name, IsEnabled: spec.IsEnabled})
	}
	*objs = out
	return []string{"Name", "Audit", "State", "Action Groups", "Actions", "Created", "Modified"}, rows, nil
}

func databaseAuditSpecificationDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	spec, err := dbObj.DatabaseAuditSpecificationByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	pairs := []string{
		"Name", spec.Name,
		"Audit", databaseAuditNameText(spec),
		"State", enabledText(spec.IsEnabled),
	}
	for i, g := range spec.ActionGroups {
		label := "Action groups"
		if i > 0 {
			label = ""
		}
		pairs = append(pairs, label, g)
	}
	for i, a := range spec.Actions {
		label := "Actions"
		if i > 0 {
			label = ""
		}
		pairs = append(pairs, label, auditActionText(a))
	}
	return propertyRows(append(pairs,
		"Created", formatSQLDate(spec.CreateDate),
		"Modified", formatSQLDate(spec.ModifyDate),
	)...)
}

// auditActionText renders one audited action on a securable the way the ADD
// clause reads it — the securable is what distinguishes two rows that share
// an action name, so it is never left out.
func auditActionText(a gosmo.DatabaseAuditAction) string {
	principal := a.Principal
	if principal == "" {
		principal = "public"
	}
	class := a.ClassDesc
	if class == "" {
		class = "OBJECT"
	}
	return fmt.Sprintf("%s ON %s::%s BY %s", a.ActionName, class, a.FullName(), principal)
}

// databaseAuditNameText is auditNameText for a database specification: the
// same orphaning is possible, and means the same thing.
func databaseAuditNameText(spec *gosmo.DatabaseAuditSpecification) string {
	if spec.AuditName == "" {
		return "(audit no longer exists)"
	}
	return spec.AuditName
}

// auditNameText names the audit a specification writes to. Dropping an audit a
// specification still references succeeds and orphans the specification, so an
// empty name is a real state to render rather than a read that failed.
func auditNameText(spec *gosmo.ServerAuditSpecification) string {
	if spec.AuditName == "" {
		return "(audit no longer exists)"
	}
	return spec.AuditName
}

// -- Database scoped credentials ---------------------------------------------------

// databaseScopedCredentialsFolderDetail lists one database's own credentials.
// As everywhere in this file, the read is independent of the tree's, so the
// folder's filter is applied here too, over the gosmo objects before the rows
// are built.
func databaseScopedCredentialsFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	creds, err := dbObj.DatabaseScopedCredentialsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	creds = filterObjects(node.data.Filter, creds, func(c *gosmo.DatabaseScopedCredential) nodeData {
		return nodeData{Name: c.Name, CreateDate: c.CreateDate}
	})

	rows := make([][]string, 0, len(creds))
	out := make([]nodeData, 0, len(creds))
	for _, c := range creds {
		rows = append(rows, []string{
			c.Name, c.Identity,
			formatSQLDate(c.CreateDate), formatSQLDate(c.ModifyDate),
		})
		out = append(out, nodeData{Type: NodeDatabaseScopedCredential, DBName: node.data.DBName, Name: c.Name})
	}
	*objs = out
	return []string{"Name", "Identity", "Created", "Modified"}, rows, nil
}

// databaseScopedCredentialDetail is one credential's Property/Value view. The
// secret is not shown because it cannot be read — see gosmo's
// database_credential.go.
func databaseScopedCredentialDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	c, err := dbObj.DatabaseScopedCredentialByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", c.Name,
		"Database", node.data.DBName,
		"Identity", c.Identity,
		"Created", formatSQLDate(c.CreateDate),
		"Modified", formatSQLDate(c.ModifyDate),
	)
}
