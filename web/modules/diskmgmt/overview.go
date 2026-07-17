package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nas-panel/common"
)

// StorageOverview is the top-level dashboard data for the storage page
type StorageOverview struct {
	TotalCapacity string         `json:"total_capacity"`
	TotalUsed     string         `json:"total_used"`
	TotalAvail    string         `json:"total_avail"`
	UsedPercent   string         `json:"used_percent"`
	Pools         []PoolSummary  `json:"pools"`          // 存储空间(含嵌套磁盘和文件夹)
	SystemDisks   []DiskSummary  `json:"system_disks"`   // 系统盘
	FreeDisks     []DiskSummary  `json:"free_disks"`     // 空闲盘(未配置)
	RAIDHealth    []RAIDStatus   `json:"raid_health,omitempty"`
	Unconfigured  int            `json:"unconfigured_count"`
	HasIssues     bool           `json:"has_issues"`
	Issues        []string       `json:"issues,omitempty"`
}

// PoolSummary represents one storage space with nested disks and folders
type PoolSummary struct {
	Name        string           `json:"name"`
	DisplayName string           `json:"display_name"`
	MountPoint  string           `json:"mountpoint"`
	Device      string           `json:"device"`
	Type        string           `json:"type"`
	RaidLevel   string           `json:"raid_level"`
	Size        string           `json:"size"`
	Used        string           `json:"used"`
	Avail       string           `json:"avail"`
	Percent     string           `json:"percent"`
	FSType      string           `json:"fstype"`
	Healthy     bool             `json:"healthy"`
	Disks       []DiskSummary    `json:"disks,omitempty"`       // 属于这个空间的磁盘
	Folders     []SharedFolder   `json:"folders,omitempty"`     // 这个空间下的共享文件夹
}

// DiskSummary is a flat disk info for the overview
type DiskSummary struct {
	Device     string            `json:"device"`     // /dev/sdb
	Friendly   string            `json:"friendly"`   // 磁盘 1
	Size       string            `json:"size"`
	Interface  string            `json:"interface"`  // SATA, NVMe, VirtIO
	Rotational string            `json:"rotational"` // 0=SSD 1=HDD
	Model      string            `json:"model"`
	Temp       string            `json:"temp"`
	Smart      string            `json:"smart"`
	Status     string            `json:"status"` // system, data, unused, pool_member
	Pool       string            `json:"pool,omitempty"` // which pool it belongs to
	Partitions []PartitionInfo  `json:"partitions,omitempty"` // 分区/卷列表
}

// PartitionInfo represents a partition or volume on a disk
type PartitionInfo struct {
	Name       string `json:"name"`       // nvme0n1p2
	Device     string `json:"device"`     // /dev/nvme0n1p2
	Size       string `json:"size"`       // 229.6G
	FSType     string `json:"fstype"`     // ext4, xfs, swap
	Mountpoint string `json:"mountpoint"` // /, /boot/efi
	Status     string `json:"status"`     // system, data, swap, unused
}

// RAIDStatus represents RAID array health
type RAIDStatus struct {
	Device   string `json:"device"`   // /dev/md0
	Level    string `json:"level"`    // 1
	State    string `json:"state"`    // active, degraded, resyncing
	Disks    int    `json:"disks"`
	ActiveDisks int `json:"active_disks"`
	SpareDisks  int `json:"spare_disks"`
	SyncPercent string `json:"sync_percent,omitempty"` // "45%"
}

// handleStorageOverview returns the complete storage dashboard data
func handleStorageOverview(w http.ResponseWriter, r *http.Request) {
	overview := StorageOverview{
		Pools:       []PoolSummary{},
		SystemDisks: []DiskSummary{},
		FreeDisks:   []DiskSummary{},
	}

	// 1. Get all data mounts (pools)
	mounts := getExistingDataMounts()
	poolSeq := 0
	mountDisplay := make(map[string]string) // mount path → display name
	for _, m := range mounts {
		poolSeq++
		mountPoint := m["mount"]
		poolName := extractPoolName(mountPoint)
		displayName := fmt.Sprintf("存储空间%d", poolSeq)
		mountDisplay[mountPoint] = displayName
		ps := PoolSummary{
			Name:        poolName,
			DisplayName: displayName,
			MountPoint:  mountPoint,
			Device:      m["device"],
			Size:        m["size"],
			Used:        m["used"],
			Avail:       m["avail"],
			Percent:     m["percent"],
			Healthy:     true,
		}
		ps.Type, ps.RaidLevel = detectPoolTypeEx(m["device"], mountPoint)
		fsOut, _ := common.ExecOutput("findmnt", "-n", "-o", "FSTYPE", "--source", m["device"])
		ps.FSType = strings.TrimSpace(fsOut)
		if ps.Type == "raid1" || ps.Type == "raid0" || ps.Type == "raid5" || ps.Type == "raid6" {
			for _, ra := range getRAIDStatus() {
				if strings.Contains(m["device"], ra.Device) {
					ps.Healthy = ra.State == "active" || ra.State == "clean"
					if ra.SyncPercent != "" {
						ps.Healthy = false
					}
				}
			}
		}
		overview.Pools = append(overview.Pools, ps)
	}

	// 2. Get all shared folders, grouped by mount point
	folderMap := make(map[string][]SharedFolder) // mount → folders
	{
		smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
		smbMap := parseSambaShares(smbConf)
		for _, m := range mounts {
			mountPoint := m["mount"]
			entries, err := os.ReadDir(mountPoint)
			if err != nil {
				out, _ := common.SudoOutput("ls", "-1", mountPoint)
				for _, name := range strings.Split(out, "\n") {
					name = strings.TrimSpace(name)
					if name == "" || name == "#recycle" {
						continue
					}
					folderMap[mountPoint] = append(folderMap[mountPoint], SharedFolder{
						Name:  name,
						Pool:  mountDisplay[mountPoint],
						Path:  mountPoint + "/" + name,
					})
				}
			} else {
				for _, entry := range entries {
					if !entry.IsDir() || entry.Name() == "#recycle" {
						continue
					}
					folderPath := filepath.Join(mountPoint, entry.Name())
					f := SharedFolder{
						Name:  entry.Name(),
						Pool:  mountDisplay[mountPoint],
						Path:  folderPath,
					}
					sizeOut, _ := common.SudoOutput("du", "-sh", folderPath)
					f.Size = parseDuSize(sizeOut)
					if smb, ok := smbMap[folderPath]; ok {
						f.SambaShare = true
						if smb["read_only"] == "yes" {
							f.Permission = "readonly"
						} else {
							f.Permission = "readwrite"
						}
						f.ValidUsers = smb["valid_users"]
						f.RecycleBin = hasRecycleBin(smbConf, entry.Name())
					} else {
						f.Permission = "noaccess"
					}
					folderMap[mountPoint] = append(folderMap[mountPoint], f)
				}
			}
		}
	}
	// Assign folders to pools
	for i := range overview.Pools {
		mp := overview.Pools[i].MountPoint
		if folders, ok := folderMap[mp]; ok {
			overview.Pools[i].Folders = folders
		}
	}

	// 3. Get disk list, assign to pools / system / free
	disks := getDiskStatus()
	diskID := 0
	for _, d := range disks {
		if d.Name == "sr0" || d.Name == "zram0" || strings.HasPrefix(d.Name, "loop") || strings.HasPrefix(d.Name, "ram") {
			continue
		}
		diskID++
		isSystem := isSystemDisk(d.Device)
		realStatus := d.Type
		if isSystem {
			realStatus = "system"
		}
		ds := DiskSummary{
			Device:     d.Device,
			Friendly:   fmt.Sprintf("磁盘 %d", diskID),
			Size:       d.Size,
			Interface:  d.Interface,
			Rotational: d.Rotational,
			Model:      d.Model,
			Temp:       d.Temp,
			Smart:      d.Smart,
			Status:     realStatus,
		}
		// Build partition list
		for _, c := range d.Children {
			pi := PartitionInfo{
				Name:       c.Name,
				Device:     c.Device,
				Size:       c.Size,
				FSType:     c.FSType,
				Mountpoint: c.Mountpoint,
				Status:     c.Type,
			}
			if c.FSType == "swap" {
				pi.Status = "swap"
				pi.Mountpoint = "[SWAP]"
			}
			ds.Partitions = append(ds.Partitions, pi)
		}

		// Assign disk to correct bucket
		if isSystem {
			overview.SystemDisks = append(overview.SystemDisks, ds)
		} else if realStatus == "unused" {
			overview.FreeDisks = append(overview.FreeDisks, ds)
			overview.Unconfigured++
		} else {
			// Data disk — find which pool it belongs to
			poolDisplayName := findDiskPoolDisplay(d.Device, mounts, mountDisplay)
			ds.Pool = poolDisplayName
			// Add to the pool's disk list
			for i := range overview.Pools {
				if overview.Pools[i].DisplayName == poolDisplayName {
					overview.Pools[i].Disks = append(overview.Pools[i].Disks, ds)
					break
				}
			}
		}

		if d.Smart == "FAILED" {
			overview.HasIssues = true
			overview.Issues = append(overview.Issues, fmt.Sprintf("%s (%s) SMART 检测失败", ds.Friendly, d.Device))
		}
	}

	// 3. Calculate total capacity from mounts
	totalSize, totalUsed, totalAvail := sumMountSizes(mounts)
	overview.TotalCapacity = totalSize
	overview.TotalUsed = totalUsed
	overview.TotalAvail = totalAvail
	overview.UsedPercent = calcPercent(totalSize, totalUsed)

	// 4. RAID health
	raidArrays := getRAIDStatus()
	overview.RAIDHealth = raidArrays
	for _, ra := range raidArrays {
		if ra.State != "active" && ra.State != "clean" {
			overview.HasIssues = true
			if ra.SyncPercent != "" {
				overview.Issues = append(overview.Issues, fmt.Sprintf("RAID %s 正在同步 %s", ra.Device, ra.SyncPercent))
			} else {
				overview.Issues = append(overview.Issues, fmt.Sprintf("RAID %s 状态: %s", ra.Device, ra.State))
			}
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"overview": overview,
	})
}

// extractPoolName gets a friendly name from mount path
func extractPoolName(mount string) string {
	if mount == "/data" {
		return "data"
	}
	if strings.HasPrefix(mount, "/data/nas") {
		return strings.TrimPrefix(mount, "/data/")
	}
	return filepath.Base(mount)
}

// detectPoolType determines if a pool is single, lvm, raid1, or separate
func detectPoolType(device, mount string) string {
	t, _ := detectPoolTypeEx(device, mount)
	return t
}

// detectPoolTypeEx returns (type, raidLevel)
func detectPoolTypeEx(device, mount string) (string, string) {
	// LVM: /dev/vg_nas/data or /dev/mapper/...
	if strings.Contains(device, "vg_") || strings.Contains(device, "mapper/") || strings.Contains(device, "/vg") {
		return "lvm", ""
	}
	// RAID: /dev/md*
	if strings.Contains(device, "/dev/md") {
		// Read actual level
		mdstat, _ := os.ReadFile("/proc/mdstat")
		mdName := strings.TrimPrefix(device, "/dev/")
		for _, line := range strings.Split(string(mdstat), "\n") {
			if strings.HasPrefix(line, mdName) {
				if strings.Contains(line, "raid1") {
					return "raid1", "1"
				}
				if strings.Contains(line, "raid0") {
					return "raid0", "0"
				}
				if strings.Contains(line, "raid5") {
					return "raid5", "5"
				}
			}
		}
		return "raid1", "1" // default
	}
	// Separate mount points (nas2, nas3...) = separate mode
	if strings.HasPrefix(mount, "/data/nas") && mount != "/data/nas1" {
		return "separate", ""
	}
	return "single", ""
}

// findDiskPoolDisplay returns the display name of the pool a disk belongs to
func findDiskPoolDisplay(device string, mounts []map[string]string, mountDisplay map[string]string) string {
	for _, m := range mounts {
		// Direct mount
		if m["device"] == device {
			if disp, ok := mountDisplay[m["mount"]]; ok {
				return disp
			}
			return extractPoolName(m["mount"])
		}
		// Check LVM PV → VG → LV
		pvOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name,vg_name")
		for _, line := range strings.Split(pvOut, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 2 && fields[0] == device {
				vgName := fields[1]
				if strings.Contains(m["device"], vgName) {
					if disp, ok := mountDisplay[m["mount"]]; ok {
						return disp
					}
					return extractPoolName(m["mount"])
				}
			}
		}
		// Check RAID members
		if strings.HasPrefix(m["device"], "/dev/md") {
			scanOut, _ := common.SudoOutput("/usr/sbin/mdadm", "--detail", m["device"])
			if strings.Contains(scanOut, device) {
				if disp, ok := mountDisplay[m["mount"]]; ok {
					return disp
				}
				return extractPoolName(m["mount"])
			}
		}
	}
	return ""
}

// findDiskPool checks which pool/mount a disk belongs to
func findDiskPool(device string, mounts []map[string]string) string {
	for _, m := range mounts {
		// Direct mount
		if m["device"] == device {
			return extractPoolName(m["mount"])
		}
		// Check LVM PV → VG → LV
		// /dev/sdb is a PV in vg_nas, /dev/vg_nas/data is the mount device
		pvOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name,vg_name")
		for _, line := range strings.Split(pvOut, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 2 && fields[0] == device {
				// This disk is PV in VG fields[1]
				vgName := fields[1]
				// Check if mount device contains this VG
				if strings.Contains(m["device"], vgName) {
					return extractPoolName(m["mount"])
				}
			}
		}
		// Check RAID members
		if strings.HasPrefix(m["device"], "/dev/md") {
			mdName := strings.TrimPrefix(m["device"], "/dev/")
			scanOut, _ := common.SudoOutput("/usr/sbin/mdadm", "--detail", m["device"])
			if strings.Contains(scanOut, device) {
				return extractPoolName(m["mount"])
			}
			_ = mdName // suppress unused
		}
	}
	return ""
}

// sumMountSizes aggregates size info from all data mounts
func sumMountSizes(mounts []map[string]string) (total, used, avail string) {
	// Parse sizes (format like "50G", "1.5T", "500M")
	var totalBytes, usedBytes, availBytes float64
	for _, m := range mounts {
		totalBytes += parseSizeToGB(m["size"])
		usedBytes += parseSizeToGB(m["used"])
		availBytes += parseSizeToGB(m["avail"])
	}
	return formatSize(totalBytes), formatSize(usedBytes), formatSize(availBytes)
}

// parseSizeToGB converts size string (50G, 1.5T, 500M) to GB float
func parseSizeToGB(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var num float64
	var unit string
	fmt.Sscanf(s, "%f%s", &num, &unit)
	switch strings.ToUpper(unit) {
	case "T":
		return num * 1024
	case "G":
		return num
	case "M":
		return num / 1024
	case "K":
		return num / (1024 * 1024)
	}
	return num
}

// formatSize converts GB float to human-readable string
func formatSize(gb float64) string {
	if gb >= 1024 {
		return fmt.Sprintf("%.1fT", gb/1024)
	}
	if gb >= 1 {
		return fmt.Sprintf("%.0fG", gb)
	}
	return fmt.Sprintf("%.0fM", gb*1024)
}

// calcPercent calculates used percentage
func calcPercent(total, used string) string {
	t := parseSizeToGB(total)
	u := parseSizeToGB(used)
	if t <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.0f", (u/t)*100)
}

// getRAIDStatus reads mdadm RAID arrays
func getRAIDStatus() []RAIDStatus {
	var arrays []RAIDStatus
	// List all md devices
	out, err := common.ExecOutput("ls", "/dev/md*")
	if err != nil {
		return arrays
	}
	for _, dev := range strings.Fields(out) {
		if !strings.HasPrefix(dev, "/dev/md") {
			continue
		}
		// Skip partitions like /dev/md0p1
		if strings.Contains(dev, "p") && !strings.HasSuffix(dev, "0") {
			// might be partition, skip for now
			if !regexp_match(`^/dev/md\d+$`, dev) {
				continue
			}
		}
		ra := RAIDStatus{Device: dev}
		// Read /proc/mdstat for this device
		mdstat, _ := os.ReadFile("/proc/mdstat")
		mdName := strings.TrimPrefix(dev, "/dev/")
		for _, line := range strings.Split(string(mdstat), "\n") {
			if strings.HasPrefix(line, mdName) {
				// Parse: "md0 : active raid1 sdb[0] sdc[1]"
				fields := strings.Fields(line)
				if len(fields) >= 4 {
					ra.State = fields[2] // active, inactive
					ra.Level = extractRAIDLevel(line)
				}
				// Count disks
				for _, f := range fields[3:] {
					if strings.Contains(f, "[") {
						ra.Disks++
						if strings.Contains(f, "(F)") {
							ra.SpareDisks++
						} else if !strings.Contains(f, "(S)") {
							ra.ActiveDisks++
						}
					}
				}
			}
			// Check for sync/recovery
			if strings.Contains(line, "recovery") || strings.Contains(line, "resync") {
				// e.g. " [==>..............]  12.5% ..."
				if idx := strings.Index(line, "]"); idx > 0 {
					pct := extractPercent(line[:idx])
					if pct != "" {
						ra.SyncPercent = pct
						ra.State = "resyncing"
					}
				}
			}
		}
		arrays = append(arrays, ra)
	}
	return arrays
}

// extractRAIDLevel gets RAID level from mdstat line
func extractRAIDLevel(line string) string {
	if strings.Contains(line, "raid1") {
		return "1"
	}
	if strings.Contains(line, "raid0") {
		return "0"
	}
	if strings.Contains(line, "raid5") {
		return "5"
	}
	if strings.Contains(line, "raid6") {
		return "6"
	}
	return "unknown"
}

// extractPercent gets percentage from [====....] 12.5%
func extractPercent(s string) string {
	// Find pattern like "12.5%"
	re := regexp.MustCompile(`(\d+\.?\d*)%`)
	matches := re.FindStringSubmatch(s)
	if len(matches) >= 2 {
		return matches[1] + "%"
	}
	return ""
}

// regexp_match is a helper to avoid importing regexp in multiple files
func regexp_match(pattern, s string) bool {
	matched, _ := regexp.MatchString(pattern, s)
	return matched
}
