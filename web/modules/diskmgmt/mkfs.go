package diskmgmt

// mkfsArgs 根据文件系统类型返回格式化参数：
// xfs 用 -f（force，xfs 的强制覆盖参数是小写 f，大写 -F 不是合法选项）；
// ext2/ext3/ext4 用 -F（force）；btrfs 用 -f（force，同 xfs）。
// 命令名固定为 "mkfs.<fstype>"，调用方拼接。
func mkfsArgs(fstype, device string) []string {
	switch fstype {
	case "xfs", "btrfs":
		return []string{"-f", device}
	case "ext2", "ext3", "ext4":
		return []string{"-F", device}
	default:
		// 未知文件系统类型：不假设任何 force 参数，只传设备路径，交给 mkfs 自身报错
		return []string{device}
	}
}
