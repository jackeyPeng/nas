package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nas-panel/common"
)

// ProgressEvent is sent to frontend via SSE
type ProgressEvent struct {
	Step   string `json:"step"`
	Status string `json:"status"` // running, done, error
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Detail string `json:"detail,omitempty"`
}

// sendProgress writes an SSE event
func sendProgress(w http.ResponseWriter, ev ProgressEvent) {
	data, _ := json.Marshal(ev)
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

// handleWizardSetupStream does storage setup with real-time progress
func handleWizardSetupStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = r.FormValue("mode")
	}
	confirm := r.URL.Query().Get("confirm")
	if confirm == "" {
		confirm = r.FormValue("confirm")
	}

	if confirm != "yes" {
		sendProgress(w, ProgressEvent{Step: "需要确认", Status: "error", Detail: "请加 confirm=yes"})
		return
	}

	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}

	// Get unused disks
	disks := getDiskStatus()
	var unusedDevs []string
	for _, d := range disks {
		if d.Name == "sr0" || d.Name == "zram0" || strings.HasPrefix(d.Name, "loop") {
			continue
		}
		if isSystemDisk(d.Device) {
			continue
		}
		if d.Type == "unused" {
			unusedDevs = append(unusedDevs, d.Device)
		}
	}

	if len(unusedDevs) == 0 {
		sendProgress(w, ProgressEvent{Step: "检查磁盘", Status: "error", Detail: "没有可用的空闲磁盘"})
		return
	}

	// Calculate total steps based on mode
	totalSteps := getModeStepCount(mode, len(unusedDevs))
	if totalSteps == 0 {
		sendProgress(w, ProgressEvent{Step: "模式检查", Status: "error", Detail: "未知模式: " + mode})
		return
	}

	// Validate mode requirements
	if err := validateMode(mode, len(unusedDevs)); err != "" {
		sendProgress(w, ProgressEvent{Step: "模式检查", Status: "error", Detail: err})
		return
	}

	step := 0

	stepDone := func(name string) {
		step++
		sendProgress(w, ProgressEvent{Step: name, Status: "done", Index: step, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	stepRunning := func(name string) {
		sendProgress(w, ProgressEvent{Step: name, Status: "running", Index: step + 1, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}

	mountPoint := "/data/nas1"

	switch mode {
	case "single":
		// LVM single disk — for future expansion
		dev := unusedDevs[0]
		setupLVMSingleStream(w, dev, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "merge":
		// LVM merge — combine all disks
		setupLVMMergeStream(w, unusedDevs, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "raid0":
		// RAID0 stripe — all disks, no redundancy
		setupRAIDStream(w, unusedDevs, 0, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "raid1":
		// RAID1 mirror — 2 disks
		setupRAIDStream(w, unusedDevs[:2], 1, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "raid5":
		// RAID5 — 3+ disks
		setupRAIDStream(w, unusedDevs, 5, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "raid6":
		// RAID6 — 4+ disks
		setupRAIDStream(w, unusedDevs, 6, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "separate":
		// Each disk independent
		setupSeparateStream(w, unusedDevs, nasUser, stepRunning, stepDone, totalSteps)
	}

	sendProgress(w, ProgressEvent{
		Step:   "完成",
		Status: "complete",
		Index:  totalSteps,
		Total:  totalSteps,
		Detail: fmt.Sprintf("存储配置完成，共 %d 块磁盘", len(unusedDevs)),
	})
}

// validateMode checks if the mode is valid for the given disk count
func validateMode(mode string, diskCount int) string {
	switch mode {
	case "single":
		if diskCount < 1 {
			return "至少需要 1 块磁盘"
		}
	case "merge", "raid0", "separate":
		if diskCount < 2 {
			return "至少需要 2 块磁盘"
		}
	case "raid1":
		if diskCount < 2 {
			return "RAID1 至少需要 2 块磁盘"
		}
	case "raid5":
		if diskCount < 3 {
			return "RAID5 至少需要 3 块磁盘"
		}
	case "raid6":
		if diskCount < 4 {
			return "RAID6 至少需要 4 块磁盘"
		}
	default:
		return "未知模式: " + mode
	}
	return ""
}

// getModeStepCount calculates total steps for a given mode
func getModeStepCount(mode string, diskCount int) int {
	switch mode {
	case "single":
		return 6 // wipe, pvcreate, vgcreate, lvcreate, format, mount+fstab
	case "merge":
		return 5 + diskCount // per-disk wipe+pvcreate, vgcreate, lvcreate, format, mount
	case "raid0", "raid1", "raid5", "raid6":
		return 7 // wipe, create raid, wait, format, mount, fstab, samba
	case "separate":
		return 5 * diskCount // per-disk: wipe, partition, format, mount, samba
	default:
		return 0
	}
}

// setupLVMSingleStream: LVM on single disk for future expansion
func setupLVMSingleStream(w http.ResponseWriter, dev, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	stepRunning("清除磁盘签名")
	common.SudoExec("/usr/sbin/wipefs", "-a", dev)
	stepDone("清除磁盘签名")

	stepRunning("创建物理卷 (pvcreate)")
	common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
	stepDone("创建物理卷")

	stepRunning("创建卷组 (vgcreate)")
	vgName := "vg_nas"
	common.SudoExec("/usr/sbin/vgcreate", "-f", vgName, dev)
	stepDone("创建卷组 vg_nas")

	stepRunning("创建逻辑卷 (lvcreate)")
	common.SudoExec("/usr/sbin/lvcreate", "-l", "100%FREE", "-n", "data", vgName)
	lvPath := "/dev/" + vgName + "/data"
	stepDone("创建逻辑卷 data")

	stepRunning("格式化 (xfs)")
	common.SudoExec("mkfs.xfs", "-f", lvPath)
	stepDone("格式化 xfs")

	stepRunning("挂载并配置")
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", lvPath, mountPoint)
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	addSambaShare("nas1", mountPoint, nasUser)
	stepDone("挂载并配置 Samba 共享")
}

// setupLVMMergeStream: LVM merge multiple disks
func setupLVMMergeStream(w http.ResponseWriter, devs []string, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	// Per-disk: wipe + pvcreate
	for i, dev := range devs {
		stepRunning(fmt.Sprintf("清除并初始化磁盘 %d/%d", i+1, len(devs)))
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
		stepDone(fmt.Sprintf("初始化磁盘 %d/%d", i+1, len(devs)))
	}

	stepRunning("创建卷组 (vgcreate)")
	vgName := "vg_nas"
	vgArgs := append([]string{"-f", vgName}, devs...)
	common.SudoExec("/usr/sbin/vgcreate", vgArgs...)
	stepDone("创建卷组 vg_nas")

	stepRunning("创建逻辑卷 (lvcreate)")
	common.SudoExec("/usr/sbin/lvcreate", "-l", "100%FREE", "-n", "data", vgName)
	lvPath := "/dev/" + vgName + "/data"
	stepDone("创建逻辑卷 data")

	stepRunning("格式化 (xfs)")
	common.SudoExec("mkfs.xfs", "-f", lvPath)
	stepDone("格式化 xfs")

	stepRunning("挂载并配置")
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", lvPath, mountPoint)
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	addSambaShare("nas1", mountPoint, nasUser)
	stepDone("挂载并配置 Samba 共享")
}

// setupRAIDStream: RAID0/1/5/6
func setupRAIDStream(w http.ResponseWriter, devs []string, level int, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	// 1. Wipe all disks
	stepRunning(fmt.Sprintf("清除 %d 块磁盘签名", len(devs)))
	for _, dev := range devs {
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
	}
	stepDone("清除磁盘签名")

	// 2. Create RAID array
	levelStr := fmt.Sprintf("raid%d", level)
	if level == 0 {
		levelStr = "stripe"
	} else if level == 1 {
		levelStr = "mirror"
	}
	stepRunning(fmt.Sprintf("创建 %s (RAID%d)", levelStr, level))
	mdDev := "/dev/md0"
	args := []string{"--create", mdDev, "--level=" + fmt.Sprintf("%d", level), "--raid-devices=" + fmt.Sprintf("%d", len(devs)), "--run"}
	args = append(args, devs...)
	common.SudoExec("/usr/sbin/mdadm", args...)
	stepDone(fmt.Sprintf("创建 RAID%d 阵列", level))

	// 3. Wait for md device
	stepRunning("等待阵列就绪")
	time.Sleep(2 * time.Second)
	stepDone("阵列就绪")

	// 4. Format
	stepRunning("格式化 (xfs)")
	common.SudoExec("mkfs.xfs", "-f", mdDev)
	stepDone("格式化 xfs")

	// 5. Mount + fstab
	stepRunning("挂载并配置")
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", mdDev, mountPoint)
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", mdDev)
	writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	stepDone("挂载并写入 fstab")

	// 6. Save RAID config
	stepRunning("保存 RAID 配置")
	common.SudoExec("bash", "-c", "/usr/sbin/mdadm --detail --scan >> /etc/mdadm/mdadm.conf 2>/dev/null || /usr/sbin/mdadm --detail --scan >> /etc/mdadm.conf 2>/dev/null || true")
	stepDone("保存 RAID 配置")

	// 7. Add Samba share
	stepRunning("配置 Samba 共享")
	addSambaShare("nas1", mountPoint, nasUser)
	stepDone("配置 Samba 共享")
}

// setupSeparateStream: each disk independent
func setupSeparateStream(w http.ResponseWriter, devs []string, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	for i, dev := range devs {
		mountPoint := fmt.Sprintf("/data/nas%d", i+1)

		stepRunning(fmt.Sprintf("磁盘 %d: 清除签名", i+1))
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		stepDone(fmt.Sprintf("磁盘 %d: 清除签名", i+1))

		stepRunning(fmt.Sprintf("磁盘 %d: 创建分区", i+1))
		common.SudoExec("/usr/sbin/parted", "-s", dev, "mklabel", "gpt", "mkpart", "primary", "xfs", "0%", "100%")
		time.Sleep(500 * time.Millisecond)
		partDev := dev + "1"
		stepDone(fmt.Sprintf("磁盘 %d: 创建分区", i+1))

		stepRunning(fmt.Sprintf("磁盘 %d: 格式化", i+1))
		common.SudoExec("mkfs.xfs", "-f", partDev)
		stepDone(fmt.Sprintf("磁盘 %d: 格式化", i+1))

		stepRunning(fmt.Sprintf("磁盘 %d: 挂载", i+1))
		common.SudoExec("mkdir", "-p", mountPoint)
		common.SudoExec("mount", partDev, mountPoint)
		uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", partDev)
		writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
		common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
		stepDone(fmt.Sprintf("磁盘 %d: 挂载到存储空间%d", i+1, i+1))

		shareName := fmt.Sprintf("nas%d", i+1)
		stepRunning(fmt.Sprintf("磁盘 %d: Samba 共享", i+1))
		addSambaShare(shareName, mountPoint, nasUser)
		stepDone(fmt.Sprintf("磁盘 %d: Samba 共享 %s", i+1, shareName))
	}
}

// handleWizardResetStream does storage reset with real-time progress
func handleWizardResetStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	confirm := r.URL.Query().Get("confirm")
	if confirm != "yes" {
		sendProgress(w, ProgressEvent{Step: "需要确认", Status: "error"})
		return
	}

	totalSteps := 7
	step := 0
	stepDone := func(name string) {
		step++
		sendProgress(w, ProgressEvent{Step: name, Status: "done", Index: step, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	stepRunning := func(name string) {
		sendProgress(w, ProgressEvent{Step: name, Status: "running", Index: step + 1, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}

	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}

	// 1. Unmount
	stepRunning("卸载数据目录")
	mounts := getExistingDataMounts()
	for _, m := range mounts {
		mount := m["mount"]
		if mount == "/data" || strings.HasPrefix(mount, "/data/nas") {
			common.SudoExec("umount", "-l", mount)
		}
	}
	stepDone("卸载数据目录")

	// 2. Clean fstab
	stepRunning("清理 fstab")
	fstabData, _ := common.SudoOutput("cat", "/etc/fstab")
	if fstabData != "" {
		lines := strings.Split(fstabData, "\n")
		var newLines []string
		for _, line := range lines {
			if strings.Contains(line, "/data") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			newLines = append(newLines, line)
		}
		cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/fstab", strings.Join(newLines, "\n"))
		common.SudoExec("bash", "-c", cmd)
	}
	stepDone("清理 fstab")

	// 3. Remove LVM
	stepRunning("删除 LVM 卷")
	vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings", "-o", "vg_name")
	if strings.TrimSpace(vgsOut) != "" {
		for _, vgName := range strings.Fields(vgsOut) {
			lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "-o", "lv_name", vgName)
			for _, lvName := range strings.Fields(lvsOut) {
				common.SudoExec("/usr/sbin/lvremove", "-f", "/dev/"+vgName+"/"+lvName)
			}
			common.SudoExec("/usr/sbin/vgremove", "-f", vgName)
		}
	}
	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name")
	for _, pvName := range strings.Fields(pvsOut) {
		if strings.HasPrefix(pvName, "/dev/") {
			common.SudoExec("/usr/sbin/pvremove", "-f", pvName)
		}
	}
	stepDone("删除 LVM 卷")

	// 4. Stop RAID
	stepRunning("停止 RAID")
	common.SudoExec("/usr/sbin/mdadm", "--stop", "--scan")
	// Zero superblock on all possible data disks
	for _, dev := range []string{"/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde", "/dev/sdf"} {
		if _, err := common.ExecOutput("ls", dev); err == nil {
			common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", dev)
		}
	}
	common.SudoExec("bash", "-c", "echo '' > /etc/mdadm/mdadm.conf 2>/dev/null || echo '' > /etc/mdadm.conf 2>/dev/null || true")
	stepDone("停止 RAID")

	// 5. Wipe disks
	stepRunning("清除磁盘签名")
	for _, dev := range []string{"/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde", "/dev/sdf"} {
		if _, err := common.ExecOutput("ls", dev); err == nil {
			common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		}
	}
	stepDone("清除磁盘签名")

	// 6. Clean Samba
	stepRunning("清理 Samba 共享")
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
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
		cmd := fmt.Sprintf("echo '%s' | sudo tee /etc/samba/smb.conf", strings.Join(newLines, "\n"))
		common.SudoExec("bash", "-c", cmd)
		common.SudoExec("systemctl", "restart", "smbd")
	}
	stepDone("清理 Samba 共享")

	// 7. Recreate /data
	stepRunning("重建 /data 目录")
	common.SudoExec("mkdir", "-p", "/data")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, "/data")
	stepDone("重建 /data 目录")

	sendProgress(w, ProgressEvent{
		Step:   "完成",
		Status: "complete",
		Index:  totalSteps,
		Total:  totalSteps,
		Detail: "存储已重置，磁盘已释放",
	})
}
