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
