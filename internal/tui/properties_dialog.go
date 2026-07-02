package tui

import (
	"fmt"

	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// PropertyRow re-exports the tuikit dialogs.PropertyRow type so application
// code can refer to tui.PropertyRow without importing tuikit directly.
type PropertyRow = dialogs.PropertyRow

// PropertiesDialog wraps tuikit/dialogs.PropertiesDialog with the
// SQL-Server-specific data loading needed for Server and Database
// properties views.
type PropertiesDialog struct {
	*dialogs.PropertiesDialog
}

// NewPropertiesDialog creates a generic properties dialog.
func NewPropertiesDialog(app *App) *PropertiesDialog {
	return &PropertiesDialog{PropertiesDialog: dialogs.NewPropertiesDialog(app.screen)}
}

// ShowServerProperties populates and shows server properties for connIdx.
func (d *PropertiesDialog) ShowServerProperties(app *App, connIdx int) {
	if connIdx < 0 || connIdx >= len(app.connections) {
		d.ShowProperties("Server Properties", []PropertyRow{
			{Key: "Status", Value: "Not connected"},
		})
		return
	}
	sc := app.connections[connIdx]
	info := sc.Server.Info()
	d.ShowProperties("Server Properties", []PropertyRow{
		{Key: "Server", Value: sc.Opts.Server},
		{Key: "Version", Value: info.ProductVersion},
		{Key: "Edition", Value: info.Edition},
		{Key: "OS Version", Value: info.OSVersion},
		{Key: "Collation", Value: info.Collation},
		{Key: "Data Path", Value: info.DefaultDataPath},
		{Key: "Log Path", Value: info.DefaultLogPath},
		{Key: "Backup Path", Value: info.DefaultBackupPath},
		{Key: "Encrypt", Value: fmt.Sprintf("%v", sc.Opts.Encrypt)},
		{Key: "Trust Server Cert", Value: fmt.Sprintf("%v", sc.Opts.TrustServerCertificate)},
	})
}

// ShowDatabaseProperties populates and shows database properties.
func (d *PropertiesDialog) ShowDatabaseProperties(app *App, connIdx int, dbName string) {
	if connIdx < 0 || connIdx >= len(app.connections) {
		d.ShowProperties("Database Properties", []PropertyRow{
			{Key: "Status", Value: "Not connected"},
		})
		return
	}
	sc := app.connections[connIdx]
	dbInfo, err := sc.Server.DatabaseByName(dbName)
	if err != nil {
		d.ShowProperties(fmt.Sprintf("Database: %s", dbName), []PropertyRow{
			{Key: "Error", Value: err.Error()},
		})
		return
	}
	sizeStr := "N/A"
	if space, err := dbInfo.SpaceUsed(); err == nil {
		sizeStr = fmt.Sprintf("%.2f", space.TotalMB)
	}
	d.ShowProperties(fmt.Sprintf("Database: %s", dbName), []PropertyRow{
		{Key: "Name", Value: dbInfo.Name()},
		{Key: "State", Value: dbInfo.State()},
		{Key: "Recovery Model", Value: string(dbInfo.RecoveryModel())},
		{Key: "Compatibility Level", Value: fmt.Sprintf("%d", dbInfo.CompatibilityLevel())},
		{Key: "Create Date", Value: formatSQLDate(dbInfo.CreateDate())},
		{Key: "Collation", Value: dbInfo.Collation()},
		{Key: "Read Only", Value: fmt.Sprintf("%v", dbInfo.IsReadOnly())},
		{Key: "Size (MB)", Value: sizeStr},
	})
}

// ShowGenericProperties shows arbitrary key-value pairs (e.g. About box).
func (d *PropertiesDialog) ShowGenericProperties(title string, rows []PropertyRow) {
	d.ShowProperties(title, rows)
}
