package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nas-panel/common"
)

// PoolStatus represents storage pool overview
type PoolStatus struct {
	VGName     string   `json:"vg_name"`
	VGSize     string   `json:"vg_size"`
	VGFree     string   `json:"vg_free"`
	TotalGB    string   `json:"total_gb"`
	UsedGB     string   `json:"used_gb"`
	FreeGB     string   `json:"free_gb"`
	UsePercent string   `json:"use_percent"`
	PVs        []PVInfo `json:"pvs"`
	LVs        []LVInfo `json:"lvs"`
	Exists     bool     `json:"exists"`
}

type PVInfo struct {
	Name string `json:"name"`
	VG   string `json:"vg"`
	Size string `json:"size"`
	Free string `json:"free"`
	Disk string `json:"disk"`
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
		if len(fields) >= 1 {
			pv.Name = fields[0]
		}
		if len(fields) >= 2 {
			pv.VG = fields[1]
		}
		if len(fields) >= 3 {
			pv.Size = fields[2]
		}
		if len(fields) >= 4 {
			pv.Free = fields[3]
		}
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
		if len(fields) >= 1 {
			lv.Name = fields[0]
		}
		if len(fields) >= 2 {
			lv.VG = fields[1]
		}
		if len(fields) >= 3 {
			lv.Size = fields[2]
		}
		if len(fields) >= 4 {
			lv.Path = fields[3]
		}
		// Get mount and fstype from findmnt
		if lv.Path != "" {
			mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET,FSTYPE", "--source", lv.Path)
			mntFields := strings.Fields(mntOut)
			if len(mntFields) >= 1 {
				lv.Mount = mntFields[0]
			}
			if len(mntFields) >= 2 {
				lv.FSType = mntFields[1]
			}
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

	common.LockDiskOp()
	defer common.UnlockDiskOp()

	vgName := r.FormValue("vg_name")
	if vgName == "" {
		vgName = "vg_nas"
	}
	lvName := r.FormValue("lv_name")
	if lvName == "" {
		lvName = "data"
	}
	mountPoint := r.FormValue("mountpoint")
	if mountPoint == "" {
		mountPoint = "/data"
	}
	fstype := r.FormValue("fstype")
	if fstype == "" {
		fstype = "ext4"
	}
	devicesStr := r.FormValue("devices")
	confirm := r.FormValue("confirm")

	if devicesStr == "" {
		http.Error(w, `{"error":"请选择至少一块磁盘"}`, http.StatusBadRequest)
		return
	}
	// 白名单校验：设备列表逐个过 /dev/ 块设备白名单（加固轮1 #1）
	devices, err := common.ValidateBlockDeviceList(devicesStr)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	// VG/LV 名只允许 LVM 命名字符，防止拼进 lvPath/命令
	if vgName != "" && !common.IsValidLVMName(vgName) {
		http.Error(w, `{"error":"非法 VG 名称"}`, http.StatusBadRequest)
		return
	}
	if lvName != "" && !common.IsValidLVMName(lvName) {
		http.Error(w, `{"error":"非法 LV 名称"}`, http.StatusBadRequest)
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
	if fstype == "xfs" {
		mkfsArgs = []string{"-f"}
	}
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
	if fstype == "xfs" {
		common.SudoExec("mount", "-o", "prjquota", lvPath, mountPoint)
	} else {
		common.SudoExec("mount", lvPath, mountPoint)
	}
	steps = append(steps, "挂载 "+lvPath+" → "+mountPoint)

	// 7. fstab — blkid 读超级块需 root，必须走 sudo；失败显式返回（S1-R3c）
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	uuid := strings.TrimSpace(uuidOut)
	if err := writeFstab(uuid, mountPoint, fstype); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"写入 fstab 失败: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "写入 fstab (UUID="+uuid+")")

	// 8. chown
	nasUser := common.GetNASUser()
	if nasUser == "" {
		nasUser = "root"
	}
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountPoint)
	steps = append(steps, "设置权限 "+nasUser)

	common.JSONResponse(w, map[string]interface{}{
		"message":    "存储池创建完成",
		"steps":      steps,
		"vg_name":    vgName,
		"lv_name":    lvName,
		"mountpoint": mountPoint,
		"total":      "已合并 " + fmt.Sprintf("%d", len(devices)) + " 块盘",
	})
}

// handlePoolExtend extends existing LVM pool with new disk
func handlePoolExtend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	common.LockDiskOp()
	defer common.UnlockDiskOp()

	vgName := r.FormValue("vg_name")
	if vgName == "" {
		vgName = "vg_nas"
	}
	device := r.FormValue("device")
	lvName := r.FormValue("lv_name")
	if lvName == "" {
		lvName = "data"
	}
	confirm := r.FormValue("confirm")

	if device == "" {
		http.Error(w, `{"error":"请选择磁盘"}`, http.StatusBadRequest)
		return
	}
	// 白名单校验（加固轮1 #1）
	if err := common.ValidateBlockDevice(device); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	if vgName != "" && !common.IsValidLVMName(vgName) {
		http.Error(w, `{"error":"非法 VG 名称"}`, http.StatusBadRequest)
		return
	}
	if lvName != "" && !common.IsValidLVMName(lvName) {
		http.Error(w, `{"error":"非法 LV 名称"}`, http.StatusBadRequest)
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

	// 4. resize filesystem（按实际文件系统类型选择 xfs_growfs/resize2fs，xfs 用挂载点而非设备路径）
	growStep, _, growErr := growFilesystemByDevice(lvPath)
	if growErr != nil {
		http.Error(w, fmt.Sprintf(`{"error":"resize 失败: %s"}`, growErr.Error()), http.StatusInternalServerError)
		return
	}
	steps = append(steps, growStep)

	common.JSONResponse(w, map[string]interface{}{
		"message": "存储池扩容完成",
		"steps":   steps,
		"device":  device,
		"vg_name": vgName,
	})
}

// poolUsageKernel 是 PoolUsage() 导出的内核：只读 vgs 查询 + 换算，
// 逻辑与 handlePoolStatus 的 VG 用量计算分支一致（机械抽取，不改既有
// 行为）。vgs 无输出（池不存在）时返回 ErrPoolNotExist。
func poolUsageKernel() (totalGB, usedGB float64, usePercent int, err error) {
	vgsOut, _ := common.SudoOutput("vgs", "--noheadings", "--units", "g", "-o", "vg_name,vg_size,vg_free")
	vgsOut = strings.TrimSpace(vgsOut)
	if vgsOut == "" {
		// 无 LVM VG：可能是 RAID/直挂池（产品形态固定挂载点 /data/nas1）。
		// F2 真机验收实抓：raid1 直挂机上水位 cron 因 ErrPoolNotExist 永远
		// 静默跳过、周报存储显示 0 B——补 findmnt+df 回退分支。
		if src, _ := common.ExecOutput("findmnt", "-n", "-o", "SOURCE", "/data/nas1"); strings.TrimSpace(src) != "" {
			dfOut, _ := common.ExecOutput("df", "-BG", "--output=size,used", "/data/nas1")
			if t, u, ok := parseDfSizeUsedGB(dfOut); ok {
				pct := 0
				if t > 0 {
					pct = int(u / t * 100)
				}
				return t, u, pct, nil
			}
		}
		return 0, 0, 0, ErrPoolNotExist
	}
	var vgSize, vgFree string
	for _, line := range strings.Split(vgsOut, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 3 {
			vgSize = fields[1]
			vgFree = fields[2]
		}
	}
	totalGB = parseFloatSafe(strings.TrimRight(vgSize, "gG"))
	freeGB := parseFloatSafe(strings.TrimRight(vgFree, "gG"))
	usedGB = totalGB - freeGB
	if totalGB > 0 {
		usePercent = int((usedGB / totalGB) * 100)
	}
	return totalGB, usedGB, usePercent, nil
}

// parseDfSizeUsedGB 解析 `df -BG --output=size,used <mnt>` 输出（首行表头，
// 次行形如 "50G 2G"），返回 (totalGB, usedGB, ok)。纯函数，供单测。
func parseDfSizeUsedGB(dfOut string) (totalGB, usedGB float64, ok bool) {
	lines := strings.Split(strings.TrimSpace(dfOut), "\n")
	if len(lines) < 2 {
		return 0, 0, false
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 2 {
		return 0, 0, false
	}
	t := parseFloatSafe(strings.TrimRight(f[0], "Gg"))
	u := parseFloatSafe(strings.TrimRight(f[1], "Gg"))
	if t <= 0 {
		return 0, 0, false
	}
	return t, u, true
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

	common.LockDiskOp()
	defer common.UnlockDiskOp()

	poolType := r.FormValue("pool_type")
	poolDevice := r.FormValue("pool_device")
	poolName := r.FormValue("pool_name")
	confirmName := r.FormValue("confirm_name")
	confirm := r.FormValue("confirm")
	common.LogAuditRequest(r, "STORAGE", "删除存储池", fmt.Sprintf("type=%s device=%s name=%s", poolType, poolDevice, poolName), "pending")
	common.EmitEvent("storage", common.EventWarn, "storage.pool_deleted",
		map[string]interface{}{"type": poolType, "device": poolDevice}, "")

	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}
	if poolName == "" || confirmName == "" || confirmName != poolName {
		http.Error(w, `{"error":"确认名不匹配，请输入存储池名称"}`, http.StatusBadRequest)
		return
	}

	var steps []string
	// 本次删池涉及的挂载点，收集后统一清理 folders.db 元数据（见 switch 之后）。
	var deletedMountPoints []string

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
					deletedMountPoints = append(deletedMountPoints, mountPoint)
				}
				common.SudoExec("/usr/sbin/lvremove", "-f", lvPath)
				steps = append(steps, "删除 LV "+lvPath)
			}
		}
		// 2. Remove VG
		common.SudoExec("/usr/sbin/vgremove", "-f", vgName)
		steps = append(steps, "删除 VG "+vgName)
		// 3. Remove PVs — only for this VG, not all PVs on the system
		pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name", "-S", "vg_name="+vgName)
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
			deletedMountPoints = append(deletedMountPoints, mountPoint)
		}
		// 2. Get member disks BEFORE stopping the array
		detailOut, _ := common.SudoOutput("/usr/sbin/mdadm", "--detail", poolDevice)
		memberDisks := parseMdMemberDisks(detailOut)
		// 3. Stop RAID
		common.SudoExec("/usr/sbin/mdadm", "--stop", poolDevice)
		steps = append(steps, "停止 "+poolDevice)
		// 4. Zero superblocks only on THIS array's member disks
		for _, disk := range memberDisks {
			common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", disk)
			common.SudoExec("/usr/sbin/wipefs", "-a", disk)
			steps = append(steps, "释放 "+disk)
		}

	case "single":
		// Independent disk: unmount + wipe
		mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", "--source", poolDevice)
		mountPoint := strings.TrimSpace(mntOut)
		if mountPoint != "" {
			common.SudoExec("umount", "-l", mountPoint)
			steps = append(steps, "卸载 "+mountPoint)
			deletedMountPoints = append(deletedMountPoints, mountPoint)
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

	// 清理 folders.db 元数据 + 重投影托管配置（与 reset.go/stream.go 整体重置同一套做法，
	// 权威源模式：只拆磁盘不清 folders.db 的话，下次 SyncAllConfigs 会照着残留元数据把
	// 已删池的 Samba/NFS 共享重新生成回来，见 reset.go 同类注释）。
	for _, mount := range deletedMountPoints {
		if n, err := RemoveFolderMetaByMountPrefix(mount); err != nil {
			steps = append(steps, "清理 folders.db 失败("+mount+"): "+err.Error())
		} else if n > 0 {
			steps = append(steps, fmt.Sprintf("清理 folders.db: %s (%d 行)", mount, n))
		}
	}
	if len(deletedMountPoints) > 0 {
		common.SudoExec("rm", "-f", "/opt/nas/data/.last_reload")
		if err := SyncAllConfigs(); err != nil {
			steps = append(steps, "重新生成托管配置失败: "+err.Error())
		} else {
			steps = append(steps, "重新生成 Samba/NFS 托管配置")
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("存储池已删除，释放 %d 步", len(steps)),
		"steps":   steps,
	})
}

// ensure json import is used
var _ = json.Marshal

// parseMdMemberDisks extracts member disk paths from "mdadm --detail" output.
// Example input line: "   0       8       16        0      active sync   /dev/sdb"
// Returns: ["/dev/sdb", "/dev/sdc", ...]
func parseMdMemberDisks(output string) []string {
	var disks []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "active") && !strings.Contains(line, "spare") {
			continue
		}
		// Find /dev/ path in the line
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "/dev/") {
				disks = append(disks, field)
			}
		}
	}
	return disks
}
