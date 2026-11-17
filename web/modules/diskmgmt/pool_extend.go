package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"

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

// sendPoolExtendProgress writes an SSE event. 返回非 nil error 表示写失败
// （典型原因：客户端已断连）——调用方（stepTracker/handler）据此中止命令链，
// 不再尝试发送后续事件（R3：此前 Fprintf 返回值被丢弃，断连后命令继续跑到底）。
func sendPoolExtendProgress(w http.ResponseWriter, ev PoolExtendEvent) error {
	data, _ := json.Marshal(ev)
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	w.(http.Flusher).Flush()
	return nil
}

// handlePoolExtendStream extends LVM pool with SSE progress
func handlePoolExtendStream(w http.ResponseWriter, r *http.Request) {
	common.LockDiskOp()
	defer common.UnlockDiskOp()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// ctx 感知（R3）：客户端断连/取消请求后 r.Context() 被标记取消，stepTracker
	// 据此中止命令链——不再执行后续 sudoExecFn，也不再发送任何新 SSE 事件
	// （连接已死，写了也白发），handler 正常返回（不 panic、不写 500）。
	ctx := r.Context()

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
	tracker := newStepTracker(totalSteps, func(ev PoolExtendEvent) error {
		return sendPoolExtendProgress(w, ev)
	}, sleepFn, ctx)

	// 1. Wipe disk（Fix 4：之前四步返回值全丢弃、无条件 stepDone，wipefs/pvcreate/
	// vgextend/lvextend 任一步失败都会被吞掉，SSE 只会在后面扩文件系统那步才报错，
	// 误导用户以为前面都成功了。现在每步接 err 并在失败时发 error 事件后中止。）
	//
	// R3：Running/Done 返回 error 既代表 ctx 取消也代表 SSE 写失败（客户端已
	// 断连）——两种情况都意味着连接已死，不再执行任何后续 sudoExecFn，也不再
	// 尝试发送 error 事件（发了也没人收），直接 return。
	if err := tracker.Running("清除磁盘签名"); err != nil {
		return
	}
	out, err := sudoExecFn("/usr/sbin/wipefs", "-a", device)
	if err != nil {
		tracker.Error("清除磁盘签名", "wipefs: "+out+": "+err.Error())
		return
	}
	if err := tracker.Done("清除磁盘签名"); err != nil {
		return
	}

	// 2. pvcreate（A3b：running/done 步骤名必须完全一致，前端按名称字符串配对
	// running→done，此前 done 少了 "(pvcreate)" 后缀，配对失败导致该 running
	// 条目永不置 done、还会多推一条独立的 done。）
	if err := tracker.Running("初始化物理卷 (pvcreate)"); err != nil {
		return
	}
	out, err = sudoExecFn("/usr/sbin/pvcreate", "-f", device)
	if err != nil {
		tracker.Error("初始化物理卷 (pvcreate)", "pvcreate: "+out+": "+err.Error())
		return
	}
	if err := tracker.Done("初始化物理卷 (pvcreate)"); err != nil {
		return
	}

	// 3. vgextend
	vgStep := fmt.Sprintf("加入卷组 %s", vgName)
	if err := tracker.Running(vgStep); err != nil {
		return
	}
	out, err = sudoExecFn("/usr/sbin/vgextend", vgName, device)
	if err != nil {
		tracker.Error(vgStep, "vgextend: "+out+": "+err.Error())
		return
	}
	if err := tracker.Done(vgStep); err != nil {
		return
	}

	// 4. lvextend
	lvPath := "/dev/" + vgName + "/" + lvName
	if err := tracker.Running("扩展逻辑卷 (lvextend)"); err != nil {
		return
	}
	out, err = sudoExecFn("/usr/sbin/lvextend", "-l", "+100%FREE", lvPath)
	if err != nil {
		tracker.Error("扩展逻辑卷 (lvextend)", "lvextend: "+out+": "+err.Error())
		return
	}
	if err := tracker.Done("扩展逻辑卷 (lvextend)"); err != nil {
		return
	}

	// 5. Resize filesystem（按实际文件系统类型选择 xfs_growfs/resize2fs，xfs 用挂载点而非设备路径）
	if err := tracker.Running("扩展文件系统"); err != nil {
		return
	}
	if _, _, err := growFilesystemByDevice(lvPath); err != nil {
		tracker.Error("扩展文件系统", err.Error())
		return
	}
	if err := tracker.Done("扩展文件系统"); err != nil {
		return
	}

	sendPoolExtendProgress(w, PoolExtendEvent{
		Step:   "完成",
		Status: "complete",
		Index:  totalSteps,
		Total:  totalSteps,
		Detail: fmt.Sprintf("存储池 %s 已扩容，新磁盘 %s 已加入", vgName, device),
	})
}
