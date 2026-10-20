package diskmgmt

import "strconv"

// partitionDevice 根据磁盘设备路径和分区号拼出分区设备路径。
// 普通磁盘（sda、vda）末字符是字母 → 直接拼数字（sda → sda1）。
// NVMe/loop 等设备（nvme0n1、loop0）末字符是数字 → 需要插入 "p" 避免歧义
// （nvme0n11 无法区分是 nvme0n1 的第 1 个分区还是 nvme0n11 本身），拼成 nvme0n1p1。
func partitionDevice(dev string, n int) string {
	if dev == "" {
		return dev
	}
	last := dev[len(dev)-1]
	if last >= '0' && last <= '9' {
		return dev + "p" + strconv.Itoa(n)
	}
	return dev + strconv.Itoa(n)
}
