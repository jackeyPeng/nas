package backup

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nas-panel/common"
)

// TestHandleBackupDataFailureEmitsEvent 验证数据备份失败路径（生产机无
// /opt/nas/scripts/backup-data.sh，SudoExec 必然失败）会发出
// backup.data_failed 事件（F2 Task 3 生产方补点）。成功路径（backup.go
// 既有 backup.data_done）本轮未改动，这里只覆盖失败分支的新事件。
func TestHandleBackupDataFailureEmitsEvent(t *testing.T) {
	common.InitEventBusForTest(t.TempDir())
	t.Cleanup(common.CloseEventBusForTest)

	// 白名单前缀改指向临时目录：测试环境里 /mnt、/data 对普通用户不可写，
	// validateBackupTarget 的 MkdirAll 会先于脚本执行失败，掩盖本测试要
	// 覆盖的"脚本不存在→500"分支。
	origPrefixes := backupTargetPrefixes
	backupTargetPrefixes = []string{t.TempDir()}
	t.Cleanup(func() { backupTargetPrefixes = origPrefixes })

	ch := common.Subscribe()
	defer common.Unsubscribe(ch)

	form := url.Values{"target": {backupTargetPrefixes[0]}}
	req := httptest.NewRequest("POST", "/api/backup/data", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handleBackupData(rec, req)

	if rec.Code != 500 {
		t.Fatalf("沙箱无 backup-data.sh，期望 500，got %d body=%s", rec.Code, rec.Body.String())
	}

	common.DrainEventsForTest()
	select {
	case ev := <-ch:
		if ev.Code != "backup.data_failed" {
			t.Fatalf("want code=backup.data_failed, got %s", ev.Code)
		}
		if ev.Type != common.EventError {
			t.Fatalf("want type=error, got %s", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到 backup.data_failed 事件")
	}
}
