package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"nas-panel/common"
)

// PoolExtendEvent is sent via SSE during pool extension
type PoolExtendEvent struct {
	Step   string `json:"step"`
	Status string `json:"status"` // running, done, error, complete
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Detail string `json:"detail,omitempty"`
}

// sendPoolExtendProgress writes an SSE event
func sendPoolExtendProgress(w http.ResponseWriter, ev PoolExtendEvent) {
	data, _ := json.Marshal(ev)
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

// handlePoolExtendStream extends LVM pool with SSE progress
func handlePoolExtendStream(w http.ResponseWriter, r *http.Request) {
	diskOpMutex.Lock()
	defer diskOpMutex.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	vgName := r.URL.Query().Get("vg_name")
	if vgName == "" {
		vgName = "vg_nas"
	}
	device := r.URL.Query().Get("device")
	lvName := r.URL.Query().Get("lv_name")
	if lvName == "" {
		lvName = "data"
	}
	confirm := r.URL.Query().Get("confirm")

	if device == "" {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "参数检查", Status: "error", Detail: "请选择磁盘"})
		return
	}
	if confirm != "yes" {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "需要确认", Status: "error", Detail: "请加 confirm=yes"})
		return
	}
	if isSystemDisk(device) {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "安全检查", Status: "error", Detail: "不允许使用系统盘"})
		return
	}

	totalSteps := 5
	step := 0

	stepDone := func(name string) {
		step++
		sendPoolExtendProgress(w, PoolExtendEvent{Step: name, Status: "done", Index: step, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	stepRunning := func(name string) {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: name, Status: "running", Index: step + 1, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}

	// 1. Wipe disk
	stepRunning("清除磁盘签名")
	common.SudoExec("/usr/sbin/wipefs", "-a", device)
	stepDone("清除磁盘签名")

	// 2. pvcreate
	stepRunning("初始化物理卷 (pvcreate)")
	common.SudoExec("/usr/sbin/pvcreate", "-f", device)
	stepDone("初始化物理卷")

	// 3. vgextend
	stepRunning(fmt.Sprintf("加入卷组 %s", vgName))
	common.SudoExec("/usr/sbin/vgextend", vgName, device)
	stepDone(fmt.Sprintf("加入卷组 %s", vgName))

	// 4. lvextend
	lvPath := "/dev/" + vgName + "/" + lvName
	stepRunning("扩展逻辑卷 (lvextend)")
	common.SudoExec("/usr/sbin/lvextend", "-l", "+100%FREE", lvPath)
	stepDone("扩展逻辑卷")

	// 5. Resize filesystem
	stepRunning("扩展文件系统")
	// Try xfs_growfs first (we use xfs), fallback to resize2fs
	_, err := common.SudoExec("/usr/sbin/xfs_growfs", lvPath)
	if err != nil {
		common.SudoExec("/usr/sbin/resize2fs", lvPath)
	}
	stepDone("扩展文件系统")

	sendPoolExtendProgress(w, PoolExtendEvent{
		Step:   "完成",
		Status: "complete",
		Index:  totalSteps,
		Total:  totalSteps,
		Detail: fmt.Sprintf("存储池 %s 已扩容，新磁盘 %s 已加入", vgName, device),
	})
}
