package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nas-panel/common"
	"nas-panel/modules/system"
)

// ProgressEvent is sent to frontend via SSE
type ProgressEvent struct {
	Step   string `json:"step"`
	Status string `json:"status"` // running, done, error
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Detail string `json:"detail,omitempty"`
}

// sendProgress writes an SSE event
func sendProgress(w http.ResponseWriter, ev ProgressEvent) {
	data, _ := json.Marshal(ev)
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

// handleWizardSetupStream does storage setup with real-time progress
func handleWizardSetupStream(w http.ResponseWriter, r *http.Request) {
	common.LockDiskOp()
	defer common.UnlockDiskOp()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = r.FormValue("mode")
	}
	confirm := r.URL.Query().Get("confirm")
	if confirm == "" {
		confirm = r.FormValue("confirm")
	}

	if confirm != "yes" {
		sendProgress(w, ProgressEvent{Step: "需要确认", Status: "error", Detail: "请加 confirm=yes"})
		return
	}

	// B2：separate（独立多池）创建模式已下线——产品定位收敛为专用整机、
	// 一机一主池，不再在创建主流程里支持这条路径。拦截前移到磁盘扫描/
	// "没有可用的空闲磁盘"检查之前：无空闲盘机器上（48 实测）此前用户
	// 看到的是"没有空闲磁盘"而非"separate 已下线"，文案误导；现在
	// mode=separate 恒定先命中这条下线文案，不再依赖磁盘状态。
	if mode == "separate" {
		sendProgress(w, ProgressEvent{Step: "模式检查", Status: "error", Detail: "separate 多池模式已下线，单池模型请使用 merge/RAID"})
		return
	}

	common.LogAuditRequest(r, "STORAGE", "存储向导配置(流式)", "mode="+mode, "pending")

	nasUser := common.GetNASUser()
	if nasUser == "" {
		nasUser = "root"
	}

	// Get unused disks
	disks := getDiskStatus()
	var unusedDevs []string
	for _, d := range disks {
		if d.Name == "sr0" || d.Name == "zram0" || strings.HasPrefix(d.Name, "loop") {
			continue
		}
		if isSystemDisk(d.Device) {
			continue
		}
		if d.Type == "unused" {
			unusedDevs = append(unusedDevs, d.Device)
		}
	}

	// 前端向导允许选盘：带 disks 参数（逗号分隔设备路径）时，只对选中的空闲盘操作。
	// Fix 2：filtered 为空不得静默回退到全部空闲盘 — 那会把操作范围从"用户选中的盘"
	// 静默扩大成"全部空闲盘"，破坏用户预期的操作边界。
	sel := strings.TrimSpace(r.URL.Query().Get("disks"))
	filtered, hasSel := filterSelectedDisks(unusedDevs, sel)
	if hasSel {
		if len(filtered) == 0 {
			sendProgress(w, ProgressEvent{Step: "检查磁盘", Status: "error", Detail: "所选磁盘已不是空闲盘，请刷新页面重试"})
			return
		}
		unusedDevs = filtered
	}

	if len(unusedDevs) == 0 {
		sendProgress(w, ProgressEvent{Step: "检查磁盘", Status: "error", Detail: "没有可用的空闲磁盘"})
		return
	}

	// Calculate total steps based on mode
	totalSteps := getModeStepCount(mode, len(unusedDevs))
	if totalSteps == 0 {
		sendProgress(w, ProgressEvent{Step: "模式检查", Status: "error", Detail: "未知模式: " + mode})
		return
	}

	// Validate mode requirements
	if err := validateMode(mode, len(unusedDevs)); err != "" {
		sendProgress(w, ProgressEvent{Step: "模式检查", Status: "error", Detail: err})
		return
	}

	step := 0

	stepDone := func(name string) {
		step++
		sendProgress(w, ProgressEvent{Step: name, Status: "done", Index: step, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	stepRunning := func(name string) {
		sendProgress(w, ProgressEvent{Step: name, Status: "running", Index: step + 1, Total: totalSteps})
		time.Sleep(100 * time.Millisecond)
	}
	// Fix C（E7审查回炉 Minor1）：setupRAIDStream 内部补发的 SSE running 事件
	// （第6步 initramfs 警告）之前没有 Index，序列化出 index:0，与同流其他
	// 事件形态不齐。stepRunning/stepDone 都是闭包，捕获的 step 变量不对外
	// 暴露——补一个同款闭包 stepIndex，让 setup*Stream 系列函数能取到"当前
	// 正在跑的那一步"的 Index（即 step+1，与 stepRunning 用的值一致）。
	stepIndex := func() int { return step + 1 }

	// 固定单池挂载 /data/nas1（新模型：整个系统一个存储池）。
	// 不再自动挑选下一个空闲挂载点——那会把池挂到 nas2/nas3，违背单池设计。
	// separate 模式已在上面被拦截，这里不再需要按模式分叉判断。
	mountPoint := "/data/nas1"
	if mntOut, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", mountPoint); strings.TrimSpace(mntOut) != "" {
		sendProgress(w, ProgressEvent{Step: "检查存储池", Status: "error", Detail: "存储池 /data/nas1 已存在，无需重复创建"})
		return
	}

	// Fix 3：setup*Stream 现在返回 error —— 非 nil 时内部已经把 error 事件发往 SSE
	// （checkExecError/mountAndWriteFstab 内部做的），这里只负责中止，不重复发错误事件，
	// 也绝不能再往下走到发 "完成" 事件（此前的 bug：无论内部成功与否都无条件报完成）。
	var setupErr error
	switch mode {
	case "single":
		// LVM single disk — for future expansion
		dev := unusedDevs[0]
		setupErr = setupLVMSingleStream(w, dev, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "merge":
		// LVM merge — combine all disks
		setupErr = setupLVMMergeStream(w, unusedDevs, mountPoint, nasUser, stepRunning, stepDone, totalSteps)

	case "raid0":
		// RAID0 stripe — all disks, no redundancy
		setupErr = setupRAIDStream(w, unusedDevs, 0, mountPoint, nasUser, stepRunning, stepDone, stepIndex, totalSteps)

	case "raid1":
		// RAID1 mirror — 2 disks
		setupErr = setupRAIDStream(w, unusedDevs[:2], 1, mountPoint, nasUser, stepRunning, stepDone, stepIndex, totalSteps)

	case "raid5":
		// RAID5 — 3+ disks
		setupErr = setupRAIDStream(w, unusedDevs, 5, mountPoint, nasUser, stepRunning, stepDone, stepIndex, totalSteps)

	case "raid6":
		// RAID6 — 4+ disks
		setupErr = setupRAIDStream(w, unusedDevs, 6, mountPoint, nasUser, stepRunning, stepDone, stepIndex, totalSteps)
	}
	if setupErr != nil {
		return
	}

	// 完成文案按模式报告实际用盘数（raid1 只取前 2 块、single 只取 1 块，
	// 旧文案统一报 len(unusedDevs) 会在 4 盘机做 raid1 时误报"共 4 块"）
	usedCount := len(unusedDevs)
	detail := ""
	switch mode {
	case "single":
		usedCount = 1
	case "raid1":
		usedCount = 2
	}
	if usedCount < len(unusedDevs) {
		detail = fmt.Sprintf("存储配置完成，%d 块磁盘加入存储池，其余 %d 块空闲盘未使用", usedCount, len(unusedDevs)-usedCount)
	} else {
		detail = fmt.Sprintf("存储配置完成，共 %d 块磁盘", usedCount)
	}
	sendProgress(w, ProgressEvent{
		Step:   "完成",
		Status: "complete",
		Index:  totalSteps,
		Total:  totalSteps,
		Detail: detail,
	})
	system.ScheduleRegistryRefresh()
}

// filterSelectedDisks 计算 unused 与用户选盘参数 sel（逗号分隔设备路径）的交集（Fix 2）。
// sel 为空串（未传该参数）时 hasSel=false，调用方应保留全部 unused，行为不变。
// sel 非空但交集为空时返回 filtered=nil, hasSel=true —— 调用方必须据此报错并中止，
// 不能回退到全部 unused（那会把操作范围从"用户选中的盘"静默扩大）。
func filterSelectedDisks(unused []string, sel string) (filtered []string, hasSel bool) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return nil, false
	}
	selected := map[string]bool{}
	for _, dev := range strings.Split(sel, ",") {
		if dev = strings.TrimSpace(dev); dev != "" {
			selected[dev] = true
		}
	}
	for _, dev := range unused {
		if selected[dev] {
			filtered = append(filtered, dev)
		}
	}
	return filtered, true
}

// validateMode checks if the mode is valid for the given disk count
func validateMode(mode string, diskCount int) string {
	switch mode {
	case "single":
		if diskCount < 1 {
			return "至少需要 1 块磁盘"
		}
	case "merge", "raid0":
		if diskCount < 2 {
			return "至少需要 2 块磁盘"
		}
	case "raid1":
		if diskCount < 2 {
			return "RAID1 至少需要 2 块磁盘"
		}
	case "raid5":
		if diskCount < 3 {
			return "RAID5 至少需要 3 块磁盘"
		}
	case "raid6":
		if diskCount < 4 {
			return "RAID6 至少需要 4 块磁盘"
		}
	default:
		return "未知模式: " + mode
	}
	return ""
}

// getModeStepCount calculates total steps for a given mode
func getModeStepCount(mode string, diskCount int) int {
	switch mode {
	case "single":
		return 6 // wipe, pvcreate, vgcreate, lvcreate, format, mount+fstab
	case "merge":
		return 5 + diskCount // per-disk wipe+pvcreate, vgcreate, lvcreate, format, mount
	case "raid0", "raid1", "raid5", "raid6":
		return 7 // wipe, create raid, wait, format, mount, fstab, samba
	default:
		return 0
	}
}

// stepError sends an error progress event and returns true if should abort
func stepError(w http.ResponseWriter, step string, detail string) {
	sendProgress(w, ProgressEvent{Step: step, Status: "error", Detail: detail})
}

// checkExecError checks if a SudoExec result has an error, sends SSE error if so
// returns true if should abort
func checkExecError(w http.ResponseWriter, stepName, output string, err error) bool {
	if err != nil {
		stepError(w, stepName, output+": "+err.Error())
		return true
	}
	return false
}

// mountAndWriteFstab 挂载后读 UUID 写 fstab 的共用逻辑（S1-R3c：blkid 必须带 sudo
// 才能读超级块，writeFstab 失败必须显式报给 SSE 并中止——不能像此前那样吞错后
// 仍对外报"挂载并配置成功"）。devPath 是分区/LV/md 设备路径。返回 true 表示应中止。
func mountAndWriteFstab(w http.ResponseWriter, stepName, devPath, mountPoint string) bool {
	uuidOut, _ := common.SudoOutput("blkid", "-s", "UUID", "-o", "value", devPath)
	if err := writeFstab(strings.TrimSpace(uuidOut), mountPoint, "xfs"); err != nil {
		stepError(w, stepName, "写入 fstab 失败: "+err.Error())
		return true
	}
	return false
}

// setupLVMSingleStream: LVM on single disk for future expansion.
// Fix 3：返回 error 让外层 handleWizardSetupStream 知道向导是否真正跑完 —— 之前
// 签名是 void，内部出错只发 SSE error 事件后 return，外层无条件在 switch 之后发
// "完成"，导致失败的向导仍对用户报成功。每个 abort 分支改为 return 非 nil error
// （错误信息已由 checkExecError/mountAndWriteFstab 发往 SSE，这里只是让外层知道
// 不该再发 complete，不重复发错误事件）。
func setupLVMSingleStream(w http.ResponseWriter, dev, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) error {

	stepRunning("清除磁盘签名")
	out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
	if checkExecError(w, "清除磁盘签名", out, err) {
		return fmt.Errorf("清除磁盘签名: %s: %w", out, err)
	}
	stepDone("清除磁盘签名")

	stepRunning("创建物理卷 (pvcreate)")
	out, err = common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
	if checkExecError(w, "创建物理卷", out, err) {
		return fmt.Errorf("创建物理卷: %s: %w", out, err)
	}
	stepDone("创建物理卷")

	stepRunning("创建卷组 (vgcreate)")
	vgName := "vg_nas"
	out, err = common.SudoExec("/usr/sbin/vgcreate", "-f", vgName, dev)
	if checkExecError(w, "创建卷组", out, err) {
		return fmt.Errorf("创建卷组: %s: %w", out, err)
	}
	stepDone("创建卷组 vg_nas")

	stepRunning("创建逻辑卷 (lvcreate)")
	out, err = common.SudoExec("/usr/sbin/lvcreate", "-y", "-l", "100%FREE", "-n", "data", vgName)
	if checkExecError(w, "创建逻辑卷", out, err) {
		return fmt.Errorf("创建逻辑卷: %s: %w", out, err)
	}
	lvPath := "/dev/" + vgName + "/data"
	stepDone("创建逻辑卷 data")

	stepRunning("格式化 (xfs)")
	out, err = common.SudoExec("mkfs.xfs", "-f", lvPath)
	if checkExecError(w, "格式化", out, err) {
		return fmt.Errorf("格式化: %s: %w", out, err)
	}
	stepDone("格式化 xfs")

	stepRunning("挂载并配置")
	// Item 1 修复：mkdir 失败（权限/只读父目录/路径冲突等）此前被忽略，紧接着
	// 的 mount 会打在一个可能不存在或状态异常的目录上——mount 本身的 error
	// 虽然会被下面 checkExecError 捕获并中止，但 mkdir 失败的真实原因会丢失，
	// 且不排除 mount 在某些边界情况下"看似成功"（如 mountPoint 恰好已存在）
	// 掩盖掉 mkdir 曾经失败的事实。现在显式检查并在失败时中止，不再往下执行
	// mount/chown/EnsurePoolStructure。
	out, err = common.SudoExec("mkdir", "-p", mountPoint)
	if checkExecError(w, "创建挂载点目录", out, err) {
		return fmt.Errorf("创建挂载点目录: %s: %w", out, err)
	}
	// prjquota：与 writeFstab 的 defaults,prjquota 一致，建池后配额立即可用
	out, err = common.SudoExec("mount", "-o", "prjquota", lvPath, mountPoint)
	if checkExecError(w, "挂载", out, err) {
		return fmt.Errorf("挂载: %s: %w", out, err)
	}
	if mountAndWriteFstab(w, "挂载并配置", lvPath, mountPoint) {
		return fmt.Errorf("挂载并配置: 写入 fstab 失败")
	}
	// Item 1 修复：移除此前的 `chown -R nasUser mountPoint`——这是对整个挂载点
	// 根目录的递归 chown，范围过大（万一 mountPoint 下已有非预期内容，会被
	// 一起改写属主）且是冗余操作：下面的 EnsurePoolStructure 已经对它创建的
	// public/ 和 home/ 子目录分别执行精确的（非递归扩散到 mountPoint 本身的）
	// chown，mountPoint 根目录本身不需要属于 nasUser（内容都在其受控子目录里）。
	if err := EnsurePoolStructure(mountPoint, nasUser); err != nil {
		stepError(w, "挂载并配置", err.Error())
		return fmt.Errorf("配置池目录结构: %w", err)
	}
	stepDone("挂载并配置 Samba 共享")
	return nil
}

// setupLVMMergeStream: LVM merge multiple disks（Fix 3：签名改为返回 error，见
// setupLVMSingleStream 顶部注释说明动机）。
func setupLVMMergeStream(w http.ResponseWriter, devs []string, mountPoint, nasUser string,
	stepRunning, stepDone func(string), totalSteps int) error {

	// Per-disk: wipe + pvcreate
	for i, dev := range devs {
		stepRunning(fmt.Sprintf("清除并初始化磁盘 %d/%d", i+1, len(devs)))
		out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		if checkExecError(w, fmt.Sprintf("清除磁盘 %d", i+1), out, err) {
			return fmt.Errorf("清除磁盘 %d: %s: %w", i+1, out, err)
		}
		out, err = common.SudoExec("/usr/sbin/pvcreate", "-f", dev)
		if checkExecError(w, fmt.Sprintf("初始化磁盘 %d", i+1), out, err) {
			return fmt.Errorf("初始化磁盘 %d: %s: %w", i+1, out, err)
		}
		stepDone(fmt.Sprintf("初始化磁盘 %d/%d", i+1, len(devs)))
	}

	stepRunning("创建卷组 (vgcreate)")
	vgName := "vg_nas"
	vgArgs := append([]string{"-f", vgName}, devs...)
	out, err := common.SudoExec("/usr/sbin/vgcreate", vgArgs...)
	if checkExecError(w, "创建卷组", out, err) {
		return fmt.Errorf("创建卷组: %s: %w", out, err)
	}
	stepDone("创建卷组 vg_nas")

	stepRunning("创建逻辑卷 (lvcreate)")
	out, err = common.SudoExec("/usr/sbin/lvcreate", "-y", "-l", "100%FREE", "-n", "data", vgName)
	if checkExecError(w, "创建逻辑卷", out, err) {
		return fmt.Errorf("创建逻辑卷: %s: %w", out, err)
	}
	lvPath := "/dev/" + vgName + "/data"
	stepDone("创建逻辑卷 data")

	stepRunning("格式化 (xfs)")
	out, err = common.SudoExec("mkfs.xfs", "-f", lvPath)
	if checkExecError(w, "格式化", out, err) {
		return fmt.Errorf("格式化: %s: %w", out, err)
	}
	stepDone("格式化 xfs")

	stepRunning("挂载并配置")
	// Item 1 修复：mkdir 失败检查，理由同 setupLVMSingleStream 顶部注释。
	out, err = common.SudoExec("mkdir", "-p", mountPoint)
	if checkExecError(w, "创建挂载点目录", out, err) {
		return fmt.Errorf("创建挂载点目录: %s: %w", out, err)
	}
	// prjquota：与 writeFstab 的 defaults,prjquota 一致，建池后配额立即可用
	out, err = common.SudoExec("mount", "-o", "prjquota", lvPath, mountPoint)
	if checkExecError(w, "挂载", out, err) {
		return fmt.Errorf("挂载: %s: %w", out, err)
	}
	if mountAndWriteFstab(w, "挂载并配置", lvPath, mountPoint) {
		return fmt.Errorf("挂载并配置: 写入 fstab 失败")
	}
	// Item 1 修复：移除 `chown -R mountPoint`，理由同 setupLVMSingleStream 顶部注释。
	if err := EnsurePoolStructure(mountPoint, nasUser); err != nil {
		stepError(w, "挂载并配置", err.Error())
		return fmt.Errorf("配置池目录结构: %w", err)
	}
	stepDone("挂载并配置 Samba 共享")
	return nil
}

// setupRAIDStream: RAID0/1/5/6（Fix 3：签名改为返回 error，见 setupLVMSingleStream
// 顶部注释说明动机）。
func setupRAIDStream(w http.ResponseWriter, devs []string, level int, mountPoint, nasUser string,
	stepRunning, stepDone func(string), stepIndex func() int, totalSteps int) error {

	// 1. Wipe all disks
	stepRunning(fmt.Sprintf("清除 %d 块磁盘签名", len(devs)))
	for _, dev := range devs {
		out, err := common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		if checkExecError(w, "清除磁盘签名", out, err) {
			return fmt.Errorf("清除磁盘签名: %s: %w", out, err)
		}
	}
	stepDone("清除磁盘签名")

	// 2. Create RAID array — find next available /dev/mdN
	stepRunning(fmt.Sprintf("创建 %s (RAID%d)", raidLevelName(level), level))
	mdDev := getNextMDDevice()
	args := []string{"--create", mdDev, "--level=" + fmt.Sprintf("%d", level), "--raid-devices=" + fmt.Sprintf("%d", len(devs)), "--run"}
	args = append(args, devs...)
	out, err := common.SudoExec("/usr/sbin/mdadm", args...)
	if checkExecError(w, "创建RAID", out, err) {
		return fmt.Errorf("创建RAID: %s: %w", out, err)
	}
	stepDone(fmt.Sprintf("创建 RAID%d 阵列 (%s)", level, mdDev))

	// 3. Wait for md device
	stepRunning("等待阵列就绪")
	time.Sleep(2 * time.Second)
	stepDone("阵列就绪")

	// 4. Format
	stepRunning("格式化 (xfs)")
	out, err = common.SudoExec("mkfs.xfs", "-f", mdDev)
	if checkExecError(w, "格式化", out, err) {
		return fmt.Errorf("格式化: %s: %w", out, err)
	}
	stepDone("格式化 xfs")

	// 5. Mount + fstab
	stepRunning("挂载并配置")
	// Item 1 修复：mkdir 失败检查，理由同 setupLVMSingleStream 顶部注释。
	out, err = common.SudoExec("mkdir", "-p", mountPoint)
	if checkExecError(w, "创建挂载点目录", out, err) {
		return fmt.Errorf("创建挂载点目录: %s: %w", out, err)
	}
	out, err = common.SudoExec("mount", "-o", "prjquota", mdDev, mountPoint)
	if checkExecError(w, "挂载", out, err) {
		return fmt.Errorf("挂载: %s: %w", out, err)
	}
	if mountAndWriteFstab(w, "挂载并配置", mdDev, mountPoint) {
		return fmt.Errorf("挂载并配置: 写入 fstab 失败")
	}
	// Item 1 修复：移除 `chown -R mountPoint`，理由同 setupLVMSingleStream 顶部
	// 注释——下面第 7 步 EnsurePoolStructure 已对其创建的子目录做精确 chown。
	stepDone("挂载并写入 fstab")

	// 6. Save RAID config —— E7：改调 persistRAIDConfig，灭掉此前 scan/cat
	// 双吞错的假成功（scan 失败时 mdadmConf 为空仍 SafeWriteFile+stepDone
	// "成功"），并补上 UUID 去重替换、写后验证、update-initramfs（真机
	// 实锤：没有这一步，重启后阵列按旧 initramfs 装配成 md127 而不是
	// conf 里写的设备名）。
	stepRunning("保存 RAID 配置")
	warning, persistErr := persistRAIDConfig(nil, mdDev)
	if persistErr != nil {
		stepError(w, "保存 RAID 配置", persistErr.Error())
		return fmt.Errorf("保存 RAID 配置: %w", persistErr)
	}
	if warning != "" {
		// Fix C（E7审查回炉 Minor1）：此前缺 Index，序列化出 index:0，与同流
		// 其他事件（如 handleWizardResetStream 里 Index: step+1 的形态）不齐，
		// 用 stepIndex() 取与 stepRunning 一致的"当前步"索引。
		sendProgress(w, ProgressEvent{Step: "保存 RAID 配置", Status: "running", Index: stepIndex(), Total: totalSteps, Detail: warning})
	}
	stepDone("保存 RAID 配置")

	// 7. Add Samba share
	// Item 1 修复：EnsurePoolStructure 的 error 此前被丢弃——public/home 目录
	// 创建失败、chown 失败或 SyncAllConfigs 失败都会被 stepDone 无条件报成
	// "配置 Samba 共享"成功，用户看不出整个池其实没有可用共享。
	stepRunning("配置 Samba 共享")
	if err := EnsurePoolStructure(mountPoint, nasUser); err != nil {
		stepError(w, "配置 Samba 共享", err.Error())
		return fmt.Errorf("配置池目录结构: %w", err)
	}
	stepDone("配置 Samba 共享")
	return nil
}

// raidLevelName returns a friendly name for RAID level
func raidLevelName(level int) string {
	switch level {
	case 0:
		return "条带"
	case 1:
		return "镜像"
	default:
		return fmt.Sprintf("RAID%d", level)
	}
}

// getNextMDDevice finds the next available /dev/mdN device
func getNextMDDevice() string {
	for i := 0; i <= 127; i++ {
		dev := fmt.Sprintf("/dev/md%d", i)
		if _, err := common.ExecOutput("ls", dev); err != nil {
			return dev
		}
	}
	return "/dev/md127"
}

// handleWizardResetStream: fail-closed stub. The reset feature is
// temporarily disabled because the current implementation cannot prove
// that the devices it would touch belong exclusively to this panel's
// managed pool (see resetDisabledMessage in reset.go). This handler sends
// a single SSE error event and returns, before touching confirm/
// confirm_token, before any sudoOutputFn/sudoExecFn call, and before any
// filesystem/LVM/RAID/disk operation.
func handleWizardResetStream(w http.ResponseWriter, r *http.Request) {
	common.LockDiskOp()
	defer common.UnlockDiskOp()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// SSE 由前端 GET 触发，全局中间件不审计 GET——这里显式补记
	common.LogAuditRequest(r, "STORAGE", "存储重置(流式)", "重置功能已禁用，请求被拒绝", "rejected")

	sendProgress(w, ProgressEvent{Step: "重置功能已禁用", Status: "error", Detail: resetDisabledMessage})
}
