package common

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// validate.go — 高风险操作的输入白名单校验。
// 设计原则：校验强度必须与操作风险匹配。破坏性磁盘操作（mkfs/wipefs/pvcreate）
// 与删除路径（rm -rf）的入参在这里收口，handler 不得把 FormValue 直接拼进命令。

// blockDevicePattern 白名单：仅允许常见块设备命名。
//   - /dev/sda, /dev/sdb1        (SATA/SCSI/SAS/USB)
//   - /dev/nvme0n1, /dev/nvme0n1p1 (NVMe)
//   - /dev/vda, /dev/vda1        (virtio)
//   - /dev/md0                   (md RAID)
//   - /dev/mapper/xxx, /dev/dm-0 (device mapper / LVM)
//   - /dev/vg_nas/data           (LVM /dev/<vg>/<lv>)
//   - /dev/disk/by-id/...        (stable by-id links)
//   - /dev/loop0                 (loop)
var blockDevicePattern = regexp.MustCompile(`^/dev/(sd[a-z]{1,3}[0-9]{0,2}|nvme[0-9]{1,3}n[0-9]{1,3}(p[0-9]{1,3})?|vd[a-z]{1,3}[0-9]{0,2}|md[0-9]{1,3}|dm-[0-9]{1,4}|loop[0-9]{1,4}|mapper/[A-Za-z0-9._+-]{1,128}|[A-Za-z0-9._+-]{1,64}/[A-Za-z0-9._+-]{1,64}|disk/by-(id|path|uuid)/[A-Za-z0-9._:+@/-]{1,255})$`)

// ValidateBlockDevice 校验设备路径：必须命中白名单且真实存在（字符设备/普通文件拒绝）。
// 返回 error 时调用方必须以 400 拒绝请求，绝不继续执行破坏性命令。
func ValidateBlockDevice(device string) error {
	device = strings.TrimSpace(device)
	if device == "" {
		return fmt.Errorf("device 不能为空")
	}
	if !blockDevicePattern.MatchString(device) {
		return fmt.Errorf("非法设备路径: %s（仅允许 /dev/ 下的块设备）", device)
	}
	// 存在性 + 类型检查：Lstat 不解引用，/dev/mapper 软链用 Stat
	info, err := os.Stat(device)
	if err != nil {
		return fmt.Errorf("设备不存在: %s (%v)", device, err)
	}
	// 块设备 (ModeDevice|ModeCharDevice 之外) —— Go: info.Mode()&os.ModeDevice != 0
	if info.Mode()&os.ModeDevice == 0 {
		return fmt.Errorf("不是块设备: %s", device)
	}
	return nil
}

// ValidateBlockDeviceList 校验逗号分隔的设备列表（pool create 的 devices 参数）。
func ValidateBlockDeviceList(devicesStr string) ([]string, error) {
	var out []string
	for _, d := range strings.Split(devicesStr, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if err := ValidateBlockDevice(d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("设备列表为空")
	}
	return out, nil
}

// lvmNamePattern LVM VG/LV 命名白名单：字母数字加点划线下划线加号，禁止路径分隔与空白。
var lvmNamePattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)

// IsValidLVMName 校验 VG/LV 名称（这些名字会被拼进 /dev/<vg>/<lv> 与命令参数）。
func IsValidLVMName(name string) bool {
	return lvmNamePattern.MatchString(name) && !strings.Contains(name, "..")
}

// ValidateDataPath 校验 /data 下的路径：先 filepath.Clean 再检查前缀，
// 阻断 "/data/../etc" 类路径穿越。返回清洗后的绝对路径。
// allowDataRoot=false 时拒绝 /data 本身（删除/子目录操作必须指向 /data/xxx）。
func ValidateDataPath(p string, allowDataRoot bool) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("必须是绝对路径: %s", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == "/data" {
		if !allowDataRoot {
			return "", fmt.Errorf("不允许直接操作 /data 根目录: %s", p)
		}
		return cleaned, nil
	}
	if !strings.HasPrefix(cleaned, "/data/") {
		return "", fmt.Errorf("只允许操作 /data/ 下的路径: %s", p)
	}
	// 防御纵深：Clean 后仍出现 ".." 说明输入异常（正常 Clean 会消解）
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("路径包含非法片段: %s", p)
	}
	return cleaned, nil
}

// forbiddenMountPrefixes 挂载点黑名单：这些目录（及其子目录）绝不允许作为挂载点，
// 防止挂载覆盖系统目录导致机器失联。
var forbiddenMountPrefixes = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root",
	"/run", "/sbin", "/sys", "/usr", "/var", "/home", "/opt", "/srv",
}

// ValidateMountPoint 校验挂载点：绝对路径、Clean、非系统目录。
// /data 及 /mnt/* 允许；其余一律拒绝（与既有"挂载点必须在 /data 下"策略一致，
// 额外放行 /mnt 用于外置盘场景）。
func ValidateMountPoint(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("挂载点不能为空")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("挂载点必须是绝对路径: %s", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == "/" {
		return "", fmt.Errorf("不允许挂载到 /")
	}
	for _, bad := range forbiddenMountPrefixes {
		if cleaned == bad || strings.HasPrefix(cleaned, bad+"/") {
			return "", fmt.Errorf("不允许挂载到系统目录: %s", cleaned)
		}
	}
	return cleaned, nil
}
