package diskmgmt

import (
	"fmt"
	"strings"

	"nas-panel/common"
)

// growFSMountPoint 根据块设备解析其当前挂载点，用于 xfs_growfs。
// Debian 12/13 的 xfsprogs 要求 xfs_growfs 的参数是挂载点路径，传块设备会直接报错；
// 之前各处直接把块设备/LV 路径传给 xfs_growfs，在生产环境必定失败。
func growFSMountPoint(dev string) (string, error) {
	out, err := common.SudoOutput("findmnt", "-n", "-o", "TARGET", "--source", dev)
	if err != nil {
		return "", fmt.Errorf("查询 %s 挂载点失败: %v", dev, err)
	}
	return parseMountPointOutput(dev, out)
}

// parseMountPointOutput 是 growFSMountPoint 的纯函数部分：解析 findmnt 的输出行，
// 空输出（设备未挂载）返回明确错误而不是空字符串。
func parseMountPointOutput(dev, findmntOut string) (string, error) {
	mp := strings.TrimSpace(findmntOut)
	if mp == "" {
		return "", fmt.Errorf("设备未挂载，无法扩展文件系统: %s", dev)
	}
	return mp, nil
}

// growFSFsType 查询设备当前挂载的文件系统类型（用于 resize2fs fallback 判定是否确实是 ext4）。
func growFSFsType(dev string) string {
	out, _ := common.SudoOutput("findmnt", "-n", "-o", "FSTYPE", "--source", dev)
	return strings.TrimSpace(out)
}

// growFilesystemByDevice 对块设备/LV 所在的文件系统执行扩容：
// xfs → 解析挂载点后对挂载点执行 xfs_growfs；ext4 → 对设备执行 resize2fs。
// 返回供上层展示的操作描述和原始命令输出；xfs_growfs 失败时把真实报错透传，不做静默 resize2fs 兜底
// （resize2fs 只在确认文件系统是 ext4 时才允许作为路径，避免对 xfs 设备误用导致进一步损坏）。
func growFilesystemByDevice(dev string) (step string, out string, err error) {
	fstype := growFSFsType(dev)
	switch fstype {
	case "xfs":
		mp, mpErr := growFSMountPoint(dev)
		if mpErr != nil {
			return "", "", mpErr
		}
		out, err = common.SudoExec("/usr/sbin/xfs_growfs", mp)
		if err != nil {
			return "", out, fmt.Errorf("xfs_growfs %s 失败: %s: %v", mp, out, err)
		}
		return "xfs_growfs " + mp, out, nil
	case "ext4", "ext3", "ext2":
		out, err = common.SudoExec("/usr/sbin/resize2fs", dev)
		if err != nil {
			return "", out, fmt.Errorf("resize2fs %s 失败: %s: %v", dev, out, err)
		}
		return "resize2fs " + dev, out, nil
	default:
		return "", "", fmt.Errorf("未知或不支持扩容的文件系统类型 %q（设备 %s）", fstype, dev)
	}
}
