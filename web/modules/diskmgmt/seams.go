package diskmgmt

import (
	"time"

	"nas-panel/common"
)

// 流式写路径（handlePoolExtendStream / handleRAIDExpandStream 及其调用链）的
// 测试注入 seam。惯例同 raid_persist_test 的 stubRunner / filemgmt 的
// poolRootOverride、getFolderMetaFn：生产代码路径不变（默认值即原函数/原
// 路径/真实 time.Sleep），单测替换包级变量后 t.Cleanup 还原，不改任何生产
// 函数签名、不碰 common 包。

// sudoExecFn 默认指向 common.SudoExec（真实 sudo 命令执行）；测试注入替身以
// 控制 wipefs/pvcreate/vgextend/lvextend/mdadm --add/--grow/xfs_growfs/
// resize2fs 等写路径命令的输出与成败，并录制调用序列。
var sudoExecFn = common.SudoExec

// sudoOutputFn 默认指向 common.SudoOutput（真实 sudo 命令执行，只读查询类，
// 如 mdadm --detail）；测试注入替身。
var sudoOutputFn = common.SudoOutput

// execOutputFn 默认指向 common.ExecOutput（非 sudo 命令执行，如 findmnt/
// pvs/lsblk/cat /proc/mdstat）；测试注入替身，同时覆盖 isSystemDisk/
// isDiskInUse 两个安全检查函数的底层调用，无需为它们单独开 seam。
var execOutputFn = common.ExecOutput

// getDiskStatusFn 默认指向 getDiskStatus（lsblk 挂链）；测试注入替身直接
// 返回构造好的 []DiskStatus，绕开真实 lsblk 调用。
var getDiskStatusFn = getDiskStatus

// sleepFn 默认指向 time.Sleep；测试注入 no-op 替身以消除 stepDone/
// stepRunning 的 100ms 步进延迟与 waitForResync 的 2s 前置/轮询延迟。
var sleepFn = time.Sleep

// sysfsRoot 默认指向真实 "/sys/block"；readRAIDSysfsValues 据此拼接
// <sysfsRoot>/<mdName>/md。测试指向 t.TempDir() 下构造的假 sysfs 目录。
var sysfsRoot = "/sys/block"

// mdstatPath 默认指向真实 "/proc/mdstat"；waitForResync 与 isDiskInUse 的
// cat 回退读取路径均改用此变量。测试指向临时文件。
var mdstatPath = "/proc/mdstat"

// findLivePoolFn 默认指向 findLivePoolByTypeDevice 搭配 buildStorageOverview()
// 的生产组合（handlePoolDelete 的服务端重解析闸门,consensus P0-A1）。
// buildStorageOverview 依赖真实 df/lsblk/mdadm 等一整条只读探测链，在无实际
// 磁盘的测试环境里必然返回空池列表，导致该闸门恒 409、switch 分支内的破坏性
// 命令路径根本无法被测试触达。测试注入时直接返回构造好的 (PoolSummary, true)，
// 绕开 overview 探测链，只验证 handlePoolDelete 在闸门通过后的行为（Bug 2：
// 各步骤错误处理）。生产路径不变。
var findLivePoolFn = func(poolType, poolDevice string) (PoolSummary, bool) {
	overview := buildStorageOverview()
	return findLivePoolByTypeDevice(overview.Pools, poolType, poolDevice)
}

// safeWriteFileFn 默认指向 common.SafeWriteFile（Bug 3：GenerateSambaConfig/
// GenerateNFSConfig 写 smb.conf/exports 失败此前被直接丢弃返回值，现在必须
// 检查错误并向上传播）；测试注入替身模拟磁盘满/权限问题等写失败场景。
var safeWriteFileFn = common.SafeWriteFile

// mountQuotaSupportFn 默认指向 common.MountQuotaSupport（真实 findmnt 探测）；
// 测试注入替身避免依赖真实挂载点（Item 7 修复：quota_test.go 并发测试用
// /mnt/pool1 这样的假路径，真实 findmnt 会探测失败）。
var mountQuotaSupportFn = common.MountQuotaSupport

// validateBlockDeviceListFn 默认指向 common.ValidateBlockDeviceList；该函数
// 对每个设备路径做 os.Stat + ModeDevice 检查，要求是真实存在的块设备节点，
// 单测环境没有（也不应该创建）真实 /dev/sdX 节点。测试注入替身跳过存在性
// 检查，只用于验证 handlePoolCreate 在 mkdir/mount 等后续步骤失败时是否
// 正确中止（Item 1/4 测试覆盖）；生产路径仍是真实校验，不放宽任何检查。
var validateBlockDeviceListFn = common.ValidateBlockDeviceList

// syncAllConfigsFn 默认指向 SyncAllConfigs。SyncAllConfigs 本身依赖
// GetAllFolderMeta() 背后的真实全局 sqlite 单例连接（initConfigDB()），
// 无法在单测里构造假数据直接端到端跑通（同 config_sync_mutex_test.go 的
// 既有说明）。import.go/pending_ops.go/stream.go 三处 Bug 3 修复关心的是
// "SyncAllConfigs 失败时，调用方是否如实把失败信息传给用户"，不是
// SyncAllConfigs 内部逻辑本身（已有独立测试覆盖）。只给这三个调用点换成
// 这个 seam，可以注入一个假失败/假成功，单独验证三处的响应组装逻辑，不碰
// 真实 DB、不影响 pool.go/folders.go/reset.go 等其它 9 个调用点的生产路径。
var syncAllConfigsFn = SyncAllConfigs
