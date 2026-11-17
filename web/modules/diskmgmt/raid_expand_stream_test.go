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
// handleRAIDExpandStream —— R1-R5（DESIGN.md §2.2），R8（R3 新增）。
// R6/R7（waitForResyncCore 抽取相关，本轮 R4 新增）见文件末尾。
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

// ═══════════════════════════════════════════════════
// R6/R7（本轮 R4 新增）：waitForResyncCore/resyncPollOnce 抽取后的行为锁定。
// ═══════════════════════════════════════════════════

// TestWaitForResyncCore_R6_TimeoutAfterMaxRounds 直测 waitForResyncCore：poll
// 恒 not-done、maxRounds=3、sleepFn no-op → 必须返回非 nil（超时），且 emit
// 被调用恰好 3 次（每轮一次 running 进度事件），不会提前因为 streak 判定误判
// 完成。
func TestWaitForResyncCore_R6_TimeoutAfterMaxRounds(t *testing.T) {
	poll := func() (bool, string) { return false, "同步进度: 10.0%" }
	var sleepCalls int
	noopSleep := func(time.Duration) { sleepCalls++ }
	var emitCalls []string
	emit := func(detail string) error {
		emitCalls = append(emitCalls, detail)
		return nil
	}

	err := waitForResyncCore(poll, 3, 3, noopSleep, emit)
	if err == nil {
		t.Fatal("waitForResyncCore() = nil, want 超时 error（poll 恒 not-done）")
	}
	if len(emitCalls) != 3 {
		t.Errorf("emit 调用次数 = %d, want 3（每轮一次）; emitCalls=%v", len(emitCalls), emitCalls)
	}
	for _, d := range emitCalls {
		if d != "同步进度: 10.0%" {
			t.Errorf("emit detail = %q, want 原样转发 poll 的 detail", d)
		}
	}
	// 循环前置 1 次 + 每轮末尾 1 次 * 3 轮 = 4 次。
	if sleepCalls != 4 {
		t.Errorf("sleepFn 调用次数 = %d, want 4（1 次前置 + 3 轮各 1 次）", sleepCalls)
	}
}

// TestHandleRAIDExpandStream_R6_ResyncTimeoutStopsBeforeFS 验证 handler 层：
// resync 超时（waitForResync 返回非 nil）后，error 事件 Detail 必须同时含
// “已中止、文件系统未扩展”与 expand-fs 入口指引；growFS 相关命令绝不执行——
// 文件系统绝不在 resync 未完成时被扩（A1）。用极小的 maxRounds 不现实（生产
// 签名的 600 轮硬编码在 waitForResync 内部），这里改用「sysfs 一直报 resync
// 进行中」且 sleepFn 记录调用次数，在跑满真实 600 轮前用 withNoopSleep 消除
// 延迟后直接跑到超时判定——验证 handler 侧对该返回值的处理，不重复 R6 核心
// 用例已经覆盖的 waitForResyncCore 循环次数判定。
func TestHandleRAIDExpandStream_R6_ResyncTimeoutStopsBeforeFS(t *testing.T) {
	withNoopSleep(t)
	mdDev := withFakeMdDev(t)
	// sync_action 恒为 "resync"（非 idle）：poll 每轮都判定未完成，
	// waitForResync 跑满 600 轮后返回超时 error。
	busyVals := map[string]string{
		"level":            "raid1",
		"array_state":      "clean",
		"degraded":         "0",
		"raid_disks":       "2",
		"sync_action":      "resync",
		"sync_completed":   "1000 / 100000",
		"sync_speed":       "500",
		"reshape_position": "none",
	}
	withFakeSysfsMD(t, mdDev, busyVals, cleanRAID1SysfsMembers())

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
	if last.Status != "error" || last.Step != "等待数据同步" {
		t.Fatalf("got last=%+v, want error Step=等待数据同步", last)
	}
	if !strings.Contains(last.Detail, "已中止") || !strings.Contains(last.Detail, "文件系统未扩展") {
		t.Errorf("Detail 应含“已中止、文件系统未扩展”, got %q", last.Detail)
	}
	if !strings.Contains(last.Detail, "/api/disk/raid/expand-fs") {
		t.Errorf("Detail 应含 expand-fs 入口指引, got %q", last.Detail)
	}
	for _, ev := range events {
		if ev.Step == "数据同步完成" {
			t.Errorf("resync 超时后不应出现“数据同步完成”事件, got %+v", ev)
		}
		if ev.Step == "扩展文件系统" {
			t.Errorf("resync 超时后不应出现“扩展文件系统”事件, got %+v", ev)
		}
	}
	if stub.countCalls("/usr/sbin/xfs_growfs") != 0 || stub.countCalls("/usr/sbin/resize2fs") != 0 {
		t.Error("文件系统绝不应在 resync 未完成时被扩")
	}
}

// TestHandleRAIDExpandFS_R6_SysfsGate 配套 handleRAIDExpandFS 闸门用例：
// sysfs sync_action=resync → 拒绝（JSON error）；idle+reshape_position=none
// → 放行走到 growFS 桩（findmnt 配好挂载点与 xfs 类型）。
func TestHandleRAIDExpandFS_R6_SysfsGate(t *testing.T) {
	t.Run("resync进行中_拒绝", func(t *testing.T) {
		mdDev := withFakeMdDev(t)
		mdName := filepath.Base(mdDev)
		withFakeSysfsMD(t, mdDev, map[string]string{
			"sync_action":      "resync",
			"reshape_position": "none",
		}, nil)

		req := httptest.NewRequest("POST", "/api/disk/raid/expand-fs",
			strings.NewReader("md_device="+mdDev+"&confirm=yes"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handleRAIDExpandFS(rec, req)

		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
		}
		errMsg, _ := body["error"].(string)
		if errMsg == "" || !strings.Contains(errMsg, "重构中") {
			t.Fatalf("got body=%+v, want error 含“重构中”；mdName=%s", body, mdName)
		}
	})

	t.Run("idle放行_走到growFS", func(t *testing.T) {
		mdDev := withFakeMdDev(t)
		withFakeSysfsMD(t, mdDev, map[string]string{
			"sync_action":      "idle",
			"reshape_position": "none",
		}, nil)
		stub := withStubExec(t, map[string]cmdResponse{
			"findmnt": {Fn: findmntResponder(map[string]string{mdDev: "/mnt/data"}, "xfs")},
		})

		req := httptest.NewRequest("POST", "/api/disk/raid/expand-fs",
			strings.NewReader("md_device="+mdDev+"&confirm=yes"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handleRAIDExpandFS(rec, req)

		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
		}
		if _, isErr := body["error"]; isErr {
			t.Fatalf("idle 态应放行，got error body=%+v", body)
		}
		if !stub.wasCalledWith("/usr/sbin/xfs_growfs", "/mnt/data") {
			t.Errorf("idle 态应放行走到 growFS（xfs_growfs /mnt/data），calls=%v", stub.calledNames())
		}
	})
}

// TestResyncPollOnce_R7_StreakRace 直测 resyncPollOnce + sysfsCleanStreakResult
// 组合：poll 序列 [busy,busy,clean,busy,clean,clean,clean] → 仅连续 3 轮
// clean 后才判定完成（第 7 轮，下标 6）；中途一次 clean 后再 busy，streak 必须
// 归零重新累计（Finding 3 回归，R3 报告交接的 waitForResyncCore 抽取验证点）。
// busy 轮 resyncPollOnce 返回的 detail 必须是“同步进度: x%”或“同步中（<action>）”
// 形态（经 resyncProgressDetail）。
func TestResyncPollOnce_R7_StreakRace(t *testing.T) {
	// sync_action 序列：busy 态写 "resync"，clean 态写 "idle"。
	actionSeq := []string{"resync", "resync", "idle", "resync", "idle", "idle", "idle"}
	mdName := "md_r7_test"

	var dones []bool
	var details []string
	for i, action := range actionSeq {
		vals := map[string]string{"sync_action": action}
		if action != "idle" {
			vals["sync_completed"] = fmt.Sprintf("%d / 100000", (i+1)*1000)
		}
		readSysfs := func(string) (map[string]string, bool) { return vals, true }
		done, detail := resyncPollOnce(mdName, readSysfs, "")
		dones = append(dones, done)
		details = append(details, detail)
	}

	wantDones := []bool{false, false, true, false, true, true, true}
	for i, want := range wantDones {
		if dones[i] != want {
			t.Errorf("第 %d 轮 resyncPollOnce done = %v, want %v（actionSeq=%v）", i, dones[i], want, actionSeq)
		}
	}
	for i, done := range dones {
		if !done {
			if !strings.Contains(details[i], "同步进度:") && !strings.Contains(details[i], "同步中（") {
				t.Errorf("第 %d 轮 busy 态 detail = %q, want 含“同步进度:”或“同步中（”", i, details[i])
			}
		}
	}

	// sysfsCleanStreakResult 是既有的多轮 streak 判定纯函数，resyncPollOnce
	// 只负责单轮判据，streak 累计委托给 waitForResyncCore——这里用
	// sysfsCleanStreakResult 独立交叉验证同一份 actionSeq 的 streak 结果，
	// 确认两者判据一致（不重复实现、分工不冲突）。
	done, atIndex := sysfsCleanStreakResult(actionSeq, 3)
	if !done || atIndex != 6 {
		t.Errorf("sysfsCleanStreakResult(%v, 3) = (%v, %d), want (true, 6)（仅连续 3 轮 clean 后完成，中途 busy 使 streak 归零）", actionSeq, done, atIndex)
	}
}

// TestWaitForResyncCore_R7_StreakRaceViaEmit 用 waitForResyncCore 本身跑一遍
// R7 的 poll 序列（经闭包按调用次数切换返回值，模拟 TempDir sysfs 内容按轮次
// 切换），验证端到端：仅连续 3 轮 clean 后返回 nil，busy 轮 emit 收到
// running 进度事件，返回 nil 前 emit 被调用次数等于序列中 busy 轮次数（4 次：
// 下标 0,1,3 外加完成前已经数完，不再轮询第 7 轮之后）。
func TestWaitForResyncCore_R7_StreakRaceViaEmit(t *testing.T) {
	// done 序列对应 actionSeq=[resync,resync,idle,resync,idle,idle,idle]。
	doneSeq := []bool{false, false, true, false, true, true, true}
	call := 0
	poll := func() (bool, string) {
		d := doneSeq[call]
		call++
		if d {
			return true, ""
		}
		return false, "同步进度: 10.0%"
	}
	var sleepCalls int
	noopSleep := func(time.Duration) { sleepCalls++ }
	var emitCount int
	emit := func(string) error {
		emitCount++
		return nil
	}

	err := waitForResyncCore(poll, 600, 3, noopSleep, emit)
	if err != nil {
		t.Fatalf("waitForResyncCore() = %v, want nil（第 7 轮应达成连续 3 轮 clean）", err)
	}
	if call != 7 {
		t.Errorf("poll 被调用 %d 次, want 7（跑完整条序列才达成 streak=3）", call)
	}
	if emitCount != 3 {
		t.Errorf("emit 被调用 %d 次, want 3（仅 busy 轮下标 0,1,3 各一次）", emitCount)
	}
}

// TestWaitForResyncCore_EmitErrorStopsImmediately 回归 R3 声明缺口：emit 恒
// 返回 error（模拟断连）→ waitForResyncCore 必须立即返回该 error，且在 emit
// 第一次返回 error 之后 sleepFn 不再被调用（不再空转到超时或继续轮询）。
func TestWaitForResyncCore_EmitErrorStopsImmediately(t *testing.T) {
	pollCalls := 0
	poll := func() (bool, string) {
		pollCalls++
		return false, "同步进度: 1.0%" // 恒 not-done，若未被 emit 错误截断会一直轮询
	}
	disconnectErr := fmt.Errorf("write: broken pipe")
	emit := func(string) error { return disconnectErr }

	var sleepCalls []time.Duration
	sleep := func(d time.Duration) { sleepCalls = append(sleepCalls, d) }

	err := waitForResyncCore(poll, 600, 3, sleep, emit)
	if err != disconnectErr {
		t.Fatalf("waitForResyncCore() = %v, want %v（emit 的 error 应原样向上返回）", err, disconnectErr)
	}
	if pollCalls != 1 {
		t.Errorf("poll 被调用 %d 次, want 1（emit 出错后不应再轮询）", pollCalls)
	}
	// 循环前置 sleepFn(2s) 必然发生（在第一次 poll 之前），但 emit 出错后
	// 本轮末尾的 sleepFn 不应再被调用——即总调用次数应恰为 1（仅前置那次）。
	if len(sleepCalls) != 1 {
		t.Errorf("sleepFn 调用次数 = %d, want 1（仅循环前置那次；emit 出错后不再空转）", len(sleepCalls))
	}
}
