package diskmgmt

import (
	"fmt"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════════════
// Item 3 回归测试：executeCreateFolder/executeUpdateFolder/executeDeleteFolder
// 此前把 mkdir/chown/SyncFolderMeta/RemoveFolderMeta/rm 的错误直接丢弃，
// ApplyPendingOps 对失败的操作仍打 "✓" 并清出队列——元数据/磁盘/用户预期
// 三者可能已经不一致，却没有任何可见信号。现在任一步骤失败都必须让
// results 里出现 "✗ ..." 且包含真实错误原因，不能假装成功。
// ═══════════════════════════════════════════════════════════

func withStubSudoExecFail(t *testing.T, failCmd string, errMsg string) {
	t.Helper()
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		if name == failCmd {
			return "", fmt.Errorf("%s", errMsg)
		}
		return orig(name, args...)
	}
	t.Cleanup(func() { sudoExecFn = orig })
}

func withStubSudoExecNoop(t *testing.T) {
	t.Helper()
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) { return "", nil }
	t.Cleanup(func() { sudoExecFn = orig })
}

// executeCreateFolder: mkdir 失败必须整体失败，且不应继续走 chown/SyncFolderMeta。
func TestExecuteCreateFolder_MkdirFailure_ReturnsError(t *testing.T) {
	withStubSudoExecFail(t, "mkdir", "mkdir: 磁盘已满")

	err := executeCreateFolder(PendingOp{
		Action: "create", FolderName: "docfail1", FolderPath: "/data/poolX/docfail1",
		Pool: "poolX", Permission: "readwrite", SambaShare: true,
	})

	if err == nil {
		t.Fatal("mkdir 失败时 executeCreateFolder 应返回 error，got nil")
	}
	if !strings.Contains(err.Error(), "创建目录失败") {
		t.Errorf("error 应说明是创建目录阶段失败，got: %v", err)
	}

	// 元数据不应被写入——否则就是"目录没建成却声称已建成"的假成功
	for _, m := range GetAllFolderMeta() {
		if m.Path == "/data/poolX/docfail1" {
			t.Errorf("mkdir 失败后不应写入元数据，got: %+v", m)
		}
	}
}

// executeCreateFolder: chown 失败必须整体失败（目录已建但属主未设置，
// 不能把这当成功吞掉）。
func TestExecuteCreateFolder_ChownFailure_ReturnsError(t *testing.T) {
	withStubSudoExecFail(t, "chown", "chown: Operation not permitted")

	err := executeCreateFolder(PendingOp{
		Action: "create", FolderName: "docfail2", FolderPath: "/data/poolX/docfail2",
		Pool: "poolX", Permission: "readwrite", SambaShare: true,
	})

	if err == nil {
		t.Fatal("chown 失败时 executeCreateFolder 应返回 error，got nil")
	}
	if !strings.Contains(err.Error(), "设置属主失败") {
		t.Errorf("error 应说明是设置属主阶段失败，got: %v", err)
	}
	for _, m := range GetAllFolderMeta() {
		if m.Path == "/data/poolX/docfail2" {
			t.Errorf("chown 失败后不应写入元数据，got: %+v", m)
		}
	}
}

// executeCreateFolder: 全部命令成功时元数据应正确写入（正常路径不受影响）。
func TestExecuteCreateFolder_Success_WritesMetadata(t *testing.T) {
	withStubSudoExecNoop(t)

	err := executeCreateFolder(PendingOp{
		Action: "create", FolderName: "docok1", FolderPath: "/data/poolX/docok1",
		Pool: "poolX", Permission: "readwrite", SambaShare: true,
	})
	if err != nil {
		t.Fatalf("预期成功，got error: %v", err)
	}
	found := false
	for _, m := range GetAllFolderMeta() {
		if m.Path == "/data/poolX/docok1" {
			found = true
		}
	}
	if !found {
		t.Error("成功路径应写入元数据")
	}
}

// executeDeleteFolder: rm -rf 失败时不应清除元数据（否则目录还在磁盘上，
// 却从元数据/前端视角"消失"，变成无主孤儿目录）。
func TestExecuteDeleteFolder_RmFailure_KeepsMetadata(t *testing.T) {
	path := "/data/poolX/delfail1"
	if err := SyncFolderMeta(FolderMeta{
		Name: "delfail1", Path: path, Pool: "poolX",
		Permission: "readwrite", SambaShare: true,
	}); err != nil {
		t.Fatalf("预置元数据失败: %v", err)
	}

	withStubSudoExecFail(t, "rm", "rm: target is busy")

	err := executeDeleteFolder(PendingOp{Action: "delete", FolderName: "delfail1", FolderPath: path})

	if err == nil {
		t.Fatal("rm 失败时 executeDeleteFolder 应返回 error，got nil")
	}
	if !strings.Contains(err.Error(), "删除目录失败") {
		t.Errorf("error 应说明是删除目录阶段失败，got: %v", err)
	}

	found := false
	for _, m := range GetAllFolderMeta() {
		if m.Path == path {
			found = true
		}
	}
	if !found {
		t.Error("rm 失败后元数据应保留（目录还在磁盘上，不能让它变孤儿）")
	}
}

// executeDeleteFolder: 全部成功时元数据应被清除（正常路径不受影响）。
func TestExecuteDeleteFolder_Success_RemovesMetadata(t *testing.T) {
	path := "/data/poolX/delok1"
	if err := SyncFolderMeta(FolderMeta{
		Name: "delok1", Path: path, Pool: "poolX",
		Permission: "readwrite", SambaShare: true,
	}); err != nil {
		t.Fatalf("预置元数据失败: %v", err)
	}
	withStubSudoExecNoop(t)

	err := executeDeleteFolder(PendingOp{Action: "delete", FolderName: "delok1", FolderPath: path})
	if err != nil {
		t.Fatalf("预期成功，got error: %v", err)
	}
	for _, m := range GetAllFolderMeta() {
		if m.Path == path {
			t.Error("成功路径应清除元数据")
		}
	}
}

// ApplyPendingOps 端到端：create 操作的 mkdir 失败时，results 必须显示
// "✗"（而不是 "✓"），用户能看到真实失败原因，不是假成功。
func TestApplyPendingOps_CreateMkdirFailure_ReportsFailureNotFakeSuccess(t *testing.T) {
	DiscardPendingOps()
	withStubSyncAllConfigs(t, nil)
	withStubSudoExecFail(t, "mkdir", "mkdir: 磁盘已满")

	if err := AddPendingOp(PendingOp{
		Action: "create", FolderName: "e2efail1", FolderPath: "/data/poolX/e2efail1",
		Pool: "poolX", Permission: "readwrite", SambaShare: true,
	}); err != nil {
		t.Fatalf("AddPendingOp 失败: %v", err)
	}

	results, err := ApplyPendingOps(newApplyRequest(t))
	if err != nil {
		t.Fatalf("顶层不应返回 error（SyncAllConfigs 已 stub 成功），got: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条 results，got: %v", results)
	}
	if strings.HasPrefix(results[0], "✓") {
		t.Fatalf("mkdir 失败时不应报告 ✓ 假成功，got: %q", results[0])
	}
	if !strings.Contains(results[0], "✗") || !strings.Contains(results[0], "磁盘已满") {
		t.Errorf("results 应包含 ✗ 与真实失败原因，got: %q", results[0])
	}

	for _, m := range GetAllFolderMeta() {
		if m.Path == "/data/poolX/e2efail1" {
			t.Errorf("mkdir 失败后不应写入元数据，got: %+v", m)
		}
	}

	// 问题 5 回归：失败的 op 必须留在队列里等待重试，不能被无条件 DELETE
	// FROM pending_ops 一起清掉——否则这条操作从此永久消失，用户只能回到
	// UI 重新手填一遍。
	remaining := GetPendingOps()
	if len(remaining) != 1 || remaining[0].FolderName != "e2efail1" {
		t.Fatalf("mkdir 失败的 op 应保留在队列中以便重试，got: %+v", remaining)
	}
}

// 问题 5 回归：一个队列里既有会失败的 op，也有会成功的 op，
// ApplyPendingOps 结束后只应清掉成功的那条，失败的那条必须留在
// pending_ops 表里（而不是被结尾那句无条件 DELETE FROM pending_ops 一并冲掉）。
func TestApplyPendingOps_MixedResult_RetainsOnlyFailedOp(t *testing.T) {
	DiscardPendingOps()
	withStubSyncAllConfigs(t, nil)
	withStubSudoExecFail(t, "mkdir", "mkdir: 磁盘已满")

	if err := AddPendingOp(PendingOp{
		Action: "create", FolderName: "mixfail1", FolderPath: "/data/poolX/mixfail1",
		Pool: "poolX", Permission: "readwrite", SambaShare: true,
	}); err != nil {
		t.Fatalf("AddPendingOp(mixfail1) 失败: %v", err)
	}
	// update 分支不经过 sudoExecFn("mkdir"...)，不受上面的失败桩影响，应当成功。
	seedUpdatePendingOp(t)

	results, err := ApplyPendingOps(newApplyRequest(t))
	if err != nil {
		t.Fatalf("顶层不应返回 error，got: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("期望 2 条 results，got: %v", results)
	}

	remaining := GetPendingOps()
	if len(remaining) != 1 {
		t.Fatalf("应只保留失败的那条 op，got %d 条: %+v", len(remaining), remaining)
	}
	if remaining[0].FolderName != "mixfail1" {
		t.Errorf("保留的应是失败的 mixfail1，got: %+v", remaining[0])
	}
}

// 问题 5 回归：第二次 ApplyPendingOps 不应重新执行第一次已经成功过的 op
// （因为它的行已经从 pending_ops 删除），只应重试仍在队列里的那条。
func TestApplyPendingOps_SecondApply_DoesNotReexecuteSucceededOp(t *testing.T) {
	DiscardPendingOps()
	withStubSyncAllConfigs(t, nil)

	// 第一轮：mkdir 对 retry2 失败，对 ok2 成功。用显式 save/restore 而非
	// withStubSudoExecFail（它的 t.Cleanup 要等整个测试结束才执行，第一轮
	// 结束后必须立刻恢复成"成功"桩，否则第二轮会继续调用第一轮的失败桩）。
	realSudoExecFn := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		if name == "mkdir" {
			return "", fmt.Errorf("mkdir: 磁盘已满")
		}
		return realSudoExecFn(name, args...)
	}
	if err := AddPendingOp(PendingOp{
		Action: "create", FolderName: "retry2", FolderPath: "/data/poolX/retry2",
		Pool: "poolX", Permission: "readwrite", SambaShare: true,
	}); err != nil {
		t.Fatalf("AddPendingOp(retry2) 失败: %v", err)
	}
	seedUpdatePendingOp(t) // ok2: 走 update 分支，成功

	if _, err := ApplyPendingOps(newApplyRequest(t)); err != nil {
		t.Fatalf("第一轮顶层不应返回 error，got: %v", err)
	}
	if n := PendingCount(); n != 1 {
		t.Fatalf("第一轮后应只剩 1 条（retry2），got %d", n)
	}

	// 第二轮：mkdir 恢复成功（委托回真实实现），统计调用次数以确认 update
	// 分支（已成功过的那条）不会被再次执行。
	mkdirCalls := 0
	sudoExecFn = func(name string, args ...string) (string, error) {
		if name == "mkdir" {
			mkdirCalls++
		}
		return realSudoExecFn(name, args...)
	}
	t.Cleanup(func() { sudoExecFn = realSudoExecFn })

	results, err := ApplyPendingOps(newApplyRequest(t))
	if err != nil {
		t.Fatalf("第二轮顶层不应返回 error，got: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("第二轮应只处理剩下的 1 条（retry2），got: %v", results)
	}
	if mkdirCalls != 1 {
		t.Errorf("第二轮应恰好调用一次 mkdir（仅 retry2），got %d 次", mkdirCalls)
	}
	if n := PendingCount(); n != 0 {
		t.Errorf("第二轮 retry2 成功后队列应清空，got %d 条剩余", n)
	}
}
