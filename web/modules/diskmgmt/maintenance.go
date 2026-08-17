package diskmgmt

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"nas-panel/common"
)

// handleReplaceDisk replaces a failed disk in a RAID array
// Steps: 1) mark failed, 2) remove from array, 3) add new disk, 4) wait for rebuild
func handleReplaceDisk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	mdDevice := r.FormValue("md_device")
	oldDevice := r.FormValue("old_device")
	newDevice := r.FormValue("new_device")
	confirm := r.FormValue("confirm")

	if mdDevice == "" || oldDevice == "" || newDevice == "" {
		http.Error(w, `{"error":"md_device, old_device, new_device required"}`, http.StatusBadRequest)
		return
	}
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认操作"}`, http.StatusBadRequest)
		return
	}
	if isSystemDisk(newDevice) {
		http.Error(w, `{"error":"不允许使用系统盘"}`, http.StatusBadRequest)
		return
	}

	var steps []string

	// 1. Mark failed disk
	out, err := common.SudoExec("/usr/sbin/mdadm", "--manage", mdDevice, "--fail", oldDevice)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"标记故障盘失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "标记 "+oldDevice+" 为故障")

	// 2. Remove failed disk
	out, err = common.SudoExec("/usr/sbin/mdadm", "--manage", mdDevice, "--remove", oldDevice)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"移除故障盘失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "移除 "+oldDevice)

	// 3. Wipe new disk
	common.SudoExec("/usr/sbin/wipefs", "-a", newDevice)
	steps = append(steps, "清除 "+newDevice+" 签名")

	// 4. Add new disk
	out, err = common.SudoExec("/usr/sbin/mdadm", "--manage", mdDevice, "--add", newDevice)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"添加新盘失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}

	// 5. Wait a moment then check rebuild status
	time.Sleep(2 * time.Second)
	rebuildInfo := getRebuildStatus(mdDevice)
	steps = append(steps, fmt.Sprintf("添加 %s 到 %s", newDevice, mdDevice))

	common.JSONResponse(w, map[string]interface{}{
		"message":  "替换盘操作完成",
		"steps":    steps,
		"rebuild":  rebuildInfo,
	})
}

// handleScrub starts a RAID scrub (data integrity check)
func handleScrub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	mdDevice := r.FormValue("md_device")
	confirm := r.FormValue("confirm")

	if mdDevice == "" {
		// Find all md devices and scrub them
		matches, err := filepath.Glob("/dev/md*")
		if err != nil || len(matches) == 0 {
			http.Error(w, `{"error":"没有找到 RAID 设备"}`, http.StatusInternalServerError)
			return
		}
		var results []string
		for _, md := range matches {
			out, err := common.SudoExec("/usr/sbin/mdadm", "--action=check", md)
			if err != nil {
				results = append(results, fmt.Sprintf("%s: 失败 — %s", md, out))
			} else {
				results = append(results, fmt.Sprintf("%s: 已开始清理", md))
			}
		}
		common.JSONResponse(w, map[string]interface{}{
			"message": "清理已启动",
			"results": results,
		})
		return
	}

	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}

	out, err := common.SudoExec("/usr/sbin/mdadm", "--action=check", mdDevice)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"清理启动失败: %s"}`, out+": "+err.Error()), http.StatusInternalServerError)
		return
	}

	// Get current sync status
	status := getRebuildStatus(mdDevice)

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("%s 清理已启动", mdDevice),
		"status":  status,
	})
}

// handleScrubStatus returns current scrub/rebuild status
func handleScrubStatus(w http.ResponseWriter, r *http.Request) {
	mdDevice := r.URL.Query().Get("md_device")
	if mdDevice == "" {
		// Find all md devices
		matches, _ := filepath.Glob("/dev/md*")
		results := make(map[string]interface{})
		for _, md := range matches {
			results[md] = getRebuildStatus(md)
		}
		common.JSONResponse(w, map[string]interface{}{"scrub_status": results})
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"scrub_status": getRebuildStatus(mdDevice),
	})
}

// getRebuildStatus reads /proc/mdstat for rebuild/sync/check progress
func getRebuildStatus(mdDevice string) map[string]interface{} {
	devName := strings.TrimPrefix(mdDevice, "/dev/")
	data, err := common.ExecOutput("cat", "/proc/mdstat")
	if err != nil {
		return map[string]interface{}{"error": "无法读取 /proc/mdstat"}
	}

	status := map[string]interface{}{
		"device": mdDevice,
		"active": false,
	}

	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, devName+" ") && !strings.HasPrefix(line, devName+":") {
			continue
		}

		status["active"] = true

		// Check for state
		if strings.Contains(line, "active") {
			status["state"] = "active"
		}
		if strings.Contains(line, "clean") {
			status["state"] = "clean"
		}
		if strings.Contains(line, "degraded") {
			status["state"] = "degraded"
		}

		// The progress line is 1-2 lines after the md device line
		// mdstat format: md0 : ... \n blocks ... \n [>...] check = N%
		found := false
		for offset := 1; offset <= 2 && i+offset < len(lines); offset++ {
			nextLine := strings.TrimSpace(lines[i+offset])
			if strings.Contains(nextLine, "recovery") || strings.Contains(nextLine, "resync") ||
				strings.Contains(nextLine, "check") || strings.Contains(nextLine, "reshape") {
				parts := strings.Fields(nextLine)
				for _, part := range parts {
					if strings.Contains(part, "%") {
						status["progress"] = strings.TrimSuffix(part, "%")
					}
					// Handle key=value format: finish=1.4min, speed=206087K/sec
					if strings.Contains(part, "=") {
						kv := strings.SplitN(part, "=", 2)
						if len(kv) == 2 {
							if kv[0] == "finish" {
								status["eta"] = kv[1]
							}
							if kv[0] == "speed" {
								status["speed"] = kv[1]
							}
						}
					}
				}
				found = true
				break
			}
		}
		if !found {
			status["progress"] = "100"
			status["state"] = "idle"
		}
	}

	return status
}

// handleSMARTScan runs SMART self-test on all or specified disks
func handleSMARTScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	device := r.FormValue("device")
	testType := r.FormValue("type")
	if testType == "" {
		testType = "short" // short or long
	}
	confirm := r.FormValue("confirm")
	if confirm != "yes" {
		http.Error(w, `{"error":"请加 confirm=yes 确认"}`, http.StatusBadRequest)
		return
	}

	var results []map[string]string

	if device != "" {
		// Single disk
		out, err := common.SudoExec("smartctl", "-t", testType, device)
		result := map[string]string{"device": device, "type": testType}
		if err != nil {
			result["status"] = "failed"
			result["detail"] = out
		} else {
			result["status"] = "started"
			result["detail"] = "测试已启动，约需2分钟(short)/数小时(long)"
		}
		results = append(results, result)
	} else {
		// All non-system disks
		disks := getDiskStatus()
		for _, d := range disks {
			if d.Name == "sr0" || d.Name == "zram0" || strings.HasPrefix(d.Name, "loop") {
				continue
			}
			if isSystemDisk(d.Device) {
				continue
			}
			// Only test whole disks, not partitions
			dev := "/dev/" + d.Name
			out, err := common.SudoExec("smartctl", "-t", testType, dev)
			result := map[string]string{"device": dev, "type": testType}
			if err != nil {
				result["status"] = "failed"
				result["detail"] = out
			} else {
				result["status"] = "started"
				result["detail"] = "测试已启动"
			}
			results = append(results, result)
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("SMART %s 测试已启动 (%d 个磁盘)", testType, len(results)),
		"results": results,
	})
}