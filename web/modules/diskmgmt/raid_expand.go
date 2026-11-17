package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
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

// sendRAIDExpandProgress writes an SSE event. 返回非 nil error 表示写失败
// （典型原因：客户端已断连）——调用方据此中止命令链，不再尝试发送后续事件
// （R3：此前 Fprintf 返回值被丢弃，断连后命令继续跑到底）。
func sendRAIDExpandProgress(w http.ResponseWriter, ev RAIDExpandEvent) error {
	data, _ := json.Marshal(ev)
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	w.(http.Flusher).Flush()
	return nil
}

// handleRAIDExpandStream expands a RAID array (RAID1/5/6) by adding a new disk
// For RAID1: adds disk, grows to N+1 devices, waits for resync, grows filesystem
// For RAID5/6: adds disk, starts reshape (returns immediately with reshaping status)
func handleRAIDExpandStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// ctx 感知（R3）：客户端断连/取消请求后 r.Context() 被标记取消，
	// raidStepTracker 据此中止命令链——不再执行后续 sudoExecFn，也不再发送
	// 任何新 SSE 事件（连接已死，写了也白发），handler 正常返回（不 panic、
	// 不写 500）。
	ctx := r.Context()

	mdDev := r.URL.Query().Get("md_device") // e.g. /dev/md0
	device := r.URL.Query().Get("device")   // new disk, e.g. /dev/sdd
	confirm := r.URL.Query().Get("confirm")

	// ── 参数检查（锁外）──
	// 廉价校验先做完再拿全局磁盘锁：错误参数的请求不应该排队等
	// 可能长达数小时的无关磁盘操作（RAID1 扩容持锁跨整个 resync）。
	if mdDev == "" || device == "" {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "参数检查", Status: "error", Detail: "缺少 md_device 或 device 参数"})
		return
	}
	if confirm != "yes" {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "需要确认", Status: "error", Detail: "请加 confirm=yes"})
		return
	}

	// ── 检查 md 设备存在（锁外，只读）──
	if _, err := os.Stat(mdDev); err != nil {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "设备检查", Status: "error", Detail: mdDev + " 不存在"})
		return
	}

	// ── 获取 RAID 信息并校验级别（锁外，只读 mdadm --detail；A2：raid0 无冗余，
	// 不支持扩容——在拿全局磁盘锁之前就拒绝，不让无效请求排队等锁）──
	detailOut, err := sudoOutputFn("/usr/sbin/mdadm", "--detail", mdDev)
	if err != nil {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "RAID信息", Status: "error", Detail: "无法读取 " + mdDev + " 详情"})
		return
	}

	raidLevel, currentDevs := parseMdDetailLevelAndDevices(detailOut)

	// 只支持 RAID1/5/6 扩容；raid0 无冗余，强制拒绝（A2）
	if raidLevel == "raid0" {
		common.LogAuditRequest(r, "STORAGE", "扩展RAID阵列(流式)", fmt.Sprintf("md=%s device=%s rejected=RAID0无冗余不支持扩容/换盘", mdDev, device), "rejected")
		sendRAIDExpandProgress(w, RAIDExpandEvent{
			Step: "RAID级别检查", Status: "error",
			Detail: "RAID0 无冗余，不支持扩容/换盘",
		})
		return
	}
	if raidLevel != "raid1" && raidLevel != "raid5" && raidLevel != "raid6" {
		common.LogAuditRequest(r, "STORAGE", "扩展RAID阵列(流式)", fmt.Sprintf("md=%s device=%s rejected=不支持扩容%s仅支持RAID1/5/6", mdDev, device, raidLevel), "rejected")
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

	common.LockDiskOp()
	defer common.UnlockDiskOp()

	// SSE 由前端 GET 触发，全局中间件不审计 GET——这里显式补记
	common.LogAuditRequest(r, "STORAGE", "扩展RAID阵列(流式)", fmt.Sprintf("md=%s device=%s", mdDev, device), "pending")
	if isSystemDisk(device) {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "安全检查", Status: "error", Detail: "不允许使用系统盘"})
		return
	}

	// ── 检查磁盘是否已在阵列中 ──
	if strings.Contains(detailOut, device) {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "磁盘检查", Status: "error", Detail: device + " 已在阵列中"})
		return
	}

	// ── 检查磁盘是否空闲 ──
	disks := getDiskStatusFn()
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

	// 纵深防御：与 handleReplaceDisk（maintenance.go）、handlePoolExtendStream
	// （pool_extend.go）的 isSystemDisk+isDiskInUse 双保险对齐——上面的
	// Type=="unused" 单层检查当前能拦住大多数场景，但 isDiskInUse 额外识别
	// PV/RAID 成员或含存量签名的盘，两层互不替代。
	if isDiskInUse(device) {
		sendRAIDExpandProgress(w, RAIDExpandEvent{Step: "磁盘检查", Status: "error", Detail: device + " 已被占用（PV/RAID成员或含存量签名），不能用于扩容"})
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

	tracker := newRAIDStepTracker(totalSteps, func(ev RAIDExpandEvent) error {
		return sendRAIDExpandProgress(w, ev)
	}, sleepFn, ctx)

	// ── Step 1: Wipe new disk ──
	// R3：Running/Done 返回 error 既代表 ctx 取消也代表 SSE 写失败（客户端
	// 已断连）——两种情况都意味着连接已死，不再执行任何后续 sudoExecFn，也
	// 不再尝试发送 error 事件，直接 return。
	if err := tracker.Running("清除新磁盘签名"); err != nil {
		return
	}
	out, err := sudoExecFn("/usr/sbin/wipefs", "-a", device)
	if checkRAIDError(tracker, "清除磁盘签名", out, err) {
		return
	}
	if err := tracker.Done("清除新磁盘签名"); err != nil {
		return
	}

	// ── Step 2: Add disk to array ──
	if err := tracker.Running(fmt.Sprintf("添加 %s 到 %s", device, mdDev)); err != nil {
		return
	}
	out, err = sudoExecFn("/usr/sbin/mdadm", mdDev, "--add", device)
	if checkRAIDError(tracker, "添加磁盘", out, err) {
		return
	}
	if err := tracker.Done(fmt.Sprintf("添加 %s 到阵列", device)); err != nil {
		return
	}

	// ── Step 3: Grow array ──
	if err := tracker.Running(fmt.Sprintf("扩展 %s 到 %d 盘", raidName, newDevs)); err != nil {
		return
	}
	out, err = sudoExecFn("/usr/sbin/mdadm", "--grow", mdDev, "--raid-devices="+fmt.Sprintf("%d", newDevs))
	if checkRAIDError(tracker, "扩展阵列", out, err) {
		return
	}
	if err := tracker.Done(fmt.Sprintf("扩展 %s 到 %d 盘", raidName, newDevs)); err != nil {
		return
	}

	if isRAID1 {
		// RAID1: wait for resync, then grow filesystem
		if err := tracker.Running("等待数据同步 (resync)"); err != nil {
			return
		}
		if err := waitForResync(w, mdDev, tracker.CurrentStep(), totalSteps); err != nil {
			tracker.Error("等待数据同步", "阵列同步未在 10 分钟内完成，已中止，文件系统未扩展；阵列仍在后台同步属正常，"+
				"等同步完成后可用「扩展文件系统」入口（/api/disk/raid/expand-fs）单独补跑")
			return
		}
		// SkipStep 补偏移：resync 轮询期间发的是 waitForResync 自己的
		// running 事件（不经过 tracker.Done），原 handler 在这里手写了
		// step++ 来补偏移，使"数据同步完成" done 事件的 Index 与原行为一致。
		tracker.SkipStep()
		if err := tracker.Done("数据同步完成"); err != nil {
			return
		}

		// Grow filesystem
		if err := tracker.Running("扩展文件系统"); err != nil {
			return
		}
		if !growFilesystem(tracker, mdDev) {
			return
		}
		if err := tracker.Done("扩展文件系统"); err != nil {
			return
		}

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
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mdDev := r.FormValue("md_device")
	confirm := r.FormValue("confirm")

	if mdDev == "" || confirm != "yes" {
		jsonError(w, "md_device and confirm=yes required", http.StatusBadRequest)
		return
	}

	common.LockDiskOp()
	defer common.UnlockDiskOp()

	// Check if reshape is still running
	// consensus R2：闸门优先看 sysfs —— sync_action != idle 或
	// reshape_position != none/0 即拒绝；sysfs 不可用（目录缺失）时回退
	// mdstat parser 路径（此前 consensus P0-D 已把头行本身找关键词的死闸门
	// 改为检查块内任一进度行，这里完整保留不删）。
	mdName := strings.TrimPrefix(mdDev, "/dev/")
	reshaping := false
	// consensus R2 回炉 Fix2：sync_action 缺键时不当作 idle（fail-open 会
	// 让仍在重构的阵列放行扩文件系统），走 mdstat 回退判据。
	sysfsUsable := false
	if vals, _, ok := readRAIDSysfsValues(mdName); ok {
		if syncAction, hasKey := sysfsSyncAction(vals); hasKey {
			sysfsUsable = true
			reshapePos := strings.TrimSpace(vals["reshape_position"])
			reshaping = !sysfsResyncDone(syncAction) || (reshapePos != "" && reshapePos != "none" && reshapePos != "0")
		}
	}
	if !sysfsUsable {
		mdstatBytes, _ := os.ReadFile(mdstatPath)
		blocks := parseMdstatBlocks(string(mdstatBytes))
		if block, found := findBlockByMdName(blocks, mdName); found && blockHasProgress(block) {
			reshaping = true
		}
	}
	if reshaping {
		common.JSONResponse(w, map[string]interface{}{
			"error": "阵列仍在重构中，请等待完成后再扩展文件系统",
		})
		return
	}

	// Grow filesystem（按实际文件系统类型选择 xfs_growfs/resize2fs，xfs 用挂载点而非设备路径）
	step, out, err := growFilesystemByDevice(mdDev)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": "文件系统已扩展",
		"step":    step,
		"output":  out,
	})
}

// handleRAIDReshapeStatus returns current reshape/resync progress
//
// consensus R2：sysfs 优先——reshaping = sync_action != idle；progress 从
// sync_completed 算百分比；speed = sync_speed + "K/sec"；eta 由
// (total-done)/speed 估算（分钟，一位小数；speed 为 0/none 时置空）。
// mdstat 的 finish= 不再是唯一来源，这里是独立的估算路径。sysfs 不可用
// （目录缺失）时完整回退下方 mdstat 路径，不删除。
func handleRAIDReshapeStatus(w http.ResponseWriter, r *http.Request) {
	mdDev := r.URL.Query().Get("md_device")
	if mdDev == "" {
		jsonError(w, "md_device required", http.StatusBadRequest)
		return
	}

	mdName := strings.TrimPrefix(mdDev, "/dev/")

	status := map[string]interface{}{
		"device":    mdDev,
		"reshaping": false,
		"progress":  "",
		"speed":     "",
		"eta":       "",
	}

	// consensus R2 回炉 Fix2：sync_action 缺键时不当作 idle（fail-open 会
	// 让仍在重构的阵列报 reshaping=false），走 mdstat 回退判据。
	if vals, _, ok := readRAIDSysfsValues(mdName); ok {
		if syncAction, hasKey := sysfsSyncAction(vals); hasKey {
			status["state"] = vals["array_state"]
			if !sysfsResyncDone(syncAction) {
				status["reshaping"] = true
				status["progress"] = syncCompletedPercent(vals["sync_completed"])
				if speed, speedOK := parseSyncSpeed(vals["sync_speed"]); speedOK {
					status["speed"] = strconv.FormatFloat(speed, 'f', 0, 64) + "K/sec"
					if done, total, syncOK := parseSyncCompleted(vals["sync_completed"]); syncOK {
						if eta, etaOK := reshapeETAMinutes(done, total, speed); etaOK {
							status["eta"] = eta
						}
					}
				}
			}
			common.JSONResponse(w, status)
			return
		}
	}

	// sysfs 不可用（目录缺失或 sync_action 缺键）：回退 mdstat 文本解析。
	// consensus P0-D：改走 parseMdstatBlocks——此前只看头行的下一行
	// (lines[i+1])，真实 mdstat 的进度行通常在 blocks 行之后（i+2 及以后），
	// 会漏检；现在进度行在阵列块内任意缩进行位置都能命中。
	mdstatBytes, _ := os.ReadFile(mdstatPath)
	blocks := parseMdstatBlocks(string(mdstatBytes))
	if block, found := findBlockByMdName(blocks, mdName); found {
		if p, ok := firstProgress(block); ok && (p.Kind == "reshape" || p.Kind == "resync") {
			status["reshaping"] = true
			status["progress"] = p.Percent
			status["speed"] = p.Speed
			status["eta"] = strings.TrimRight(p.Finish, "min")
		}
		if len(block.HeaderFields) >= 3 {
			status["state"] = block.HeaderFields[2]
		}
	}

	common.JSONResponse(w, status)
}

// waitForResync polls RAID sync status until resync completes (for RAID1)。
// 返回 error：正常在限时内完成返回 nil；600 轮（10 分钟）超时仍未完成返回非 nil —
// 调用方必须据此中止，不能再继续 stepDone/growFilesystem（A1：阵列还在重构时
// 扩文件系统会损坏数据）。
//
// R4：外壳签名不变（既有测试 TestWaitForResyncReturnsNilWhenNoResyncLine 直调），
// 内部改为构造 poll 闭包（resyncPollOnce：sysfs 优先、mdstat 回退）+ emit 回调
// （经 sendRAIDExpandProgress）→ 委托 waitForResyncCore 做循环判定。R3 声明
// 缺口收口：emit 返回 error（ctx 取消/SSE 写失败）时 waitForResyncCore 立即
// 停止轮询并把该 error 向上传播，不再空转到超时或 resync 自然完成。
//
// consensus R2：优先轮询 sysfs sync_action（/sys/block/<md>/md/sync_action），
// 完成判据改为"连续 cleanStreak 轮 idle"（sysfsCleanStreakResult 抽出的纯函数
// 判定逻辑一致）；sysfs 不可用（目录缺失）时回退下方 mdstat parser 路径，
// 完整保留，不删除。进度事件文案沿用现有格式，百分比优先从 sync_completed
// 算（syncCompletedPercent），sysfs 不可用时取 mdstat 进度行的 Percent。
//
// Finding 3（首轮竞态）：mdadm --add/--grow 后内核把 resync 状态写出有延迟，
// 若循环一开始就立即读取，很可能在内核还没来得及写出之前就读到"干净"状态，
// 直接误判完成返回 nil，growFilesystem 会在阵列实际还在重构时跑。修法两处：
// ① 循环前先 sleep 2s，给内核一点时间把状态写出来；② 不再是"读到一次干净就
// 算完成"，而是要求连续 cleanStreak 达到 3 轮（约 6s，含循环内已有的 2s 间隔）
// 都是 idle/无进度行才判定完成——期间只要观察到过进行中状态，streak 清零
// 重新累计。600 轮总预算（10 分钟）不变。
func waitForResync(w http.ResponseWriter, mdDev string, currentStep, totalSteps int) error {
	mdName := strings.TrimPrefix(mdDev, "/dev/")
	readSysfs := func(name string) (map[string]string, bool) {
		vals, _, ok := readRAIDSysfsValues(name)
		return vals, ok
	}
	poll := func() (bool, string) {
		mdstatBytes, _ := os.ReadFile(mdstatPath)
		return resyncPollOnce(mdName, readSysfs, string(mdstatBytes))
	}
	emit := func(detail string) error {
		return sendRAIDExpandProgress(w, RAIDExpandEvent{
			Step: "等待数据同步", Status: "running",
			Index: currentStep + 1, Total: totalSteps,
			Detail: detail,
		})
	}
	const requiredCleanStreak = 3
	const maxRounds = 600 // max 10 minutes
	return waitForResyncCore(poll, maxRounds, requiredCleanStreak, sleepFn, emit)
}

// waitForResyncCore 是 waitForResync 的纯核：判据经 poll 注入、睡眠经 sleepFn
// 注入、轮数上限经 maxRounds 参数化，不依赖 http.ResponseWriter——进度事件
// 的发送经 emit 回调完成。
//
// 语义：
//   - 循环前先 sleepFn(2s)，给轮询判据一点时间稳定下来（Finding 3 首轮竞态：
//     mdadm --add/--grow 后内核写出 resync 状态有延迟）。
//   - 每轮调 poll()；done=false 时调 emit(detail) 发一次 running 进度事件——
//     emit 返回 error（R4：ctx 取消/SSE 写失败）立即停止轮询并把该 error
//     向上返回，不再继续 sleepFn 空转（R3 声明缺口收口：此前轮询期间的写
//     失败被忽略，断连后仍会空转到超时）。
//   - done=true 累计 cleanStreak；达到 requiredCleanStreak 后返回 nil；
//     done=false 则 cleanStreak 归零重新累计。
//   - 每轮末尾（无论 done 与否）sleepFn(2s)；满 maxRounds 仍未达成 streak
//     返回超时 error。
func waitForResyncCore(poll func() (done bool, detail string), maxRounds, requiredCleanStreak int, sleepFn func(time.Duration), emit func(detail string) error) error {
	sleepFn(2 * time.Second)
	cleanStreak := 0
	for i := 0; i < maxRounds; i++ {
		done, detail := poll()

		if !done {
			if err := emit(detail); err != nil {
				return err
			}
		}

		if done {
			cleanStreak++
			if cleanStreak >= requiredCleanStreak {
				return nil
			}
		} else {
			cleanStreak = 0
		}
		sleepFn(2 * time.Second)
	}
	return fmt.Errorf("阵列同步未在 10 分钟内完成")
}

// resyncPollOnce 是 waitForResync 单轮判据的纯函数版：sysfs 优先（经
// readSysfs 注入，生产传 readRAIDSysfsValues）、sync_action 缺键或目录缺失
// 时回退 mdstatText 文本解析。与既有 sysfsCleanStreakResult（多轮 streak 判定）
// /resyncProgressDetail（文案拼接）分工不重复：本函数只负责"这一轮是否
// done + 本轮 detail 文案"，streak 累计逻辑留在 waitForResyncCore，sysfs
// 分支的文案拼接委托 resyncProgressDetail。
func resyncPollOnce(mdName string, readSysfs func(string) (map[string]string, bool), mdstatText string) (done bool, detail string) {
	if vals, ok := readSysfs(mdName); ok {
		if syncAction, hasKey := sysfsSyncAction(vals); hasKey {
			if sysfsResyncDone(syncAction) {
				return true, ""
			}
			// consensus R2 回炉 Fix3：sync_completed 为 none/畸形时
			// syncCompletedPercent 返回空串，不能再拼 "同步进度: " 留下
			// 悬挂冒号——改用 resyncProgressDetail 统一生成文案，空百分比
			// 时退化为"同步中（<syncAction>）"。
			return false, resyncProgressDetail(syncCompletedPercent(vals["sync_completed"]), syncAction)
		}
	}

	// sysfs 不可用（目录缺失或 sync_action 缺键）：回退 mdstat parser 路径。
	// consensus P0-D：改走 parseMdstatBlocks——此前只看头行的下一行
	// (lines[j+1])，真实进度行通常在 blocks 行之后（j+2 及以后），会
	// 漏检，导致阵列明明还在重构却被误判"完成"，growFilesystem 在
	// 重构中跑会损坏数据。现在进度行在阵列块内任意缩进行位置都能
	// 命中。
	blocks := parseMdstatBlocks(mdstatText)
	if block, found := findBlockByMdName(blocks, mdName); found {
		if p, ok := firstProgress(block); ok && (p.Kind == "resync" || p.Kind == "recovery") {
			// mdstat 分支不动：沿用既有 "同步进度: " + percent 形态。
			return false, "同步进度: " + p.Percent
		}
	}
	return true, ""
}

// resyncProgressDetail 构造 waitForResync sysfs 分支的进度文案。
//
// consensus R2 回炉 Fix3（审查实锤）：sync_completed 为 "none" 或畸形值时
// syncCompletedPercent 返回空串，此前直接拼 "同步进度: "+percent 会留下
// 悬挂冒号（"同步进度: "）。percent 非空时沿用既有 "同步进度: "+percent
// 形态；percent 为空时改用 "同步中（"+syncAction+"）"，至少把 sync_action
// 本身（如 "resync"/"recover"）展示出来，不留下空文案。
func resyncProgressDetail(percent, syncAction string) string {
	if percent != "" {
		return "同步进度: " + percent
	}
	return "同步中（" + syncAction + "）"
}

// growFilesystem grows the filesystem on md device.
// 返回 ok=false 表示扩容失败，调用方不应再标记该步骤为完成（xfs_growfs 失败不许静默吞掉）。
// R3：error 事件改经 tracker.Error 发送——ctx 已取消/连接已断时不再尝试写入
// （tracker 内部会先查 ctx.Err()，命中则直接跳过发送）。
func growFilesystem(tracker *raidStepTracker, mdDev string) bool {
	if _, _, err := growFilesystemByDevice(mdDev); err != nil {
		tracker.Error("扩展文件系统", err.Error())
		return false
	}
	return true
}

// checkRAIDError checks error and sends SSE error event via tracker（R3：ctx
// 已取消/连接已断时 tracker.Error 不发送，调用方据此中止）。
func checkRAIDError(tracker *raidStepTracker, step, output string, err error) bool {
	if err != nil {
		tracker.Error(step, output+": "+err.Error())
		return true
	}
	return false
}

// parseMdDetailLevelAndDevices 从 `mdadm --detail` 输出中解析 RAID 级别（如 "raid1"）
// 和当前设备数。纯函数，供单测；handleRAIDExpandStream 与 mdArrayLevel 共用解析逻辑。
func parseMdDetailLevelAndDevices(detailOut string) (level string, devices int) {
	for _, line := range strings.Split(detailOut, "\n") {
		if strings.Contains(line, "Raid Level") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				level = strings.TrimSpace(parts[1])
			}
		}
		if strings.Contains(line, "Raid Devices") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &devices)
			}
		}
	}
	return level, devices
}

// mdArrayLevel 查询 md 设备当前 RAID 级别（如 "raid0"/"raid1"），查询失败或无法
// 解析时返回空串（调用方应把空串当"未知"处理，不强行放行也不强行拒绝——handleReplaceDisk
// 只在明确识别出 raid0 时才拒绝，避免误伤非 RAID 场景）。A2：handleReplaceDisk 换盘前
// 用它判断目标阵列是否是 raid0。
func mdArrayLevel(mdDev string) string {
	detailOut, err := common.SudoOutput("/usr/sbin/mdadm", "--detail", mdDev)
	if err != nil {
		return ""
	}
	level, _ := parseMdDetailLevelAndDevices(detailOut)
	return level
}
