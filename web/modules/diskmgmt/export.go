package diskmgmt

import "errors"

// OverviewData 导出存储总览内核，供 AI 工具域（get_storage_overview）等
// 进程内调用方直接复用。薄导出层，逻辑全部在 buildStorageOverview 里
// （由 handleStorageOverview 的既有构造体机械抽取而来，行为不变）。
func OverviewData() StorageOverview {
	return buildStorageOverview()
}

// SmartSnapshot 导出磁盘 SMART/温度/序列号快照，供 AI 工具域
// （get_smart_status）等进程内调用方直接复用，走既有 smartctl 缓存
// （smartctlCacheTTL，见 storage.go）。薄导出层，逻辑全部在 getDiskStatus 里。
func SmartSnapshot() []DiskStatus {
	return getDiskStatus()
}

// CreateFolder 导出创建共享文件夹内核，供 AI 工具域（create_folder）等
// 进程内调用方直接复用，不走 HTTP 自环。薄导出层，逻辑全部在
// createFolderKernel 里（= handleCreateFolder 的内核，valid_users/
// recycle_bin/nfs 取表单路径的默认值，quotaGB<=0 表示不设配额）。
func CreateFolder(pool, name, permission string, quotaGB int) error {
	return createFolderKernel(pool, name, permission, quotaGB)
}

// ErrPoolNotExist 表示尚未创建存储池（vgs 无输出）——触发规则据此静默
// 跳过而非当作失败处理，调用方用 errors.Is 判定。
var ErrPoolNotExist = errors.New("存储池不存在")

// PoolUsage 导出存储池用量内核（F2 Task 3），供 triggers 模块的水位 cron
// 只读调用：池不存在时返回 ErrPoolNotExist（调用方据此静默跳过，不是
// 失败语义）。薄导出层，逻辑机械抽取自 handlePoolStatus 的 VG 用量计算
// 分支（只读 vgs 查询 + parseFloatSafe 换算，不改既有行为）。
func PoolUsage() (totalGB, usedGB float64, usePercent int, err error) {
	return poolUsageKernel()
}
