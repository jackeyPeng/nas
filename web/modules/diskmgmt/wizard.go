package diskmgmt

import (
	"fmt"
	"net/http"
	"strings"

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
	out, _ := execOutputFn("df", "-h", "--output=source,size,used,avail,pcent,target")
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

// handleWizardSetup：旧的非流式建池入口，已下线（Item 2）。
//
// 下线原因：这个入口的建池路径（setupSingleDisk/setupMergeDisks/setupRaid1/
// setupRaidN）绕开了流式版本（handleWizardSetupStream，stream.go）后来补上的
// 多项前置检查——(a) 不检查 /data/nas1 是否已存在，可对已有存储池重复建池、
// 覆盖现有数据；(b) 建池步骤里大量 common.SudoExec 调用忽略返回的 error，
// 失败后仍继续往下执行、最终 steps 里却不体现真实失败；(c) mode=separate
// 拦截虽然在入口补过一次，但其余检查缺失的问题本身没有解决。前端
// （web/frontend/app.js）只调用 setup-stream，这个非流式接口没有真实前端
// 调用方，属于可直接下线的死接口；为保留明确的 API 契约（而不是改路由表
// 悄悄消失），保留路由注册，入口直接返回"已下线"错误，不执行任何磁盘/
// LVM/RAID 命令，不假装成功。
func handleWizardSetup(w http.ResponseWriter, r *http.Request) {
	jsonError(w, "该接口已下线，请使用流式接口 /api/disk/wizard/setup-stream", http.StatusGone)
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
