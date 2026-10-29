package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// ServerCommands is the server group: its node and its commands. Its
// summary names only what exists.
func ServerCommands(d Deps) []*spec.Command {
	return slices.Concat([]*spec.Command{
		{
			Path:    []string{"server"},
			Summary: "Show the server's facts and memory, list and change its sp_configure settings and trace flags, and show the free space on the volumes holding its files",
			Class:   spec.ClassRead,
		},
		serverInfoCommand(d),
		serverMemoryCommand(d),
		serverVolumesCommand(d),
	}, serverConfigCommands(d), serverTraceFlagCommands(d))
}

// --- server volumes ---------------------------------------------------------

func serverVolumesCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"server", "volumes"},
		Summary: "List the volumes holding database files: total, free, free %, file count",
		Description: "One row per volume holding at least one database file of any database, in any state " +
			"(sys.master_files × sys.dm_os_volume_stats), by volume. volume is the mount point or drive letter, " +
			"else the OS volume label, else one file's path on it (some container hosts report neither). " +
			"db_files counts the database files on the volume. A volume whose figures cannot be right (a total " +
			"of 0, or more free than total, as Azure SQL Managed Instance reports some) is left out with a " +
			"warning. The DMV needs VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server 2022): " +
			"without it the command fails, exit 9. Warnings: a volume under 10 % free.",
		Options: append(ConnectOptions(), unitOption()),
		Class:   spec.ClassRead,
		Run:     d.serverVolumes,
		Examples: []string{
			"gossms server volumes -p dev",
			"gossms server volumes -p dev --unit gb -o json",
		},
	}
}

// volumeRow is one volume server volumes reports.
type volumeRow struct {
	Name           string
	TotalBytes     int64
	AvailableBytes int64
	Files          int
}

// volumesDenied is server volumes' refusal without the DMV's permission.
func volumesDenied(msg string) *output.Error {
	return output.NewError(output.CodePermissionDenied, msg,
		"sys.dm_os_volume_stats needs VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE from SQL Server "+
			"2022). Use a login that has it, or have it granted.")
}

func (d Deps) serverVolumes(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, err := d.connectCatalog(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	vols, visible, err := cat.FileVolumes(ctx)
	switch {
	case err != nil && gosmo.IsPermissionDenied(err):
		return res, volumesDenied(err.Error())
	case err != nil:
		return res, readError(err)
	case !visible:
		return res, volumesDenied("the login cannot read sys.dm_os_volume_stats")
	}
	rows, dropped := groupVolumes(vols)
	res.Sets = []output.Set{volumeSet(rows, unitOf(inv))}
	if dropped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%d database file(s) left out: the host reported their volume with a total of 0 or more free than total.", dropped))
	}
	for _, r := range rows {
		if free, ok := pctOf(r.AvailableBytes, r.TotalBytes); ok && free < warnVolumePct {
			res.Warnings = append(res.Warnings, fmt.Sprintf("volume %s: %s %% free (under %g %%).", r.Name, mbf(free), warnVolumePct))
		}
	}
	return res, nil
}

// groupVolumes is vols, one row per file, as one row per volume in volume
// order, and the number of files left out for implausible figures. A volume
// is its mount point and label, as the TUI dedups them; one the host names
// neither way is its figures, as Server.DiskVolumes groups it, and shows the
// lowest file path on it (lessPath). The figures are the first file's: a volume's free
// space can move between two files' reads.
func groupVolumes(vols []gosmo.FileVolume) (rows []volumeRow, dropped int) {
	byKey := map[string]int{}
	for _, v := range vols {
		if !v.Plausible() {
			dropped++
			continue
		}
		key := v.MountPoint + "\x00" + v.VolumeName
		if key == "\x00" {
			key += "\x00" + strconv.FormatInt(v.TotalBytes, 10) + "\x00" + strconv.FormatInt(v.AvailableBytes, 10)
		}
		i, ok := byKey[key]
		if !ok {
			i = len(rows)
			byKey[key] = i
			rows = append(rows, volumeRow{Name: cmp.Or(v.MountPoint, v.VolumeName, v.PhysicalName),
				TotalBytes: v.TotalBytes, AvailableBytes: v.AvailableBytes})
		}
		if v.MountPoint == "" && v.VolumeName == "" && lessPath(v.PhysicalName, rows[i].Name) {
			rows[i].Name = v.PhysicalName
		}
		rows[i].Files++
	}
	slices.SortStableFunc(rows, func(a, b volumeRow) int { return cmp.Compare(a.Name, b.Name) })
	return rows, dropped
}

// lessPath orders file paths as DiskVolumes' MIN(physical_name) does under
// a case-insensitive collation, the usual server one: by folded case, then
// bytes. Byte order alone shows a different file than the TUI on Linux
// (HealthClinic.mdf before backup_test.mdf).
func lessPath(a, b string) bool {
	return cmp.Or(cmp.Compare(strings.ToLower(a), strings.ToLower(b)), cmp.Compare(a, b)) < 0
}

// volumeSet is rows as server volumes' result set, sizes in u: §10.8's
// columns.
func volumeSet(rows []volumeRow, u sizeUnit) output.Set {
	set := output.Set{Columns: []output.Column{
		{Name: "volume", Type: "nvarchar(260)"},
		u.column("total", false), u.column("free", false), pctColumn("free_pct", false),
		{Name: "db_files", Type: "int"},
	}}
	for _, r := range rows {
		set.Rows = append(set.Rows, []*string{ptr(r.Name),
			u.cell(r.TotalBytes / 1024), u.cell(r.AvailableBytes / 1024), pctCell(r.AvailableBytes, r.TotalBytes),
			ptr(strconv.Itoa(r.Files))})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
