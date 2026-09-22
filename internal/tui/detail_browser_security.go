package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_security.go is the Detail Browser's view of the server-level
// Security families that are not logins — Credentials, Cryptographic
// Providers, Audits and Server Audit Specifications — plus a database's own
// Audit Specifications, Scoped Credentials, Certificates and asymmetric and
// symmetric keys, which share their rendering.
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

// -- Certificates --------------------------------------------------------------

// certificatesFolderDetail lists one database's certificates. Expiry is a
// column rather than a label suffix here, so the "(Expired)" the tree adds is
// the Expired column instead.
func certificatesFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	certs, err := dbObj.CertificatesContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	certs = filterObjects(node.data.Filter, certs, func(c *gosmo.Certificate) nodeData {
		return nodeData{Name: c.Name}
	})

	now := time.Now()
	rows := make([][]string, 0, len(certs))
	out := make([]nodeData, 0, len(certs))
	for _, c := range certs {
		rows = append(rows, []string{
			c.Name, c.Subject, formatSQLDate(c.ExpiryDate),
			yesNo(certificateExpired(c, now)), privateKeyText(c.PvtKeyEncryptionType),
		})
		out = append(out, nodeData{Type: NodeCertificate, DBName: node.data.DBName, Name: c.Name})
	}
	*objs = out
	return []string{"Name", "Subject", "Expiry", "Expired", "Private key"}, rows, nil
}

// certificateDetail is one certificate's Property/Value view. A certificate
// dropped since the tree was read is an error from findCertificate, not a nil.
func certificateDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	c, err := findCertificate(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	backup := formatSQLDate(c.PvtKeyLastBackupDate)
	if backup == "" {
		backup = "Never"
	}
	return propertyRows(
		"Name", c.Name,
		"Database", node.data.DBName,
		"Owner", c.Owner,
		"Subject", c.Subject,
		"Issuer", c.IssuerName,
		"Serial number", c.SerialNumber,
		"Valid from", formatSQLDate(c.StartDate),
		"Expiry", formatSQLDate(c.ExpiryDate),
		"Expired", yesNo(certificateExpired(c, time.Now())),
		"Key length", keyLengthText(c.KeyLength),
		"Thumbprint", hexPreview(c.Thumbprint),
		"Private key", privateKeyText(c.PvtKeyEncryptionType),
		"Private key last backed up", backup,
		"Active for BEGIN_DIALOG", yesNo(c.IsActiveForBeginDialog),
		"Attested by", c.AttestedBy,
	)
}

// privateKeyText renders a certificate's or asymmetric key's
// pvt_key_encryption_type_desc as a reader would say it. An unrecognised value
// is shown as the server wrote it.
func privateKeyText(desc string) string {
	switch desc {
	case "NO_PRIVATE_KEY", "":
		return "None"
	case "ENCRYPTED_BY_MASTER_KEY":
		return "Encrypted by master key"
	case "ENCRYPTED_BY_PASSWORD":
		return "Encrypted by password"
	}
	return desc
}

// -- Asymmetric Keys -----------------------------------------------------------

// asymmetricKeysFolderDetail lists one database's asymmetric keys.
func asymmetricKeysFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	keys, err := dbObj.AsymmetricKeysContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys = filterObjects(node.data.Filter, keys, func(k *gosmo.AsymmetricKey) nodeData {
		return nodeData{Name: k.Name}
	})

	rows := make([][]string, 0, len(keys))
	out := make([]nodeData, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{
			k.Name, k.Algorithm, keyLengthText(k.KeyLength), privateKeyText(k.PvtKeyEncryptionType),
		})
		out = append(out, nodeData{Type: NodeAsymmetricKey, DBName: node.data.DBName, Name: k.Name})
	}
	*objs = out
	return []string{"Name", "Algorithm", "Length", "Private key"}, rows, nil
}

// asymmetricKeyDetail is one asymmetric key's Property/Value view.
func asymmetricKeyDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	k, err := findAsymmetricKey(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	return propertyRows(
		"Name", k.Name,
		"Database", node.data.DBName,
		"Owner", k.Owner,
		"Algorithm", k.Algorithm,
		"Key length", keyLengthText(k.KeyLength),
		"Thumbprint", hexPreview(k.Thumbprint),
		"Private key", privateKeyText(k.PvtKeyEncryptionType),
		"Provider", asymmetricKeyProviderText(k),
		"Attested by", k.AttestedBy,
	)
}

// keyLengthText is a key length in bits, or empty when the catalog had none.
func keyLengthText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// asymmetricKeyProviderText says where the key lives: provider_type is set
// only for a key an EKM provider holds.
func asymmetricKeyProviderText(k *gosmo.AsymmetricKey) string {
	if k.ProviderType == "" {
		return "SQL Server"
	}
	return k.ProviderType
}

// -- Symmetric Keys ------------------------------------------------------------

// symmetricKeysFolderDetail lists one database's symmetric keys, under a first
// row that says whether the database has a master key. The master key gets no
// node of its own (a later item), yet it is what every certificate- or
// asymmetric-key-protected symmetric key rests on, and whether the New
// dialogs will ask for its password — so its presence is shown here, where
// the keys it protects are listed. The row is not an object: its rowObjs
// entry is the zero nodeData, which no object operation claims, so selecting
// it offers nothing rather than something that acts on the wrong row.
func symmetricKeysFolderDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode, objs *[]nodeData) ([]string, [][]string, error) {
	dbObj, err := sc.Server.DatabaseByNameContext(ctx, node.data.DBName)
	if err != nil {
		return nil, nil, err
	}
	keys, err := dbObj.SymmetricKeysContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys = filterObjects(node.data.Filter, keys, func(k *gosmo.SymmetricKey) nodeData {
		return nodeData{Name: k.Name, CreateDate: k.CreateDate}
	})

	rows := make([][]string, 0, len(keys)+1)
	out := make([]nodeData, 0, len(keys)+1)
	rows = append(rows, []string{"Database master key: " + masterKeyText(ctx, dbObj), "", "", "", ""})
	out = append(out, nodeData{})
	for _, k := range keys {
		rows = append(rows, []string{
			k.Name, k.Algorithm, keyLengthText(k.KeyLength), formatSQLDate(k.CreateDate),
			symmetricKeyEncryptionsText(k.Encryptions),
		})
		out = append(out, nodeData{Type: NodeSymmetricKey, DBName: node.data.DBName, Name: k.Name})
	}
	*objs = out
	return []string{"Name", "Algorithm", "Length", "Created", "Encrypted by"}, rows, nil
}

// masterKeyText is "present" or "absent", or "unknown" when the check itself
// failed — the key list is still worth showing then. See gosmo's HasMasterKey
// for the one master key a low-privilege principal still reads as absent.
func masterKeyText(ctx context.Context, d *gosmo.Database) string {
	has, err := d.HasMasterKeyContext(ctx)
	switch {
	case err != nil:
		return "unknown"
	case has:
		return "present"
	}
	return "absent"
}

// symmetricKeyDetail is one symmetric key's Property/Value view, one
// "Encrypted by" row per encryption.
func symmetricKeyDetail(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	k, err := findSymmetricKey(ctx, sc, node.data.DBName, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	kv := []string{
		"Name", k.Name,
		"Database", node.data.DBName,
		"Owner", k.Owner,
		"Algorithm", k.Algorithm,
		"Key length", keyLengthText(k.KeyLength),
		"Key GUID", k.KeyGUID,
		"Created", formatSQLDate(k.CreateDate),
		"Modified", formatSQLDate(k.ModifyDate),
		"Provider", symmetricKeyProviderText(k),
	}
	for _, e := range k.Encryptions {
		kv = append(kv, "Encrypted by", symmetricKeyEncryptionText(e))
	}
	return propertyRows(kv...)
}

// symmetricKeyEncryptionText names one encryption as a reader would say it:
// "Certificate claims_cert", "Password". An encryptor the caller cannot see
// has no name to show, and an unrecognised crypt_type_desc is shown as the
// server wrote it.
func symmetricKeyEncryptionText(e gosmo.SymmetricKeyEncryption) string {
	var kind string
	switch e.Kind {
	case gosmo.SymmetricKeyByCertificate:
		kind = "Certificate"
	case gosmo.SymmetricKeyByAsymmetricKey:
		kind = "Asymmetric key"
	case gosmo.SymmetricKeyBySymmetricKey:
		kind = "Symmetric key"
	case gosmo.SymmetricKeyByPassword:
		return "Password"
	case gosmo.SymmetricKeyByMasterKey:
		return "Master key"
	default:
		return e.CryptTypeDesc
	}
	if e.Name == "" {
		return kind + " (not visible)"
	}
	return kind + " " + e.Name
}

// symmetricKeyEncryptionsText is every encryption of a key on one line, for
// the folder list.
func symmetricKeyEncryptionsText(es []gosmo.SymmetricKeyEncryption) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = symmetricKeyEncryptionText(e)
	}
	return strings.Join(parts, ", ")
}

// symmetricKeyProviderText is asymmetricKeyProviderText for a symmetric key.
func symmetricKeyProviderText(k *gosmo.SymmetricKey) string {
	if k.ProviderType == "" {
		return "SQL Server"
	}
	return k.ProviderType
}
