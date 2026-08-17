package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"nas-panel/common"
)

// PoolStatus represents storage pool overview
type PoolStatus struct {
	VGName    string         `json:"vg_name"`
	VGSize    string         `json:"vg_size"`
	VGFree    string         `json:"vg_free"`
	TotalGB   string         `json:"total_gb"`
	UsedGB    string         `json:"used_gb"`
	FreeGB    string         `json:"free_gb"`
	UsePercent string        `json:"use_percent"`
	PVs       []PVInfo       `json:"pvs"`
	LVs       []LVInfo       `json:"lvs"`
	Exists    bool           `json:"exists"`
}

type PVInfo struct {
	Name   string `json:"name"`
	VG     string `json:"vg"`
	Size   string `json:"size"`
	Free   string `json:"free"`
	Disk   string `json:"disk"`
}

type LVInfo struct {
	Name   string `json:"name"`
	VG     string `json:"vg"`
	Size   string `json:"size"`
	Path   string `json:"path"`
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
}

// handlePoolStatus returns LVM storage pool status
func handlePoolStatus(w http.ResponseWriter, r *http.Request) {
	pool := PoolStatus{Exists: false}

	// Check if any VG exists
	vgsOut, _ := common.SudoOutput("vgs", "--noheadings", "--units", "g", "-o", "vg_name,vg_size,vg_free")
	vgsOut = strings.TrimSpace(vgsOut)
	if vgsOut != "" {
		pool.Exists = true
		for _, line := range strings.Split(vgsOut, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 3 {
				pool.VGName = fields[0]
				pool.VGSize = fields[1]
				pool.VGFree = fields[2]
			}
		}
	}

	// Get PVs
	pvsOut, _ := common.SudoOutput("pvs", "--noheadings", "--units", "g", "-o", "pv_name,vg_name,pv_size,pv_free")
	for _, line := range strings.Split(pvsOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		pv := PVInfo{}
		if len(fields) >= 1 { pv.Name = fields[0] }
		if len(fields) >= 2 { pv.VG = fields[1] }
		if len(fields) >= 3 { pv.Size = fields[2] }
		if len(fields) >= 4 { pv.Free = fields[3] }
		// Extract disk name from PV path
		if strings.HasPrefix(pv.Name, "/dev/") {
			disk := pv.Name[5:]
			// Remove partition number for display
			disk = strings.TrimRight(disk, "0123456789")
			pv.Disk = "/dev/" + disk
		}
		pool.PVs = append(pool.PVs, pv)
	}

	// Get LVs
	lvsOut, _ := common.SudoOutput("lvs", "--noheadings", "--units", "g", "-o", "lv_name,vg_name,lv_size,lv_path")
	for _, line := range strings.Split(lvsOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		lv := LVInfo{}
		if len(fields) >= 1 { lv.Name = fields[0] }
		if len(fields) >= 2 { lv.VG = fields[1] }
		if len(fields) >= 3 { lv.Size = fields[2] }
		if len(fields) >= 4 { lv.Path = fields[3] }
		// Get mount and fstype from findmnt
		if lv.Path != "" {
			mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET,FSTYPE", "--source", lv.Path)
			mntFields := strings.Fields(mntOut)
			if len(mntFields) >= 1 { lv.Mount = mntFields[0] }
			if len(mntFields) >= 2 { lv.FSType = mntFields[1] }
		}
		pool.LVs = append(pool.LVs, lv)
	}

	// Calculate total/used/free in GB
	if pool.Exists {
		pool.TotalGB = strings.TrimRight(pool.VGSize, "gG")
		pool.FreeGB = strings.TrimRight(pool.VGFree, "gG")
		// Parse and compute used
		totalF := parseFloatSafe(pool.TotalGB)
		freeF := parseFloatSafe(pool.FreeGB)
		usedF := totalF - freeF
		pool.UsedGB = fmt.Sprintf("%.1f", usedF)
		if totalF > 0 {
			pool.UsePercent = fmt.Sprintf("%.0f", (usedF/totalF)*100)
		}
	}

	common.JSONResponse(w, map[string]interface{}{"pool": pool})
}

// handlePoolCreate creates LVM storage pool from multiple disks
func handlePoolCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	vgName := r.FormValue("vg_name")
	if vgName == "" { vgName = "vg_nas" }
	lvName := r.FormValue("lv_name")
	if lvName == "" { lvName = "data" }
	mountPoint := r.FormValue("mountpoint")
	if mountPoint == "" { mountPoint = "/data" }
	fstype := r.FormValue("fstype")
	if fstype == "" { fstype = "ext4" }
	devicesStr := r.FormValue("devices")
	confirm := r.FormValue("confirm")

	if devicesStr == "" {
		http.Error(w, `{"error":"请选择至少一块磁盘"}`, http.StatusBadRequest)
		return
	}
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(mountPoint, "/data") {
		http.Error(w, `{"error":"挂载点必须在 /data 下"}`, http.StatusBadRequest)
		return
	}

	devices := strings.Split(devicesStr, ",")
	var steps []string

	// 1. pvcreate
	for _, dev := range devices {
		dev = strings.TrimSpace(dev)
		if isSystemDisk(dev) {
			http.Error(w, fmt.Sprintf(`{"error":"不允许使用系统盘 %s"}`, dev), http.StatusBadRequest)
			return
		}
		out, err := common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"pvcreate 失败: %s"}`, out), http.StatusInternalServerError)
			return
		}
		steps = append(steps, "pvcreate "+dev)
	}

	// 2. vgcreate
	vgArgs := append([]string{"-f", vgName}, devices...)
	out, err := common.SudoExec("/usr/sbin/vgcreate", vgArgs...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"vgcreate 失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "vgcreate "+vgName+" ("+devicesStr+")")

	// 3. lvcreate
	out, err = common.SudoExec("/usr/sbin/lvcreate", "-y", "-l", "100%FREE", "-n", lvName, vgName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"lvcreate 失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	lvPath := "/dev/" + vgName + "/" + lvName
	steps = append(steps, "lvcreate "+lvName+" (100%FREE)")

	// 4. format
	mkfsCmd := "mkfs." + fstype
	mkfsArgs := []string{"-F"}
	if fstype == "xfs" { mkfsArgs = []string{"-f"} }
	mkfsArgs = append(mkfsArgs, lvPath)
	out, err = common.SudoExec(mkfsCmd, mkfsArgs...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"格式化失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "格式化 "+lvPath+" 为 "+fstype)

	// 5. mkdir
	if mountPoint != "/data" {
		common.SudoExec("mkdir", "-p", mountPoint)
		steps = append(steps, "创建目录 "+mountPoint)
	}

	// 6. mount
	common.SudoExec("mount", lvPath, mountPoint)
	steps = append(steps, "挂载 "+lvPath+" → "+mountPoint)

	// 7. fstab
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	uuid := strings.TrimSpace(uuidOut)
	if uuid != "" {
		fstabLine := fmt.Sprintf("UUID=%s %s %s defaults 0 2", uuid, mountPoint, fstype)
		fstabData, _ := os.ReadFile("/etc/fstab")
		content := string(fstabData)
		if !strings.HasSuffix(content, "\n") { content += "\n" }
		content += fstabLine + "\n"
		common.SafeWriteFile("/etc/fstab", content)
		steps = append(steps, "写入 fstab (UUID="+uuid+")")
	}

	// 8. chown
	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" { nasUser = "root" }
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限 "+nasUser)

	common.JSONResponse(w, map[string]interface{}{
		"message": "存储池创建完成",
		"steps":   steps,
		"vg_name": vgName,
		"lv_name": lvName,
		"mountpoint": mountPoint,
		"total":   "已合并 " + fmt.Sprintf("%d", len(devices)) + " 块盘",
	})
}

// handlePoolExtend extends existing LVM pool with new disk
func handlePoolExtend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	vgName := r.FormValue("vg_name")
	if vgName == "" { vgName = "vg_nas" }
	device := r.FormValue("device")
	lvName := r.FormValue("lv_name")
	if lvName == "" { lvName = "data" }
	confirm := r.FormValue("confirm")

	if device == "" {
		http.Error(w, `{"error":"请选择磁盘"}`, http.StatusBadRequest)
		return
	}
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}
	if isSystemDisk(device) {
		http.Error(w, `{"error":"不允许使用系统盘"}`, http.StatusBadRequest)
		return
	}

	var steps []string

	// 1. pvcreate
	out, err := common.SudoExec("pvcreate", "-f", device)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"pvcreate 失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "pvcreate "+device)

	// 2. vgextend
	out, err = common.SudoExec("/usr/sbin/vgextend", vgName, device)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"vgextend 失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "vgextend "+vgName+" + "+device)

	// 3. lvextend
	lvPath := "/dev/" + vgName + "/" + lvName
	out, err = common.SudoExec("/usr/sbin/lvextend", "-l", "+100%FREE", lvPath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"lvextend 失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "lvextend "+lvName+" +100%FREE")

	// 4. resize filesystem
	out, err = common.SudoExec("/usr/sbin/resize2fs", lvPath)
	if err != nil {
		// Try xfs_growfs for xfs
		out2, err2 := common.SudoExec("/usr/sbin/xfs_growfs", lvPath)
		if err2 != nil {
			http.Error(w, fmt.Sprintf(`{"error":"resize 失败: %s / %s"}`, out, out2), http.StatusInternalServerError)
			return
		}
		steps = append(steps, "xfs_growfs "+lvName)
	} else {
		steps = append(steps, "resize2fs "+lvName)
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": "存储池扩容完成",
		"steps":   steps,
		"device":  device,
		"vg_name": vgName,
	})
}

func parseFloatSafe(s string) float64 {
	s = strings.TrimSpace(strings.TrimRight(s, "gG "))
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

// handlePoolDelete destroys a storage pool and releases all its disks
func handlePoolDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	poolType := r.FormValue("pool_type")
	poolDevice := r.FormValue("pool_device")
	confirm := r.FormValue("confirm")

	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}

	var steps []string

	switch poolType {
	case "lvm":
		// 1. Find and unmount all LVs in this VG
		vgName := extractVGName(poolDevice)
		if vgName == "" {
			vgName = "vg_nas"
		}
		lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "-o", "lv_name,lv_path", vgName)
		for _, line := range strings.Split(lvsOut, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 2 {
				lvPath := fields[1]
				// Find mountpoint
				mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", "--source", lvPath)
				mountPoint := strings.TrimSpace(mntOut)
				if mountPoint != "" {
					common.SudoExec("umount", "-l", mountPoint)
					steps = append(steps, "卸载 "+mountPoint)
				}
				common.SudoExec("/usr/sbin/lvremove", "-f", lvPath)
				steps = append(steps, "删除 LV "+lvPath)
			}
		}
		// 2. Remove VG
		common.SudoExec("/usr/sbin/vgremove", "-f", vgName)
		steps = append(steps, "删除 VG "+vgName)
		// 3. Remove PVs
		pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name")
		for _, pvName := range strings.Fields(pvsOut) {
			if strings.HasPrefix(pvName, "/dev/") {
				common.SudoExec("/usr/sbin/pvremove", "-f", pvName)
				common.SudoExec("/usr/sbin/wipefs", "-a", pvName)
				steps = append(steps, "释放 "+pvName)
			}
		}

	case "raid1", "raid0", "raid5", "raid6":
		// 1. Unmount
		mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", "--source", poolDevice)
		mountPoint := strings.TrimSpace(mntOut)
		if mountPoint != "" {
			common.SudoExec("umount", "-l", mountPoint)
			steps = append(steps, "卸载 "+mountPoint)
		}
		// 2. Stop RAID
		common.SudoExec("/usr/sbin/mdadm", "--stop", poolDevice)
		steps = append(steps, "停止 "+poolDevice)
		// 3. Zero superblocks on member disks
		disks := getDiskStatus()
		for _, d := range disks {
			if d.Name == "sr0" || strings.HasPrefix(d.Name, "loop") {
				continue
			}
			if isSystemDisk(d.Device) {
				continue
			}
			common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", d.Device)
			common.SudoExec("/usr/sbin/wipefs", "-a", d.Device)
			steps = append(steps, "释放 "+d.Device)
		}

	case "single":
		// Independent disk: unmount + wipe
		mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", "--source", poolDevice)
		mountPoint := strings.TrimSpace(mntOut)
		if mountPoint != "" {
			common.SudoExec("umount", "-l", mountPoint)
			steps = append(steps, "卸载 "+mountPoint)
		}
		common.SudoExec("/usr/sbin/wipefs", "-a", poolDevice)
		steps = append(steps, "释放 "+poolDevice)

	default:
		http.Error(w, fmt.Sprintf(`{"error":"未知池类型: %s"}`, poolType), http.StatusBadRequest)
		return
	}

	// Clean fstab entries for /data/nas*
	fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
	if fstabData != "" {
		lines := strings.Split(fstabData, "\n")
		var newLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				newLines = append(newLines, line)
				continue
			}
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 && strings.HasPrefix(fields[1], "/data/nas") {
				continue
			}
			newLines = append(newLines, line)
		}
		content := strings.Join(newLines, "\n")
		if content != fstabData {
			common.SafeWriteFile("/etc/fstab", content)
			steps = append(steps, "清理 fstab")
		}
	}

	// Clean Samba shares for nas*
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
		newConf := removeSambaSharesByPrefix(smbConf, "nas")
		if newConf != smbConf {
			common.SafeWriteFile("/etc/samba/smb.conf", newConf)
			common.SudoExec("systemctl", "restart", "smbd")
			steps = append(steps, "清理 Samba 共享")
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("存储池已删除，释放 %d 步", len(steps)),
		"steps":   steps,
	})
}

// ensure json import is used
var _ = json.Marshal
