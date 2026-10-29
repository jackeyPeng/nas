package diskmgmt

import (
	"fmt"
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
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	common.LockDiskOp()
	defer common.UnlockDiskOp()
	confirm := r.FormValue("confirm")
	confirmToken := r.FormValue("confirm_token")
	common.LogAuditRequest(r, "STORAGE", "存储重置", "清空所有存储池配置", "pending")
	common.EmitEvent("storage", common.EventWarn, "storage.reset", nil, "清空所有存储池配置")
	if confirm != "yes" || confirmToken != "DELETE" {
		jsonError(w, "请输入 DELETE 确认（不可恢复）", http.StatusBadRequest)
		return
	}

	var steps []string

	// consensus P0-A2：在任何拆除动作之前（LVM/PV/RAID 都还活着）收集纳管擦盘
	// 目标集合——pvs/mdstat 一旦等 LVM/PV 被拆了再查就是空的，必须在最前面采集。
	mounts := getExistingDataMounts()
	pvsOutPre, pvsErrPre := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name,vg_name")
	mdstatPre, mdstatErrPre := common.SudoOutput("cat", "/proc/mdstat")
	// consensus R1 Fix 4：采集失败不中止重置（无 LVM 的机器上 pvs 缺失是合法
	// 状态），但不能静默吞错——pvs 失败时 managedTargets 近乎为空，重置照常
	// "成功"但纳管盘不被擦除（幽灵盘），必须留痕让用户/审计看出"擦除不完整"。
	if pvsErrPre != nil || mdstatErrPre != nil {
		steps = append(steps, fmt.Sprintf("无法枚举纳管盘（pvs/mdstat 失败）: pvs=%v mdstat=%v，擦除集合可能不完整", pvsErrPre, mdstatErrPre))
	}
	managedTargets := collectManagedWipeTargets(pvsOutPre, mdstatPre, mounts)

	// 1. Unmount all /data/nas* and /data (if it's LVM/RAID)
	var resetMountPoints []string
	for _, m := range mounts {
		mount := m["mount"]
		if mount == "/data" || strings.HasPrefix(mount, "/data/nas") {
			common.SudoExec("umount", "-l", mount)
			steps = append(steps, "卸载 "+mount)
			resetMountPoints = append(resetMountPoints, mount)
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

	// consensus P0-A2：擦盘集合收敛为面板纳管盘 —— 不再对 getDataDisks()
	// （全部非系统整盘）无差别 zero-superblock + wipefs，只对最前面采集到的
	// managedTargets（PV、RAID成员、/data/nas*挂载点解析出的底层盘）执行擦除；
	// 外来 USB 备份盘等从未纳管的盘完全不碰。每个目标落命令前再过 isSystemDisk
	// （命中则跳过该盘记日志，不中止整个 reset —— 与 handlePoolDelete 的
	// "拒绝整个操作"语义不同，这里是尽力拆除）。
	steps = append(steps, wipeManagedTargets(managedTargets)...)
	// E7：改调 resetRAIDConfig —— 只删 ARRAY 行，保留 Debian 默认的
	// HOMEHOST/MAILADDR 等其余内容（此前整文件清空会连它们一起抹掉），
	// 并在有变化时跟进 update-initramfs -u（失败只留警告，不阻断重置）。
	if warning, err := resetRAIDConfig(nil); err != nil {
		steps = append(steps, "清除 RAID 配置失败: "+err.Error())
	} else if warning != "" {
		steps = append(steps, "清除 RAID 配置（警告: "+warning+"）")
	} else {
		steps = append(steps, "清除 RAID 配置")
	}

	dataDisks := getDataDisks()
	for _, dev := range skipUnmanagedDisks(dataDisks, managedTargets) {
		steps = append(steps, "跳过未纳管盘 "+dev)
	}

	// 6. 清理 folders.db 元数据 + 重投影托管配置（权威源模式）
	// 文件系统/LVM 已拆除完成，此时清掉 folders.db 里属于这些挂载点的行，
	// 再调 SyncAllConfigs() 重新生成 smb.conf/exports 的 Z1 托管段——
	// 元数据少了行，托管段自然收敛，不用再手工删 smb.conf 片段（旧的 removeSambaSharesByPrefix
	// 调用已移除：它只改 smb.conf 不碰 folders.db，重置后再次 SyncAllConfigs 会把删掉的共享重新生成回来）。
	for _, mount := range resetMountPoints {
		if n, err := RemoveFolderMetaByMountPrefix(mount); err != nil {
			steps = append(steps, "清理 folders.db 失败("+mount+"): "+err.Error())
		} else if n > 0 {
			steps = append(steps, fmt.Sprintf("清理 folders.db: %s (%d 行)", mount, n))
		}
	}
	common.SudoExec("rm", "-f", "/opt/nas/data/.last_reload")
	if err := SyncAllConfigs(); err != nil {
		steps = append(steps, "重新生成托管配置失败: "+err.Error())
	} else {
		steps = append(steps, "重新生成 Samba/NFS 托管配置")
	}

	// 7. Recreate base /data directory
	common.SudoExec("mkdir", "-p", "/data")
	nasUser := common.GetNASUser()
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
