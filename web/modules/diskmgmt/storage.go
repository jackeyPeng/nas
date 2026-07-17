package diskmgmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"nas-panel/common"
)

// DiskStatus represents a single disk device
type DiskStatus struct {
	Device     string       `json:"device"`         // /dev/sdb
	Name       string       `json:"name"`           // sdb
	Size       string       `json:"size"`           // 50G
	Type       string       `json:"type"`           // system, data, unused, pool_member
	FSType     string       `json:"fstype"`         // xfs, ext4, btrfs, ""
	Mountpoint string       `json:"mountpoint"`
	Model      string       `json:"model"`
	Interface  string       `json:"interface"`     // SATA, NVMe, VirtIO, USB
	Rotational string       `json:"rotational"`    // 0=SSD, 1=HDD
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

// getDiskTemp reads disk temperature via smartctl
func getDiskTemp(devName string) string {
	// Only try for real disks (not partitions)
	if strings.HasPrefix(devName, "sd") || strings.HasPrefix(devName, "nvme") || strings.HasPrefix(devName, "vd") {
		out, err := common.SudoOutput("smartctl", "-A", "-d", "ata", "/dev/"+devName)
		if err != nil {
			// Try without ata flag for NVMe
			out2, err2 := common.SudoOutput("smartctl", "-A", "/dev/"+devName)
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
	out, err := common.SudoOutput("smartctl", "-H", "/dev/"+devName)
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
	out, err := common.SudoOutput("smartctl", "-i", "/dev/"+devName)
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
			if childDS.Mountpoint == "/" || childDS.Mountpoint == "/boot" || childDS.Mountpoint == "/boot/efi" {
				childDS.Type = "system"
				ds.Type = "system"
			} else if childDS.Mountpoint != "" {
				childDS.Type = "data"
				ds.Type = "data"
			} else if childDS.FSType != "" {
				childDS.Type = "data"
			} else {
				childDS.Type = "unused"
			}
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

// handleQuickSetup does: format + mkdir + mount + fstab + chown
func handleQuickSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	device := r.FormValue("device")
	mountpoint := r.FormValue("mountpoint")
	fstype := r.FormValue("fstype")
	confirm := r.FormValue("confirm")
	autoSamba := r.FormValue("samba")

	if device == "" || mountpoint == "" {
		http.Error(w, `{"error": "device and mountpoint required"}`, http.StatusBadRequest)
		return
	}
	if fstype == "" {
		fstype = "xfs"
	}
	if confirm != "yes" {
		http.Error(w, `{"error": "请加 confirm=yes 确认操作"}`, http.StatusBadRequest)
		return
	}
	if isSystemDisk(device) {
		http.Error(w, `{"error": "不允许操作系统盘"}`, http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(mountpoint, "/data/") && mountpoint != "/data" {
		http.Error(w, `{"error": "挂载点必须在 /data/ 下"}`, http.StatusBadRequest)
		return
	}

	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}

	var steps []string

	out, err := common.SudoExec("mkfs."+fstype, "-F", device)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "格式化失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "格式化 "+device+" 为 "+fstype)

	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", device)
	uuid := strings.TrimSpace(uuidOut)

	common.SudoExec("mkdir", "-p", mountpoint)
	steps = append(steps, "创建目录 "+mountpoint)

	common.SudoExec("mount", device, mountpoint)
	steps = append(steps, "挂载 "+device+" 到 "+mountpoint)

	if uuid != "" {
		fstabLine := fmt.Sprintf("UUID=%s %s %s defaults 0 2", uuid, mountpoint, fstype)
		fstabData, _ := os.ReadFile("/etc/fstab")
		fstabLines := strings.Split(string(fstabData), "\n")
		found := false
		for _, line := range fstabLines {
			if strings.Contains(line, uuid) {
				found = true
				break
			}
		}
		if !found {
			content := string(fstabData)
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += fstabLine + "\n"
			cmd := exec.Command("sudo", "tee", "/etc/fstab")
			cmd.Stdin = strings.NewReader(content)
			cmd.Run()
			steps = append(steps, "写入 /etc/fstab 持久化 (UUID="+uuid+")")
		}
	}

	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountpoint)
	steps = append(steps, "设置权限 "+nasUser+":"+nasUser)

	if autoSamba == "yes" {
		shareName := filepath.Base(mountpoint)
		smbConf := fmt.Sprintf("\n[%s]\n   path = %s\n   browseable = yes\n   writable = yes\n   valid users = %s\n",
			shareName, mountpoint, nasUser)
		cmd := exec.Command("sudo", "tee", "-a", "/etc/samba/smb.conf")
		cmd.Stdin = strings.NewReader(smbConf)
		cmd.Run()
		common.SudoExec("systemctl", "restart", "smbd")
		steps = append(steps, "添加 Samba 共享 "+shareName+" 并重启 smbd")
	}

	common.JSONResponse(w, map[string]interface{}{
		"message":   "快速配置完成",
		"steps":     steps,
		"device":    device,
		"mountpoint": mountpoint,
		"fstype":    fstype,
		"uuid":      uuid,
	})
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
	baseDevice := strings.TrimSuffix(device, "0123456789")
	for i := 1; i <= 9; i++ {
		partDev := fmt.Sprintf("%s%d", baseDevice, i)
		out2, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", partDev)
		if strings.TrimSpace(out2) == "/" {
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

// unused: ensure regexp is imported
var _ = regexp.MustCompile
