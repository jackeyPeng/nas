package diskmgmt

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"nas-panel/common"
)

// handleWizardReset: destroy existing storage config, release disks
// Modes: destroy (just unmount+remove fstab), reconfig (destroy + back to wizard)
func handleWizardReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	confirm := r.FormValue("confirm")
	if confirm != "yes" {
		http.Error(w, `{"error":"请确认操作"}`, http.StatusBadRequest)
		return
	}

	var steps []string

	// 1. Unmount all /data/nas* and /data (if it's LVM/RAID)
	mounts := getExistingDataMounts()
	for _, m := range mounts {
		mount := m["mount"]
		if mount == "/data" || strings.HasPrefix(mount, "/data/nas") {
			common.SudoExec("umount", "-l", mount)
			steps = append(steps, "卸载 "+mount)
		}
	}

	// 2. Remove from fstab — precise match, only /data and /data/nasN entries
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
			if len(fields) >= 2 {
				mountPoint := fields[1]
				// Only remove /data and /data/nasN entries
				if mountPoint == "/data" || isDataNasMount(mountPoint) {
					continue
				}
			}
			newLines = append(newLines, line)
		}
		content := strings.Join(newLines, "\n")
		if content != fstabData {
			common.SafeWriteFile("/etc/fstab", content)
			steps = append(steps, "清理 fstab")
		}
	}

	// 3. Remove LVM (if exists)
	vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings", "-o", "vg_name")
	vgsOut = strings.TrimSpace(vgsOut)
	if vgsOut != "" {
		for _, vgName := range strings.Fields(vgsOut) {
			vgName = strings.TrimSpace(vgName)
			if vgName == "" {
				continue
			}
			// Remove LVs
			lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "-o", "lv_name", vgName)
			for _, lvName := range strings.Fields(lvsOut) {
				common.SudoExec("/usr/sbin/lvremove", "-f", "/dev/"+vgName+"/"+lvName)
			}
			common.SudoExec("/usr/sbin/vgremove", "-f", vgName)
			steps = append(steps, "删除卷组 "+vgName)
		}
	}

	// 4. Remove PVs
	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name")
	for _, pvName := range strings.Fields(pvsOut) {
		if strings.HasPrefix(pvName, "/dev/") {
			common.SudoExec("/usr/sbin/pvremove", "-f", pvName)
		}
	}
	steps = append(steps, "清除物理卷")

	// 5. Stop RAID (only /dev/mdN devices, not --scan)
	stopRAIDArrays()
	// Zero superblock on all data disks
	dataDisks := getDataDisks()
	for _, dev := range dataDisks {
		common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", dev)
	}
	// Clear mdadm config
	common.SafeWriteFile("/etc/mdadm/mdadm.conf", "")
	steps = append(steps, "清除 RAID 配置")

	// 6. Wipe disk signatures on all data disks
	for _, dev := range dataDisks {
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
	}
	steps = append(steps, "清除磁盘签名")

	// 7. Remove Samba shares (nas1, nas2, etc)
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
		newConf := removeSambaSharesByPrefix(smbConf, "nas")
		if newConf != smbConf {
			common.SafeWriteFile("/etc/samba/smb.conf", newConf)
			common.SudoExec("systemctl", "restart", "smbd")
			steps = append(steps, "清理 Samba 共享")
		}
	}

	// 8. Recreate base /data directory
	common.SudoExec("mkdir", "-p", "/data")
	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, "/data")
	steps = append(steps, "重建 /data 目录")

	common.JSONResponse(w, map[string]interface{}{
		"message": "存储已重置，磁盘已释放",
		"steps":   steps,
	})
}

// isDataNasMount checks if a path matches /data/nasN pattern
func isDataNasMount(path string) bool {
	matched, _ := regexp.MatchString(`^/data/nas\d+$`, path)
	return matched
}

// stopRAIDArrays stops all /dev/mdN arrays individually (not --scan)
func stopRAIDArrays() {
	matches, _ := filepath.Glob("/dev/md[0-9]*")
	for _, dev := range matches {
		// Only match /dev/mdN (not /dev/mdNp1 partitions)
		if regexp_match(`^/dev/md\d+$`, dev) {
			common.SudoExec("/usr/sbin/mdadm", "--stop", dev)
		}
	}
}

// removeSambaSharesByPrefix removes all share sections whose name starts with prefix
func removeSambaSharesByPrefix(conf, prefix string) string {
	lines := strings.Split(conf, "\n")
	var newLines []string
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			shareName := strings.Trim(trimmed, "[]")
			if strings.HasPrefix(shareName, prefix) {
				skip = true
				continue
			} else {
				skip = false
			}
		}
		if !skip {
			newLines = append(newLines, line)
		}
	}
	return strings.Join(newLines, "\n")
}
