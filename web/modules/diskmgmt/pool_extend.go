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
	common.LockDiskOp()
	defer common.UnlockDiskOp()

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

	// SSE 由前端 GET 触发，全局中间件不审计 GET——这里显式补记
	common.LogAuditRequest(r, "STORAGE", "扩展存储池(流式)", fmt.Sprintf("vg=%s device=%s lv=%s", vgName, device, lvName), "pending")
	if isSystemDisk(device) {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "安全检查", Status: "error", Detail: "不允许使用系统盘"})
		return
	}
	if isDiskInUse(device) {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "安全检查", Status: "error", Detail: "该磁盘已在存储池中使用，不能用于扩容"})
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

	// 1. Wipe disk（Fix 4：之前四步返回值全丢弃、无条件 stepDone，wipefs/pvcreate/
	// vgextend/lvextend 任一步失败都会被吞掉，SSE 只会在后面扩文件系统那步才报错，
	// 误导用户以为前面都成功了。现在每步接 err 并在失败时发 error 事件后中止。）
	stepRunning("清除磁盘签名")
	out, err := common.SudoExec("/usr/sbin/wipefs", "-a", device)
	if err != nil {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "清除磁盘签名", Status: "error", Detail: "wipefs: " + out + ": " + err.Error()})
		return
	}
	stepDone("清除磁盘签名")

	// 2. pvcreate
	stepRunning("初始化物理卷 (pvcreate)")
	out, err = common.SudoExec("/usr/sbin/pvcreate", "-f", device)
	if err != nil {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "初始化物理卷 (pvcreate)", Status: "error", Detail: "pvcreate: " + out + ": " + err.Error()})
		return
	}
	stepDone("初始化物理卷")

	// 3. vgextend
	stepRunning(fmt.Sprintf("加入卷组 %s", vgName))
	out, err = common.SudoExec("/usr/sbin/vgextend", vgName, device)
	if err != nil {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: fmt.Sprintf("加入卷组 %s", vgName), Status: "error", Detail: "vgextend: " + out + ": " + err.Error()})
		return
	}
	stepDone(fmt.Sprintf("加入卷组 %s", vgName))

	// 4. lvextend
	lvPath := "/dev/" + vgName + "/" + lvName
	stepRunning("扩展逻辑卷 (lvextend)")
	out, err = common.SudoExec("/usr/sbin/lvextend", "-l", "+100%FREE", lvPath)
	if err != nil {
		sendPoolExtendProgress(w, PoolExtendEvent{Step: "扩展逻辑卷 (lvextend)", Status: "error", Detail: "lvextend: " + out + ": " + err.Error()})
		return
	}
	stepDone("扩展逻辑卷")

	// 5. Resize filesystem（按实际文件系统类型选择 xfs_growfs/resize2fs，xfs 用挂载点而非设备路径）
	stepRunning("扩展文件系统")
	if _, _, err := growFilesystemByDevice(lvPath); err != nil {
		sendPoolExtendProgress(w, PoolExtendEvent{
			Step: "扩展文件系统", Status: "error",
			Detail: err.Error(),
		})
		return
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
