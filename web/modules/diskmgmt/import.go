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
			if mounted[lvPath] {
				continue
			}
			sizeOut, _ := common.SudoOutput("/usr/sbin/lvs", "--noheadings", "--units", "g", "-o", "lv_size", vg+"/"+lvName)
			fsOut, _ := common.ExecOutput("blkid", "-s", "TYPE", "-o", "value", lvPath)
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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	diskOpMutex.Lock()
	defer diskOpMutex.Unlock()

	vgName := r.FormValue("vg_name")
	if vgName == "" {
		http.Error(w, `{"error":"缺少 vg_name"}`, http.StatusBadRequest)
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, `{"error":"请确认导入"}`, http.StatusBadRequest)
		return
	}

	// 1. 激活卷组（幂等）
	if out, err := common.SudoExec("/usr/sbin/vgchange", "-ay", vgName); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"激活卷组失败: %s"}`, strings.TrimSpace(out)), http.StatusInternalServerError)
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
		http.Error(w, `{"error":"未找到逻辑卷"}`, http.StatusInternalServerError)
		return
	}

	// 3. 挂载到 /data/nas1
	mountPoint := "/data/nas1"
	common.SudoExec("mkdir", "-p", mountPoint)
	if out, err := common.SudoExec("mount", lvPath, mountPoint); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"挂载失败: %s"}`, strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}

	// 4. fstab（按 UUID，避免设备名漂移）
	fsOut, _ := common.ExecOutput("findmnt", "-n", "-o", "FSTYPE", "--source", lvPath)
	fsType := strings.TrimSpace(fsOut)
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", lvPath)
	uuid := strings.TrimSpace(uuidOut)
	if uuid != "" {
		if data, err := os.ReadFile("/etc/fstab"); err == nil {
			content := string(data)
			if !strings.Contains(content, mountPoint) {
				if !strings.HasSuffix(content, "\n") {
					content += "\n"
				}
				content += fmt.Sprintf("UUID=%s %s %s defaults 0 2\n", uuid, mountPoint, fsType)
				common.SafeWriteFile("/etc/fstab", content)
			}
		}
	}

	// 5. 重建共享文件夹元数据（目录扫描，不新建目录）
	rebuildFoldersFromDisk(mountPoint)

	// 6. 重生成 SMB/NFS 托管配置
	SyncAllConfigs()

	common.LogAudit("system", "导入存储池", "STORAGE", "/api/disk/import/pool", "vg="+vgName, "success", "")
	common.JSONResponse(w, map[string]interface{}{
		"message":    "存储池已导入并挂载到 " + mountPoint,
		"vg_name":    vgName,
		"mountpoint": mountPoint,
	})
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
			SyncFolderMeta("public", path, mountPoint, "readwrite", "", "", true, true, false, 0)
		} else {
			SyncFolderMeta(name, path, mountPoint, "readwrite", name, "", true, false, false, 0)
		}
	}
}
