package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════
// Requirement 2 专项测试：pool_delete_recovery 持久化恢复记录。
//
// 本包的 initConfigDB/initPendingDB 走 TestMain 固定的临时目录（见
// main_test.go），所有用例操作的是真实 sqlite 文件，不是内存 mock——
// 这正是 Requirement 2 要验证的点：记录必须真的落盘，而不是只存在于
// 进程内存里（否则崩溃后同样会丢失）。
// ═══════════════════════════════════════════════════

// clearAllPoolDeleteRecoveries 测试辅助：清空表，保证每个用例看到的
// GetPoolDeleteRecoveries() 只反映自己写入的记录，不被其它用例残留污染
// （sqlite 文件在整个测试二进制生命周期内共享同一个临时目录）。
func clearAllPoolDeleteRecoveries(t *testing.T) {
	t.Helper()
	db := initPendingDB()
	if db == nil {
		t.Fatal("initPendingDB() 返回 nil，无法建立测试前提")
	}
	ensurePoolDeleteRecoverySchema(db)
	if _, err := db.Exec("DELETE FROM pool_delete_recovery"); err != nil {
		t.Fatalf("清空 pool_delete_recovery 失败: %v", err)
	}
}

// mustGetPoolDeleteRecoveries 是测试里的查询便捷包装：出错时直接 t.Fatalf，
// 大多数用例只关心"查询本身必须成功、返回的记录列表是什么"，DB 查询失败
// 场景由专门的 TestGetPoolDeleteRecoveries_DBFailure... 用例覆盖。
func mustGetPoolDeleteRecoveries(t *testing.T) []PoolDeleteRecovery {
	t.Helper()
	recs, err := GetPoolDeleteRecoveries()
	if err != nil {
		t.Fatalf("GetPoolDeleteRecoveries() 返回 error: %v", err)
	}
	return recs
}

// withFakeDeviceIdentity 把 captureDeviceIdentityFn 替换为固定返回"路径+
// 伪造序列号"的身份（不读真实 blkid/smartctl），满足 recordPoolDeleteStart
// 的 fail-closed 前提（至少一个成员字段非空）——大多数测试只关心删除流程
// 本身，不关心身份采集细节，用这个 helper 避免身份采集失败把它们一起拖垂。
// 需要验证身份采集本身（成功/失败）的用例直接调用 captureDeviceIdentities
// 或替换 captureDeviceIdentityFn 为更具体的假实现，不使用这个 helper。
func withFakeDeviceIdentity(t *testing.T) {
	t.Helper()
	orig := captureDeviceIdentityFn
	captureDeviceIdentityFn = func(devPath string) DeviceIdentity {
		return DeviceIdentity{Path: devPath, Serial: "FAKE-SERIAL-" + devPath}
	}
	t.Cleanup(func() { captureDeviceIdentityFn = orig })
}

// withBrokenConfigDB 关闭包级单例 configDB 底层连接，让后续任何
// initPendingDB()/initConfigDB() 的 db.Query/db.Exec 调用确定性地失败
// （sql.ErrConnDone），模拟"数据库不可用"场景。t.Cleanup 重新 Open 同一个
// 文件路径的连接写回单例，恢复其它用例可用（不触碰 configDBOnce 本身，
// 做法与 pool_structure_test.go 的 TestEnsurePoolStructure_SyncFolderMetaFailurePropagates
// 一致）。
func withBrokenConfigDB(t *testing.T) {
	t.Helper()
	db := initConfigDB()
	if db == nil {
		t.Fatal("initConfigDB() 返回 nil，无法建立测试前提")
	}
	db.Close()
	t.Cleanup(func() {
		reopened, err := common.OpenSQLite(configDBPath())
		if err != nil {
			t.Fatalf("测试清理阶段重新打开元数据库失败: %v", err)
		}
		configDB = reopened
	})
}

// TestRecordPoolDeleteStart_PersistsBeforeIrreversibleStep 验证记录写入后
// 立即可以通过 GetPoolDeleteRecoveries 查到——模拟"不可逆命令执行前落盘"
// 这一步本身是可靠的,成员列表、池类型、设备、名称都完整保留。
func TestRecordPoolDeleteStart_PersistsBeforeIrreversibleStep(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	id, err := recordPoolDeleteStart("lvm", "/dev/vg_nas", "vg_nas", []string{"/dev/sdb", "/dev/sdc"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}
	if id == 0 {
		t.Fatal("want 非零记录 ID")
	}

	recs := mustGetPoolDeleteRecoveries(t)
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1；recs=%+v", len(recs), recs)
	}
	rec := recs[0]
	if rec.PoolType != "lvm" || rec.PoolDevice != "/dev/vg_nas" || rec.PoolName != "vg_nas" {
		t.Errorf("rec = %+v, want type=lvm device=/dev/vg_nas name=vg_nas", rec)
	}
	if !strings.Contains(rec.Members, "/dev/sdb") || !strings.Contains(rec.Members, "/dev/sdc") {
		t.Errorf("rec.Members = %q, want 包含 /dev/sdb 和 /dev/sdc", rec.Members)
	}
	if rec.Phase != "started" {
		t.Errorf("rec.Phase = %q, want %q（尚未越过任何不可逆步骤）", rec.Phase, "started")
	}
}

// TestUpdatePoolDeletePhase_PersistsNewPhase 验证阶段更新后，再次查询能看到
// 新的阶段值——崩溃恢复场景下，这是判断"VG/阵列元数据是否已经被拆除"的
// 唯一依据。
func TestUpdatePoolDeletePhase_PersistsNewPhase(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	id, err := recordPoolDeleteStart("raid1", "/dev/md0", "md0", []string{"/dev/sdb", "/dev/sdc"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}

	if err := updatePoolDeletePhase(id, "array_stopped"); err != nil {
		t.Fatalf("updatePoolDeletePhase 失败: %v", err)
	}

	recs := mustGetPoolDeleteRecoveries(t)
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1", len(recs))
	}
	if recs[0].Phase != "array_stopped" {
		t.Errorf("Phase = %q, want array_stopped", recs[0].Phase)
	}

	// 成员列表字段不应随阶段更新而改变——恢复时需要看到"这次删除涉及的
	// 全部成员"，不是动态收缩的列表（见 pool_delete_recovery.go 字段注释）。
	if !strings.Contains(recs[0].Members, "/dev/sdb") || !strings.Contains(recs[0].Members, "/dev/sdc") {
		t.Errorf("Members = %q, 阶段更新不应改变成员列表", recs[0].Members)
	}
}

// TestClearPoolDeleteRecovery_RemovesRecord 验证清除之后记录彻底消失——
// 对应管理员通过确认接口（confirmPoolDeleteRecovery）核实完毕后手动清除
// 的场景。clearPoolDeleteRecovery 本身不再由 handlePoolDelete 自动调用，
// 只是确认流程内部的底层数据库操作，这里直接测试该底层函数的行为。
func TestClearPoolDeleteRecovery_RemovesRecord(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	id, err := recordPoolDeleteStart("single", "/dev/sdz", "disk-z", []string{"/dev/sdz"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}
	if len(mustGetPoolDeleteRecoveries(t)) != 1 {
		t.Fatal("清除前应该能查到 1 条记录")
	}

	clearPoolDeleteRecovery(id)

	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 0 {
		t.Errorf("清除后 len(recs) = %d, want 0；recs=%+v", len(recs), recs)
	}
}

// TestClearPoolDeleteRecovery_ZeroIDIsNoop 验证 id=0（未建立记录，如
// recordPoolDeleteStart 从未被调用或写入本身失败的场景）传给 clear/update
// 不会 panic、不会误删其它记录——recoveryID 的零值语义在 handlePoolDelete
// 里代表"尚未进入任何需要持久化恢复的分支"。
func TestClearPoolDeleteRecovery_ZeroIDIsNoop(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	id, err := recordPoolDeleteStart("single", "/dev/sdy", "disk-y", []string{"/dev/sdy"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}

	if err := updatePoolDeletePhase(0, "should_not_apply"); err != nil {
		t.Fatalf("updatePoolDeletePhase(0, ...) 应该是 no-op，不应返回 error: %v", err)
	}
	clearPoolDeleteRecovery(0)

	recs := mustGetPoolDeleteRecoveries(t)
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1（id=0 的调用不应影响已存在的记录）", len(recs))
	}
	if recs[0].ID != int(id) || recs[0].Phase != "started" {
		t.Errorf("rec = %+v, want 未被 id=0 的调用改动", recs[0])
	}
}

// ═══════════════════════════════════════════════════
// 端到端：通过 handlePoolDelete 驱动，验证记录在真实请求路径里的生命周期。
// ═══════════════════════════════════════════════════

// TestHandlePoolDelete_SingleSuccess_RecoveryRecordRetainedUntilConfirmed 验证
// single 分支全程成功时，恢复记录仍必须保留——写 HTTP 响应不等于调用方
// 真的收到了它（连接可能已断、代理可能已超时丢弃响应体、进程可能写完
// 响应后立刻被杀）。记录只能通过显式的
// POST /api/disk/pool/delete/recovery/confirm 被管理员手动清除。
func TestHandlePoolDelete_SingleSuccess_RecoveryRecordRetainedUntilConfirmed(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdq"})
	withStubExec(t, map[string]cmdResponse{
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"cat":              {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: nil},
	})

	form := deletePoolForm("single", "/dev/sdq", "disk-q")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200；body=%s", w.Code, w.Body.String())
	}
	recs := mustGetPoolDeleteRecoveries(t)
	if len(recs) != 1 {
		t.Fatalf("完全成功后 len(recs) = %d, want 1（记录必须保留到管理员确认为止）；recs=%+v", len(recs), recs)
	}
	if recs[0].PoolDevice != "/dev/sdq" {
		t.Errorf("recs[0].PoolDevice = %q, want /dev/sdq", recs[0].PoolDevice)
	}

	// 记录可以被后续重新查询到（模拟进程重启后管理员核实）。
	recsAgain := mustGetPoolDeleteRecoveries(t)
	if len(recsAgain) != 1 {
		t.Fatalf("重新查询 len(recsAgain) = %d, want 1", len(recsAgain))
	}

	// 只能通过显式确认接口清除。
	if err := confirmPoolDeleteRecovery(int64(recs[0].ID)); err != nil {
		t.Fatalf("confirmPoolDeleteRecovery 失败: %v", err)
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 0 {
		t.Errorf("显式确认后 len(recs) = %d, want 0", len(recs))
	}
}

// TestHandlePoolDelete_WipefsFailure_RecoveryRecordRetainedUntilConfirmed 验证
// single 分支 wipefs 本身失败时，记录依然保留——sudoExecFn 返回失败并不
// 代表设备上什么都没发生：wipefs 可能在部分写入签名后才失败（I/O 错误、
// 写到一半被打断），不能把"命令返回非零"简单等同于"设备安然无损"而自动
// 清除记录。
func TestHandlePoolDelete_WipefsFailure_RecoveryRecordRetainedUntilConfirmed(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdq"})
	withStubExec(t, map[string]cmdResponse{
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: fmt.Errorf("device or resource busy")},
	})

	form := deletePoolForm("single", "/dev/sdq", "disk-q")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200")
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 1 {
		t.Errorf("wipefs 本身失败后 len(recs) = %d, want 1（记录必须保留，不得自动清除）；recs=%+v", len(recs), recs)
	}
}

// TestHandlePoolDelete_LVMPartialFailure_RecoveryRecordRetainedUntilConfirmed
// 验证 LVM 分支 vgremove 成功（越过不可逆步骤）、但部分 PV 清理失败时，
// 记录依然保留——失败设备清单出现在响应体里只能证明"服务器尝试过把它
// 写出去"，不能证明管理员已经看到。保留与否的判据只有一个：是否已经
// 被管理员通过确认接口明确核实，不是"响应体里有没有写这件事"。
func TestHandlePoolDelete_LVMPartialFailure_RecoveryRecordRetainedUntilConfirmed(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	withFakeLivePool(t, PoolSummary{Type: "lvm", Device: "/dev/vg_nas"})
	withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/vgs":      {Out: "  vg_nas\n", Err: nil},
		"/usr/sbin/pvs":      {Out: "/dev/sdb /dev/sdc", Err: nil},
		"/usr/sbin/lvs":      {Out: "data /dev/vg_nas/data\n", Err: nil},
		"findmnt":            {Out: "", Err: nil},
		"/usr/sbin/lvremove": {Out: "", Err: nil},
		"/usr/sbin/vgremove": {Out: "", Err: nil},
		"/usr/sbin/pvremove": {Fn: func(args []string) (string, error) {
			for _, a := range args {
				if a == "/dev/sdb" {
					return "", fmt.Errorf("device or resource busy")
				}
			}
			return "", nil
		}},
		"/usr/sbin/wipefs": {Out: "", Err: nil},
	})

	form := deletePoolForm("lvm", "/dev/vg_nas", "vg_nas")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（部分 PV 残留）；body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/dev/sdb") {
		t.Fatalf("响应体必须包含失败设备 /dev/sdb，前提不满足；body=%s", w.Body.String())
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 1 {
		t.Errorf("部分失败后 len(recs) = %d, want 1（记录必须保留到管理员确认为止）；recs=%+v", len(recs), recs)
	}
}

// TestHandlePoolDelete_SimulatedCrash_RecoveryRecordSurvivesForInspection 模拟
// "不可逆命令已执行、但进程在交付结果之前崩溃"：直接调用 recordPoolDeleteStart+
// updatePoolDeletePhase（相当于 handlePoolDelete 执行到 vgremove 成功之后的
// 状态），不调用 clearPoolDeleteRecovery（相当于进程在此处被杀，HTTP 响应
// 从未写出）。验证记录确实留存，且可以通过 handlePoolDeleteRecoveryStatus
// （即 /api/disk/pool/delete/recovery）查询到，内容完整到足以让管理员知道
// "哪种池、哪个设备、哪些成员、处于什么阶段"。
func TestHandlePoolDelete_SimulatedCrash_RecoveryRecordSurvivesForInspection(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	id, err := recordPoolDeleteStart("lvm", "/dev/vg_nas", "vg_nas", []string{"/dev/sdb", "/dev/sdc"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}
	if err := updatePoolDeletePhase(id, "vg_removed"); err != nil {
		t.Fatalf("updatePoolDeletePhase 失败: %v", err)
	}
	// 故意不调用 clearPoolDeleteRecovery——模拟进程崩溃，HTTP 响应从未送达。

	r := httptest.NewRequest(http.MethodGet, "/api/disk/pool/delete/recovery", nil)
	w := httptest.NewRecorder()
	handlePoolDeleteRecoveryStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200；body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"/dev/vg_nas", "vg_nas", "/dev/sdb", "/dev/sdc", "vg_removed"} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want 包含 %q（崩溃恢复场景下管理员需要看到这些信息）", body, want)
		}
	}
	if !strings.Contains(body, `"count":1`) {
		t.Errorf("body = %q, want count=1", body)
	}
}

// TestHandlePoolDelete_LVMPhaseUpdateFailure_HaltsBeforePVRemove 验证 LVM 分支
// vgremove 成功越过不可逆步骤之后，若 updatePoolDeletePhase(vg_removed) 写入
// 失败（模拟数据库在这一刻恰好不可用），handlePoolDelete 必须立即中止、
// 不再执行任何 pvremove/wipefs——持久化记录已经不可信时，继续执行更多
// 破坏性命令只会让"记录与真实状态"的落差进一步扩大。用 vgremove 的 Fn
// 钩子在"命令本身成功返回"之后、函数返回前关闭 configDB，精确模拟
// "不可逆命令已执行，但紧随其后的阶段持久化写入失败"这个时间窗口。
func TestHandlePoolDelete_LVMPhaseUpdateFailure_HaltsBeforePVRemove(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	withFakeLivePool(t, PoolSummary{Type: "lvm", Device: "/dev/vg_nas"})

	db := initConfigDB()
	if db == nil {
		t.Fatal("initConfigDB() 返回 nil，无法建立测试前提")
	}
	t.Cleanup(func() {
		reopened, err := common.OpenSQLite(configDBPath())
		if err != nil {
			t.Fatalf("测试清理阶段重新打开元数据库失败: %v", err)
		}
		configDB = reopened
	})

	stub := withStubExec(t, map[string]cmdResponse{
		"/usr/sbin/vgs": {Out: "  vg_nas\n", Err: nil},
		"/usr/sbin/pvs": {Out: "/dev/sdb /dev/sdc", Err: nil},
		"/usr/sbin/lvs": {Out: "data /dev/vg_nas/data\n", Err: nil},
		"findmnt":       {Out: "", Err: nil},
		"/usr/sbin/vgremove": {Fn: func(args []string) (string, error) {
			// vgremove 本身成功执行（不可逆命令已越过），紧接着让
			// configDB 失效，让随后的 updatePoolDeletePhase 调用确定性失败。
			db.Close()
			return "", nil
		}},
		"/usr/sbin/pvremove": {Out: "", Err: nil},
		"/usr/sbin/wipefs":   {Out: "", Err: nil},
	})

	form := deletePoolForm("lvm", "/dev/vg_nas", "vg_nas")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（阶段更新失败必须中止）；body=%s", w.Body.String())
	}
	if n := stub.countCalls("/usr/sbin/pvremove"); n != 0 {
		t.Errorf("pvremove 被调用了 %d 次, want 0（vg_removed 阶段更新失败后不得执行任何后续破坏性命令）", n)
	}
	if n := stub.countCalls("/usr/sbin/wipefs"); n != 0 {
		t.Errorf("wipefs 被调用了 %d 次, want 0（同上）", n)
	}
}

// TestConfirmPoolDeleteRecovery_RejectsInvalidOrMissingID 验证确认接口对
// 非法/缺失/不存在的 id 一律拒绝，不能把"误触确认"或"id 打错"悄悄当成
// 成功处理——确认操作是清除恢复记录的唯一入口，必须严格校验。
func TestConfirmPoolDeleteRecovery_RejectsInvalidOrMissingID(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)

	if err := confirmPoolDeleteRecovery(0); err == nil {
		t.Error("id=0 应被拒绝")
	}
	if err := confirmPoolDeleteRecovery(-1); err == nil {
		t.Error("id=-1 应被拒绝")
	}
	if err := confirmPoolDeleteRecovery(999999); err == nil {
		t.Error("不存在的 id 应返回 error（不能假装清除成功）")
	}
}

// TestHandlePoolDeleteRecoveryConfirm_ClearsOnlyViaExplicitPOST 验证
// /api/disk/pool/delete/recovery/confirm 必须是 POST + 带 id 才能清除记录，
// 且确认过程本身不执行任何擦除/阵列命令——它只是数据库记录的清除，这是
// 它之所以可以安全地要求"人工晚一步手动确认"而不带来额外破坏性风险的
// 前提，必须用零 sudo/exec 调用次数验证，不能只靠文档注释自证。
func TestHandlePoolDeleteRecoveryConfirm_ClearsOnlyViaExplicitPOST(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	stub := withStubExec(t, map[string]cmdResponse{})

	id, err := recordPoolDeleteStart("single", "/dev/sdz", "disk-z", []string{"/dev/sdz"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}

	// GET 不应清除记录（状态接口必须保持只读）。
	rGet := httptest.NewRequest(http.MethodGet, "/api/disk/pool/delete/recovery/confirm", nil)
	wGet := httptest.NewRecorder()
	handlePoolDeleteRecoveryConfirm(wGet, rGet)
	if wGet.Code == http.StatusOK {
		t.Errorf("GET /recovery/confirm 不应成功（status=%d）", wGet.Code)
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 1 {
		t.Fatalf("GET 请求后 len(recs) = %d, want 1（GET 不应有清除副作用）", len(recs))
	}

	// 正确的 POST + id 请求应该清除记录。
	form := url.Values{"id": {fmt.Sprintf("%d", id)}}
	rPost := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete/recovery/confirm", strings.NewReader(form.Encode()))
	rPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wPost := httptest.NewRecorder()
	handlePoolDeleteRecoveryConfirm(wPost, rPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("POST /recovery/confirm status = %d, want 200；body=%s", wPost.Code, wPost.Body.String())
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 0 {
		t.Errorf("POST 确认后 len(recs) = %d, want 0", len(recs))
	}
	// 只断言破坏性命令本身，不断言 len(stub.calls)==0：包级共享的
	// sudoExecFn/execOutputFn 在测试二进制里还承载着与本用例无关的后台
	// 轮询（如磁盘用量 df 刷新），与"确认接口是否触发了擦除/阵列命令"
	// 无关，断言总调用数为零会把这类无关背景噪音误判为本用例的失败。
	for _, destructive := range []string{"/usr/sbin/wipefs", "/usr/sbin/vgremove", "/usr/sbin/pvremove", "/usr/sbin/lvremove", "mdadm"} {
		if n := stub.countCalls(destructive); n != 0 {
			t.Errorf("确认接口触发了 %d 次 %s 调用, want 0（确认只应操作数据库，不得执行任何擦除/阵列命令）", n, destructive)
		}
	}
}

// TestHandlePoolDeleteRecoveryStatus_DoesNotClearRecords 验证只读状态接口
// 被反复查询时不会产生任何清除副作用——这是 Item 3 要求的"recovery/status
// 接口必须严格只读展示"的直接验证，而不仅是文档层面的承诺。
func TestHandlePoolDeleteRecoveryStatus_DoesNotClearRecords(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	if _, err := recordPoolDeleteStart("single", "/dev/sdz", "disk-z", []string{"/dev/sdz"}); err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/disk/pool/delete/recovery", nil)
		w := httptest.NewRecorder()
		handlePoolDeleteRecoveryStatus(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次查询 status = %d, want 200", i+1, w.Code)
		}
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 1 {
		t.Errorf("反复查询只读状态接口后 len(recs) = %d, want 1（只读接口不得有清除副作用）", len(recs))
	}
}

// TestHandlePoolDeleteRecoveryStatus_NoRecords_ReturnsEmptyList 验证正常情况下
// （没有被中断的删除操作）接口返回空列表而不是 null/error，前端可以直接
// 按空数组渲染，不需要额外的 null 判断。
func TestHandlePoolDeleteRecoveryStatus_NoRecords_ReturnsEmptyList(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)

	r := httptest.NewRequest(http.MethodGet, "/api/disk/pool/delete/recovery", nil)
	w := httptest.NewRecorder()
	handlePoolDeleteRecoveryStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"recoveries":[]`) {
		t.Errorf("body = %q, want recoveries:[]", body)
	}
	if !strings.Contains(body, `"count":0`) {
		t.Errorf("body = %q, want count:0", body)
	}
}

// ═══════════════════════════════════════════════════
// 本轮复查新增：数据库失败场景下的 fail-closed 行为。
// ═══════════════════════════════════════════════════

// TestRecordPoolDeleteStart_DBFailure_FailsClosed 验证数据库不可用时
// recordPoolDeleteStart 返回 error（而不是悄悄返回 id=0 或 panic）——调用方
// （pool.go）据此在任何不可逆命令之前拒绝删除。
func TestRecordPoolDeleteStart_DBFailure_FailsClosed(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	withBrokenConfigDB(t)

	id, err := recordPoolDeleteStart("single", "/dev/sdz", "disk-z", []string{"/dev/sdz"})
	if err == nil {
		t.Fatal("数据库不可用时 recordPoolDeleteStart 必须返回 error")
	}
	if id != 0 {
		t.Errorf("id = %d, want 0（写入失败不应返回非零 ID）", id)
	}
}

// TestUpdatePoolDeletePhase_DBFailure_ReturnsError 验证数据库不可用时
// updatePoolDeletePhase 返回 error（即使此时不可逆命令已经执行完毕，调用方
// 也必须知道阶段字段没能写入，不能悄悄假装成功）。
func TestUpdatePoolDeletePhase_DBFailure_ReturnsError(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)

	id, err := recordPoolDeleteStart("lvm", "/dev/vg_nas", "vg_nas", []string{"/dev/sdb"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}

	withBrokenConfigDB(t)

	if err := updatePoolDeletePhase(id, "vg_removed"); err == nil {
		t.Fatal("数据库不可用时 updatePoolDeletePhase 必须返回 error")
	}
}

// TestGetPoolDeleteRecoveries_DBFailure_FailsClosed 验证数据库不可用时
// GetPoolDeleteRecoveries 返回 (nil, error)——不能与"确实没有记录"的
// (nil, nil)/([]... , nil) 混淆，否则 handlePoolDeleteRecoveryStatus 会把
// "无法确认"误报成"一切正常"。
func TestGetPoolDeleteRecoveries_DBFailure_FailsClosed(t *testing.T) {
	withBrokenConfigDB(t)

	recs, err := GetPoolDeleteRecoveries()
	if err == nil {
		t.Fatal("数据库不可用时 GetPoolDeleteRecoveries 必须返回 error")
	}
	if recs != nil {
		t.Errorf("recs = %+v, want nil（出错时不应返回任何记录，避免被当成「零记录」）", recs)
	}
}

// TestHandlePoolDeleteRecoveryStatus_DBFailure_Returns500NotEmptyList 验证
// /api/disk/pool/delete/recovery 在数据库查询失败时返回 500 + 明确的"无法
// 确认"文案，而不是退化成看起来一切正常的空列表（这正是 parent review 指出
// 的 fail-open 问题：查询错误被当成"零记录"）。
func TestHandlePoolDeleteRecoveryStatus_DBFailure_Returns500NotEmptyList(t *testing.T) {
	withBrokenConfigDB(t)

	r := httptest.NewRequest(http.MethodGet, "/api/disk/pool/delete/recovery", nil)
	w := httptest.NewRecorder()
	handlePoolDeleteRecoveryStatus(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500；body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `"recoveries":[]`) {
		t.Errorf("body = %q, 查询失败时不应退化成空列表（会让管理员误以为没有被中断的删除）", body)
	}
	if !strings.Contains(body, "无法确认") {
		t.Errorf("body = %q, want 包含明确的\"无法确认\"文案", body)
	}
}

// TestHandlePoolDelete_DBFailure_AbortsBeforeAnyIrreversibleCommand 验证
// handlePoolDelete 在持久化恢复记录写入失败时，在真正执行任何不可逆命令
// （wipefs/vgremove/mdadm --stop）之前就已经中止——用 withStubExec 让
// wipefs 本身"本来会成功"，但数据库损坏应该让流程根本走不到那一步。
func TestHandlePoolDelete_DBFailure_AbortsBeforeAnyIrreversibleCommand(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeDeviceIdentity(t)
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdq"})
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: nil},
	})
	withBrokenConfigDB(t)

	form := deletePoolForm("single", "/dev/sdq", "disk-q")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（数据库不可用应拒绝删除）；body=%s", w.Body.String())
	}
	if stub.countCalls("/usr/sbin/wipefs") != 0 {
		t.Errorf("wipefs 被调用了 %d 次, want 0（持久化恢复记录写入失败必须在任何不可逆命令之前中止）", stub.countCalls("/usr/sbin/wipefs"))
	}
}

// ═══════════════════════════════════════════════════
// 本轮复查新增：设备身份采集失败场景下的 fail-closed 行为。
// ═══════════════════════════════════════════════════

// TestCaptureDeviceIdentities_NoUUIDNoSerial_FailsClosed 验证某个成员设备
// 既无 UUID 也无序列号时，captureDeviceIdentities 整体返回 error（不是
// "部分设备有身份就放行"）——这正是"身份不稳定"问题的核心修复点：如果放行，
// 恢复记录里对这个设备仍然只有裸路径，等于没解决问题。
func TestCaptureDeviceIdentities_NoUUIDNoSerial_FailsClosed(t *testing.T) {
	orig := captureDeviceIdentityFn
	t.Cleanup(func() { captureDeviceIdentityFn = orig })
	captureDeviceIdentityFn = func(devPath string) DeviceIdentity {
		if devPath == "/dev/sdb" {
			// sdc 有序列号，sdb 什么都没有——必须整体拒绝，不能因为 sdc
			// 有身份就只拒绝 sdb、放行 sdc。
			return DeviceIdentity{Path: devPath}
		}
		return DeviceIdentity{Path: devPath, Serial: "REAL-SERIAL-" + devPath}
	}

	ids, err := captureDeviceIdentities([]string{"/dev/sdb", "/dev/sdc"})
	if err == nil {
		t.Fatal("存在既无 UUID 也无序列号的设备时，captureDeviceIdentities 必须返回 error")
	}
	if ids != nil {
		t.Errorf("ids = %+v, want nil（fail-closed 不应返回部分结果）", ids)
	}
	if !strings.Contains(err.Error(), "/dev/sdb") {
		t.Errorf("err = %v, want 指明具体是哪个设备（/dev/sdb）无法建立身份", err)
	}
}

// TestCaptureDeviceIdentities_WWNOnlyIsNotEnough 验证"只有 WWN、没有 UUID
// 也没有序列号"仍然不满足身份要求——很多消费级盘本身没有 WWN，这里反过来
// 确认 WWN 不是身份判据的"救命稻草"：captureDeviceIdentity 本身不读取 WWN
// 作为 fail-closed 判据的一部分（只看 UUID/Serial），避免"有 WWN 就放行"
// 这种看似合理但未在设计里承诺的宽松判据悄悄生效。
func TestCaptureDeviceIdentities_WWNOnlyIsNotEnough(t *testing.T) {
	orig := captureDeviceIdentityFn
	t.Cleanup(func() { captureDeviceIdentityFn = orig })
	captureDeviceIdentityFn = func(devPath string) DeviceIdentity {
		return DeviceIdentity{Path: devPath, WWN: "0x5000c500abcdef12"}
	}

	if _, err := captureDeviceIdentities([]string{"/dev/sdb"}); err == nil {
		t.Fatal("只有 WWN、没有 UUID/Serial 时仍应 fail-closed 拒绝（设计上 WWN 不是必要身份判据）")
	}
}

// TestRecordPoolDeleteStart_IdentityCaptureFailure_AbortsBeforeDBWrite 验证
// 身份采集失败时 recordPoolDeleteStart 根本不会写入数据库（而不是先写了
// 一条只有裸路径的记录，再返回 error——那样恢复记录里仍然留着不可靠的
// 残留数据）。
func TestRecordPoolDeleteStart_IdentityCaptureFailure_AbortsBeforeDBWrite(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	orig := captureDeviceIdentityFn
	t.Cleanup(func() { captureDeviceIdentityFn = orig })
	captureDeviceIdentityFn = func(devPath string) DeviceIdentity {
		return DeviceIdentity{Path: devPath} // 既无 UUID 也无 Serial
	}

	id, err := recordPoolDeleteStart("single", "/dev/sdz", "disk-z", []string{"/dev/sdz"})
	if err == nil {
		t.Fatal("身份采集失败时 recordPoolDeleteStart 必须返回 error")
	}
	if id != 0 {
		t.Errorf("id = %d, want 0", id)
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 0 {
		t.Errorf("recs = %+v, want 空（身份采集失败不应留下任何记录，哪怕只有裸路径）", recs)
	}
}

// TestHandlePoolDelete_IdentityCaptureFailure_AbortsBeforeIrreversibleCommand
// 端到端验证：成员盘身份采集失败（例如设备刚被拔出、blkid/smartctl 都读不
// 到任何东西）时，handlePoolDelete 必须在 wipefs 等不可逆命令之前就中止，
// 不能"记录没写成也无所谓，反正磁盘层面还是删了"。
func TestHandlePoolDelete_IdentityCaptureFailure_AbortsBeforeIrreversibleCommand(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)
	withFakeLivePool(t, PoolSummary{Type: "single", Device: "/dev/sdq"})
	stub := withStubExec(t, map[string]cmdResponse{
		"findmnt":          {Out: "", Err: nil},
		"blkid":            {Out: "", Err: nil},
		"/usr/sbin/wipefs": {Out: "", Err: nil},
	})
	orig := captureDeviceIdentityFn
	t.Cleanup(func() { captureDeviceIdentityFn = orig })
	captureDeviceIdentityFn = func(devPath string) DeviceIdentity {
		return DeviceIdentity{Path: devPath} // 既无 UUID 也无 Serial，模拟设备已拔出
	}

	form := deletePoolForm("single", "/dev/sdq", "disk-q")
	r := httptest.NewRequest(http.MethodPost, "/api/disk/pool/delete", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handlePoolDelete(w, r)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want non-200（身份采集失败应拒绝删除）；body=%s", w.Body.String())
	}
	if stub.countCalls("/usr/sbin/wipefs") != 0 {
		t.Errorf("wipefs 被调用了 %d 次, want 0（身份采集失败必须在任何不可逆命令之前中止）", stub.countCalls("/usr/sbin/wipefs"))
	}
	if recs := mustGetPoolDeleteRecoveries(t); len(recs) != 0 {
		t.Errorf("recs = %+v, want 空", recs)
	}
}

// ═══════════════════════════════════════════════════
// 本轮复查新增：parentBlockDevice（分区→父盘解析）与 captureDeviceIdentity
// 绕过只读展示缓存（cachedSmartctl）这两个点的直接回归测试——此前这两处
// 修复只有源码注释，没有测试覆盖真实行为。
// ═══════════════════════════════════════════════════

// TestParentBlockDevice_PartitionResolvesToParent 验证分区路径（lsblk 返回
// 非空 PKNAME）被解析为 /dev/ 前缀 + PKNAME 的父盘路径。
func TestParentBlockDevice_PartitionResolvesToParent(t *testing.T) {
	orig := execOutputFn
	t.Cleanup(func() { execOutputFn = orig })
	execOutputFn = func(name string, args ...string) (string, error) {
		if name != "lsblk" {
			t.Fatalf("意外的命令: %s %v", name, args)
		}
		if len(args) != 3 || args[2] != "/dev/sdb1" {
			t.Fatalf("意外的参数: %v", args)
		}
		return "sdb\n", nil
	}

	got := parentBlockDevice("/dev/sdb1")
	if got != "/dev/sdb" {
		t.Errorf("parentBlockDevice(/dev/sdb1) = %q, want /dev/sdb", got)
	}
}

// TestParentBlockDevice_WholeDiskReturnsItself 验证整盘设备（lsblk PKNAME
// 返回空字符串，内核没有父设备）原样返回，不被错误地拼接成 "/dev/"。
func TestParentBlockDevice_WholeDiskReturnsItself(t *testing.T) {
	orig := execOutputFn
	t.Cleanup(func() { execOutputFn = orig })
	execOutputFn = func(name string, args ...string) (string, error) {
		return "", nil
	}

	got := parentBlockDevice("/dev/sdb")
	if got != "/dev/sdb" {
		t.Errorf("parentBlockDevice(/dev/sdb) = %q, want /dev/sdb（整盘设备应原样返回）", got)
	}
}

// TestParentBlockDevice_LsblkFailure_ReturnsOriginalPath 验证 lsblk 本身
// 执行失败（如命令不存在/设备已拔出）时保守返回原路径，交给上层真实的
// smartctl 调用去验证这个路径到底能不能读到序列号——不能在"父设备解析
// 失败"这一步就静默吞掉错误、伪造出一个看似合理的路径。
func TestParentBlockDevice_LsblkFailure_ReturnsOriginalPath(t *testing.T) {
	orig := execOutputFn
	t.Cleanup(func() { execOutputFn = orig })
	execOutputFn = func(name string, args ...string) (string, error) {
		return "", fmt.Errorf("lsblk: 设备不存在")
	}

	got := parentBlockDevice("/dev/sdb1")
	if got != "/dev/sdb1" {
		t.Errorf("parentBlockDevice 在 lsblk 失败时 = %q, want 原路径 /dev/sdb1", got)
	}
}

// TestCaptureDeviceIdentity_ResolvesPartitionToParentBeforeSerialQuery 验证
// captureDeviceIdentity 对分区路径采集序列号时，传给 smartctl -i 的是
// parentBlockDeviceFn 解析出的父盘路径，而不是分区路径本身——否则对
// LVM-on-partition 这类场景，smartctl 在分区节点上通常读不到任何序列号。
func TestCaptureDeviceIdentity_ResolvesPartitionToParentBeforeSerialQuery(t *testing.T) {
	origParent := parentBlockDeviceFn
	origSudoOutput := sudoOutputFn
	t.Cleanup(func() {
		parentBlockDeviceFn = origParent
		sudoOutputFn = origSudoOutput
	})

	parentBlockDeviceFn = func(devPath string) string {
		if devPath != "/dev/sdb1" {
			t.Fatalf("parentBlockDeviceFn 收到意外路径: %s", devPath)
		}
		return "/dev/sdb"
	}

	var smartctlCalledWith string
	sudoOutputFn = func(name string, args ...string) (string, error) {
		switch name {
		case "blkid":
			return "", fmt.Errorf("no uuid")
		case "smartctl":
			if len(args) < 2 || args[0] != "-i" {
				t.Fatalf("意外的 smartctl 参数: %v", args)
			}
			smartctlCalledWith = args[1]
			return "Serial Number: PARENT-DISK-SERIAL\n", nil
		case "lsblk":
			return "", nil
		}
		return "", nil
	}

	id := captureDeviceIdentity("/dev/sdb1")

	if smartctlCalledWith != "/dev/sdb" {
		t.Errorf("smartctl -i 被调用时传入的设备 = %q, want /dev/sdb（父盘路径，不是分区路径 /dev/sdb1）", smartctlCalledWith)
	}
	if id.Serial != "PARENT-DISK-SERIAL" {
		t.Errorf("id.Serial = %q, want PARENT-DISK-SERIAL", id.Serial)
	}
	if id.Path != "/dev/sdb1" {
		t.Errorf("id.Path = %q, want /dev/sdb1（Path 字段应保留原始采集路径，不应被父盘路径覆盖）", id.Path)
	}
}

// TestCaptureDeviceIdentity_IgnoresStaleReadOnlyDisplayCache 验证
// captureDeviceIdentity 不经过 cachedSmartctl（30s TTL 的只读展示缓存）——
// 预先向 smartctlCache 塞入一条"陈旧/错误"的序列号缓存项（模拟热插拔场景：
// 旧盘的序列号在 TTL 窗口内被缓存，新盘已经插入同一个 /dev/sdX 路径），
// 然后验证 captureDeviceIdentity 读到的是 sudoOutputFn 当次返回的"新盘"
// 序列号，而不是缓存里的旧值——这是本轮修复的核心安全属性：身份采集
// 这条安全关键路径必须永远是未缓存的实时查询。
func TestCaptureDeviceIdentity_IgnoresStaleReadOnlyDisplayCache(t *testing.T) {
	origSudoOutput := sudoOutputFn
	origParent := parentBlockDeviceFn
	t.Cleanup(func() {
		sudoOutputFn = origSudoOutput
		parentBlockDeviceFn = origParent
		// 清掉本用例塞入的缓存项，避免污染其它用例（即使其它用例目前
		// 不依赖 smartctlCache，仍按"用过的全局状态必须清理"的约定处理）。
		smartctlCache.Delete(strings.Join([]string{"-i", "/dev/sdb"}, "\x00"))
	})
	parentBlockDeviceFn = func(devPath string) string { return devPath }

	// 模拟"旧盘"的序列号已经被只读展示路径（getDiskSerial/cachedSmartctl）
	// 缓存住，且缓存仍在 30s TTL 有效期内。
	smartctlCache.Store(strings.Join([]string{"-i", "/dev/sdb"}, "\x00"), &smartctlCacheEntry{
		value:     "Serial Number: STALE-OLD-DISK-SERIAL\n",
		err:       nil,
		fetchedAt: time.Now(),
	})

	// 模拟热插拔之后，这个 /dev/sdb 路径上实际插着的是新盘——未缓存的
	// 实时 smartctl 查询应该读到新盘的序列号。
	sudoOutputFn = func(name string, args ...string) (string, error) {
		switch name {
		case "blkid":
			return "", fmt.Errorf("no uuid")
		case "smartctl":
			return "Serial Number: FRESH-NEW-DISK-SERIAL\n", nil
		case "lsblk":
			return "", nil
		}
		return "", nil
	}

	id := captureDeviceIdentity("/dev/sdb")

	if id.Serial != "FRESH-NEW-DISK-SERIAL" {
		t.Errorf("id.Serial = %q, want FRESH-NEW-DISK-SERIAL（必须绕开 cachedSmartctl，不能读到缓存里的 STALE-OLD-DISK-SERIAL）", id.Serial)
	}
}

// TestParseSmartctlSerial_ExtractsSerialNumberLine 验证解析逻辑本身：大小写
// 不敏感匹配 "serial number:" 行，提取冒号后内容并去除首尾空白；没有该行时
// 返回空字符串而不是 panic。
func TestParseSmartctlSerial_ExtractsSerialNumberLine(t *testing.T) {
	out := "Model Family:     Some Model\nSerial Number:    ABC123XYZ  \nUser Capacity:    1TB\n"
	if got := parseSmartctlSerial(out); got != "ABC123XYZ" {
		t.Errorf("parseSmartctlSerial = %q, want ABC123XYZ", got)
	}
	if got := parseSmartctlSerial("no matching line here"); got != "" {
		t.Errorf("parseSmartctlSerial(无匹配行) = %q, want 空字符串", got)
	}
}

// ═══════════════════════════════════════════════════
// 本轮复查新增：设备路径重编号（/dev/sdX 跨重启变化）场景下，恢复记录仍需
// 可靠——这是"身份不稳定"问题要解决的核心场景本身。
// ═══════════════════════════════════════════════════

// TestRecordPoolDeleteStart_MemberIdentities_SurvivesDevicePathReassignment
// 模拟内核在重启/热插拔后把同一块物理盘重新编号为不同的 /dev/sdX：验证
// 落盘的 MemberIdentities 里带有采集时刻的 UUID/Serial，不止是当时的路径
// ——核实时应该用 UUID/Serial 去匹配"重编号之后它现在叫什么"，而不是
// 盲目相信记录里的旧路径仍然指向同一块盘。
func TestRecordPoolDeleteStart_MemberIdentities_SurvivesDevicePathReassignment(t *testing.T) {
	clearAllPoolDeleteRecoveries(t)

	// 模拟采集时刻（重启前）：/dev/sdb 和 /dev/sdc 各有稳定的 UUID/Serial。
	orig := captureDeviceIdentityFn
	t.Cleanup(func() { captureDeviceIdentityFn = orig })
	fakeIdentities := map[string]DeviceIdentity{
		"/dev/sdb": {Path: "/dev/sdb", UUID: "uuid-disk-A", Serial: "SERIAL-DISK-A"},
		"/dev/sdc": {Path: "/dev/sdc", UUID: "uuid-disk-B", Serial: "SERIAL-DISK-B"},
	}
	captureDeviceIdentityFn = func(devPath string) DeviceIdentity {
		return fakeIdentities[devPath]
	}

	id, err := recordPoolDeleteStart("raid1", "/dev/md0", "md0", []string{"/dev/sdb", "/dev/sdc"})
	if err != nil {
		t.Fatalf("recordPoolDeleteStart 失败: %v", err)
	}
	if id == 0 {
		t.Fatal("want 非零记录 ID")
	}

	// 模拟进程崩溃、重启后再查询——这时即使内核把 /dev/sdb 重新编号成了
	// /dev/sdd（比如拔插了另一块盘），记录里的 UUID/Serial 依然不变，
	// 管理员可以拿这两个值去匹配"现在叫什么"，而不是直接假定 /dev/sdb
	// 还是原来那块盘。
	recs := mustGetPoolDeleteRecoveries(t)
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1", len(recs))
	}
	rec := recs[0]
	for _, want := range []string{"uuid-disk-A", "SERIAL-DISK-A", "uuid-disk-B", "SERIAL-DISK-B"} {
		if !strings.Contains(rec.MemberIdentities, want) {
			t.Errorf("MemberIdentities = %q, want 包含 %q（必须保留稳定身份，不能只有易变路径）", rec.MemberIdentities, want)
		}
	}

	// 核实 MemberIdentities 是可解析的 []DeviceIdentity JSON，而不仅仅是
	// "字符串里碰巧包含这些子串"——管理员端的恢复工具需要能反序列化它。
	var parsed []DeviceIdentity
	if err := json.Unmarshal([]byte(rec.MemberIdentities), &parsed); err != nil {
		t.Fatalf("MemberIdentities 不是合法 JSON: %v；raw=%q", err, rec.MemberIdentities)
	}
	if len(parsed) != 2 {
		t.Fatalf("len(parsed) = %d, want 2", len(parsed))
	}
	byUUID := map[string]DeviceIdentity{}
	for _, d := range parsed {
		byUUID[d.UUID] = d
	}
	if byUUID["uuid-disk-A"].Serial != "SERIAL-DISK-A" {
		t.Errorf("uuid-disk-A 对应记录 = %+v, want Serial=SERIAL-DISK-A", byUUID["uuid-disk-A"])
	}
	if byUUID["uuid-disk-B"].Serial != "SERIAL-DISK-B" {
		t.Errorf("uuid-disk-B 对应记录 = %+v, want Serial=SERIAL-DISK-B", byUUID["uuid-disk-B"])
	}
}
