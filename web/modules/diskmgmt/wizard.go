package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nas-panel/common"
)

// WizardDisk represents a disk with friendly name
type WizardDisk struct {
	ID       int    `json:"id"`        // 1, 2, 3...
	Device   string `json:"device"`    // /dev/sdb
	Friendly string `json:"friendly"`  // 磁盘 1
	Size     string `json:"size"`      // 50G
	Model    string `json:"model"`
	Type     string `json:"type"`  // system/unused/data
}

// WizardStatus returns current storage state + available disks
func handleWizardStatus(w http.ResponseWriter, r *http.Request) {
	disks := getDiskStatus()
	unused := make([]WizardDisk, 0)
	globalID := 0 // global disk number, matches overview numbering
	for _, d := range disks {
		if d.Name == "sr0" || d.Name == "zram0" || strings.HasPrefix(d.Name, "loop") {
			continue
		}
		globalID++
		// Skip system disks entirely
		if isSystemDisk(d.Device) {
			continue
		}
		// Check if disk or its partitions are unused
		isUnused := d.Type == "unused"
		if !isUnused && len(d.Children) > 0 {
			allUnused := true
			for _, c := range d.Children {
				if c.Type != "unused" && c.FSType != "" {
					allUnused = false
					break
				}
			}
			if allUnused {
				isUnused = true
			}
		}
		if isUnused {
			wd := WizardDisk{
				ID:       globalID, // keep global number, consistent with overview
				Device:   d.Device,
				Friendly: fmt.Sprintf("磁盘 %d", globalID),
				Size:     d.Size,
				Model:    d.Model,
				Type:     "unused",
			}
			_ = d
			unused = append(unused, wd)
		}
	}

	// Get current pool status
	pool := getPoolStatusSimple()

	// Check existing mounts under /data
	existingMounts := getExistingDataMounts()

	// Calculate RAID options based on available disks
	minSize := getMinDiskSizeGB(unused)
	raidOptions := getRaidOptions(len(unused), minSize)

	common.JSONResponse(w, map[string]interface{}{
		"unused_disks":     unused,
		"unused_count":     len(unused),
		"pool":             pool,
		"existing_mounts":  existingMounts,
		"has_storage":       len(existingMounts) > 0 || pool["exists"] == true,
		"raid_options":      raidOptions,
	})
}

func getPoolStatusSimple() map[string]interface{} {
	pool := map[string]interface{}{"exists": false}
	vgsOut, _ := common.SudoOutput("/usr/sbin/vgs", "--noheadings", "--units", "g", "-o", "vg_name,vg_size,vg_free")
	vgsOut = strings.TrimSpace(vgsOut)
	if vgsOut != "" {
		fields := strings.Fields(vgsOut)
		if len(fields) >= 3 {
			total := parseFloatSafe(strings.TrimRight(fields[1], "gG"))
			free := parseFloatSafe(strings.TrimRight(fields[2], "gG"))
			used := total - free
			pct := 0.0
			if total > 0 {
				pct = (used / total) * 100
			}
			pool = map[string]interface{}{
				"exists":      true,
				"vg_name":     fields[0],
				"total_gb":    fmt.Sprintf("%.0f", total),
				"used_gb":     fmt.Sprintf("%.1f", used),
				"free_gb":     fmt.Sprintf("%.0f", free),
				"percent":     fmt.Sprintf("%.0f", pct),
			}
		}
	}
	return pool
}

func getExistingDataMounts() []map[string]string {
	var mounts []map[string]string
	out, _ := common.ExecOutput("df", "-h", "--output=source,size,used,avail,pcent,target")
	for _, line := range strings.Split(out, "\n")[1:] {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "/data") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			mounts = append(mounts, map[string]string{
				"device":  fields[0],
				"size":    fields[1],
				"used":    fields[2],
				"avail":   fields[3],
				"percent": fields[4],
				"mount":   fields[5],
			})
		}
	}
	return mounts
}

// handleWizardSetup: one-click storage setup by mode
// Modes: single, merge, separate, raid1
func handleWizardSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	diskOpMutex.Lock()
	defer diskOpMutex.Unlock()

	mode := r.FormValue("mode")
	confirm := r.FormValue("confirm")
	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}

	if confirm != "yes" {
		http.Error(w, `{"error":"请确认操作"}`, http.StatusBadRequest)
		return
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
		http.Error(w, `{"error":"没有可用的空闲磁盘"}`, http.StatusBadRequest)
		return
	}

	var steps []string
	var resultMount string

	switch mode {
	case "single":
		if len(unusedDevs) < 1 {
			http.Error(w, `{"error":"至少需要 1 块磁盘"}`, http.StatusBadRequest)
			return
		}
		dev := unusedDevs[0]
		resultMount = "/data/nas1"
		steps = setupSingleDisk(dev, resultMount, nasUser)

	case "merge":
		if len(unusedDevs) < 2 {
			http.Error(w, `{"error":"合并模式至少需要 2 块磁盘"}`, http.StatusBadRequest)
			return
		}
		resultMount = "/data/nas1"
		steps = setupMergeDisks(unusedDevs, resultMount, nasUser)

	case "separate":
		resultMount = "/data"
		steps = setupSeparateDisks(unusedDevs, nasUser)

	case "raid1":
		if len(unusedDevs) < 2 {
			http.Error(w, `{"error":"RAID1 至少需要 2 块磁盘"}`, http.StatusBadRequest)
			return
		}
		resultMount = "/data/nas1"
		steps = setupRaid1(unusedDevs[:2], resultMount, nasUser)

	case "raid0":
		if len(unusedDevs) < 2 {
			http.Error(w, `{"error":"RAID0 至少需要 2 块磁盘"}`, http.StatusBadRequest)
			return
		}
		resultMount = "/data/nas1"
		steps = setupRaidN(unusedDevs, resultMount, nasUser, 0) // raid0

	case "raid5":
		if len(unusedDevs) < 3 {
			http.Error(w, `{"error":"RAID5 至少需要 3 块磁盘"}`, http.StatusBadRequest)
			return
		}
		resultMount = "/data/nas1"
		steps = setupRaidN(unusedDevs, resultMount, nasUser, 5) // raid5

	case "raid6":
		if len(unusedDevs) < 4 {
			http.Error(w, `{"error":"RAID6 至少需要 4 块磁盘"}`, http.StatusBadRequest)
			return
		}
		resultMount = "/data/nas1"
		steps = setupRaidN(unusedDevs, resultMount, nasUser, 6) // raid6

	default:
		http.Error(w, `{"error":"未知模式: single/merge/separate/raid0/raid1/raid5/raid6"}`, http.StatusBadRequest)
		return
	}

	// Auto-add Samba share
	shareName := "nas1"
	if mode == "separate" {
		for i := range unusedDevs {
			shareName = fmt.Sprintf("nas%d", i+1)
			addSambaShare(shareName, fmt.Sprintf("/data/nas%d", i+1), nasUser)
		}
	} else {
		addSambaShare(shareName, resultMount, nasUser)
	}
	steps = append(steps, "添加 Samba 共享")

	common.JSONResponse(w, map[string]interface{}{
		"message":  "存储配置完成！",
		"mode":     mode,
		"steps":    steps,
		"disks":    len(unusedDevs),
		"mount":    resultMount,
	})
}

// setupSingleDisk: format + mount + fstab + chown
// NOTE: This is the deprecated non-streaming path. Prefer the SSE streaming version in stream.go.
// TODO: Migrate remaining callers to streaming and remove this function.
func setupSingleDisk(dev, mountPoint, nasUser string) []string {
	var steps []string
	// Wipe existing partition table
	if out, err := common.SudoOutput("/usr/sbin/wipefs", "-a", dev); err != nil {
		steps = append(steps, "失败: wipefs "+dev+" - "+err.Error()+" "+out)
		return steps
	}
	// Create partition
	if out, err := common.SudoOutput("/usr/sbin/parted", "-s", dev, "mklabel", "gpt", "mkpart", "primary", "ext4", "0%", "100%"); err != nil {
		steps = append(steps, "失败: parted "+dev+" - "+err.Error()+" "+out)
		return steps
	}
	// NVMe partitions use "p1" suffix: nvme0n1 → nvme0n1p1, sda → sda1
	partDev := dev + "1"
	if strings.HasPrefix(dev, "/dev/nvme") {
		partDev = dev + "p1"
	}
	steps = append(steps, "分区 "+dev)
	// Format
	if out, err := common.SudoOutput("mkfs.xfs", "-f", partDev); err != nil {
		steps = append(steps, "失败: mkfs.xfs "+partDev+" - "+err.Error()+" "+out)
		return steps
	}
	steps = append(steps, "格式化 xfs")
	// Mount
	common.SudoExec("mkdir", "-p", mountPoint)
	if out, err := common.SudoOutput("mount", partDev, mountPoint); err != nil {
		steps = append(steps, "失败: mount "+partDev+" - "+err.Error()+" "+out)
		return steps
	}
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", partDev)
	uuid := strings.TrimSpace(uuidOut)
	writeFstab(uuid, mountPoint, "xfs")
	steps = append(steps, "写入 fstab 持久化")
	// chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限")
	return steps
}

// setupMergeDisks: LVM VG + LV → single mount
func setupMergeDisks(devs []string, mountPoint, nasUser string) []string {
	var steps []string
	// pvcreate all
	for _, dev := range devs {
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
		steps = append(steps, "pvcreate "+dev)
	}
	// vgcreate
	vgName := "vg_nas"
	vgArgs := append([]string{"-f", vgName}, devs...)
	common.SudoExec("/usr/sbin/vgcreate", vgArgs...)
	steps = append(steps, "创建卷组 "+vgName)
	// lvcreate
	common.SudoExec("/usr/sbin/lvcreate", "-l", "100%FREE", "-n", "data", vgName)
	lvPath := "/dev/" + vgName + "/data"
	steps = append(steps, "创建逻辑卷 data")
	// format
	common.SudoExec("mkfs.xfs", "-f", lvPath)
	steps = append(steps, "格式化 xfs")
	// mount
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", lvPath, mountPoint)
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	uuid := strings.TrimSpace(uuidOut)
	writeFstab(uuid, mountPoint, "xfs")
	steps = append(steps, "写入 fstab 持久化")
	// chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限")
	return steps
}

// setupSeparateDisks: each disk → /data/nas1, /data/nas2...
func setupSeparateDisks(devs []string, nasUser string) []string {
	var allSteps []string
	for i, dev := range devs {
		mountPoint := fmt.Sprintf("/data/nas%d", i+1)
		steps := setupSingleDisk(dev, mountPoint, nasUser)
		allSteps = append(allSteps, "磁盘 "+fmt.Sprintf("%d", i+1)+": "+strings.Join(steps, " → "))
	}
	return allSteps
}

// setupRaid1: mdadm RAID1 mirror → single mount
func setupRaid1(devs []string, mountPoint, nasUser string) []string {
	var steps []string
	// Check mdadm
	mdadmPath := "/usr/sbin/mdadm"
	if _, err := exec.LookPath("mdadm"); err != nil {
		// Try install
		exec.Command("sudo", "apt-get", "install", "-y", "mdadm").Run()
	} else {
		mdadmPath = "mdadm"
	}
	// Create RAID1
	mdDev := "/dev/md0"
	args := []string{"--create", mdDev, "--level=1", "--raid-devices=" + fmt.Sprintf("%d", len(devs)), "--run"}
	for _, dev := range devs {
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		args = append(args, dev)
	}
	common.SudoExec(mdadmPath, args...)
	steps = append(steps, "创建 RAID1 镜像")
	// Format
	common.SudoExec("mkfs.xfs", "-f", mdDev)
	steps = append(steps, "格式化 xfs")
	// Mount
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", mdDev, mountPoint)
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", mdDev)
	uuid := strings.TrimSpace(uuidOut)
	writeFstab(uuid, mountPoint, "xfs")
	steps = append(steps, "写入 fstab 持久化")
	// chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限")
	return steps
}

// setupRaidN: mdadm RAID0/5/6 → single mount (通用版)
func setupRaidN(devs []string, mountPoint, nasUser string, level int) []string {
	var steps []string
	mdadmPath := "/usr/sbin/mdadm"
	if _, err := exec.LookPath("mdadm"); err != nil {
		exec.Command("sudo", "apt-get", "install", "-y", "mdadm").Run()
	} else {
		mdadmPath = "mdadm"
	}
	// 找下一个可用 md 设备号
	mdDev := "/dev/md0"
	for i := 0; ; i++ {
		mdDev = fmt.Sprintf("/dev/md%d", i)
		if _, err := os.Stat(mdDev); os.IsNotExist(err) {
			break
		}
	}
	levelStr := fmt.Sprintf("raid%d", level)
	args := []string{"--create", mdDev, "--level=" + fmt.Sprintf("%d", level), "--raid-devices=" + fmt.Sprintf("%d", len(devs)), "--run"}
	for _, dev := range devs {
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		args = append(args, dev)
	}
	common.SudoExec(mdadmPath, args...)
	steps = append(steps, fmt.Sprintf("创建 %s", levelStr))
	// 等待设备出现
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(mdDev); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	// Format
	common.SudoExec("mkfs.xfs", "-f", mdDev)
	steps = append(steps, "格式化 xfs")
	// Mount
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", mdDev, mountPoint)
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", mdDev)
	uuid := strings.TrimSpace(uuidOut)
	writeFstab(uuid, mountPoint, "xfs")
	steps = append(steps, "写入 fstab 持久化")
	// Save mdadm config (read output, then write to file — exec.Command doesn't use shell)
	mdadmConf, err := common.SudoOutput(mdadmPath, "--detail", "--scan")
	if err == nil && mdadmConf != "" {
		common.SudoExec("/bin/sh", "-c", fmt.Sprintf("echo '%s' >> /etc/mdadm/mdadm.conf", strings.TrimSpace(mdadmConf)))
	}
	common.SudoExec("update-initramfs", "-u")
	steps = append(steps, "保存 RAID 配置")
	// chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限")
	return steps
}

// writeFstab appends UUID mount line to /etc/fstab, with mount point dedup check
func writeFstab(uuid, mountPoint, fstype string) {
	if uuid == "" {
		return
	}
	fstabLine := fmt.Sprintf("UUID=%s %s %s defaults 0 2\n", uuid, mountPoint, fstype)
	data, _ := common.SudoOutput("cat", "/etc/fstab")
	content := data
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	// Check if UUID or mount point already exists
	lines := strings.Split(content, "\n")
	var newLines []string
	replaced := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, line)
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 {
			existingUUID := fields[0]
			existingMount := fields[1]
			// Replace if same UUID or same mount point
			if existingUUID == "UUID="+uuid || existingMount == mountPoint {
				if !replaced {
					newLines = append(newLines, fstabLine[:len(fstabLine)-1]) // strip trailing \n
					replaced = true
				}
				continue
			}
		}
		newLines = append(newLines, line)
	}
	if !replaced {
		newLines = append(newLines, fstabLine[:len(fstabLine)-1])
	}
	newContent := strings.Join(newLines, "\n")
	if newContent != content {
		common.SafeWriteFile("/etc/fstab", newContent)
	}
}

// shareNameFromMount derives Samba share name from mount point
// e.g. /data/nas3 → nas3, /data/system → system
func shareNameFromMount(mountPoint string) string {
	if mountPoint == "/data" {
		return "data"
	}
	// Extract last path component
	idx := strings.LastIndex(mountPoint, "/")
	if idx >= 0 && idx < len(mountPoint)-1 {
		return mountPoint[idx+1:]
	}
	return filepath.Base(mountPoint)
}

// addSambaShare adds a share to smb.conf and restarts smbd
// Removes existing share with same name first to avoid duplicates
func addSambaShare(name, path, user string) {
	// Read existing config and remove any existing share with same name
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	conf := fmt.Sprintf("\n[%s]\n   path = %s\n   browseable = yes\n   writable = yes\n   valid users = %s\n", name, path, user)
	if smbConf != "" {
		// Remove existing share with same name
		newConf := removeSambaShare(smbConf, name)
		common.SafeWriteFile("/etc/samba/smb.conf", newConf+conf)
	} else {
		common.SafeWriteFile("/etc/samba/smb.conf", conf)
	}
	common.SudoExec("systemctl", "restart", "smbd")
}
