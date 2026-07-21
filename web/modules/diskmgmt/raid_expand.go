package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"nas-panel/common"
)

// RAIDExpandEvent is sent via SSE during RAID expansion
type RAIDExpandEvent struct {
	Step   string `json:"step"`
	Status string `json:"status"` // running, done, error, complete, reshaping
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Detail string `json:"detail,omitempty"`
}

func sendRAIDExpandProgress(w http.ResponseWriter, ev RAIDExpandEvent) {
	data, _ := json.Marshal(ev)
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

// handleRAIDExpandStream expands a RAID array (RAID1/5/6) by adding a new disk
// For RAID1: adds disk, grows to N+1 devices, waits for resync, grows filesystem
// For RAID5/6: adds disk, starts reshape (returns immediately with reshaping status)
func handleRAIDExpandStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	mdDev := r.URL.Query().Get("md_device")   // e.g. /dev/md0
	device := r.URL.Query().Get("device")     // new disk, e.g. /dev/sdd
	confirm := r.URL.Query().Get("confirm")

	// ── 参数检查 ──
	if mdDev == "" || device == "" {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "参数检查", Status: "error", Detail: "缺少 md_device 或 device 参数"})
		return
	}
	if confirm != "yes" {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "需要确认", Status: "error", Detail: "请加 confirm=yes"})
		return
	}
	if isSystemDisk(device) {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "安全检查", Status: "error", Detail: "不允许使用系统盘"})
		return
	}

	// ── 检查 md 设备存在 ──
	if _, err := os.Stat(mdDev); err != nil {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "设备检查", Status: "error", Detail: mdDev + " 不存在"})
		return
	}

	// ── 获取 RAID 信息 ──
	detailOut, err := common.SudoOutput("/usr/sbin/mdadm", "--detail", mdDev)
	if err != nil {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "RAID信息", Status: "error", Detail: "无法读取 " + mdDev + " 详情"})
		return
	}

	raidLevel := ""
	currentDevs := 0
	for _, line := range strings.Split(detailOut, "\n") {
		if strings.Contains(line, "Raid Level") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				raidLevel = strings.TrimSpace(parts[1])
			}
		}
		if strings.Contains(line, "Raid Devices") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &currentDevs)
			}
		}
	}

	// 只支持 RAID1/5/6 扩容
	if raidLevel != "raid1" && raidLevel != "raid5" && raidLevel != "raid6" {
		sendRAIDExpandProgress(w, RAIDExpandEvent{
			Step: "RAID级别检查", Status: "error",
			Detail: "不支持扩容 " + raidLevel + "，仅支持 RAID1/5/6",
		})
		return
	}

	if currentDevs == 0 {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "RAID信息", Status: "error", Detail: "无法解析当前阵列设备数"})
		return
	}

	// ── 检查磁盘是否已在阵列中 ──
	if strings.Contains(detailOut, device) {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "磁盘检查", Status: "error", Detail: device + " 已在阵列中"})
		return
	}

	// ── 检查磁盘是否空闲 ──
	disks := getDiskStatus()
	diskFree := false
	for _, d := range disks {
		if d.Device == device && d.Type == "unused" {
			diskFree = true
			break
		}
	}
	if !diskFree {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "磁盘检查", Status: "error", Detail: device + " 不是空闲磁盘"})
		return
	}

	newDevs := currentDevs + 1
	raidName := strings.ToUpper(raidLevel)

	// 步骤数: wipe + add + grow (+ resync + fs resize)
	totalSteps := 3
	isRAID1 := raidLevel == "raid1"
	if isRAID1 {
		totalSteps = 5 // wipe, add, grow, resync, xfs_growfs
	} else {
		totalSteps = 4 // wipe, add, grow(reshape启动), 完成提示
	}

	step := 0
	stepDone := func(name string) {
		step++
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: name, Status: "done", Index: step, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	stepRunning := func(name string) {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: name, Status: "running", Index: step + 1, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}

	// ── Step 1: Wipe new disk ──
	stepRunning("清除新磁盘签名")
	out, err := common.SudoExec("/usr/sbin/wipefs", "-a", device)
	if checkRAIDError(w, "清除磁盘签名", out, err) {
		return
	}
	stepDone("清除新磁盘签名")

	// ── Step 2: Add disk to array ──
	stepRunning(fmt.Sprintf("添加 %s 到 %s", device, mdDev))
	out, err = common.SudoExec("/usr/sbin/mdadm", mdDev, "--add", device)
	if checkRAIDError(w, "添加磁盘", out, err) {
		return
	}
	stepDone(fmt.Sprintf("添加 %s 到阵列", device))

	// ── Step 3: Grow array ──
	stepRunning(fmt.Sprintf("扩展 %s 到 %d 盘", raidName, newDevs))
	out, err = common.SudoExec("/usr/sbin/mdadm", "--grow", mdDev, "--raid-devices="+fmt.Sprintf("%d", newDevs))
	if checkRAIDError(w, "扩展阵列", out, err) {
		return
	}
	stepDone(fmt.Sprintf("扩展 %s 到 %d 盘", raidName, newDevs))

	if isRAID1 {
		// RAID1: wait for resync, then grow filesystem
		stepRunning("等待数据同步 (resync)")
		waitForResync(w, mdDev, step, totalSteps)
		step++
		stepDone("数据同步完成")

		// Grow filesystem
		stepRunning("扩展文件系统")
		growFilesystem(w, mdDev)
		stepDone("扩展文件系统")

		sendRAIDExpandProgress(w, RAIDExpandEvent{
			Step: "完成", Status: "complete", Index: totalSteps, Total: totalSteps,
			Detail: fmt.Sprintf("%s 已从 %d 盘扩展到 %d 盘，容量不变（镜像）", raidName, currentDevs, newDevs),
		})
	} else {
		// RAID5/6: reshape takes hours, return immediately
		sendRAIDExpandProgress(w, RAIDExpandEvent{
			Step: "完成", Status: "reshaping", Index: totalSteps, Total: totalSteps,
			Detail: fmt.Sprintf("%s 正在重构数据 (%d→%d 盘)，这可能需要几小时。重构完成后需手动扩展文件系统。", raidName, currentDevs, newDevs),
		})
	}
}

// handleRAIDExpandFS grows the filesystem after RAID5/6 reshape completes
// Called separately after user confirms reshape is done
func handleRAIDExpandFS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	mdDev := r.FormValue("md_device")
	confirm := r.FormValue("confirm")

	if mdDev == "" || confirm != "yes" {
		http.Error(w, `{"error":"md_device and confirm=yes required"}`, http.StatusBadRequest)
		return
	}

	// Check if reshape is still running
	mdstat, _ := os.ReadFile("/proc/mdstat")
	mdName := strings.TrimPrefix(mdDev, "/dev/")
	for _, line := range strings.Split(string(mdstat), "\n") {
		if strings.HasPrefix(line, mdName) && (strings.Contains(line, "reshape") || strings.Contains(line, "resync")) {
			common.JSONResponse(w, map[string]interface{}{
				"error": "阵列仍在重构中，请等待完成后再扩展文件系统",
			})
			return
		}
	}

	// Grow filesystem
	out, err := common.SudoExec("/usr/sbin/xfs_growfs", mdDev)
	if err != nil {
		common.SudoExec("/usr/sbin/resize2fs", mdDev)
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": "文件系统已扩展",
		"output":  out,
	})
}

// handleRAIDReshapeStatus returns current reshape/resync progress
func handleRAIDReshapeStatus(w http.ResponseWriter, r *http.Request) {
	mdDev := r.URL.Query().Get("md_device")
	if mdDev == "" {
		http.Error(w, `{"error":"md_device required"}`, http.StatusBadRequest)
		return
	}

	mdName := strings.TrimPrefix(mdDev, "/dev/")
	mdstat, _ := os.ReadFile("/proc/mdstat")

	status := map[string]interface{}{
		"device":   mdDev,
		"reshaping": false,
		"progress":  "",
		"speed":     "",
		"eta":       "",
	}

	lines := strings.Split(string(mdstat), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, mdName) {
			// Check next line for progress (reshape/resync line)
			if i+1 < len(lines) {
				nextLine := lines[i+1]
				if strings.Contains(nextLine, "reshape") || strings.Contains(nextLine, "resync") {
					status["reshaping"] = true
					status["progress"] = extractPercent(nextLine)
					// Extract speed and ETA
					if idx := strings.Index(nextLine, "speed="); idx > 0 {
						speedPart := nextLine[idx:]
						if endIdx := strings.Index(speedPart, " "); endIdx > 0 {
							status["speed"] = speedPart[:endIdx]
						}
					}
					if idx := strings.Index(nextLine, "finish="); idx > 0 {
						etaPart := nextLine[idx:]
						if endIdx := strings.Index(etaPart, " "); endIdx > 0 {
							status["eta"] = strings.TrimRight(etaPart[:endIdx], "min")
						}
					}
				}
			}
			// Parse state from the md line itself
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				status["state"] = fields[2]
			}
			break
		}
	}

	common.JSONResponse(w, status)
}

// waitForResync polls /proc/mdstat until resync completes (for RAID1)
func waitForResync(w http.ResponseWriter, mdDev string, currentStep, totalSteps int) {
	mdName := strings.TrimPrefix(mdDev, "/dev/")
	for i := 0; i < 600; i++ { // max 10 minutes
		mdstat, _ := os.ReadFile("/proc/mdstat")
		resyncDone := true
		lines := strings.Split(string(mdstat), "\n")
		for j, line := range lines {
			if strings.HasPrefix(line, mdName) {
				if j+1 < len(lines) {
					nextLine := lines[j+1]
					if strings.Contains(nextLine, "resync") || strings.Contains(nextLine, "recovery") {
						resyncDone = false
						pct := extractPercent(nextLine)
						sendRAIDExpandProgress(w, RAIDExpandEvent{
							Step: "等待数据同步", Status: "running",
							Index: currentStep + 1, Total: totalSteps,
							Detail: "同步进度: " + pct,
						})
					}
				}
				break
			}
		}
		if resyncDone {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// growFilesystem grows the filesystem on md device
func growFilesystem(w http.ResponseWriter, mdDev string) {
	_, err := common.SudoExec("/usr/sbin/xfs_growfs", mdDev)
	if err != nil {
		common.SudoExec("/usr/sbin/resize2fs", mdDev)
	}
}

// checkRAIDError checks error and sends SSE error event
func checkRAIDError(w http.ResponseWriter, step, output string, err error) bool {
	if err != nil {
		sendRAIDExpandProgress(w, RAIDExpandEvent{
			Step: step, Status: "error",
			Detail: output + ": " + err.Error(),
		})
		return true
	}
	return false
}
