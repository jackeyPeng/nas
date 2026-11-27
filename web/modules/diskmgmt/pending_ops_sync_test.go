package diskmgmt

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════════════
// Bug 3 回归测试：ApplyPendingOps 此前把 SyncAllConfigs() 的错误直接丢弃
// （return results, nil），队列里的每条操作即使全部执行成功，只要收尾的
// Samba/NFS 托管配置重投影失败，调用方（handlePendingApply → 前端）也完全
// 看不出来——"配置已应用"响应照常返回 200，用户以为共享已按新配置生效。
//
// 通过 syncAllConfigsFn seam 注入假失败/假成功，驱动一条真实的 update 类
// PendingOp（只触碰 findFolderMeta/SyncFolderMeta，不调用 common.SudoExec/
// ApplyHomeACL，不需要额外的 sudoExecFn 桩），验证：
//   1. 队列已清空、op 执行成功的 results 不因 sync 失败而丢失；
//   2. sync 失败时 ApplyPendingOps 返回非 nil error；
//   3. sync 成功时不返回 error。
// NAS_DATA_DIR 由 TestMain（main_test.go）固定到一次性临时目录，folders.db/
// pending_ops 表都是本进程临时文件，不碰真实系统路径。
// ═══════════════════════════════════════════════════════════

func withStubSyncAllConfigs(t *testing.T, err error) {
	t.Helper()
	orig := syncAllConfigsFn
	syncAllConfigsFn = func() error { return err }
	t.Cleanup(func() { syncAllConfigsFn = orig })
}

func newApplyRequest(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodPost, "/api/disk/pending/apply", strings.NewReader(""))
}

func seedUpdatePendingOp(t *testing.T) {
	t.Helper()
	// 先落地一条现有 folder 元数据，供 update 操作的 findFolderMeta 命中。
	SyncFolderMeta(FolderMeta{
		Name: "docs", Path: "/data/pool1/docs", Pool: "pool1",
		Permission: "readwrite", SambaShare: true,
	})
	if err := AddPendingOp(PendingOp{
		Action: "update", FolderName: "docs", FolderPath: "/data/pool1/docs",
		Pool: "pool1", Permission: "readonly",
	}); err != nil {
		t.Fatalf("AddPendingOp 失败: %v", err)
	}
}

func TestApplyPendingOps_SyncFailure_PropagatesErrorButKeepsResults(t *testing.T) {
	seedUpdatePendingOp(t)
	withStubSyncAllConfigs(t, fmt.Errorf("exportfs -a 执行失败: 模拟磁盘故障"))

	results, err := ApplyPendingOps(newApplyRequest(t))

	if err == nil {
		t.Fatal("SyncAllConfigs 失败时 ApplyPendingOps 应返回非 nil error，got nil")
	}
	if !strings.Contains(err.Error(), "同步 Samba/NFS 托管配置失败") {
		t.Errorf("error 应说明是同步阶段失败，got: %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0], "docs") {
		t.Fatalf("op 执行成功的 results 不应因 sync 失败而丢失，got: %v", results)
	}
	if PendingCount() != 0 {
		t.Error("队列应已清空（op 本身已落地，不因收尾同步失败而回滚/重放）")
	}
}

func TestApplyPendingOps_SyncSuccess_NoError(t *testing.T) {
	seedUpdatePendingOp(t)
	withStubSyncAllConfigs(t, nil)

	results, err := ApplyPendingOps(newApplyRequest(t))

	if err != nil {
		t.Fatalf("SyncAllConfigs 成功时不应返回 error，got: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("期望 1 条 results，got: %v", results)
	}
}

func TestApplyPendingOps_EmptyQueue_NoSyncCall(t *testing.T) {
	// 队列为空时 ApplyPendingOps 提前返回 (nil, nil)，不应触及 syncAllConfigsFn
	// （与 handlePendingApply 对"无待操作"场景的既有语义一致）。
	called := false
	orig := syncAllConfigsFn
	syncAllConfigsFn = func() error { called = true; return nil }
	t.Cleanup(func() { syncAllConfigsFn = orig })

	// 确保队列确实为空（之前用例可能遗留，这里显式清空）
	DiscardPendingOps()

	results, err := ApplyPendingOps(newApplyRequest(t))
	if err != nil || results != nil {
		t.Fatalf("空队列应返回 (nil, nil)，got (%v, %v)", results, err)
	}
	if called {
		t.Error("空队列不应调用 syncAllConfigsFn")
	}
}
