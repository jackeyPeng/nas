package diskmgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════════
// handleRAIDExpandStream —— R1-R5（DESIGN.md §2.2），R8（本轮 R3 新增）。
// R6/R7（waitForResyncCore 抽取相关）明确留给 R4，本轮不做。
// ═══════════════════════════════════════════════════

// withCancelableRAIDSSE 与 withSSE 等价，但返回的 *http.Request 携带一个可
// 取消的 context.Context 及其 cancel 函数，供 R8 的 ctx 取消子场景使用。
func withCancelableRAIDSSE(method, path string, query map[string]string) (*http.Request, *httptest.ResponseRecorder, context.CancelFunc) {
	u := &url.URL{Path: path}
	q := url.Values{}
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	req := httptest.NewRequest(method, u.String(), nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	return req, rec, cancel
}

// decodeRAIDEvents 把 parseSSE 返回的原始 JSON 行反序列化为 RAIDExpandEvent 切片。
func decodeRAIDEvents(t *testing.T, raw []string) []RAIDExpandEvent {
	t.Helper()
	events := make([]RAIDExpandEvent, 0, len(raw))
	for _, r := range raw {
		var ev RAIDExpandEvent
		if err := json.Unmarshal([]byte(r), &ev); err != nil {
			t.Fatalf("decode SSE event %q: %v", r, err)
		}
		events = append(events, ev)
	}
	return events
}

// withFakeSysfsMD 在 t.TempDir() 下构造 <root>/<mdName>/md/ 目录，写入 vals 里的标量
// 文件及 members 里的 rd*/state 文件，并把 sysfsRoot 指向 root，t.Cleanup 还原。
// vals/members 为 nil 时只建目录本身（对应"sysfs 不可用"场景可以不调用本 helper）。
func withFakeSysfsMD(t *testing.T, mdName string, vals, members map[string]string) {
	t.Helper()
	root := t.TempDir()
	mdDir := filepath.Join(root, mdName, "md")
	if err := os.MkdirAll(mdDir, 0o755); err != nil {
		t.Fatalf("mkdir fake sysfs md dir: %v", err)
	}
	for k, v := range vals {
		if err := os.WriteFile(filepath.Join(mdDir, k), []byte(v), 0o644); err != nil {
			t.Fatalf("write fake sysfs file %s: %v", k, err)
		}
	}
	for rd, state := range members {
		rdDir := filepath.Join(mdDir, rd)
		if err := os.MkdirAll(rdDir, 0o755); err != nil {
			t.Fatalf("mkdir fake sysfs member dir %s: %v", rd, err)
		}
		if err := os.WriteFile(filepath.Join(rdDir, "state"), []byte(state), 0o644); err != nil {
			t.Fatalf("write fake sysfs member state %s: %v", rd, err)
		}
	}
	orig := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = orig })
}

// withFakeMdDev 在 t.TempDir() 下创建一个空文件作为 mdDev 路径，让
// handleRAIDExpandStream 的 os.Stat(mdDev) 存在性检查通过；返回该文件的绝对路径。
func withFakeMdDev(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "md0")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatalf("create fake md device file: %v", err)
	}
	return p
}

// raid1Detail 构造 `mdadm --detail` 对 raid1、currentDevs 盘的输出（不含目标新盘字符串，
// 用于 R1/R2 等"目标盘不在阵列中"的默认场景）。
func raid1Detail(currentDevs int) string {
	return fmt.Sprintf("        Raid Level : raid1\n      Raid Devices : %d\n", currentDevs)
}

func raid5Detail(currentDevs int) string {
	return fmt.Sprintf("        Raid Level : raid5\n      Raid Devices : %d\n", currentDevs)
}

// R1: RAID1 全链路成功——wipe→add→grow→resync(sysfs clean)→growFS(xfs)→complete，
// Detail 提示"容量不变（镜像）"且显示 N→N+1 盘。
func TestHandleRAIDExpandStream_R1_RAID1FullSuccess(t *testing.T) {
	withNoopSleep(t)
	mdDev := withFakeMdDev(t)
	// waitForResync 内部用 strings.TrimPrefix(mdDev, "/dev/") 得到 mdName；mdDev 是
	// TempDir 下的绝对路径，不带 "/dev/" 前缀，故 TrimPrefix 不生效、mdName 等于
	// mdDev 的 basename 所在的完整路径本身（readRAIDSysfsValues 直接拼
	// sysfsRoot/mdName/md）。用 mdDev 本身作为 fixture 目录名即可命中。
	withFakeSysfsMD(t, mdDev, cleanRAID1SysfsVals(), cleanRAID1SysfsMembers())

	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
			if len(args) > 0 && args[0] == "--detail" {
				return raid1Detail(2), nil
			}
			return "", nil
		}},
		"findmnt": {Fn: findmntResponder(map[string]string{mdDev: "/mnt/data"}, "xfs")},
	})
	origGetDiskStatus := getDiskStatusFn
	getDiskStatusFn = func() []DiskStatus {
		return []DiskStatus{{Device: "/dev/sdz", Type: "unused"}}
	}
	t.Cleanup(func() { getDiskStatusFn = origGetDiskStatus })

	req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
		"md_device": mdDev,
		"device":    "/dev/sdz",
		"confirm":   "yes",
	})
	handleRAIDExpandStream(rec, req)

	events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
	if len(events) == 0 {
		t.Fatalf("no SSE events; body=%s", rec.Body.String())
	}

	wantSteps := []string{"清除新磁盘签名"}
	for _, step := range wantSteps {
		var running, done *RAIDExpandEvent
		for i := range events {
			if events[i].Step == step && events[i].Status == "running" && running == nil {
				running = &events[i]
			}
			if events[i].Step == step && events[i].Status == "done" && done == nil {
				done = &events[i]
			}
		}
		if running == nil || done == nil {
			t.Fatalf("missing running/done pair for step %q; events=%+v", step, events)
		}
	}

	// resync 完成事件必须在 growFS running 事件之前
	resyncDoneIdx, growRunningIdx := -1, -1
	for i, ev := range events {
		if ev.Step == "数据同步完成" && ev.Status == "done" && resyncDoneIdx == -1 {
			resyncDoneIdx = i
		}
		if ev.Step == "扩展文件系统" && ev.Status == "running" && growRunningIdx == -1 {
			growRunningIdx = i
		}
	}
	if resyncDoneIdx == -1 {
		t.Fatalf("missing 数据同步完成 done event; events=%+v", events)
	}
	if growRunningIdx == -1 || growRunningIdx < resyncDoneIdx {
		t.Fatalf("扩展文件系统 running 事件必须在数据同步完成之后; resyncDoneIdx=%d growRunningIdx=%d", resyncDoneIdx, growRunningIdx)
	}

	last := events[len(events)-1]
	if last.Status != "complete" || last.Index != 5 || last.Total != 5 {
		t.Fatalf("got last=%+v, want complete 5/5", last)
	}
	if !strings.Contains(last.Detail, "容量不变（镜像）") {
		t.Errorf("complete Detail 应含“容量不变（镜像）”, got %q", last.Detail)
	}
	if !strings.Contains(last.Detail, "2") || !strings.Contains(last.Detail, "3") {
		t.Errorf("complete Detail 应显示 2→3 盘, got %q", last.Detail)
	}

	if !stub.wasCalledWith("/usr/sbin/wipefs", "-a", "/dev/sdz") {
		t.Error("wipefs -a /dev/sdz 未被调用")
	}
	if !stub.wasCalledWith("/usr/sbin/mdadm", mdDev, "--add", "/dev/sdz") {
		t.Error("mdadm <md> --add /dev/sdz 未被调用")
	}
	if !stub.wasCalledWith("/usr/sbin/mdadm", "--grow", mdDev, "--raid-devices=3") {
		t.Error("mdadm --grow <md> --raid-devices=3 未被调用")
	}
	if !stub.wasCalledWith("/usr/sbin/xfs_growfs", "/mnt/data") {
		t.Error("xfs_growfs /mnt/data 未被调用（growFilesystem 应作用于挂载点）")
	}
}

// R2: RAID5 路径——add+grow(reshape 启动)后立即返回 reshaping 状态，不进入
// resync 轮询（sleepFn 不应记录任何 2s 调用），不调用 growFS，Detail 提示需
// 手动扩展文件系统。
func TestHandleRAIDExpandStream_R2_RAID5ReshapeOnly(t *testing.T) {
	sleepCalls := withNoopSleep(t)
	mdDev := withFakeMdDev(t)

	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
			if len(args) > 0 && args[0] == "--detail" {
				return raid5Detail(3), nil
			}
			return "", nil
		}},
	})
	origGetDiskStatus := getDiskStatusFn
	getDiskStatusFn = func() []DiskStatus {
		return []DiskStatus{{Device: "/dev/sdz", Type: "unused"}}
	}
	t.Cleanup(func() { getDiskStatusFn = origGetDiskStatus })

	req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
		"md_device": mdDev,
		"device":    "/dev/sdz",
		"confirm":   "yes",
	})
	handleRAIDExpandStream(rec, req)

	events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
	last := events[len(events)-1]
	if last.Status != "reshaping" {
		t.Fatalf("got last.Status=%q, want reshaping; events=%+v", last.Status, events)
	}
	if last.Index != 4 || last.Total != 4 {
		t.Errorf("RAID5 totalSteps 应为 4, got Index/Total=%d/%d", last.Index, last.Total)
	}
	if !strings.Contains(last.Detail, "手动扩展文件系统") {
		t.Errorf("Detail 应提示需手动扩展文件系统, got %q", last.Detail)
	}
	if !strings.Contains(last.Detail, "3") || !strings.Contains(last.Detail, "4") {
		t.Errorf("Detail 应显示 3→4 盘, got %q", last.Detail)
	}

	for _, ev := range events {
		if ev.Step == "扩展文件系统" {
			t.Errorf("RAID5 不应出现“扩展文件系统”步骤事件, got %+v", ev)
		}
		if ev.Step == "等待数据同步" {
			t.Errorf("RAID5 不应进入 resync 轮询步骤, got %+v", ev)
		}
	}
	for _, d := range *sleepCalls {
		if d == 2*time.Second {
			t.Error("RAID5 不应调用 waitForResync（不应记录 2s 的 sleepFn 调用）")
		}
	}
	if stub.countCalls("/usr/sbin/xfs_growfs") != 0 || stub.countCalls("/usr/sbin/resize2fs") != 0 {
		t.Error("RAID5 不应调用任何 growFS 命令")
	}
}

// R3: mdadm --detail 失败 / md 设备文件不存在——两个独立子场景，均在
// common.LockDiskOp() 之前拒绝（锁外拒绝）。
func TestHandleRAIDExpandStream_R3_DeviceOrDetailFailure(t *testing.T) {
	t.Run("md设备文件不存在", func(t *testing.T) {
		withNoopSleep(t)
		stub := withStubExec(t, map[string]cmdResponse{})
		req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": "/dev/md_test_nonexistent_9999",
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		if len(events) != 1 || events[0].Status != "error" || !strings.Contains(events[0].Detail, "不存在") {
			t.Fatalf("got events=%+v, want 单条 error 含“不存在”", events)
		}
		if len(stub.calls) != 0 {
			t.Errorf("设备不存在应在锁外拒绝，不应触发任何命令；calls=%v", stub.calledNames())
		}
	})

	t.Run("mdadm_detail失败", func(t *testing.T) {
		withNoopSleep(t)
		mdDev := withFakeMdDev(t)
		stub := withStubExec(t, map[string]cmdResponse{
			"/usr/sbin/mdadm": {Out: "", Err: errAny},
		})
		req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": mdDev,
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		if len(events) != 1 || events[0].Status != "error" || !strings.Contains(events[0].Detail, "无法读取") {
			t.Fatalf("got events=%+v, want 单条 error 含“无法读取”", events)
		}
		if stub.countCalls("/usr/sbin/wipefs") != 0 {
			t.Error("mdadm --detail 失败应在锁外拒绝，wipefs 不应执行")
		}
	})
}

// R4: raid0 拒绝 + 不支持的级别（raid10）+ 设备数解析失败——三个独立子场景，
// 各自文案不同，均不获取锁、不执行 wipefs。
func TestHandleRAIDExpandStream_R4_LevelRejections(t *testing.T) {
	run := func(name, detail, wantSubstr string) {
		t.Run(name, func(t *testing.T) {
			withNoopSleep(t)
			mdDev := withFakeMdDev(t)
			stub := withStubExec(t, map[string]cmdResponse{
				"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
					if len(args) > 0 && args[0] == "--detail" {
						return detail, nil
					}
					return "", nil
				}},
			})
			req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
				"md_device": mdDev,
				"device":    "/dev/sdz",
				"confirm":   "yes",
			})
			handleRAIDExpandStream(rec, req)

			events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
			last := events[len(events)-1]
			if last.Status != "error" || !strings.Contains(last.Detail, wantSubstr) {
				t.Fatalf("got last=%+v, want error 含 %q", last, wantSubstr)
			}
			if stub.countCalls("/usr/sbin/wipefs") != 0 {
				t.Error("不支持的级别应被拒绝，wipefs 不应执行")
			}
		})
	}

	run("raid0无冗余", "        Raid Level : raid0\n      Raid Devices : 2\n", "RAID0 无冗余")
	run("不支持的级别raid10", "        Raid Level : raid10\n      Raid Devices : 4\n", "不支持扩容")
	run("设备数解析失败", "        Raid Level : raid1\n", "无法解析当前阵列设备数")
}

// R5: 目标盘不空闲——三个独立子场景（已在阵列中 / getDiskStatus 非 unused /
// isDiskInUse 命中），均在 wipefs 之前拒绝（三层防御各自独立生效）。
func TestHandleRAIDExpandStream_R5_TargetDiskNotFree(t *testing.T) {
	t.Run("已在阵列中", func(t *testing.T) {
		withNoopSleep(t)
		mdDev := withFakeMdDev(t)
		stub := withStubExec(t, map[string]cmdResponse{
			"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
				if len(args) > 0 && args[0] == "--detail" {
					return "        Raid Level : raid1\n      Raid Devices : 2\n" +
						"    Number   Major   Minor   RaidDevice State\n" +
						"       0       8        0        0      active sync   /dev/sda\n" +
						"       1       8       16        1      active sync   /dev/sdz\n", nil
				}
				return "", nil
			}},
		})
		req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": mdDev,
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		last := events[len(events)-1]
		if last.Status != "error" || !strings.Contains(last.Detail, "已在阵列中") {
			t.Fatalf("got last=%+v, want error 含“已在阵列中”", last)
		}
		if stub.countCalls("/usr/sbin/wipefs") != 0 {
			t.Error("已在阵列中的盘应被拒绝，wipefs 不应执行")
		}
	})

	t.Run("getDiskStatus非unused", func(t *testing.T) {
		withNoopSleep(t)
		mdDev := withFakeMdDev(t)
		stub := withStubExec(t, map[string]cmdResponse{
			"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
				if len(args) > 0 && args[0] == "--detail" {
					return raid1Detail(2), nil
				}
				return "", nil
			}},
		})
		origGetDiskStatus := getDiskStatusFn
		getDiskStatusFn = func() []DiskStatus {
			return []DiskStatus{{Device: "/dev/sdz", Type: "data"}}
		}
		t.Cleanup(func() { getDiskStatusFn = origGetDiskStatus })

		req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": mdDev,
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		last := events[len(events)-1]
		if last.Status != "error" || !strings.Contains(last.Detail, "不是空闲磁盘") {
			t.Fatalf("got last=%+v, want error 含“不是空闲磁盘”", last)
		}
		if stub.countCalls("/usr/sbin/wipefs") != 0 {
			t.Error("非 unused 状态的盘应被拒绝，wipefs 不应执行")
		}
	})

	t.Run("isDiskInUse命中", func(t *testing.T) {
		withNoopSleep(t)
		mdDev := withFakeMdDev(t)
		stub := withStubExec(t, map[string]cmdResponse{
			"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
				if len(args) > 0 && args[0] == "--detail" {
					return raid1Detail(2), nil
				}
				return "", nil
			}},
			"lsblk": {Fn: func(args []string) (string, error) {
				return "sdz xfs\n", nil
			}},
		})
		origGetDiskStatus := getDiskStatusFn
		getDiskStatusFn = func() []DiskStatus {
			return []DiskStatus{{Device: "/dev/sdz", Type: "unused"}}
		}
		t.Cleanup(func() { getDiskStatusFn = origGetDiskStatus })

		req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": mdDev,
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		last := events[len(events)-1]
		if last.Status != "error" || !strings.Contains(last.Detail, "已被占用") {
			t.Fatalf("got last=%+v, want error 含“已被占用”", last)
		}
		if stub.countCalls("/usr/sbin/wipefs") != 0 {
			t.Error("isDiskInUse 命中的盘应被拒绝，wipefs 不应执行")
		}
	})
}

// R8: mdadm --grow 失败——error 事件含原始输出，之后不再执行任何命令（既不
// resync 轮询也不 growFS）；另有一个 ctx 取消子场景，镜像 P7 的断言手法。
func TestHandleRAIDExpandStream_R8_GrowFailsAndCtxCancel(t *testing.T) {
	t.Run("mdadm_grow失败", func(t *testing.T) {
		withNoopSleep(t)
		mdDev := withFakeMdDev(t)
		stub := withStubExec(t, map[string]cmdResponse{
			"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
				if len(args) > 0 && args[0] == "--detail" {
					return raid1Detail(2), nil
				}
				if len(args) > 0 && args[0] == "--grow" {
					return "mdadm: Cannot set device size/shape for /dev/md0: Device or resource busy", errAny
				}
				return "", nil
			}},
		})
		origGetDiskStatus := getDiskStatusFn
		getDiskStatusFn = func() []DiskStatus {
			return []DiskStatus{{Device: "/dev/sdz", Type: "unused"}}
		}
		t.Cleanup(func() { getDiskStatusFn = origGetDiskStatus })

		req, rec := withSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": mdDev,
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		last := events[len(events)-1]
		if last.Status != "error" || last.Step != "扩展阵列" {
			t.Fatalf("got last=%+v, want error Step=扩展阵列", last)
		}
		if !strings.Contains(last.Detail, "Cannot set device size") {
			t.Errorf("Detail 应含原始输出, got %q", last.Detail)
		}
		if !strings.Contains(last.Detail, errAny.Error()) {
			t.Errorf("Detail 应含错误本身, got %q", last.Detail)
		}
		for _, ev := range events {
			if ev.Step == "等待数据同步" {
				t.Errorf("mdadm --grow 失败后不应进入 resync 轮询, got %+v", ev)
			}
			if ev.Step == "扩展文件系统" {
				t.Errorf("mdadm --grow 失败后不应出现 growFS 相关事件, got %+v", ev)
			}
		}
		if stub.countCalls("/usr/sbin/xfs_growfs") != 0 || stub.countCalls("/usr/sbin/resize2fs") != 0 {
			t.Error("mdadm --grow 失败后不应调用任何 growFS 命令")
		}
	})

	t.Run("ctx取消阻止后续命令", func(t *testing.T) {
		withNoopSleep(t)
		mdDev := withFakeMdDev(t)
		req, rec, cancel := withCancelableRAIDSSE("GET", "/api/disk/raid/expand-stream", map[string]string{
			"md_device": mdDev,
			"device":    "/dev/sdz",
			"confirm":   "yes",
		})
		stub := withStubExec(t, map[string]cmdResponse{
			"/usr/sbin/mdadm": {Fn: func(args []string) (string, error) {
				if len(args) > 0 && args[0] == "--detail" {
					return raid1Detail(2), nil
				}
				return "", nil
			}},
			// wipefs 是命令链第一步；它返回之后立即取消 ctx，模拟客户端在
			// 第一条命令跑完、还没来得及推下一条事件时断开连接。
			"/usr/sbin/wipefs": {Fn: func(args []string) (string, error) {
				cancel()
				return "", nil
			}},
		})
		origGetDiskStatus := getDiskStatusFn
		getDiskStatusFn = func() []DiskStatus {
			return []DiskStatus{{Device: "/dev/sdz", Type: "unused"}}
		}
		t.Cleanup(func() { getDiskStatusFn = origGetDiskStatus })

		handleRAIDExpandStream(rec, req)

		events := decodeRAIDEvents(t, parseSSE(rec.Body.String()))
		for _, ev := range events {
			if ev.Step == "清除新磁盘签名" && ev.Status == "done" {
				t.Errorf("ctx 取消后不应再发送“清除新磁盘签名” done 事件, got %+v", ev)
			}
			if strings.Contains(ev.Step, "添加") {
				t.Errorf("ctx 取消后不应再发送“添加磁盘”相关事件, got %+v", ev)
			}
		}
		if stub.countCalls("/usr/sbin/mdadm") != 1 { // 仅 --detail 那一次
			t.Errorf("ctx 取消后不应再调用 mdadm --add/--grow，got %d 次调用：%v", stub.countCalls("/usr/sbin/mdadm"), stub.calledNames())
		}
	})
}
