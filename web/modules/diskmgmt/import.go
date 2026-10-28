package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"nas-panel/common"
)

// ImportablePool 一个可导入的既有存储池（LVM 卷组）
type ImportablePool struct {
	VGName string   `json:"vg_name"`
	PVs    []string `json:"pvs"`
	Size   string   `json:"size"`
	LVName string   `json:"lv_name"`
	LVPath string   `json:"lv_path"`
	FSType string   `json:"fstype"`
}

// detectImportablePools 扫描 LVM 卷组，返回未被挂载（可导入）的池。
func detectImportablePools() []ImportablePool {
	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name,vg_name")
	vgPVs := map[string][]string{}
	for _, line := range strings.Split(pvsOut, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[1] != "" {
			vgPVs[f[1]] = append(vgPVs[f[1]], f[0])
		}
	}

	// 已挂载的 /data 卷（= 活动池），导入时跳过
	mounted := map[string]bool{}
	for _, m := range getExistingDataMounts() {
		mounted[m["device"]] = true
	}

	var result []ImportablePool
	for vg, pvs := range vgPVs {
		lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "-o", "lv_name,lv_path", vg)
		for _, line := range strings.Split(lvsOut, "\n") {
			f := strings.Fields(strings.TrimSpace(line))
			if len(f) < 2 {
				continue
			}
			lvName, lvPath := f[0], f[1]
			// df --output=source 对 LVM 卷返回 /dev/mapper/vg-lv，而 lv_path 返回 /dev/vg/lv，
			// 两个字符串不等；归一化成 dm 形式一起比对，否则已挂载的池会被误判为可导入。
			dmPath := "/dev/mapper/" + strings.ReplaceAll(strings.TrimPrefix(lvPath, "/dev/"), "/", "-")
			if mounted[lvPath] || mounted[dmPath] {
				continue
			}
			sizeOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "--units", "g", "-o", "lv_size", vg+"/"+lvName)
			// blkid 读超级块需 root，必须走 sudo（S1-R3c：此前 ExecOutput 不带 sudo，
			// fm 下 FSType 静默读空，可导入池列表显示空文件系统类型）
			fsOut, _ := common.SudoOutput("blkid", "-s", "TYPE", "-o", "value", lvPath)
			result = append(result, ImportablePool{
				VGName: vg, PVs: pvs, Size: strings.TrimSpace(sizeOut),
				LVName: lvName, LVPath: lvPath, FSType: strings.TrimSpace(fsOut),
			})
		}
	}
	return result
}

// handleImportList 返回可导入的存储池列表
func handleImportList(w http.ResponseWriter, r *http.Request) {
	pools := detectImportablePools()
	if pools == nil {
		pools = []ImportablePool{}
	}
	common.JSONResponse(w, map[string]interface{}{"pools": pools})
}

// handleImportPool 导入（激活+挂载+重建元数据）。非破坏性：不 wipe、不 format、不 remove。
func handleImportPool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	common.LockDiskOp()
	defer common.UnlockDiskOp()

	vgName := r.FormValue("vg_name")
	if vgName == "" {
		jsonError(w, "缺少 vg_name", http.StatusBadRequest)
		return
	}
	if r.FormValue("confirm") != "yes" {
		jsonError(w, "请确认导入", http.StatusBadRequest)
		return
	}

	// 1. 激活卷组（幂等）
	if out, err := common.SudoExec("/usr/sbin/vgchange", "-ay", vgName); err != nil {
		jsonError(w, fmt.Sprintf("激活卷组失败: %s", strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}
	// 2. 取第一个 LV
	lvsOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "-o", "lv_name,lv_path", vgName)
	var lvPath string
	for _, line := range strings.Split(lvsOut, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 {
			lvPath = f[1]
			break
		}
	}
	if lvPath == "" {
		jsonError(w, "未找到逻辑卷", http.StatusInternalServerError)
		return
	}

	// 3. 挂载到 /data/nas1
	mountPoint := "/data/nas1"
	common.SudoExec("mkdir", "-p", mountPoint)
	if out, err := common.SudoExec("mount", lvPath, mountPoint); err != nil {
		jsonError(w, fmt.Sprintf("挂载失败: %s", strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}

	// 4. 检测文件系统类型；xfs 重新挂载加 prjquota（面板目录配额依赖该挂载选项）
	fsOut, _ := common.ExecOutput("findmnt", "-n", "-o", "FSTYPE", "--source", lvPath)
	fsType := strings.TrimSpace(fsOut)
	if fsType == "xfs" {
		common.SudoExec("mount", "-o", "remount,prjquota", mountPoint)
	}

	// 5. fstab（按 UUID，避免设备名漂移）。blkid 读超级块需 root，必须走 sudo；
	// 挂载本身已经成功，fstab 写入失败不回滚挂载（导入是非破坏性操作），但必须
	// 显式告知调用方持久化未完成，而不是静默吞错后谎称"已导入"（S1-R3c）。
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	uuid := strings.TrimSpace(uuidOut)
	fstabWarning := ""
	if err := writeFstab(uuid, mountPoint, fsType); err != nil {
		fstabWarning = "挂载成功，但写入 fstab 持久化失败（重启后需重新导入）: " + err.Error()
	}

	// 6. 重建共享文件夹元数据（目录扫描，不新建目录）
	rebuildFoldersFromDisk(mountPoint)

	// 7. 重生成 SMB/NFS 托管配置
	SyncAllConfigs()

	auditDetail := "vg=" + vgName
	if fstabWarning != "" {
		auditDetail += " warning=" + fstabWarning
	}
	common.LogAuditRequest(r, "STORAGE", "导入存储池", auditDetail, "success")
	common.EmitEvent("storage", common.EventSuccess, "storage.pool_imported",
		map[string]interface{}{"vg": vgName}, "")
	resp := map[string]interface{}{
		"message":    "存储池已导入并挂载到 " + mountPoint,
		"vg_name":    vgName,
		"mountpoint": mountPoint,
	}
	if fstabWarning != "" {
		resp["warning"] = fstabWarning
	}
	common.JSONResponse(w, resp)
}

// rebuildFoldersFromDisk 扫描挂载点下的目录，重建 folders.db 元数据。
// public → 所有人读写 + NFS；其它目录 → 视为用户 home（valid_users=目录名）。
func rebuildFoldersFromDisk(mountPoint string) {
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "#recycle" || name == "lost+found" || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(mountPoint, name)
		if name == "public" {
			SyncFolderMeta(FolderMeta{Name: "public", Path: path, Pool: mountPoint, Permission: "readwrite", SambaShare: true, NFSExport: true})
		} else {
			SyncFolderMeta(FolderMeta{Name: name, Path: path, Pool: mountPoint, Permission: "readwrite", ValidUsers: name, SambaShare: true})
		}
	}
}
