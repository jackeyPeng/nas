package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"nas-panel/common"
)

// DiskStatus represents a single disk device
type DiskStatus struct {
	Device     string       `json:"device"` // /dev/sdb
	Name       string       `json:"name"`   // sdb
	Size       string       `json:"size"`   // 50G
	Type       string       `json:"type"`   // system, data, unused, pool_member
	FSType     string       `json:"fstype"` // xfs, ext4, btrfs, ""
	Mountpoint string       `json:"mountpoint"`
	Model      string       `json:"model"`
	Interface  string       `json:"interface"`       // SATA, NVMe, VirtIO, USB
	Rotational string       `json:"rotational"`      // 0=SSD, 1=HDD
	Temp       string       `json:"temp,omitempty"`  // 35°C
	Smart      string       `json:"smart,omitempty"` // PASSED/FAILED/unknown
	Serial     string       `json:"serial,omitempty"`
	UUID       string       `json:"uuid,omitempty"`
	Children   []DiskStatus `json:"children,omitempty"`
}

// detectInterface determines disk interface type from device name + sysfs
func detectInterface(devName string) string {
	// NVMe: nvme0n1, nvme1n2...
	if strings.HasPrefix(devName, "nvme") {
		return "NVMe"
	}
	// VirtIO: vda, vdb...
	if strings.HasPrefix(devName, "vd") {
		return "VirtIO"
	}
	// USB: usually starts with sd but has USB in sysfs
	usbPath := "/sys/block/" + devName + "/device/uevent"
	if data, err := os.ReadFile(usbPath); err == nil {
		uevent := string(data)
		if strings.Contains(uevent, "usb") {
			return "USB"
		}
	}
	// SD/SCSI: check sysfs for SATA/link info
	linkPath := "/sys/block/" + devName + "/device/sata_phy"
	if _, err := os.Stat(linkPath); err == nil {
		return "SATA"
	}
	// Fallback: check transport
	transPath := "/sys/block/" + devName + "/device/transport"
	if data, err := os.ReadFile(transPath); err == nil {
		t := strings.TrimSpace(string(data))
		if strings.Contains(t, "sata") || strings.Contains(t, "ata") {
			return "SATA"
		}
		if strings.Contains(t, "usb") {
			return "USB"
		}
	}
	// Default for sd* devices
	if strings.HasPrefix(devName, "sd") {
		return "SATA"
	}
	return "Unknown"
}

// smartctlCacheTTL 是 overview/list 等只读展示路径的 smartctl 缓存时长。
const smartctlCacheTTL = 30 * time.Second

// getDiskTemp reads disk temperature via smartctl
func getDiskTemp(devName string) string {
	// Only try for real disks (not partitions)
	if strings.HasPrefix(devName, "sd") || strings.HasPrefix(devName, "nvme") || strings.HasPrefix(devName, "vd") {
		out, err := cachedSmartctl(smartctlCacheTTL, "-A", "-d", "ata", "/dev/"+devName)
		if err != nil {
			// Try without ata flag for NVMe
			out2, err2 := cachedSmartctl(smartctlCacheTTL, "-A", "/dev/"+devName)
			if err2 != nil {
				return ""
			}
			out = out2
		}
		// Parse temperature: look for Temperature_Celsius or Temperature_NA
		for _, line := range strings.Split(out, "\n") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "temperature_celsius") || strings.Contains(lower, "temperature:") {
				fields := strings.Fields(line)
				if len(fields) >= 10 {
					// smartctl -A format: ID# ATTRIBUTE_NAME ... RAW_VALUE
					return fields[len(fields)-1] + "°C"
				}
				// NVMe format: Temperature: 35 Celsius
				for i, f := range fields {
					if strings.Contains(strings.ToLower(f), "celsius") && i > 0 {
						return fields[i-1] + "°C"
					}
				}
			}
		}
		// NVMe: look for line like "Temperature:                        35"
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "Temperature:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					return fields[len(fields)-1] + "°C"
				}
			}
		}
	}
	return ""
}

// getSmartStatus gets SMART health for a disk
func getSmartStatus(devName string) string {
	if !strings.HasPrefix(devName, "sd") && !strings.HasPrefix(devName, "nvme") && !strings.HasPrefix(devName, "vd") {
		return ""
	}
	out, err := cachedSmartctl(smartctlCacheTTL, "-H", "/dev/"+devName)
	if err != nil {
		return "unknown"
	}
	if strings.Contains(out, "PASSED") {
		return "PASSED"
	}
	if strings.Contains(out, "FAILED") {
		return "FAILED"
	}
	// NVMe may use "OK" instead
	if strings.Contains(out, "OK") {
		return "PASSED"
	}
	return "unknown"
}

// getDiskSerial reads disk serial number
func getDiskSerial(devName string) string {
	out, err := cachedSmartctl(smartctlCacheTTL, "-i", "/dev/"+devName)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "serial number:") {
			fields := strings.SplitN(line, ":", 2)
			if len(fields) == 2 {
				return strings.TrimSpace(fields[1])
			}
		}
	}
	return ""
}

// classifyChildPartition 推导子分区类型，并返回联动后的父盘类型（Fix 1a）。
// parentType 是推导前父盘的当前类型（整盘无 FSTYPE 时的默认 "unused"，
// 或挂载点匹配系统路径时已置的 "system"）。
//
// 规则：
//   - 子分区挂载点是系统路径（/、/boot、/boot/efi）→ 子/父都置 "system"。
//   - 子分区是 swap（含未挂载的 swap 分区）→ 子置 "swap"；父若已是 "system" 保持，
//     否则置 "data"（swap 签名说明整盘已被使用，不是空闲盘）。
//   - 子分区有挂载点（非 swap/系统路径）→ 子置 "data"；父若已是 "system" 保持，否则置 "data"。
//   - 子分区无挂载点但有 FSType（含未挂载的 xfs/ext4 等）→ 子置 "data"；父同上联动。
//     这是本次修复的核心：未挂载但带文件系统签名的分区 = 存量数据，不是空闲盘，
//     否则会被 overview.go 的 FreeDisks 收录，扩容/向导对其 wipefs 造成数据丢失。
//   - 子分区既无挂载点也无 FSType → 子置 "unused"，父类型不变。
//
// 已是 "system" 的父盘类型不会被本函数覆盖成 "data"（职责边界由调用方保证：
// parentType 传入时若是 "system"，本函数只会保持或不变，不会降级）。
func classifyChildPartition(childMountpoint, childFSType, parentType string) (childType, newParentType string) {
	newParentType = parentType
	switch {
	case childMountpoint == "/" || childMountpoint == "/boot" || childMountpoint == "/boot/efi":
		childType = "system"
		newParentType = "system"
	case childFSType == "swap":
		// swap 分区不改父盘类型为 swap — 系统盘上的 swap 不应把 system 覆盖成 data，
		// 但 swap 签名说明整盘不是空闲盘。
		childType = "swap"
		if newParentType != "system" {
			newParentType = "data"
		}
	case childMountpoint != "":
		childType = "data"
		// 只有非系统盘才覆盖成 data — 已是 system 的保持 system
		if newParentType != "system" {
			newParentType = "data"
		}
	case childFSType != "":
		// 有文件系统签名但未挂载 — 仍是存量数据分区，不是空闲盘（Fix 1a）
		childType = "data"
		if newParentType != "system" {
			newParentType = "data"
		}
	default:
		childType = "unused"
	}
	return childType, newParentType
}

// getDiskStatus returns structured disk overview
func getDiskStatus() []DiskStatus {
	out, err := common.ExecOutput("lsblk", "-J", "-o", "NAME,SIZE,TYPE,MOUNTPOINT,MODEL,ROTA,FSTYPE,UUID")
	if err != nil {
		return nil
	}

	var raw struct {
		Blockdevices []struct {
			Name       string      `json:"name"`
			Size       string      `json:"size"`
			Type       string      `json:"type"`
			Mountpoint interface{} `json:"mountpoint"`
			Model      interface{} `json:"model"`
			Rota       interface{} `json:"rota"`
			Fstype     interface{} `json:"fstype"`
			UUID       interface{} `json:"uuid"`
			Children   []struct {
				Name       string      `json:"name"`
				Size       string      `json:"size"`
				Type       string      `json:"type"`
				Mountpoint interface{} `json:"mountpoint"`
				Fstype     interface{} `json:"fstype"`
				UUID       interface{} `json:"uuid"`
			} `json:"children"`
		} `json:"blockdevices"`
	}

	if json.Unmarshal([]byte(out), &raw) != nil {
		return nil
	}

	var result []DiskStatus
	for _, dev := range raw.Blockdevices {
		rota := "1"
		if r, ok := dev.Rota.(bool); ok && !r {
			rota = "0"
		}
		ds := DiskStatus{
			Device:     "/dev/" + dev.Name,
			Name:       dev.Name,
			Size:       dev.Size,
			Rotational: rota,
			Interface:  detectInterface(dev.Name),
		}
		if m, ok := dev.Model.(string); ok {
			ds.Model = m
		}
		if f, ok := dev.Fstype.(string); ok {
			ds.FSType = f
		}
		if u, ok := dev.UUID.(string); ok {
			ds.UUID = u
		}
		if m, ok := dev.Mountpoint.(string); ok {
			ds.Mountpoint = m
		}

		// Determine disk type
		mp := ds.Mountpoint
		if mp == "/" || mp == "/boot" || mp == "/boot/efi" {
			ds.Type = "system"
		} else if mp != "" {
			ds.Type = "data"
		} else if ds.FSType != "" {
			ds.Type = "data"
		} else {
			ds.Type = "unused"
		}

		// Check children (partitions)
		for _, child := range dev.Children {
			childDS := DiskStatus{
				Device:    "/dev/" + child.Name,
				Name:      child.Name,
				Size:      child.Size,
				Interface: ds.Interface,
			}
			if f, ok := child.Fstype.(string); ok {
				childDS.FSType = f
			}
			if u, ok := child.UUID.(string); ok {
				childDS.UUID = u
			}
			if m, ok := child.Mountpoint.(string); ok {
				childDS.Mountpoint = m
			}
			childDS.Type, ds.Type = classifyChildPartition(childDS.Mountpoint, childDS.FSType, ds.Type)
			ds.Children = append(ds.Children, childDS)
		}

		// SMART, temperature, serial for real disks (not sr0/cdrom)
		if dev.Type == "disk" && dev.Name != "sr0" {
			ds.Smart = getSmartStatus(dev.Name)
			ds.Temp = getDiskTemp(dev.Name)
			ds.Serial = getDiskSerial(dev.Name)
		}

		result = append(result, ds)
	}
	return result
}

// handleDiskStatus returns structured disk overview as JSON
func handleDiskStatus(w http.ResponseWriter, r *http.Request) {
	disks := getDiskStatus()
	if disks == nil {
		common.JSONResponse(w, map[string]interface{}{"error": "无法获取磁盘信息"})
		return
	}
	common.JSONResponse(w, map[string]interface{}{"disks": disks})
}

// handleFstab returns current /etc/fstab content
func handleFstab(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		out, _ := common.SudoOutput("cat", "/etc/fstab")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(out))
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}

// isSystemDisk checks if device is a system disk
func isSystemDisk(device string) bool {
	out, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", device)
	mount := strings.TrimSpace(out)
	if mount == "/" || mount == "/boot" || mount == "/boot/efi" {
		return true
	}
	// Strip trailing digits to get base device, then check partitions
	// e.g. /dev/nvme0n1 → /dev/nvme0n, /dev/sda → /dev/sd
	baseDevice := device
	for len(baseDevice) > 0 && baseDevice[len(baseDevice)-1] >= '0' && baseDevice[len(baseDevice)-1] <= '9' {
		baseDevice = baseDevice[:len(baseDevice)-1]
	}
	// Check common partition naming: base+digit (sda1) and base+p+digit (nvme0n1p1)
	for i := 1; i <= 9; i++ {
		// Standard: sda1, sdb2
		partDev := fmt.Sprintf("%s%d", baseDevice, i)
		out2, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", partDev)
		if strings.TrimSpace(out2) == "/" || strings.TrimSpace(out2) == "/boot" || strings.TrimSpace(out2) == "/boot/efi" {
			return true
		}
		// NVMe: nvme0n1p1, nvme0n1p2
		partDevNvme := fmt.Sprintf("%sp%d", device, i)
		out3, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", partDevNvme)
		if strings.TrimSpace(out3) == "/" || strings.TrimSpace(out3) == "/boot" || strings.TrimSpace(out3) == "/boot/efi" {
			return true
		}
	}
	return false
}

// isDiskInUse checks if device is already an active pool member (LVM PV or RAID array member),
// or carries any filesystem/swap signature on itself or its partitions (Fix 1b).
// 扩容/替换盘的「新盘」必须是空闲盘；已挂进某个池/阵列的盘、或带存量数据分区签名的盘，
// 若被 wipefs 会造成数据丢失。
func isDiskInUse(device string) bool {
	// 1) 已是活跃 LVM PV（在某个 VG 里）
	pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", "--noheadings", "-o", "pv_name")
	for _, line := range strings.Split(pvsOut, "\n") {
		if strings.TrimSpace(line) == device {
			return true
		}
	}
	// 2) 已是活跃 RAID 数组成员（/proc/mdstat 的成员列表形如 "sdb[0]"）
	mdOut, _ := common.ExecOutput("cat", "/proc/mdstat")
	short := strings.TrimPrefix(device, "/dev/")
	for _, tok := range strings.Fields(mdOut) {
		if tok == short || strings.HasPrefix(tok, short+"[") {
			return true
		}
	}
	// 3) 设备自身或其任一分区带文件系统/swap 签名（整盘 FSTYPE 为空但子分区有存量数据，
	// 或整盘本身就有 FSTYPE，都算 in use）
	lsblkOut, _ := common.ExecOutput("lsblk", "-n", "-r", "-o", "NAME,FSTYPE", device)
	if hasFstypeSignature(lsblkOut) {
		return true
	}
	return false
}

// hasFstypeSignature 解析 `lsblk -n -r -o NAME,FSTYPE <device>` 的输出，判断设备自身
// 或其任一子分区是否带非空 FSTYPE（含 swap）。纯函数，供单测（Fix 1b）。
// lsblk -r 以空格分隔字段，每行一个设备/分区：NAME FSTYPE（FSTYPE 为空则该行只有 NAME）。
func hasFstypeSignature(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "" {
			return true
		}
	}
	return false
}

// handleDiskListFree returns disks that are unused/unformatted
func handleDiskListFree(w http.ResponseWriter, r *http.Request) {
	disks := getDiskStatus()
	if disks == nil {
		common.JSONResponse(w, map[string]interface{}{"free": []interface{}{}})
		return
	}
	var freeDisks []DiskStatus
	for _, d := range disks {
		if d.Type == "unused" {
			freeDisks = append(freeDisks, d)
		}
		for _, c := range d.Children {
			if c.Type == "unused" && c.FSType == "" {
				freeDisks = append(freeDisks, c)
			}
		}
	}
	common.JSONResponse(w, map[string]interface{}{"free": freeDisks})
}

// getDataDisks returns all non-system block devices (whole disks, not partitions).
// Includes sd*, nvme*, vd* devices. Excludes sr0, loop*, zram*, ram*.
func getDataDisks() []string {
	disks := getDiskStatus()
	var result []string
	for _, d := range disks {
		if d.Name == "sr0" || d.Name == "zram0" || strings.HasPrefix(d.Name, "loop") || strings.HasPrefix(d.Name, "ram") {
			continue
		}
		if isSystemDisk(d.Device) {
			continue
		}
		result = append(result, d.Device)
	}
	return result
}

// unused: ensure regexp is imported
var _ = regexp.MustCompile
