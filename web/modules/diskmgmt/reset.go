package diskmgmt

import (
	"fmt"
	"net/http"
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

	// 2. Remove from fstab
	fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
	if fstabData != "" {
		lines := strings.Split(fstabData, "\n")
		var newLines []string
		for _, line := range lines {
			// Keep system entries, remove /data entries
			if strings.Contains(line, "/data") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			newLines = append(newLines, line)
		}
		content := strings.Join(newLines, "\n")
		cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/fstab", content)
		common.SudoExec("bash", "-c", cmd)
		steps = append(steps, "清理 fstab")
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

	// 5. Stop RAID (if exists)
	common.SudoExec("/usr/sbin/mdadm", "--stop", "--scan")
	common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", "/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde")
	// Remove mdadm config
	common.SudoExec("bash", "-c", "echo '' > /etc/mdadm/mdadm.conf 2>/dev/null || true")
	steps = append(steps, "清除 RAID 配置")

	// 6. Wipe disk signatures
	for _, dev := range []string{"/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde"} {
		// Check if device exists
		if _, err := common.ExecOutput("ls", dev); err == nil {
			common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		}
	}
	steps = append(steps, "清除磁盘签名")

	// 7. Remove Samba shares (nas1, nas2, etc)
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
		// Remove nas1, nas2, nas3... share sections
		lines := strings.Split(smbConf, "\n")
		var newLines []string
		skip := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[nas") && strings.HasSuffix(trimmed, "]") {
				skip = true
				continue
			}
			if skip && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				skip = false
			}
			if !skip {
				newLines = append(newLines, line)
			}
		}
		content := strings.Join(newLines, "\n")
		cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/samba/smb.conf", content)
		common.SudoExec("bash", "-c", cmd)
		common.SudoExec("systemctl", "restart", "smbd")
		steps = append(steps, "清理 Samba 共享")
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
