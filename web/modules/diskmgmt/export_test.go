package diskmgmt

import "testing"

// TestOverviewDataDoesNotPanic 验证 OverviewData() 在开发机（无 lsblk/
// smartctl/samba 等真实存储环境）下不 panic。buildStorageOverview 对各数据
// 源的失败路径都是静默跳过/置空，返回的 StorageOverview 在开发机上多半是
// 空池、空盘的零值形态，这里只断言调用安全。
func TestOverviewDataDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OverviewData() panicked: %v", r)
		}
	}()
	_ = OverviewData()
}

// TestSmartSnapshotDoesNotPanic 验证 SmartSnapshot() 不 panic；lsblk 不存在
// 或解析失败时 getDiskStatus 返回 nil，是合法的"空值"兜底。
func TestSmartSnapshotDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SmartSnapshot() panicked: %v", r)
		}
	}()
	_ = SmartSnapshot()
}
