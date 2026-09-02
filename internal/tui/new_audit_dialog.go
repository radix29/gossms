package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_audit_dialog.go is the New Audit creation dialog (Object Explorer's
// Security > Audits folder, "New Audit..."), built on newObjectDialog like
// every other New-X.
//
// The audit is created disabled, which is what CREATE SERVER AUDIT does and
// what SSMS's own dialog produces; Enable Audit on the new node turns it on.

// nauditPrefetch holds what the dialog needs before it opens: the existing
// audit names for the uniqueness preflight, and the server's default backup
// directory, which is the only server-side path goSSMS knows and so the least
// wrong place to point the file browser at first.
type nauditPrefetch struct {
	existingNames map[string]bool
	defaultDir    string
}

func fetchNewAuditPrefetch(ctx context.Context, sc *db.ServerConn) (*nauditPrefetch, error) {
	audits, err := sc.Server.ServerAuditsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(audits))
	for _, a := range audits {
		existing[strings.ToLower(a.Name)] = true
	}
	return &nauditPrefetch{existingNames: existing, defaultDir: sc.Server.Info().DefaultBackupPath}, nil
}

// NewAuditDialog is the New Audit creation dialog.
type NewAuditDialog struct {
	newObjectDialog[nauditPrefetch]
}

// NewNewAuditDialog creates the dialog and wires its callbacks.
func NewNewAuditDialog(app *App) *NewAuditDialog {
	d := &NewAuditDialog{}
	d.init(app, newObjectConfig[nauditPrefetch]{
		title:   "New Audit",
		noun:    "Audit",
		pages:   []string{"General"},
		fetch:   fetchNewAuditPrefetch,
		build:   d.buildPages,
		refresh: func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeAudits) },
	})
	return d
}

// auditDestinationItems and auditDestinationValues are one table split in two,
// the same pairing rule as audit_props.go's on-failure pair: item i means
// value i, and new_audit_dialog_test.go pins it by name.
var (
	auditDestinationItems  = []string{"File", "Application Log", "Security Log"}
	auditDestinationValues = []string{
		gosmo.AuditToFile,
		gosmo.AuditToApplicationLog,
		gosmo.AuditToSecurityLog,
	}
)

func (d *NewAuditDialog) buildPages(pf *nauditPrefetch) {
	sc := d.sc

	nameField := propsheet.Text("Audit name", "", 40)
	delayField := propsheet.Int("Queue delay", 1000, 0, 2147483647, "ms")
	failureField := propsheet.Select("On audit log failure", auditFailureItems, 0)
	destField := propsheet.Select("Audit destination", auditDestinationItems, 0)
	pathField := propsheet.Text("File path", pf.defaultDir, 50)
	sizeField := propsheet.Int("Maximum file size", 0, 0, 2147483647, "MB (0 = unlimited)")
	countKindField := propsheet.Select("File count limit", auditFileCountItems, auditFileCountUnlimited)
	countField := propsheet.Int("Number of files", 0, 0, 2147483647, "")
	reserveField := propsheet.Check("Reserve disk space", false)
	predicateField := propsheet.Text("Filter predicate", "", 60)

	d.forms[0] = propsheet.NewForm(
		propsheet.Section("Audit"),
		nameField,
		delayField,
		failureField,
		propsheet.Section("Audit destination"),
		destField,
		pathField,
		sizeField,
		countKindField,
		countField,
		reserveField,
		propsheet.Note("The file settings apply to a File audit only, and the path is a directory resolved on the SQL Server host, which the service account must be able to write. A destination cannot be changed usefully afterwards, so pick it here."),
		propsheet.Section("Filter"),
		predicateField,
		propsheet.Note("The WHERE clause the audit filters on, without the keyword. The audit is created disabled — use Enable Audit on the new node to start it."),
	)
	d.objectName = func() string { return strings.TrimSpace(nameField.Value()) }
	d.preflight = func() error {
		name := d.objectName()
		if name == "" {
			return fmt.Errorf("audit name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("an audit named %q already exists", name)
		}
		if auditDestinationValues[destField.Selected()] == gosmo.AuditToFile &&
			strings.TrimSpace(pathField.Value()) == "" {
			return fmt.Errorf("a file audit needs a file path")
		}
		for _, f := range []*propsheet.TextRow{delayField, sizeField, countField} {
			if _, err := f.IntValue(); err != nil {
				return fmt.Errorf("%s: must be a whole number", f.Label())
			}
		}
		return nil
	}
	d.applyFns[0] = func(ctx context.Context) error {
		delay, _ := delayField.IntValue()
		spec := gosmo.ServerAuditSpec{
			Name:       d.objectName(),
			Type:       auditDestinationValues[destField.Selected()],
			QueueDelay: int(delay),
			OnFailure:  auditFailureValues[failureField.Selected()],
			Predicate:  strings.TrimSpace(predicateField.Value()),
		}
		if spec.Type == gosmo.AuditToFile {
			size, _ := sizeField.IntValue()
			count, _ := countField.IntValue()
			spec.FilePath = strings.TrimSpace(pathField.Value())
			spec.MaxFileSize = size
			spec.ReserveDiskSpace = reserveField.Checked()
			switch countKindField.Selected() {
			case auditFileCountMax:
				spec.MaxFiles = int(count)
			case auditFileCountRollover:
				spec.MaxRolloverFiles = int(count)
			default:
				spec.MaxRolloverFiles = gosmo.AuditUnlimited
			}
		}
		_, err := sc.Server.CreateServerAuditContext(ctx, spec)
		return err
	}
}
