package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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
		if d.Name == "sr0" || isSystemDisk(d.Device) {
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
	totalSteps := 6 // default
	switch mode {
	case "single":
		totalSteps = 5
	case "merge":
		totalSteps = 5 + len(unusedDevs)
	case "separate":
		totalSteps = 5 * len(unusedDevs)
	case "raid1":
		totalSteps = 6
	default:
		sendProgress(w, ProgressEvent{Step: "模式检查", Status: "error", Detail: "未知模式"})
		return
	}

	step := 0

	// Helper to send step progress
	stepDone := func(name string) {
		step++
		sendProgress(w, ProgressEvent{Step: name, Status: "done", Index: step, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	stepRunning := func(name string) {
		sendProgress(w, ProgressEvent{Step: name, Status: "running", Index: step + 1, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}

	switch mode {
	case "single":
		dev := unusedDevs[0]
		mountPoint := "/data/nas1"

		stepRunning("清除磁盘签名")
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		stepDone("清除磁盘签名")

		stepRunning("创建分区")
		common.SudoExec("/usr/sbin/parted", "-s", dev, "mklabel", "gpt", "mkpart", "primary", "xfs", "0%", "100%")
		time.Sleep(500 * time.Millisecond) // wait for kernel
		partDev := dev + "1"
		stepDone("创建分区")

		stepRunning("格式化 (xfs)")
		common.SudoExec("mkfs.xfs", "-f", partDev)
		stepDone("格式化 (xfs)")

		stepRunning("挂载到 " + mountPoint)
		common.SudoExec("mkdir", "-p", mountPoint)
		common.SudoExec("mount", partDev, mountPoint)
		uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", partDev)
		writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
		common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
		stepDone("挂载到 " + mountPoint)

		stepRunning("添加 Samba 共享")
		addSambaShare("nas1", mountPoint, nasUser)
		stepDone("添加 Samba 共享")

	case "merge":
		mountPoint := "/data/nas1"

		// pvcreate each disk
		for i, dev := range unusedDevs {
			stepRunning(fmt.Sprintf("清除并初始化磁盘 %d/%d", i+1, len(unusedDevs)))
			common.SudoExec("/usr/sbin/wipefs", "-a", dev)
			common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
			stepDone(fmt.Sprintf("初始化磁盘 %d/%d", i+1, len(unusedDevs)))
		}

		stepRunning("创建卷组")
		vgName := "vg_nas"
		vgArgs := append([]string{"-f", vgName}, unusedDevs...)
		common.SudoExec("/usr/sbin/vgcreate", vgArgs...)
		stepDone("创建卷组")

		stepRunning("创建逻辑卷")
		common.SudoExec("/usr/sbin/lvcreate", "-l", "100%FREE", "-n", "data", vgName)
		lvPath := "/dev/" + vgName + "/data"
		stepDone("创建逻辑卷")

		stepRunning("格式化 (xfs)")
		common.SudoExec("mkfs.xfs", "-f", lvPath)
		stepDone("格式化 (xfs)")

		stepRunning("挂载到 " + mountPoint)
		common.SudoExec("mkdir", "-p", mountPoint)
		common.SudoExec("mount", lvPath, mountPoint)
		uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
		writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
		common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
		stepDone("挂载到 " + mountPoint)

		stepRunning("添加 Samba 共享")
		addSambaShare("nas1", mountPoint, nasUser)
		stepDone("添加 Samba 共享")

	case "separate":
		for i, dev := range unusedDevs {
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
			stepDone(fmt.Sprintf("磁盘 %d: 挂载到 %s", i+1, mountPoint))

			shareName := fmt.Sprintf("nas%d", i+1)
			stepRunning(fmt.Sprintf("磁盘 %d: Samba", i+1))
			addSambaShare(shareName, mountPoint, nasUser)
			stepDone(fmt.Sprintf("磁盘 %d: Samba 共享", i+1))
		}

	case "raid1":
		devs := unusedDevs[:2]
		mountPoint := "/data/nas1"

		stepRunning("清除磁盘签名")
		for _, dev := range devs {
			common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		}
		stepDone("清除磁盘签名")

		stepRunning("创建 RAID1 镜像")
		mdadmPath := "/usr/sbin/mdadm"
		args := []string{"--create", "/dev/md0", "--level=1", "--raid-devices=" + fmt.Sprintf("%d", len(devs)), "--run"}
		args = append(args, devs...)
		common.SudoExec(mdadmPath, args...)
		stepDone("创建 RAID1 镜像")

		// Wait for md device to appear
		time.Sleep(2 * time.Second)

		stepRunning("格式化 (xfs)")
		common.SudoExec("mkfs.xfs", "-f", "/dev/md0")
		stepDone("格式化 (xfs)")

		stepRunning("挂载到 " + mountPoint)
		common.SudoExec("mkdir", "-p", mountPoint)
		common.SudoExec("mount", "/dev/md0", mountPoint)
		uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", "/dev/md0")
		writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs")
		common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
		stepDone("挂载到 " + mountPoint)

		stepRunning("添加 Samba 共享")
		addSambaShare("nas1", mountPoint, nasUser)
		stepDone("添加 Samba 共享")

		// Note: RAID sync continues in background
		stepRunning("RAID 后台同步中")
		stepDone("RAID 后台同步中")
	}

	sendProgress(w, ProgressEvent{
		Step:   "完成",
		Status: "complete",
		Index:  totalSteps,
		Total:  totalSteps,
		Detail: fmt.Sprintf("存储配置完成，共 %d 块磁盘", len(unusedDevs)),
	})
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
	common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", "/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde")
	common.SudoExec("bash", "-c", "echo '' > /etc/mdadm/mdadm.conf 2>/dev/null || true")
	stepDone("停止 RAID")

	// 5. Wipe disks
	stepRunning("清除磁盘签名")
	for _, dev := range []string{"/dev/sdb", "/dev/sdc", "/dev/sdd", "/dev/sde"} {
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

// ensure os import used
var _ = os.Stdin
var _ = exec.Command
