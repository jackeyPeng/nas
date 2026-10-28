package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"nas-panel/common"
)

// WizardDisk represents a disk with friendly name
type WizardDisk struct {
	ID       int    `json:"id"`       // 1, 2, 3...
	Device   string `json:"device"`   // /dev/sdb
	Friendly string `json:"friendly"` // 磁盘 1
	Size     string `json:"size"`     // 50G
	Model    string `json:"model"`
	Type     string `json:"type"` // system/unused/data
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
		"unused_disks":    unused,
		"unused_count":    len(unused),
		"pool":            pool,
		"existing_mounts": existingMounts,
		"has_storage":     len(existingMounts) > 0 || pool["exists"] == true,
		"raid_options":    raidOptions,
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
				"exists":   true,
				"vg_name":  fields[0],
				"total_gb": fmt.Sprintf("%.0f", total),
				"used_gb":  fmt.Sprintf("%.1f", used),
				"free_gb":  fmt.Sprintf("%.0f", free),
				"percent":  fmt.Sprintf("%.0f", pct),
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

// handleWizardSetup: one-click storage setup by mode (deprecated non-stream path)
// Modes: single, merge, raid0, raid1, raid5, raid6 — separate 已下线（B2/Finding 5），
// 入口直接拒绝，不进入 switch。
func handleWizardSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	common.LockDiskOp()
	defer common.UnlockDiskOp()

	mode := r.FormValue("mode")
	confirm := r.FormValue("confirm")
	nasUser := common.GetNASUser()
	if nasUser == "" {
		nasUser = "root"
	}
	common.LogAuditRequest(r, "STORAGE", "存储向导配置", "mode="+mode, "pending")

	if confirm != "yes" {
		http.Error(w, `{"error":"请确认操作"}`, http.StatusBadRequest)
		return
	}

	// B2：separate（独立多池创建）模式已下线——产品定位收敛为专用整机、一机一主池。
	// stream.go 的流式版本（handleWizardSetupStream）已经拦了这条路径，但非流式
	// 的这个旧接口（handleWizardSetup）还留着完整的 separate 分支，直接 API 调用
	// 能绕过下线决策创建 separate 多池（Finding 5）。拦在入口，不进入下面任何
	// 步数计算/建池分支。
	if mode == "separate" {
		jsonError(w, "separate 多池模式已下线，单池模型请使用 merge/RAID", http.StatusBadRequest)
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
		jsonError(w, "未知模式: single/merge/raid0/raid1/raid5/raid6", http.StatusBadRequest)
		return
	}

	// 创建 public + 用户 home 并生成托管共享（SMB public + home、NFS 仅 public）
	EnsurePoolStructure(resultMount, nasUser)
	steps = append(steps, "创建 public + 用户 home 并生成托管共享")

	common.JSONResponse(w, map[string]interface{}{
		"message": "存储配置完成！",
		"mode":    mode,
		"steps":   steps,
		"disks":   len(unusedDevs),
		"mount":   resultMount,
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
	partDev := partitionDevice(dev, 1)
	steps = append(steps, "分区 "+dev)
	// Format
	if out, err := common.SudoOutput("mkfs.xfs", "-f", partDev); err != nil {
		steps = append(steps, "失败: mkfs.xfs "+partDev+" - "+err.Error()+" "+out)
		return steps
	}
	steps = append(steps, "格式化 xfs")
	// Mount
	common.SudoExec("mkdir", "-p", mountPoint)
	// prjquota：与 writeFstab 写入的 defaults,prjquota 保持一致，建池后配额立即可用（无需重启）
	if out, err := common.SudoOutput("mount", "-o", "prjquota", partDev, mountPoint); err != nil {
		steps = append(steps, "失败: mount "+partDev+" - "+err.Error()+" "+out)
		return steps
	}
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab — blkid 读超级块需 root，必须走 sudo（S1-R3c：此前用 ExecOutput 不带
	// sudo，fm 下读不到 UUID，writeFstab 静默跳过，步骤仍 append"成功"）
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", partDev)
	uuid := strings.TrimSpace(uuidOut)
	if err := writeFstab(uuid, mountPoint, "xfs"); err != nil {
		steps = append(steps, "失败: 写入 fstab 持久化 - "+err.Error())
		return steps
	}
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
	common.SudoExec("/usr/sbin/lvcreate", "-y", "-l", "100%FREE", "-n", "data", vgName)
	lvPath := "/dev/" + vgName + "/data"
	steps = append(steps, "创建逻辑卷 data")
	// format
	common.SudoExec("mkfs.xfs", "-f", lvPath)
	steps = append(steps, "格式化 xfs")
	// mount
	common.SudoExec("mkdir", "-p", mountPoint)
	common.SudoExec("mount", "-o", "prjquota", lvPath, mountPoint)
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab — blkid 读超级块需 root，必须走 sudo（同 setupSingleDisk 注释）
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	uuid := strings.TrimSpace(uuidOut)
	if err := writeFstab(uuid, mountPoint, "xfs"); err != nil {
		steps = append(steps, "失败: 写入 fstab 持久化 - "+err.Error())
		return steps
	}
	steps = append(steps, "写入 fstab 持久化")
	// chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限")
	return steps
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
	common.SudoExec("mount", "-o", "prjquota", mdDev, mountPoint)
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab — blkid 读超级块需 root，必须走 sudo（同 setupSingleDisk 注释）
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", mdDev)
	uuid := strings.TrimSpace(uuidOut)
	if err := writeFstab(uuid, mountPoint, "xfs"); err != nil {
		steps = append(steps, "失败: 写入 fstab 持久化 - "+err.Error())
		return steps
	}
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
	common.SudoExec("mount", "-o", "prjquota", mdDev, mountPoint)
	steps = append(steps, "挂载 → "+mountPoint)
	// fstab — blkid 读超级块需 root，必须走 sudo（同 setupSingleDisk 注释）
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", mdDev)
	uuid := strings.TrimSpace(uuidOut)
	if err := writeFstab(uuid, mountPoint, "xfs"); err != nil {
		steps = append(steps, "失败: 写入 fstab 持久化 - "+err.Error())
		return steps
	}
	steps = append(steps, "写入 fstab 持久化")
	// Save mdadm config (read output, then write to file — exec.Command doesn't use shell).
	// 曾用 `/bin/sh -c "echo ... >> mdadm.conf"` 做字符串拼接追加，等价于把 mdadmConf
	// 内容直接喂进 shell -c，任何特殊字符都是潜在的 shell 注入面，且 sudoers 若要放行
	// 这个形状必须整条放行 /bin/sh -c（root-equivalent 通配）。改用既有的
	// SafeAppendFile（cat 读旧内容 + SafeWriteFile 的 cp 落地，无 shell 解释）与
	// stream.go setupRAIDStream 的同款逻辑一致。
	mdadmConf, err := common.SudoOutput(mdadmPath, "--detail", "--scan")
	if err == nil && mdadmConf != "" {
		common.SafeAppendFile("/etc/mdadm/mdadm.conf", strings.TrimSpace(mdadmConf)+"\n")
	}
	common.SudoExec("update-initramfs", "-u")
	steps = append(steps, "保存 RAID 配置")
	// chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限")
	return steps
}

// writeFstab appends UUID mount line to /etc/fstab, with mount point dedup check。
// 返回 error 而不是静默吞错——S1-R3c 实证修复：uuid 为空（blkid 失败/未授权）
// 或 cat/写回失败时，调用方必须能把"失败"报给向导步骤列表，而不是假装成功。
func writeFstab(uuid, mountPoint, fstype string) error {
	if uuid == "" {
		return fmt.Errorf("UUID 为空（blkid 未能读取设备 UUID）")
	}
	fstabLine := fmt.Sprintf("UUID=%s %s %s %s 0 2\n", uuid, mountPoint, fstype, fstabOptions(fstype))
	data, err := common.SudoOutput("cat", "/etc/fstab")
	if err != nil {
		return fmt.Errorf("读取 /etc/fstab 失败: %w", err)
	}
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
		if err := common.SafeWriteFile("/etc/fstab", newContent); err != nil {
			return fmt.Errorf("写入 /etc/fstab 失败: %w", err)
		}
	}
	return nil
}
