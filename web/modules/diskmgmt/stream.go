package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	// Determine mount point: find next available /data/nasN
	mountPoint := "/data/nas1"
	for i := 1; i <= 9; i++ {
		testMount := fmt.Sprintf("/data/nas%d", i)
		// Check if already mounted
		mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", testMount)
		if strings.TrimSpace(mntOut) == "" {
			// Check if directory exists and is non-empty (already a data dir)
			if entries, err := os.ReadDir(testMount); err != nil || len(entries) == 0 {
				mountPoint = testMount
				break
			}
		}
	}

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

// stepError sends an error progress event and returns true if should abort
func stepError(w http.ResponseWriter, step string, detail string) {
	sendProgress(w, ProgressEvent{Step: step, Status: "error", Detail: detail})
}

// checkExecError checks if a SudoExec result has an error, sends SSE error if so
// returns true if should abort
func checkExecError(w http.ResponseWriter, stepName, output string, err error) bool {
	if err != nil {
		stepError(w, stepName, output+": "+err.Error())
		return true
	}
	return false
}

// setupLVMSingleStream: LVM on single disk for future expansion
func setupLVMSingleStream(w http.ResponseWriter, dev, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	stepRunning("清除磁盘签名")
	out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
	if checkExecError(w, "清除磁盘签名", out, err) { return }
	stepDone("清除磁盘签名")

	stepRunning("创建物理卷 (pvcreate)")
	out, err = common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
	if checkExecError(w, "创建物理卷", out, err) { return }
	stepDone("创建物理卷")

	stepRunning("创建卷组 (vgcreate)")
	vgName := "vg_nas"
	out, err = common.SudoExec("/usr/sbin/vgcreate", "-f", vgName, dev)
	if checkExecError(w, "创建卷组", out, err) { return }
	stepDone("创建卷组 vg_nas")

	stepRunning("创建逻辑卷 (lvcreate)")
	out, err = common.SudoExec("/usr/sbin/lvcreate", "-l", "100%FREE", "-n", "data", vgName)
	if checkExecError(w, "创建逻辑卷", out, err) { return }
	lvPath := "/dev/" + vgName + "/data"
	stepDone("创建逻辑卷 data")

	stepRunning("格式化 (xfs)")
	out, err = common.SudoExec("mkfs.xfs", "-f", lvPath)
	if checkExecError(w, "格式化", out, err) { return }
	stepDone("格式化 xfs")

	stepRunning("挂载并配置")
	common.SudoExec("mkdir", "-p", mountPoint)
	out, err = common.SudoExec("mount", lvPath, mountPoint)
	if checkExecError(w, "挂载", out, err) { return }
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	addSambaShare(shareNameFromMount(mountPoint), mountPoint, nasUser)
	stepDone("挂载并配置 Samba 共享")
}

// setupLVMMergeStream: LVM merge multiple disks
func setupLVMMergeStream(w http.ResponseWriter, devs []string, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	// Per-disk: wipe + pvcreate
	for i, dev := range devs {
		stepRunning(fmt.Sprintf("清除并初始化磁盘 %d/%d", i+1, len(devs)))
		out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		if checkExecError(w, fmt.Sprintf("清除磁盘 %d", i+1), out, err) { return }
		out, err = common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
		if checkExecError(w, fmt.Sprintf("初始化磁盘 %d", i+1), out, err) { return }
		stepDone(fmt.Sprintf("初始化磁盘 %d/%d", i+1, len(devs)))
	}

	stepRunning("创建卷组 (vgcreate)")
	vgName := "vg_nas"
	vgArgs := append([]string{"-f", vgName}, devs...)
	out, err := common.SudoExec("/usr/sbin/vgcreate", vgArgs...)
	if checkExecError(w, "创建卷组", out, err) { return }
	stepDone("创建卷组 vg_nas")

	stepRunning("创建逻辑卷 (lvcreate)")
	out, err = common.SudoExec("/usr/sbin/lvcreate", "-l", "100%FREE", "-n", "data", vgName)
	if checkExecError(w, "创建逻辑卷", out, err) { return }
	lvPath := "/dev/" + vgName + "/data"
	stepDone("创建逻辑卷 data")

	stepRunning("格式化 (xfs)")
	out, err = common.SudoExec("mkfs.xfs", "-f", lvPath)
	if checkExecError(w, "格式化", out, err) { return }
	stepDone("格式化 xfs")

	stepRunning("挂载并配置")
	common.SudoExec("mkdir", "-p", mountPoint)
	out, err = common.SudoExec("mount", lvPath, mountPoint)
	if checkExecError(w, "挂载", out, err) { return }
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	addSambaShare(shareNameFromMount(mountPoint), mountPoint, nasUser)
	stepDone("挂载并配置 Samba 共享")
}

// setupRAIDStream: RAID0/1/5/6
func setupRAIDStream(w http.ResponseWriter, devs []string, level int, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	// 1. Wipe all disks
	stepRunning(fmt.Sprintf("清除 %d 块磁盘签名", len(devs)))
	for _, dev := range devs {
		out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		if checkExecError(w, "清除磁盘签名", out, err) { return }
	}
	stepDone("清除磁盘签名")

	// 2. Create RAID array — find next available /dev/mdN
	stepRunning(fmt.Sprintf("创建 %s (RAID%d)", raidLevelName(level), level))
	mdDev := getNextMDDevice()
	args := []string{"--create", mdDev, "--level=" + fmt.Sprintf("%d", level), "--raid-devices=" + fmt.Sprintf("%d", len(devs)), "--run"}
	args = append(args, devs...)
	out, err := common.SudoExec("/usr/sbin/mdadm", args...)
	if checkExecError(w, "创建RAID", out, err) { return }
	stepDone(fmt.Sprintf("创建 RAID%d 阵列 (%s)", level, mdDev))

	// 3. Wait for md device
	stepRunning("等待阵列就绪")
	time.Sleep(2 * time.Second)
	stepDone("阵列就绪")

	// 4. Format
	stepRunning("格式化 (xfs)")
	out, err = common.SudoExec("mkfs.xfs", "-f", mdDev)
	if checkExecError(w, "格式化", out, err) { return }
	stepDone("格式化 xfs")

	// 5. Mount + fstab
	stepRunning("挂载并配置")
	common.SudoExec("mkdir", "-p", mountPoint)
	out, err = common.SudoExec("mount", mdDev, mountPoint)
	if checkExecError(w, "挂载", out, err) { return }
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", mdDev)
	writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	stepDone("挂载并写入 fstab")

	// 6. Save RAID config
	stepRunning("保存 RAID 配置")
	mdadmConf, _ := common.SudoOutput("/usr/sbin/mdadm", "--detail", "--scan")
	existingConf, _ := common.SudoOutput("cat", "/etc/mdadm/mdadm.conf")
	if existingConf == "" {
		existingConf, _ = common.SudoOutput("cat", "/etc/mdadm.conf")
		common.SafeWriteFile("/etc/mdadm/mdadm.conf", mdadmConf)
	} else {
		common.SafeWriteFile("/etc/mdadm/mdadm.conf", existingConf+"\n"+mdadmConf)
	}
	stepDone("保存 RAID 配置")

	// 7. Add Samba share
	stepRunning("配置 Samba 共享")
	addSambaShare(shareNameFromMount(mountPoint), mountPoint, nasUser)
	stepDone("配置 Samba 共享")
}

// raidLevelName returns a friendly name for RAID level
func raidLevelName(level int) string {
	switch level {
	case 0:
		return "条带"
	case 1:
		return "镜像"
	default:
		return fmt.Sprintf("RAID%d", level)
	}
}

// getNextMDDevice finds the next available /dev/mdN device
func getNextMDDevice() string {
	for i := 0; i <= 127; i++ {
		dev := fmt.Sprintf("/dev/md%d", i)
		if _, err := common.ExecOutput("ls", dev); err != nil {
			return dev
		}
	}
	return "/dev/md127"
}

// setupSeparateStream: each disk independent
func setupSeparateStream(w http.ResponseWriter, devs []string, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) {

	for i, dev := range devs {
		mountPoint := fmt.Sprintf("/data/nas%d", i+1)

		stepRunning(fmt.Sprintf("磁盘 %d: 清除签名", i+1))
		out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		if checkExecError(w, fmt.Sprintf("磁盘%d清除", i+1), out, err) { return }
		stepDone(fmt.Sprintf("磁盘 %d: 清除签名", i+1))

		stepRunning(fmt.Sprintf("磁盘 %d: 创建分区", i+1))
		out, err = common.SudoExec("/usr/sbin/parted", "-s", dev, "mklabel", "gpt", "mkpart", "primary", "xfs", "0%", "100%")
		if checkExecError(w, fmt.Sprintf("磁盘%d分区", i+1), out, err) { return }
		time.Sleep(500 * time.Millisecond)
		partDev := dev + "1"
		stepDone(fmt.Sprintf("磁盘 %d: 创建分区", i+1))

		stepRunning(fmt.Sprintf("磁盘 %d: 格式化", i+1))
		out, err = common.SudoExec("mkfs.xfs", "-f", partDev)
		if checkExecError(w, fmt.Sprintf("磁盘%d格式化", i+1), out, err) { return }
		stepDone(fmt.Sprintf("磁盘 %d: 格式化", i+1))

		stepRunning(fmt.Sprintf("磁盘 %d: 挂载", i+1))
		common.SudoExec("mkdir", "-p", mountPoint)
		out, err = common.SudoExec("mount", partDev, mountPoint)
		if checkExecError(w, fmt.Sprintf("磁盘%d挂载", i+1), out, err) { return }
		uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", partDev)
		writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
		common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
		stepDone(fmt.Sprintf("磁盘 %d: 挂载到存储空间%d", i+1, i+1))

		shareName := shareNameFromMount(mountPoint)
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
		if mount == "/data" || isDataNasMount(mount) {
			common.SudoExec("umount", "-l", mount)
		}
	}
	stepDone("卸载数据目录")

	// 2. Clean fstab — precise match, only /data and /data/nasN entries
	stepRunning("清理 fstab")
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
				if mountPoint == "/data" || isDataNasMount(mountPoint) {
					continue
				}
			}
			newLines = append(newLines, line)
		}
		content := strings.Join(newLines, "\n")
		if content != fstabData {
			common.SafeWriteFile("/etc/fstab", content)
		}
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

	// 4. Stop RAID — only /dev/mdN devices, not --scan
	stepRunning("停止 RAID")
	stopRAIDArrays()
	// Zero superblock on all data disks (dynamic scan)
	dataDisks := getDataDisks()
	for _, dev := range dataDisks {
		common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", dev)
	}
	common.SafeWriteFile("/etc/mdadm/mdadm.conf", "")
	stepDone("停止 RAID")

	// 5. Wipe disks — dynamic scan
	stepRunning("清除磁盘签名")
	for _, dev := range dataDisks {
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
	}
	stepDone("清除磁盘签名")

	// 6. Clean Samba
	stepRunning("清理 Samba 共享")
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if smbConf != "" {
		newConf := removeSambaSharesByPrefix(smbConf, "nas")
		if newConf != smbConf {
			common.SafeWriteFile("/etc/samba/smb.conf", newConf)
			common.SudoExec("systemctl", "restart", "smbd")
		}
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
