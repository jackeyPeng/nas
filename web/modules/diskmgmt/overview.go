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

// ═══════════════════════════════════════════════════
// 四层模型结构体
// Physical Disk → Storage Pool → Volume → Shared Folder
// ═══════════════════════════════════════════════════

// StorageOverview 存储首页顶层
type StorageOverview struct {
	TotalCapacity  string          `json:"total_capacity"`
	TotalUsed     string          `json:"total_used"`
	TotalAvail    string          `json:"total_avail"`
	UsedPercent   string          `json:"used_percent"`
	Pools         []PoolSummary   `json:"pools"`                    // 存储池
	SystemDisks   []DiskSummary   `json:"system_disks"`            // 系统盘
	FreeDisks     []DiskSummary   `json:"free_disks"`              // 空闲盘
	SystemFolders []SharedFolder  `json:"system_folders,omitempty"` // 系统盘共享目录
	SystemShareSize string        `json:"system_share_size,omitempty"`
	RAIDHealth    []RAIDStatus    `json:"raid_health,omitempty"`
	Unconfigured  int             `json:"unconfigured_count"`
	HasIssues     bool            `json:"has_issues"`
	Issues        []string        `json:"issues,omitempty"`
}

// PoolSummary 存储池 — RAID/LVM VG/单盘，不暴露底层实现
type PoolSummary struct {
	Name         string           `json:"name"`           // 内部名(nas1, vg_nas)
	DisplayName  string           `json:"display_name"`   // 存储池1
	Type         string           `json:"type"`           // lvm, raid1, raid5, raid6, single
	RaidLevel    string           `json:"raid_level"`     // "1","5","6","" (UI 不直接展示)
	Device      string           `json:"device"`         // /dev/md0, /dev/vg_nas (UI 不展示)
	Size        string           `json:"size"`           // 池总容量(各 Volume 之和)
	Used        string           `json:"used"`
	Avail       string           `json:"avail"`
	Percent     string           `json:"percent"`
	Healthy     bool             `json:"healthy"`
	MemberDisks []DiskSummary    `json:"disks,omitempty"`       // 成员物理盘
	Volumes     []VolumeSummary  `json:"volumes"`               // ★ 逻辑卷列表
}

// VolumeSummary 逻辑卷 — 池上可分配空间，快照/配额/压缩的挂载点
type VolumeSummary struct {
	Name               string          `json:"name"`               // data, md0 (内部)
	DisplayName        string          `json:"display_name"`        // 卷1
	Device             string          `json:"device"`              // /dev/vg_nas/data, /dev/md0 (UI 不展示)
	MountPoint         string          `json:"mountpoint"`          // /data/nas1
	FSType             string          `json:"fstype"`              // xfs, ext4, btrfs
	Size               string          `json:"size"`
	Used               string          `json:"used"`
	Avail              string          `json:"avail"`
	Percent            string          `json:"percent"`
	Healthy            bool            `json:"healthy"`
	QuotaEnabled       bool            `json:"quota_enabled,omitempty"`        // 预留：配额
	CompressionEnabled bool            `json:"compression,omitempty"`         // 预留：压缩
	SnapshotCount      int             `json:"snapshot_count,omitempty"`      // 预留：快照数
	Folders            []SharedFolder  `json:"folders,omitempty"`             // 这个卷下的共享文件夹
}

// DiskSummary 物理盘 — 只描述硬件
type DiskSummary struct {
	Device       string            `json:"device"`       // /dev/sdb
	Friendly     string            `json:"friendly"`     // 磁盘 1
	Size         string            `json:"size"`
	Interface    string            `json:"interface"`    // SATA, NVMe, VirtIO, USB
	Rotational   string            `json:"rotational"`   // 0=SSD 1=HDD
	Model        string            `json:"model"`
	Temp         string            `json:"temp"`
	Smart        string            `json:"smart"`
	Serial       string            `json:"serial,omitempty"`        // 序列号
	PowerOnHours string            `json:"power_on_hours,omitempty"` // 通电时间
	BadBlocks    string            `json:"bad_blocks,omitempty"`     // 坏块
	Status       string            `json:"status"`                   // system, data, unused, pool_member
	Pool         string            `json:"pool,omitempty"`
	Partitions   []PartitionInfo   `json:"partitions,omitempty"`
}

// PartitionInfo 分区/卷信息
type PartitionInfo struct {
	Name       string `json:"name"`       // nvme0n1p2
	Device     string `json:"device"`     // /dev/nvme0n1p2
	Size       string `json:"size"`       // 229.6G
	FSType     string `json:"fstype"`     // ext4, xfs, swap
	Mountpoint string `json:"mountpoint"` // /, /boot/efi
	Status     string `json:"status"`     // system, data, swap, unused
}

// RAIDStatus RAID 阵列健康
type RAIDStatus struct {
	Device      string `json:"device"`
	Level       string `json:"level"`
	State       string `json:"state"`
	Disks       int    `json:"disks"`
	ActiveDisks int    `json:"active_disks"`
	SpareDisks  int    `json:"spare_disks"`
	SyncPercent string `json:"sync_percent,omitempty"`
}

// ═══════════════════════════════════════════════════
// handler
// ═══════════════════════════════════════════════════

// handleStorageOverview returns the complete storage dashboard data
func handleStorageOverview(w http.ResponseWriter, r *http.Request) {
	cachedPVMappings = nil
	cachedRAIDMembers = nil

	overview := StorageOverview{
		Pools:       []PoolSummary{},
		SystemDisks: []DiskSummary{},
		FreeDisks:   []DiskSummary{},
	}

	// 1. 获取所有 /data 挂载点（= Volume 的来源）
	mounts := getExistingDataMounts()
	poolSeq := 0
	mountDisplay := make(map[string]string) // mount path → pool display name

	// 按 device 分组挂载点：同一个 Pool（md0 或 vg_nas）下可能有多个 Volume
	poolMounts := make(map[string][]map[string]string) // device → mounts
	poolOrder := []string{}                           // 保持顺序

	for _, m := range mounts {
		mountPoint := m["mount"]
		if !strings.HasPrefix(mountPoint, "/data") {
			continue
		}
		// 判断 pool device
		poolDev := detectPoolDevice(m["device"])
		if _, ok := poolMounts[poolDev]; !ok {
			poolOrder = append(poolOrder, poolDev)
		}
		poolMounts[poolDev] = append(poolMounts[poolDev], m)
	}

	// 为每个 Pool 构造 PoolSummary
	raidStatuses := getRAIDStatus()
	raidMap := make(map[string]RAIDStatus)
	for _, ra := range raidStatuses {
		raidMap[ra.Device] = ra
	}

	for _, poolDev := range poolOrder {
		pMounts := poolMounts[poolDev]
		if len(pMounts) == 0 {
			continue
		}
		poolSeq++
		poolType, raidLevel := detectPoolTypeEx(poolDev, pMounts[0]["mount"])

		// Pool 总容量 = 各 Volume 之和
		var totalSize, totalUsed, totalAvail float64
		for _, m := range pMounts {
			totalSize += parseSizeToGB(m["size"])
			totalUsed += parseSizeToGB(m["used"])
			totalAvail += parseSizeToGB(m["avail"])
		}

		healthy := true
		if poolType == "raid1" || poolType == "raid0" || poolType == "raid5" || poolType == "raid6" {
			if ra, ok := raidMap[poolDev]; ok {
				healthy = ra.State == "active" || ra.State == "clean"
				if ra.SyncPercent != "" {
					healthy = false
				}
			}
		}

		poolName := extractPoolName(pMounts[0]["mount"])
		if poolType == "lvm" {
			// LVM 的 poolName 用 vg 名
			poolName = extractVGName(poolDev)
		}
		displayName := fmt.Sprintf("存储池%d", poolSeq)

		ps := PoolSummary{
			Name:        poolName,
			DisplayName: displayName,
			Type:        poolType,
			RaidLevel:   raidLevel,
			Device:      poolDev,
			Size:        formatSize(totalSize),
			Used:        formatSize(totalUsed),
			Avail:       formatSize(totalAvail),
			Percent:     calcPercent(totalSize, totalUsed),
			Healthy:     healthy,
			Volumes:     []VolumeSummary{},
		}
		mountDisplay[poolDev] = displayName

		// 构造 Volume 列表
		for vi, m := range pMounts {
			volName := extractPoolName(m["mount"])
			volDisplay := fmt.Sprintf("卷%d", vi+1)
			fsOut, _ := common.ExecOutput("findmnt", "-n", "-o", "FSTYPE", "--source", m["device"])
			fsType := strings.TrimSpace(fsOut)

			vol := VolumeSummary{
				Name:        volName,
				DisplayName: volDisplay,
				Device:      m["device"],
				MountPoint:  m["mount"],
				FSType:      fsType,
				Size:        m["size"],
				Used:        m["used"],
				Avail:       m["avail"],
				Percent:     m["percent"],
				Healthy:     healthy, // Volume 继承 Pool 健康状态
				Folders:     []SharedFolder{},
			}
			ps.Volumes = append(ps.Volumes, vol)
		}

		overview.Pools = append(overview.Pools, ps)
	}

	// 2. 获取共享文件夹，按挂载点分组，分配到 Volume
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
						Name:   name,
						Path:   mountPoint + "/" + name,
						Source: "local",
					})
				}
			} else {
				for _, entry := range entries {
					if !entry.IsDir() || entry.Name() == "#recycle" {
						continue
					}
					folderPath := filepath.Join(mountPoint, entry.Name())
					f := SharedFolder{
						Name:   entry.Name(),
						Path:   folderPath,
						Source: "local",
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

	// 分配 folders 到 Volume（通过 mountpoint 匹配）
	for pi := range overview.Pools {
		for vi := range overview.Pools[pi].Volumes {
			mp := overview.Pools[pi].Volumes[vi].MountPoint
			if folders, ok := folderMap[mp]; ok {
				overview.Pools[pi].Volumes[vi].Folders = folders
			}
		}
	}

	// 3. 获取物理盘，分配到 Pool 成员 / 系统盘 / 空闲盘
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
			Device:       d.Device,
			Friendly:     fmt.Sprintf("磁盘 %d", diskID),
			Size:         d.Size,
			Interface:    d.Interface,
			Rotational:   d.Rotational,
			Model:        d.Model,
			Temp:         d.Temp,
			Smart:        d.Smart,
			Serial:       d.Serial,
			Status:       realStatus,
		}
		// 分区列表
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

		// 分配到正确的桶
		if isSystem {
			overview.SystemDisks = append(overview.SystemDisks, ds)
		} else if realStatus == "unused" {
			overview.FreeDisks = append(overview.FreeDisks, ds)
			overview.Unconfigured++
		} else {
			// 数据盘 — 找到它属于哪个 Pool
			poolDev := findDiskPoolDevice(d.Device, poolOrder)
			ds.Pool = mountDisplay[poolDev]
			ds.Status = "pool_member"
			for i := range overview.Pools {
				if overview.Pools[i].Device == poolDev {
					overview.Pools[i].MemberDisks = append(overview.Pools[i].MemberDisks, ds)
					break
				}
			}
		}

		if d.Smart == "FAILED" {
			overview.HasIssues = true
			overview.Issues = append(overview.Issues, fmt.Sprintf("%s (%s) SMART 检测失败", ds.Friendly, d.Device))
		}
	}

	// 4. 总容量
	totalSize, totalUsed, totalAvail := sumMountSizes(mounts)
	overview.TotalCapacity = totalSize
	overview.TotalUsed = totalUsed
	overview.TotalAvail = totalAvail
	overview.UsedPercent = calcPercent(
		parseSizeToGB(totalSize), parseSizeToGB(totalUsed))

	// 5. 系统盘共享目录 (/data/system)
	systemSharePath := "/data/system"
	if _, err := os.Stat(systemSharePath); err != nil && !os.IsNotExist(err) {
		// 其他错误（如权限不足），跳过
	} else {
		if os.IsNotExist(err) {
			common.SudoExec("mkdir", "-p", systemSharePath)
			nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
			if nasUser == "" {
				nasUser = "root"
			}
			common.SudoExec("chown", "-R", nasUser+":"+nasUser, systemSharePath)
		}

		sizeOut, _ := common.SudoOutput("du", "-sh", systemSharePath)
		overview.SystemShareSize = parseDuSize(sizeOut)

		smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
		smbMap := parseSambaShares(smbConf)
		entries, err := os.ReadDir(systemSharePath)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || entry.Name() == "#recycle" {
					continue
				}
				folderPath := filepath.Join(systemSharePath, entry.Name())
				f := SharedFolder{
					Name:   entry.Name(),
					Path:   folderPath,
					Pool:   "系统盘",
					Source: "local",
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
				overview.SystemFolders = append(overview.SystemFolders, f)
			}
		}
	}

	// 6. RAID 健康
	overview.RAIDHealth = raidStatuses
	for _, ra := range raidStatuses {
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

// detectPoolDevice 从 mount device 提取 Pool 级别的设备名
// /dev/md0 → /dev/md0 (RAID 池)
// /dev/vg_nas/data 或 /dev/mapper/vg_nas-data → /dev/vg_nas (LVM VG 池)
// /dev/sdb1 → /dev/sdb1 (单盘池)
func detectPoolDevice(device string) string {
	// LVM: /dev/vg_nas/data — 直接取 VG 路径
	if strings.HasPrefix(device, "/dev/vg_") {
		// /dev/vg_nas/data → /dev/vg_nas
		if idx := strings.LastIndex(device, "/"); idx > 4 {
			return device[:idx]
		}
		return device
	}
	// LVM mapper: /dev/mapper/vg_nas-data → /dev/vg_nas
	if strings.Contains(device, "/dev/mapper/") {
		// mapper 命名: vgname-lvname，VG 名中的 - 会变成 --
		// 用 vgs 确认更可靠，但这里先简单处理：查 pvs 映射
		// 由于 getCachedPVMappings 在此函数之后才初始化，
		// 这里用 vgs 直接查
		vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings", "-o", "vg_name")
		bestVG := ""
		for _, line := range strings.Split(vgsOut, "\n") {
			vg := strings.TrimSpace(line)
			if vg == "" {
				continue
			}
			// mapper 设备名里包含 vg 名（- 替换）
			mapperName := strings.TrimPrefix(device, "/dev/mapper/")
			// LVM mapper 规则: vg 中的 - 变成 --, / 变成 -
			// 简单匹配: 如果 mapper 名以 vg 名(替换-为--)开头
			vgEscaped := strings.ReplaceAll(vg, "-", "--")
			if strings.HasPrefix(mapperName, vgEscaped+"-") || strings.HasPrefix(mapperName, vg+"-") {
				bestVG = vg
				break
			}
			// 兜底: 去掉最后一段 - 后面的部分匹配
			if strings.Contains(mapperName, vg) {
				bestVG = vg
				break
			}
		}
		if bestVG != "" {
			return "/dev/" + bestVG
		}
		return device
	}
	// RAID: /dev/md0
	if strings.Contains(device, "/dev/md") {
		return device
	}
	// 单盘分区: /dev/sdb1 → 池就是它自己
	return device
}

// extractVGName 从 /dev/vg_nas 提取 vg_nas
func extractVGName(device string) string {
	base := filepath.Base(device)
	if strings.HasPrefix(base, "vg_") {
		return base
	}
	return base
}

// findDiskPoolDevice 找到物理盘属于哪个 Pool device
func findDiskPoolDevice(device string, poolOrder []string) string {
	// 1. 直接匹配
	for _, pd := range poolOrder {
		if pd == device {
			return pd
		}
	}
	// 2. LVM PV → VG 映射
	pvToVG := getCachedPVMappings()
	if vgName, ok := pvToVG[device]; ok {
		vgDev := "/dev/" + vgName
		for _, pd := range poolOrder {
			if pd == vgDev {
				return pd
			}
		}
	}
	// 3. RAID 成员 → md 设备
	raidMembers := getCachedRAIDMembers()
	if mdDev, ok := raidMembers[device]; ok {
		return mdDev
	}
	return ""
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

// detectPoolTypeEx returns (type, raidLevel)
func detectPoolTypeEx(device, mount string) (string, string) {
	// LVM
	if strings.Contains(device, "vg_") || strings.Contains(device, "mapper/") || strings.Contains(device, "/vg") {
		return "lvm", ""
	}
	// RAID
	if strings.Contains(device, "/dev/md") {
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
				if strings.Contains(line, "raid6") {
					return "raid6", "6"
				}
			}
		}
		return "raid1", "1"
	}
	return "single", ""
}

// getCachedPVMappings returns PV→VG mapping (cached per request)
var cachedPVMappings map[string]string

func getCachedPVMappings() map[string]string {
	if cachedPVMappings != nil {
		return cachedPVMappings
	}
	cachedPVMappings = make(map[string]string)
	pvOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name,vg_name")
	for _, line := range strings.Split(pvOut, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 {
			cachedPVMappings[fields[0]] = fields[1]
		}
	}
	return cachedPVMappings
}

// getCachedRAIDMembers returns disk→md mapping (cached per request)
var cachedRAIDMembers map[string]string

func getCachedRAIDMembers() map[string]string {
	if cachedRAIDMembers != nil {
		return cachedRAIDMembers
	}
	cachedRAIDMembers = make(map[string]string)
	mdDevs, _ := filepath.Glob("/dev/md[0-9]*")
	for _, dev := range mdDevs {
		if !regexp_match(`^/dev/md\d+$`, dev) {
			continue
		}
		scanOut, _ := common.SudoOutput("/usr/sbin/mdadm", "--detail", dev)
		for _, line := range strings.Split(scanOut, "\n") {
			if strings.Contains(line, "/dev/sd") || strings.Contains(line, "/dev/nvme") || strings.Contains(line, "/dev/vd") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					last := fields[len(fields)-1]
					if strings.HasPrefix(last, "/dev/") {
						cachedRAIDMembers[last] = dev
					}
				}
			}
		}
	}
	return cachedRAIDMembers
}

// sumMountSizes aggregates size info from all data mounts
func sumMountSizes(mounts []map[string]string) (total, used, avail string) {
	var totalBytes, usedBytes, availBytes float64
	for _, m := range mounts {
		totalBytes += parseSizeToGB(m["size"])
		usedBytes += parseSizeToGB(m["used"])
		availBytes += parseSizeToGB(m["avail"])
	}
	return formatSize(totalBytes), formatSize(usedBytes), formatSize(availBytes)
}

// parseSizeToGB converts size string to GB float
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

// formatSize converts GB float to human-readable
func formatSize(gb float64) string {
	if gb >= 1024 {
		return fmt.Sprintf("%.1fT", gb/1024)
	}
	if gb >= 1 {
		return fmt.Sprintf("%.0fG", gb)
	}
	return fmt.Sprintf("%.0fM", gb*1024)
}

// calcPercent calculates used percentage from GB floats (no format round-trip)
func calcPercent(totalGB, usedGB float64) string {
	if totalGB <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.0f", (usedGB/totalGB)*100)
}

// getRAIDStatus reads mdadm RAID arrays
func getRAIDStatus() []RAIDStatus {
	var arrays []RAIDStatus
	out, err := common.ExecOutput("ls", "/dev/md*")
	if err != nil {
		return arrays
	}
	mdstat, _ := os.ReadFile("/proc/mdstat")
	for _, dev := range strings.Fields(out) {
		if !regexp_match(`^/dev/md\d+$`, dev) {
			continue
		}
		ra := RAIDStatus{Device: dev}
		mdName := strings.TrimPrefix(dev, "/dev/")
		for _, line := range strings.Split(string(mdstat), "\n") {
			if strings.HasPrefix(line, mdName) {
				fields := strings.Fields(line)
				if len(fields) >= 4 {
					ra.State = fields[2]
					ra.Level = extractRAIDLevel(line)
				}
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
			if strings.Contains(line, "recovery") || strings.Contains(line, "resync") {
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

var percentRe = regexp.MustCompile(`(\d+\.?\d*)%`)

// extractPercent gets percentage from [====....] 12.5%
func extractPercent(s string) string {
	matches := percentRe.FindStringSubmatch(s)
	if len(matches) >= 2 {
		return matches[1] + "%"
	}
	return ""
}

// regexp_match helper
func regexp_match(pattern, s string) bool {
	matched, _ := regexp.MatchString(pattern, s)
	return matched
}
