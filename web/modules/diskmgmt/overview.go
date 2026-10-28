package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════
// 四层模型结构体
// Physical Disk → Storage Pool → Volume → Shared Folder
// ═══════════════════════════════════════════════════

// StorageOverview 存储首页顶层
type StorageOverview struct {
	TotalCapacity   string         `json:"total_capacity"`
	TotalUsed       string         `json:"total_used"`
	TotalAvail      string         `json:"total_avail"`
	UsedPercent     string         `json:"used_percent"`
	Pools           []PoolSummary  `json:"pools"`                    // 存储池
	SystemDisks     []DiskSummary  `json:"system_disks"`             // 系统盘
	FreeDisks       []DiskSummary  `json:"free_disks"`               // 空闲盘
	UnmanagedDisks  []DiskSummary  `json:"unmanaged_disks"`          // 未纳管盘(有数据但找不到所属池，A5)
	SystemFolders   []SharedFolder `json:"system_folders,omitempty"` // 系统盘共享目录
	SystemShareSize string         `json:"system_share_size,omitempty"`
	RAIDHealth      []RAIDStatus   `json:"raid_health,omitempty"`
	Unconfigured    int            `json:"unconfigured_count"`
	HasIssues       bool           `json:"has_issues"`
	Issues          []string       `json:"issues,omitempty"`
	Stats           StorageStats   `json:"stats"`            // 四层计数（总览卡片）
	Alerts          []StorageAlert `json:"alerts,omitempty"` // 告警栏（高温/SMART失败/RAID降级）
}

// StorageStats 四层对象计数（总览页顶部统计卡片）
type StorageStats struct {
	TotalDisks   int `json:"total_disks"` // 物理磁盘总数（含系统盘）
	TotalPools   int `json:"total_pools"`
	TotalVolumes int `json:"total_volumes"`
	TotalShares  int `json:"total_shares"`
}

// StorageAlert 总览页告警条目
type StorageAlert struct {
	Level   string `json:"level"`   // warning / critical
	Message string `json:"message"` // "磁盘 sdd 温度 52°C，建议检查机箱散热"
	Device  string `json:"device,omitempty"`
}

// PoolSummary 存储池 — RAID/LVM VG/单盘，不暴露底层实现
type PoolSummary struct {
	Name        string          `json:"name"`         // 内部名(nas1, vg_nas)
	DisplayName string          `json:"display_name"` // 存储池1
	Type        string          `json:"type"`         // lvm, raid1, raid5, raid6, single
	RaidLevel   string          `json:"raid_level"`   // "1","5","6","" (UI 不直接展示)
	Device      string          `json:"device"`       // /dev/md0, /dev/vg_nas (UI 不展示)
	Size        string          `json:"size"`         // 池总容量(各 Volume 之和)
	Used        string          `json:"used"`
	Avail       string          `json:"avail"`
	Percent     string          `json:"percent"`
	Healthy     bool            `json:"healthy"`
	MemberDisks []DiskSummary   `json:"disks,omitempty"` // 成员物理盘
	Volumes     []VolumeSummary `json:"volumes"`         // ★ 逻辑卷列表
}

// VolumeSummary 逻辑卷 — 池上可分配空间，快照/配额/压缩的挂载点
type VolumeSummary struct {
	Name               string         `json:"name"`         // data, md0 (内部)
	DisplayName        string         `json:"display_name"` // 卷1
	Device             string         `json:"device"`       // /dev/vg_nas/data, /dev/md0 (UI 不展示)
	MountPoint         string         `json:"mountpoint"`   // /data/nas1
	FSType             string         `json:"fstype"`       // xfs, ext4, btrfs
	Size               string         `json:"size"`
	Used               string         `json:"used"`
	Avail              string         `json:"avail"`
	Percent            string         `json:"percent"`
	Healthy            bool           `json:"healthy"`
	QuotaEnabled       bool           `json:"quota_enabled,omitempty"`  // 预留：配额
	CompressionEnabled bool           `json:"compression,omitempty"`    // 预留：压缩
	SnapshotCount      int            `json:"snapshot_count,omitempty"` // 预留：快照数
	Folders            []SharedFolder `json:"folders,omitempty"`        // 这个卷下的共享文件夹
}

// DiskSummary 物理盘 — 只描述硬件
type DiskSummary struct {
	Device       string          `json:"device"`   // /dev/sdb
	Friendly     string          `json:"friendly"` // 磁盘 1
	Size         string          `json:"size"`
	Interface    string          `json:"interface"`  // SATA, NVMe, VirtIO, USB
	Rotational   string          `json:"rotational"` // 0=SSD 1=HDD
	Model        string          `json:"model"`
	Temp         string          `json:"temp"`
	Smart        string          `json:"smart"`
	Serial       string          `json:"serial,omitempty"`         // 序列号
	PowerOnHours string          `json:"power_on_hours,omitempty"` // 通电时间
	BadBlocks    string          `json:"bad_blocks,omitempty"`     // 坏块
	Status       string          `json:"status"`                   // system, data, unused, pool_member
	Pool         string          `json:"pool,omitempty"`
	Partitions   []PartitionInfo `json:"partitions,omitempty"`
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
	// FailedDisks 统计 (F) 失败成员数量（consensus P0-D：此前被错误计入
	// SpareDisks，导致前端看到的降级阵列"spare 盘数"虚高、失败盘数无处体现）。
	// 新增字段，前端未引用 spare_disks 原值做展示逻辑，向后兼容追加不改语义。
	FailedDisks int    `json:"failed_disks"`
	SyncPercent string `json:"sync_percent,omitempty"`
	// Health/Activity 新增字段（consensus R2 阶段1：健康与活动状态分离）。
	// 既有字段名/语义全部保留，向后兼容追加，前端未引用这两个新字段时不受影响。
	//
	// Health："normal"/"degraded"/"failed"——degraded>0 或 FailedDisks>0 时
	// "degraded"；array_state=="inactive" 或 degraded==raid_disks 时 "failed"；
	// 否则 "normal"。判定只看 degraded 计数，不信 array_state（真机实测：
	// 降级阵列 array_state 仍是 "clean"）。
	// Activity：sysfs sync_action 原值（idle/resync/recover/reshape/check/
	// repair）；mdstat 回退路径按进度行 Kind 映射到同名语义（recovery→recover）。
	Health   string `json:"health"`
	Activity string `json:"activity"`
}

// ═══════════════════════════════════════════════════
// handler
// ═══════════════════════════════════════════════════

// handleStorageOverview returns the complete storage dashboard data
func handleStorageOverview(w http.ResponseWriter, r *http.Request) {
	overview := buildStorageOverview()
	common.JSONResponse(w, map[string]interface{}{
		"overview": overview,
	})
}

// buildStorageOverview 构造完整存储总览数据（四层模型：物理盘/存储池/卷/共享文件夹
// + 总容量 + 告警）。从 handleStorageOverview 抽出的纯函数——handler 现在只负责
// 调它再序列化，行为逐字节不变。AI 工具域 diskmgmt.OverviewData() 复用同一内核。
func buildStorageOverview() StorageOverview {
	resetOverviewCaches()

	overview := StorageOverview{
		Pools:          []PoolSummary{},
		SystemDisks:    []DiskSummary{},
		FreeDisks:      []DiskSummary{},
		UnmanagedDisks: make([]DiskSummary, 0),
	}

	// 1. 获取所有 /data 挂载点（= Volume 的来源）
	mounts := getExistingDataMounts()
	poolSeq := 0
	mountDisplay := make(map[string]string) // mount path → pool display name

	// 按 device 分组挂载点：同一个 Pool（md0 或 vg_nas）下可能有多个 Volume
	poolMounts := make(map[string][]map[string]string) // device → mounts
	poolOrder := []string{}                            // 保持顺序

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
			// consensus R2：健康判定改用 Health/Activity 分离字段——
			// sysfs 回退到 mdstat 路径同样填充这两个字段，语义一致，
			// 调用点不需要区分数据来源。活动中（非 idle）也不算健康
			// （与此前 SyncPercent != "" 置 false 的语义等价）。
			if ra, ok := raidMap[poolDev]; ok {
				healthy = ra.Health == "normal" && ra.Activity == "idle"
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
	// 元数据优先：folders.db 是权威源，smb.conf/exports 只做未纳管目录兜底
	metaMap := make(map[string]FolderMeta)
	for _, fm := range GetAllFolderMeta() {
		metaMap[fm.Path] = fm
	}
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
					if name == "" || name == "#recycle" || name == ".recycle" {
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
					if !entry.IsDir() || entry.Name() == "#recycle" || entry.Name() == ".recycle" {
						continue
					}
					folderPath := filepath.Join(mountPoint, entry.Name())
					f := SharedFolder{
						Name:   entry.Name(),
						Path:   folderPath,
						Source: "local",
					}
					size, _ := common.CachedDirSize(folderPath, 60*time.Second)
					f.Size = size
					enrichSharedFolder(&f, metaMap, smbMap, smbConf)
					// Check WebDAV (rclone serve webdav)
					if isWebDAVServed(entry.Name()) {
						f.WebDAVAccess = true
					}
					// FTP 不参与共享文件夹（vsftpd chroot 到 /data/nas1，靠文件系统权限隔离），
					// 因此不再给共享文件夹打 FTP 标记，避免夸大。
					// Check S3 (rclone serve s3)
					if isS3Served(folderPath) {
						f.S3Access = true
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
			Device:     d.Device,
			Friendly:   fmt.Sprintf("磁盘 %d", diskID),
			Size:       d.Size,
			Interface:  d.Interface,
			Rotational: d.Rotational,
			Model:      d.Model,
			Temp:       d.Temp,
			Smart:      d.Smart,
			Serial:     d.Serial,
			Status:     realStatus,
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
			if poolDev == "" {
				// A5：找不到所属池，说明盘上有数据但未被任何已识别的池纳管
				// （例如存量遗留分区、识别逻辑覆盖不到的组合）。此前这里会被
				// 强行打上 Status="pool_member" 却进不了任何 Pool.MemberDisks，
				// 前端看到的是一块"属于某池"却在哪个池卡片里都找不到的幽灵盘。
				// 现在如实标记为 unmanaged，单独清单展示，提示用户核实。
				ds.Status = "unmanaged"
				overview.UnmanagedDisks = append(overview.UnmanagedDisks, ds)
			} else {
				ds.Pool = mountDisplay[poolDev]
				ds.Status = "pool_member"
				for i := range overview.Pools {
					if overview.Pools[i].Device == poolDev {
						overview.Pools[i].MemberDisks = append(overview.Pools[i].MemberDisks, ds)
						break
					}
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
			nasUser := common.GetNASUser()
			if nasUser == "" {
				nasUser = "root"
			}
			common.SudoExec("chown", "-R", nasUser+":"+nasUser, systemSharePath)
		}

		size, _ := common.CachedDirSize(systemSharePath, 60*time.Second)
		overview.SystemShareSize = size

		smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
		smbMap := parseSambaShares(smbConf)
		entries, err := os.ReadDir(systemSharePath)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || entry.Name() == "#recycle" || entry.Name() == ".recycle" {
					continue
				}
				folderPath := filepath.Join(systemSharePath, entry.Name())
				f := SharedFolder{
					Name:   entry.Name(),
					Path:   folderPath,
					Pool:   "系统盘",
					Source: "local",
				}
				size, _ := common.CachedDirSize(folderPath, 60*time.Second)
				f.Size = size
				enrichSharedFolder(&f, metaMap, smbMap, smbConf)
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

	// 7. 统计计数（总览卡片）+ 告警栏
	// A5：unmanaged 盘也要计入物理盘总数，否则这部分盘会从统计卡片"凭空消失"
	overview.Stats.TotalDisks = len(overview.SystemDisks) + len(overview.FreeDisks) + len(overview.UnmanagedDisks)
	volCount := 0
	shareCount := 0
	poolMemberDevs := map[string]bool{}
	for _, p := range overview.Pools {
		volCount += len(p.Volumes)
		for _, v := range p.Volumes {
			shareCount += len(v.Folders)
		}
		for _, md := range p.MemberDisks {
			poolMemberDevs[md.Device] = true
		}
	}
	overview.Stats.TotalDisks += len(poolMemberDevs)
	overview.Stats.TotalPools = len(overview.Pools)
	overview.Stats.TotalVolumes = volCount
	overview.Stats.TotalShares = shareCount + len(overview.SystemFolders)

	// 告警：磁盘温度 / SMART / RAID 状态
	allDisks := append([]DiskSummary{}, overview.SystemDisks...)
	allDisks = append(allDisks, overview.FreeDisks...)
	allDisks = append(allDisks, overview.UnmanagedDisks...)
	for _, p := range overview.Pools {
		allDisks = append(allDisks, p.MemberDisks...)
	}
	for _, d := range allDisks {
		dev := strings.TrimPrefix(d.Device, "/dev/")
		if d.Temp != "" && d.Temp != "-" {
			temp := 0
			fmt.Sscanf(d.Temp, "%d", &temp)
			if temp >= 50 {
				overview.Alerts = append(overview.Alerts, StorageAlert{
					Level:   "warning",
					Message: fmt.Sprintf("磁盘 %s 温度 %d°C，建议检查机箱散热", dev, temp),
					Device:  dev,
				})
			} else if temp >= 45 {
				overview.Alerts = append(overview.Alerts, StorageAlert{
					Level:   "warning",
					Message: fmt.Sprintf("磁盘 %s 温度偏高 %d°C", dev, temp),
					Device:  dev,
				})
			}
		}
		if d.Smart == "FAILED" {
			overview.Alerts = append(overview.Alerts, StorageAlert{
				Level:   "critical",
				Message: fmt.Sprintf("磁盘 %s SMART 检测失败，建议尽快备份并更换", dev),
				Device:  dev,
			})
		}
	}
	for _, ra := range raidStatuses {
		if ra.State != "active" && ra.State != "clean" {
			lvl := "warning"
			msg := fmt.Sprintf("RAID %s 状态异常: %s", ra.Device, ra.State)
			if ra.SyncPercent != "" {
				msg = fmt.Sprintf("RAID %s 正在重建/同步 %s", ra.Device, ra.SyncPercent)
			}
			if ra.State == "degraded" || ra.State == "FAILED" {
				lvl = "critical"
				msg = fmt.Sprintf("RAID %s 已降级，数据处于风险中，建议尽快处理", ra.Device)
			}
			overview.Alerts = append(overview.Alerts, StorageAlert{Level: lvl, Message: msg, Device: ra.Device})
		}
	}

	return overview
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
		var vgList []string
		for _, line := range strings.Split(vgsOut, "\n") {
			vg := strings.TrimSpace(line)
			if vg != "" {
				vgList = append(vgList, vg)
			}
		}
		mapperName := strings.TrimPrefix(device, "/dev/mapper/")
		if bestVG := matchVGForMapper(mapperName, vgList); bestVG != "" {
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

// matchVGForMapper 从 /dev/mapper/<mapperName> 反推所属 VG 名。
// A4：原实现在 vgList 上逐个 vg 做前缀匹配和 Contains 兜底匹配混在同一次循环里，
// 一旦命中 Contains 就 break——如果 vgList 顺序恰好把 "vg"（短名、被包含）排在
// "vg_nas"（真正匹配的长名）前面，会误命中短名。现在拆成两轮：第一轮在全部 VG
// 上做精确前缀匹配（LVM mapper 命名规则：VG 名中的 - 转义为 --，再接 "-" 分隔符），
// 只有全部 VG 都没有前缀命中时，才进入第二轮 Contains 兜底。
func matchVGForMapper(mapperName string, vgList []string) string {
	// 第一轮：精确前缀匹配
	for _, vg := range vgList {
		if vg == "" {
			continue
		}
		vgEscaped := strings.ReplaceAll(vg, "-", "--")
		if strings.HasPrefix(mapperName, vgEscaped+"-") || strings.HasPrefix(mapperName, vg+"-") {
			return vg
		}
	}
	// 第二轮：兜底子串匹配（仅在无精确前缀命中时才用）
	for _, vg := range vgList {
		if vg == "" {
			continue
		}
		if strings.Contains(mapperName, vg) {
			return vg
		}
	}
	return ""
}

// extractVGName 从设备路径提取 VG 名，支持三种形态（consensus R2 Item B）：
//   - `/dev/vg_nas` → `vg_nas`（整盘/VG 路径本身，取 base，现状保持）
//   - `/dev/vg_nas/data` → `vg_nas`（LV 路径形式，VG 是父目录名）
//   - `/dev/mapper/vg_nas-data` → `vg_nas`（dm-mapper 命名：VG 名里的字面 "-"
//     被转义为 "--"，VG 与 LV 之间用单个未转义的 "-" 分隔；解析时要先在
//     mapper 名里找到第一个"落单"的 "-"（即不是 "--" 转义对的一部分），
//     左边部分把 "--" 还原成 "-" 就是真实 VG 名）
//   - `/dev/mapper/control` → `""`（control 是 device-mapper 控制设备，不是
//     任何 VG 的 LV，特判返回空串）
//
// 此前两个分支（mapper 与非 mapper）都直接 filepath.Base(device)，对
// `/dev/mapper/vg_nas-data` 会得出整段 "vg_nas-data" 当 VG 名——pvs -S
// vg_name= 查询用这个错误值永远查空但不报错，调用点（handlePoolDelete 的
// lvm 分支、overview.go 池显示名、pool.go 扩容路径）全部受影响，详见
// 报告的调用点说明。
func extractVGName(device string) string {
	if strings.HasPrefix(device, "/dev/mapper/") {
		mapperName := strings.TrimPrefix(device, "/dev/mapper/")
		if mapperName == "control" {
			return ""
		}
		return unescapeMapperVGName(mapperName)
	}
	base := filepath.Base(device)
	if base == device {
		// 没有任何 "/" 分隔的裸名（如 "vg_nas"，不带任何路径前缀）才会走到
		// 这里——`/dev/vg_nas` 本身带 "/"，base!=device，会落入下面的 LV
		// 分支，经 parentBase=="dev" 排除后兜底落到 return base（结果同样
		// 是 "vg_nas"，但路径不同）。
		return base
	}
	// LV 直路径 /dev/vg_nas/data：VG 是父目录名，即 filepath.Dir 的 base。
	parent := filepath.Dir(device)
	parentBase := filepath.Base(parent)
	if parentBase != "" && parentBase != "." && parentBase != "/" && parentBase != "dev" {
		return parentBase
	}
	return base
}

// unescapeMapperVGName 从 dm-mapper 命名（如 "vg_nas-data"、"my--vg-data"）中
// 解析出 VG 名部分。LVM mapper 命名规则：VG/LV 名内的字面 "-" 转义为 "--"，
// VG 与 LV 名之间用单个未转义的 "-" 分隔。算法：逐字符扫描，遇到 "--" 就是
// 转义对（还原成一个 "-" 并跳过两个字符），遇到单个 "-"（后面不是另一个
// "-"）就是分隔符，扫描到此停止，之前积累的内容就是还原后的 VG 名。
// 找不到分隔符（整串都是转义对，没有真正的分隔符）时，把整串还原后返回
// （调用方后续会用 vgs 校验这个名字是否真实存在）。
func unescapeMapperVGName(mapperName string) string {
	var sb strings.Builder
	i := 0
	for i < len(mapperName) {
		if mapperName[i] == '-' {
			if i+1 < len(mapperName) && mapperName[i+1] == '-' {
				// "--" 转义对：还原成一个 "-"，跳过两个字符。
				sb.WriteByte('-')
				i += 2
				continue
			}
			// 单个 "-"：VG/LV 分隔符，停止。
			break
		}
		sb.WriteByte(mapperName[i])
		i++
	}
	return sb.String()
}

// findDiskPoolDevice 找到物理盘属于哪个 Pool device
func findDiskPoolDevice(device string, poolOrder []string) string {
	// 1. Direct match
	for _, pd := range poolOrder {
		if pd == device {
			return pd
		}
	}
	// 2. LVM PV → VG mapping
	pvToVG := getCachedPVMappings()
	if vgName, ok := pvToVG[device]; ok {
		vgDev := "/dev/" + vgName
		for _, pd := range poolOrder {
			if pd == vgDev {
				return pd
			}
		}
	}
	// 3. RAID member → md device
	raidMembers := getCachedRAIDMembers()
	if mdDev, ok := raidMembers[device]; ok {
		return mdDev
	}
	// 4. Partition → parent disk (for single/separate mode)
	// e.g. /dev/sdb1 → /dev/sdb
	for _, pd := range poolOrder {
		// If pool device is a partition, check if our disk is its parent
		if strings.HasPrefix(pd, device) && len(pd) > len(device) {
			rest := pd[len(device):]
			// rest should be just digits (partition number) or pN (NVMe)
			isPart := true
			for _, c := range rest {
				if c < '0' || c > '9' {
					isPart = false
					break
				}
			}
			if isPart {
				return pd
			}
		}
		// Reverse: if our disk is a partition, check if pool device is its parent
		if strings.HasPrefix(device, pd) && len(device) > len(pd) {
			rest := device[len(pd):]
			isPart := true
			for _, c := range rest {
				if c < '0' || c > '9' {
					isPart = false
					break
				}
			}
			if isPart {
				return pd
			}
		}
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

// consensus P1-B（C1）：cachedPVMappings/cachedRAIDMembers 是包级变量，此前
// buildStorageOverview 入口置 nil、getter 懒填充，跨请求并发调用会对同一组
// 变量产生 data race（go test -race 能直接打出来）。两种修法二选一：
//
//	a) 包级 sync.Mutex 保护全部读写点，getter 在锁内执行外部命令并填充；
//	b) 重构为请求级传参（不用包级变量）。
//
// 选 (a)：findDiskPoolDevice→getCachedPVMappings/getCachedRAIDMembers 只有
// buildStorageOverview 这一条调用路径，改造成请求级传参需要连带改
// findDiskPoolDevice 的签名和它的单测，影响面更大；而 overview 构建本身不是
// 高频/低延迟要求的路径（面板总览页请求），串行化可接受。锁粒度覆盖"读取前
// 判断是否已填充"到"填充完成"的整个临界区，避免 TOCTOU。
var overviewCacheMu sync.Mutex

// resetOverviewCaches 清空两个包级缓存，用于下一次 buildStorageOverview 重新
// 懒加载。必须在持锁状态下执行，不单独导出给外部调用。
func resetOverviewCaches() {
	overviewCacheMu.Lock()
	cachedPVMappings = nil
	cachedRAIDMembers = nil
	overviewCacheMu.Unlock()
}

// getCachedPVMappings returns PV→VG mapping (cached per request)
var cachedPVMappings map[string]string

func getCachedPVMappings() map[string]string {
	overviewCacheMu.Lock()
	defer overviewCacheMu.Unlock()
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
	overviewCacheMu.Lock()
	defer overviewCacheMu.Unlock()
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

// getRAIDStatus reads RAID array status。
// consensus R2 阶段1：sysfs 优先采集 —— /sys/block/<md>/md/ 下是内核暴露的
// 结构化标量文件，字段稳定、health/activity 语义清晰分离，不需要像 mdstat
// 一样拿字符串下标/正则抠进度条。只有 sysfs 枚举不到任何 md 设备（目录缺失，
// 如容器环境/权限问题）且 mdstat 里确实有阵列时，才回退 mdstat 文本解析
// （buildRAIDStatusFromMdstat，完整保留不删）。
//
// 枚举来源注记（历史坑）：此前先 `ls /dev/md*` 拿设备节点列表再逐个查 mdstat
// 块——common.ExecOutput 是 exec.Command 直调，没有 shell，"/dev/md*" 字面量
// 不会被展开，ls 必定报错(rc=2)，导致本函数恒返回空切片，下游 RAID 健康监控
// 全盲（真机实锤：md0 resync 57% 时面板仍报 healthy=true）。后改为遍历
// parseMdstatBlocks 的块；现在 sysfs 路径进一步改为直接 glob
// /sys/block/md*/md 枚举，同样不依赖 /dev 下设备节点是否存在。
func getRAIDStatus() []RAIDStatus {
	mdNames := enumerateSysfsMDNames()
	if len(mdNames) > 0 {
		var arrays []RAIDStatus
		for _, name := range mdNames {
			vals, members, ok := readRAIDSysfsValues(name)
			if !ok {
				continue
			}
			arrays = append(arrays, buildRAIDStatusFromSysfs(name, vals, members))
		}
		if len(arrays) > 0 {
			return arrays
		}
	}
	// sysfs 不可用/为空：回退 mdstat 文本解析。
	mdstatBytes, _ := os.ReadFile("/proc/mdstat")
	return buildRAIDStatusFromMdstat(string(mdstatBytes))
}

// mdstatProgressKindToActivity 把 mdstat 进度行的 Kind（resync/recovery/
// reshape/check/repair）映射到与 sysfs sync_action 一致的 Activity 值。
// 语义陷阱：sysfs 的值是 "recover"，mdstat 文本却是 "recovery"——两边拼写
// 不同，映射时不能直接照抄字符串。
func mdstatProgressKindToActivity(kind string) string {
	if kind == "recovery" {
		return "recover"
	}
	return kind
}

// buildRAIDStatusFromMdstat 纯函数：把 /proc/mdstat 全文解析为 []RAIDStatus。
// 从 getRAIDStatus 抽出，供表驱动单测直接注入 fixture，不发起任何系统调用。
// 字段填充语义与此前基于 ls 枚举时逐字节一致，只是枚举来源换成了
// parseMdstatBlocks 产出的块本身（MdName 匹配 ^md\d+$ 的块各出一条）。
// consensus R2：新增 Health/Activity 字段填充，与 sysfs 路径语义对齐——
// Activity 默认 "idle"，有进度行时取该行 Kind 映射值；Health 按
// raidHealthFromCounts 同一套规则推导（mdstat 没有 array_state，这里用
// HeaderFields[2]（如 "active"）顶替，FailedDisks==raid_disks 时判 failed）。
func buildRAIDStatusFromMdstat(content string) []RAIDStatus {
	var arrays []RAIDStatus
	blocks := parseMdstatBlocks(content)
	for _, block := range blocks {
		if !mdNameRe.MatchString(block.MdName) {
			continue
		}
		ra := RAIDStatus{Device: "/dev/" + block.MdName, Activity: "idle"}
		headerState := ""
		if len(block.HeaderFields) >= 3 {
			headerState = block.HeaderFields[2]
			ra.State = headerState
			ra.Level = extractRAIDLevel(strings.Join(block.HeaderFields, " "))
		}
		for _, tok := range block.MemberTokens {
			ra.Disks++
			switch memberFlag(tok) {
			case "F":
				ra.FailedDisks++
			case "S":
				ra.SpareDisks++
			default:
				ra.ActiveDisks++
			}
		}
		if ra.FailedDisks > 0 {
			ra.State = "degraded"
		}
		for _, p := range block.Progress {
			if p.Kind == "recovery" || p.Kind == "resync" {
				if p.Percent != "" {
					ra.SyncPercent = p.Percent
					ra.State = "resyncing"
				}
			}
			ra.Activity = mdstatProgressKindToActivity(p.Kind)
		}
		// consensus R2 回炉 Fix1（审查实锤）：mdstat 没有独立的 "degraded
		// 计数" 字段，此前直接用 FailedDisks 顶替——但 (F) token 只在失败
		// 成员还挂在阵列里时才存在，一旦被 `mdadm --remove` 摘除，头行成员
		// token 直接消失（不会留下 (F) 标记），降级信息只剩 StatusLine
		// （如 "[2/1] [U_]"）里的下划线。此时 FailedDisks=0，若只信
		// FailedDisks 会把已降级的阵列误报成 normal。
		// 修法：从 StatusLine 里补算 statusDegraded（下划线数）和
		// designDisks（槛位总数，即设计成员数），与 FailedDisks/Disks 取
		// max 后再传入 raidHealthFromCounts——两个来源谁先观察到降级就信谁，
		// 不会重复计数（同一降级状态下两者通常相等）。StatusLine 为空（如
		// 无状态行的纯文本）时 statusDegraded/designDisks 均为 0，max 退化
		// 为原值，不影响既有行为。
		statusDegraded := strings.Count(block.StatusLine, "_")
		designDisks := 0
		if block.StatusLine != "" {
			designDisks = len(block.StatusLine) - 2 // 去掉首尾的 "[" "]"
		}
		degraded := ra.FailedDisks
		if statusDegraded > degraded {
			degraded = statusDegraded
		}
		raidDisks := ra.Disks
		if designDisks > raidDisks {
			raidDisks = designDisks
		}
		if degraded > 0 && ra.State != "resyncing" {
			ra.State = "degraded"
		}
		ra.Health = raidHealthFromCounts(headerState, degraded, raidDisks, ra.FailedDisks)
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

// regexp_match helper
func regexp_match(pattern, s string) bool {
	matched, _ := regexp.MatchString(pattern, s)
	return matched
}

// ═══════════════════════════════════════════════════
// 维护页操作日志
// ═══════════════════════════════════════════════════

// OpLogEntry 操作日志条目
type OpLogEntry struct {
	Time    string `json:"time"`
	Action  string `json:"action"` // success / warning / error
	Message string `json:"message"`
}

// handleOperationsLog returns last N disk operations from journalctl
func handleOperationsLog(w http.ResponseWriter, r *http.Request) {
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "20"
	}

	// 过滤 nas-panel 日志中与存储操作相关的条目（fm 不在 adm/systemd-journal 组，
	// journalctl -u 读其他单元日志需要 root，S1-R3c）
	out, err := common.SudoOutput("journalctl", "-u", "nas-panel", "--no-pager", "-n", limit, "-o", "short-iso")
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"operations": []OpLogEntry{}})
		return
	}

	var entries []OpLogEntry
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		if len(line) < 20 {
			continue
		}
		// 格式: 2026-08-14T10:23:00 hostname nas-panel[pid]: message
		timeStr := ""
		msg := ""
		if len(line) > 25 {
			timeStr = strings.TrimSpace(line[:25])
			parts := strings.SplitN(line, "]: ", 2)
			if len(parts) == 2 {
				msg = parts[1]
			} else {
				msg = line[26:]
			}
		}
		if msg == "" {
			msg = line
		}
		action := "success"
		lowerMsg := strings.ToLower(msg)
		if strings.Contains(lowerMsg, "error") || strings.Contains(lowerMsg, "fail") || strings.Contains(lowerMsg, "失败") {
			action = "error"
		} else if strings.Contains(lowerMsg, "warn") || strings.Contains(lowerMsg, "警告") {
			action = "warning"
		}
		// 过滤只保留存储相关日志
		if !strings.Contains(lowerMsg, "disk") && !strings.Contains(lowerMsg, "pool") &&
			!strings.Contains(lowerMsg, "raid") && !strings.Contains(lowerMsg, "lvm") &&
			!strings.Contains(lowerMsg, "volume") && !strings.Contains(lowerMsg, "share") &&
			!strings.Contains(lowerMsg, "scrub") && !strings.Contains(lowerMsg, "rebuild") &&
			!strings.Contains(lowerMsg, "expand") && !strings.Contains(lowerMsg, "replace") &&
			!strings.Contains(lowerMsg, "磁盘") && !strings.Contains(lowerMsg, "存储") &&
			!strings.Contains(lowerMsg, "池") && !strings.Contains(lowerMsg, "卷") &&
			!strings.Contains(lowerMsg, "扩容") && !strings.Contains(lowerMsg, "重建") &&
			!strings.Contains(lowerMsg, "替换") && !strings.Contains(lowerMsg, "清理") {
			continue
		}
		entries = append(entries, OpLogEntry{
			Time:    timeStr,
			Action:  action,
			Message: strings.TrimSpace(msg),
		})
	}

	common.JSONResponse(w, map[string]interface{}{"operations": entries})
}

// isNFSExported checks if a path is exported via NFS in /etc/exports
func isNFSExported(path string) bool {
	out, _ := common.SudoOutput("cat", "/etc/exports")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, path) {
			return true
		}
	}
	return false
}

// isWebDAVServed checks if a folder is served via rclone WebDAV
func isWebDAVServed(folderName string) bool {
	out, err := common.SudoOutput("systemctl", "is-active", "rclone-webdav")
	if err != nil || strings.TrimSpace(out) != "active" {
		return false
	}
	return true
}

// isS3Served checks if rclone S3 server is running
func isS3Served(path string) bool {
	out, err := common.SudoOutput("systemctl", "is-active", "rclone-s3")
	if err != nil || strings.TrimSpace(out) != "active" {
		return false
	}
	return strings.HasPrefix(path, "/data")
}
