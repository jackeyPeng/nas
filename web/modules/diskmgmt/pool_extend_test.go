package diskmgmt

import (
	"encoding/json"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════
// handlePoolExtendStream —— P1-P6（DESIGN.md §2.1）
// P7/P8（ctx 中断 / 中断后重试恢复）明确留给 R3，本轮不做。
// ═══════════════════════════════════════════════════

// findmntResponder 根据 findmnt 调用的参数区分三类调用：
//   - isSystemDisk：-n -o TARGET <device>（无 --source）
//   - growFSMountPoint：-n -o TARGET --source <dev>
//   - growFSFsType：-n -o FSTYPE --source <dev>
//
// mountByDevice 配置「--source <dev>」或裸设备命中时的挂载点；fstype 配置
// 文件系统类型（默认 "xfs"，走 xfs_growfs 分支，与 P1 的「growFS xfs 分支」
// 断言对齐）。
func findmntResponder(mountByDevice map[string]string, fstype string) func(args []string) (string, error) {
	if fstype == "" {
		fstype = "xfs"
	}
	return func(args []string) (string, error) {
		var dev string
		for i, a := range args {
			if a == "--source" && i+1 < len(args) {
				dev = args[i+1]
			}
		}
		if dev == "" && len(args) > 0 {
			// isSystemDisk 裸调用：最后一个参数是设备/分区路径
			dev = args[len(args)-1]
		}
		isFSType := false
		for _, a := range args {
			if a == "FSTYPE" {
				isFSType = true
			}
		}
		if isFSType {
			return fstype, nil
		}
		return mountByDevice[dev], nil
	}
}

// decodePoolEvents 把 parseSSE 返回的原始 JSON 行反序列化为 PoolExtendEvent 切片。
func decodePoolEvents(t *testing.T, raw []string) []PoolExtendEvent {
	t.Helper()
	events := make([]PoolExtendEvent, 0, len(raw))
	for _, r := range raw {
		var ev PoolExtendEvent
		if err := json.Unmarshal([]byte(r), &ev); err != nil {
			t.Fatalf("decode SSE event %q: %v", r, err)
		}
		events = append(events, ev)
	}
	return events
}

// P1: 全链路成功（wipefs→pvcreate→vgextend→lvextend→growFS xfs）
func TestHandlePoolExtendStream_P1_FullSuccess(t *testing.T) {
	withNoopSleep(t)
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt": {Fn: findmntResponder(map[string]string{"/dev/vg_nas/data": "/mnt/data"}, "xfs")},
		// isSystemDisk/isDiskInUse 相关命令均默认返回空（非系统盘、未被占用）
	})

	req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
		"device":  "/dev/sdx",
		"confirm": "yes",
	})
	handlePoolExtendStream(rec, req)

	events := decodePoolEvents(t, parseSSE(rec.Body.String()))

	wantSteps := []string{"清除磁盘签名", "初始化物理卷 (pvcreate)", "加入卷组 vg_nas", "扩展逻辑卷 (lvextend)", "扩展文件系统"}
	gotIndex := 0
	for _, step := range wantSteps {
		var running, done *PoolExtendEvent
		for i := range events {
			if events[i].Step == step && events[i].Status == "running" && running == nil {
				running = &events[i]
			}
			if events[i].Step == step && events[i].Status == "done" && done == nil {
				done = &events[i]
			}
		}
		if running == nil {
			t.Fatalf("missing running event for step %q", step)
		}
		if done == nil {
			t.Fatalf("missing done event for step %q (running→done 必须按步骤名逐字配对, A3b)", step)
		}
		gotIndex++
		if done.Index != gotIndex {
			t.Errorf("step %q done.Index = %d, want %d (Index 单调 1..5)", step, done.Index, gotIndex)
		}
		if done.Total != 5 {
			t.Errorf("step %q done.Total = %d, want 5", step, done.Total)
		}
	}

	last := events[len(events)-1]
	if last.Status != "complete" {
		t.Fatalf("末事件 Status = %q, want complete", last.Status)
	}
	if last.Index != 5 || last.Total != 5 {
		t.Errorf("complete 事件 Index/Total = %d/%d, want 5/5", last.Index, last.Total)
	}

	// 命令序列与参数精确匹配
	if !stub.wasCalledWith("/usr/sbin/wipefs", "-a", "/dev/sdx") {
		t.Error("wipefs -a /dev/sdx 未被调用")
	}
	if !stub.wasCalledWith("/usr/sbin/pvcreate", "-f", "/dev/sdx") {
		t.Error("pvcreate -f /dev/sdx 未被调用")
	}
	if !stub.wasCalledWith("/usr/sbin/vgextend", "vg_nas", "/dev/sdx") {
		t.Error("vgextend vg_nas /dev/sdx 未被调用")
	}
	if !stub.wasCalledWith("/usr/sbin/lvextend", "-l", "+100%FREE", "/dev/vg_nas/data") {
		t.Error("lvextend 的 +100 percent FREE 调用未命中 /dev/vg_nas/data")
	}
	// growFS xfs 分支：对挂载点而非设备执行 xfs_growfs
	if !stub.wasCalledWith("/usr/sbin/xfs_growfs", "/mnt/data") {
		t.Error("xfs_growfs /mnt/data 未被调用（growFS xfs 分支应使用挂载点而非设备路径）")
	}
}

// P2: 参数默认值（vg_name/lv_name 留空 → 落到 vg_nas/data 默认值）
func TestHandlePoolExtendStream_P2_DefaultParams(t *testing.T) {
	withNoopSleep(t)
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt": {Fn: findmntResponder(map[string]string{"/dev/vg_nas/data": "/mnt/data"}, "xfs")},
	})

	req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
		"device":  "/dev/sdy",
		"confirm": "yes",
		// vg_name / lv_name 留空
	})
	handlePoolExtendStream(rec, req)

	events := decodePoolEvents(t, parseSSE(rec.Body.String()))
	last := events[len(events)-1]
	if last.Status != "complete" {
		t.Fatalf("末事件 Status = %q, want complete; body=%s", last.Status, rec.Body.String())
	}
	if !stub.wasCalledWith("/usr/sbin/vgextend", "vg_nas", "/dev/sdy") {
		t.Error("vg_name 留空应默认落到 vg_nas")
	}
	if !stub.wasCalledWith("/usr/sbin/lvextend", "-l", "+100%FREE", "/dev/vg_nas/data") {
		t.Error("lv_name 留空应默认落到 data，lvextend 目标应为 /dev/vg_nas/data")
	}
}

// P3: 缺 device / 缺 confirm=yes —— error 事件且未发生任何 sudoExec（廉价拒绝不触碰磁盘）
func TestHandlePoolExtendStream_P3_MissingParams(t *testing.T) {
	t.Run("缺device", func(t *testing.T) {
		withNoopSleep(t)
		stub := withStubExec(t, map[string]cmdResponse{})
		req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
			"confirm": "yes",
		})
		handlePoolExtendStream(rec, req)

		events := decodePoolEvents(t, parseSSE(rec.Body.String()))
		if len(events) != 1 || events[0].Status != "error" {
			t.Fatalf("got events=%+v, want 单条 error 事件", events)
		}
		if len(stub.calls) != 0 {
			t.Errorf("缺 device 应廉价拒绝，不触碰磁盘；but calls=%v", stub.calledNames())
		}
	})

	t.Run("缺confirm", func(t *testing.T) {
		withNoopSleep(t)
		stub := withStubExec(t, map[string]cmdResponse{})
		req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
			"device": "/dev/sdx",
		})
		handlePoolExtendStream(rec, req)

		events := decodePoolEvents(t, parseSSE(rec.Body.String()))
		if len(events) != 1 || events[0].Status != "error" {
			t.Fatalf("got events=%+v, want 单条 error 事件", events)
		}
		if len(stub.calls) != 0 {
			t.Errorf("缺 confirm=yes 应廉价拒绝，不触碰磁盘；but calls=%v", stub.calledNames())
		}
	})
}

// P4: 安全检查拒绝（系统盘 / isDiskInUse 命中 PV）——error 事件，wipefs 未执行
func TestHandlePoolExtendStream_P4_SafetyRejection(t *testing.T) {
	t.Run("系统盘", func(t *testing.T) {
		withNoopSleep(t)
		stub := withStubExec(t, map[string]cmdResponse{
			"findmnt": {Fn: findmntResponder(map[string]string{"/dev/sdx": "/"}, "xfs")},
		})
		req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
			"device":  "/dev/sdx",
			"confirm": "yes",
		})
		handlePoolExtendStream(rec, req)

		events := decodePoolEvents(t, parseSSE(rec.Body.String()))
		last := events[len(events)-1]
		if last.Status != "error" || !strings.Contains(last.Detail, "系统盘") {
			t.Fatalf("got last=%+v, want error 含“系统盘”", last)
		}
		if stub.countCalls("/usr/sbin/wipefs") != 0 {
			t.Error("系统盘应被拒绝，wipefs 不应执行")
		}
	})

	t.Run("已在存储池中使用", func(t *testing.T) {
		withNoopSleep(t)
		// isDiskInUse 的 pvs 调用不在本轮 seam 路由范围内（brief 只路由 isSystemDisk
		// 的 findmnt 与 isDiskInUse 的 cat /proc/mdstat），改用同样走 execOutputFn
		// 的 lsblk FSTYPE 签名分支触发"已占用"判定（hasFstypeSignature）。
		stub := withStubExec(t, map[string]cmdResponse{
			"findmnt": {Fn: findmntResponder(map[string]string{}, "xfs")},
			"lsblk": {Fn: func(args []string) (string, error) {
				return "sdx xfs\n", nil
			}},
		})
		req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
			"device":  "/dev/sdx",
			"confirm": "yes",
		})
		handlePoolExtendStream(rec, req)

		events := decodePoolEvents(t, parseSSE(rec.Body.String()))
		last := events[len(events)-1]
		if last.Status != "error" || !strings.Contains(last.Detail, "已在存储池中使用") {
			t.Fatalf("got last=%+v, want error 含“已在存储池中使用”", last)
		}
		if stub.countCalls("/usr/sbin/wipefs") != 0 {
			t.Error("已占用磁盘应被拒绝（含数据盘不许隐式擦除），wipefs 不应执行")
		}
	})
}

// P5: 命令注入失败负向——vgextend 失败，error 事件 Step=「加入卷组 vg_nas」，
// lvextend/resize2fs 未被调用，已执行命令序列止于 vgextend（Fix 4 回归）。
func TestHandlePoolExtendStream_P5_VgextendFails(t *testing.T) {
	withNoopSleep(t)
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt": {Fn: findmntResponder(map[string]string{}, "xfs")},
		"/usr/sbin/vgextend": {
			Out: "  Volume group \"vg_nas\" not found",
			Err: errAny,
		},
	})

	req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
		"device":  "/dev/sdx",
		"confirm": "yes",
	})
	handlePoolExtendStream(rec, req)

	events := decodePoolEvents(t, parseSSE(rec.Body.String()))
	last := events[len(events)-1]
	if last.Status != "error" || last.Step != "加入卷组 vg_nas" {
		t.Fatalf("got last=%+v, want error Step=加入卷组 vg_nas", last)
	}
	if !strings.Contains(last.Detail, "Volume group") {
		t.Errorf("Detail 应含原始输出, got %q", last.Detail)
	}
	if !strings.Contains(last.Detail, errAny.Error()) {
		t.Errorf("Detail 应含错误本身, got %q", last.Detail)
	}
	if stub.countCalls("/usr/sbin/lvextend") != 0 {
		t.Error("vgextend 失败后 lvextend 不应被调用")
	}
	if stub.countCalls("/usr/sbin/resize2fs") != 0 {
		t.Error("vgextend 失败后 resize2fs 不应被调用")
	}
	// 已执行命令序列止于 vgextend：wipefs、pvcreate 之后只有 vgextend，没有更后面的命令
	names := stub.calledNames()
	lastCmdIdx := -1
	for i, n := range names {
		if n == "/usr/sbin/vgextend" {
			lastCmdIdx = i
		}
	}
	if lastCmdIdx == -1 {
		t.Fatal("vgextend 未被调用")
	}
	for _, n := range names[lastCmdIdx+1:] {
		if n == "/usr/sbin/lvextend" || n == "/usr/sbin/resize2fs" || n == "/usr/sbin/xfs_growfs" {
			t.Errorf("vgextend 之后不应再出现写路径命令，但发现 %q", n)
		}
	}
}

// P6: growFS 失败（findmnt 空输出=未挂载）——error 事件含“设备未挂载”，
// 末步不置 done、无 complete 事件（A1 同族语义：扩 FS 失败不许静默吞）。
func TestHandlePoolExtendStream_P6_GrowFSFails(t *testing.T) {
	withNoopSleep(t)
	withStubExec(t, map[string]cmdResponse{
		// findmnt 对所有设备都返回空输出 → growFSMountPoint 报"未挂载"
		"findmnt": {Fn: findmntResponder(map[string]string{}, "xfs")},
	})

	req, rec := withSSE("GET", "/api/disk/pool/extend-stream", map[string]string{
		"device":  "/dev/sdx",
		"confirm": "yes",
	})
	handlePoolExtendStream(rec, req)

	events := decodePoolEvents(t, parseSSE(rec.Body.String()))
	last := events[len(events)-1]
	if last.Status != "error" || !strings.Contains(last.Detail, "设备未挂载") {
		t.Fatalf("got last=%+v, want error 含“设备未挂载”", last)
	}
	for _, ev := range events {
		if ev.Step == "扩展文件系统" && ev.Status == "done" {
			t.Error("扩展文件系统失败后不应出现该步骤的 done 事件")
		}
		if ev.Status == "complete" {
			t.Error("扩展文件系统失败后不应出现 complete 事件")
		}
	}
}

// errAny 是 P5/R3 等用例共用的占位 error，仅用于让 stubExec 返回非 nil error；
// 文案不参与断言（断言靠 Detail 里的 Out 原始输出文本）。
var errAny = &stubError{"stub command failed"}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }
