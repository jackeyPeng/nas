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
	Device     string `json:"device"`
	Name       string `json:"name"`
	Size       string `json:"size"`
	Type       string `json:"type"`      // system, data, unused
	FSType     string `json:"fstype"`     // ext4, xfs, btrfs, ""
	Mountpoint string `json:"mountpoint"`
	Model      string `json:"model"`
	Rotational string `json:"rotational"` // 0=SSD, 1=HDD
	Children   []DiskStatus `json:"children,omitempty"`
	Smart      string `json:"smart,omitempty"` // PASSED/FAILED/unknown
	UUID       string `json:"uuid,omitempty"`
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
				Device: "/dev/" + child.Name,
				Name:   child.Name,
				Size:   child.Size,
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
			} else if childDS.Mountpoint != "" {
				childDS.Type = "data"
			} else if childDS.FSType != "" {
				childDS.Type = "data"
			} else {
				childDS.Type = "unused"
			}
			ds.Children = append(ds.Children, childDS)
		}

		// SMART status for whole disks
		if dev.Type == "disk" && (strings.HasPrefix(ds.Name, "sd") || strings.HasPrefix(ds.Name, "nvme")) {
			smartOut, err := common.SudoOutput("smartctl", "-H", "/dev/"+dev.Name)
			if err == nil {
				if strings.Contains(smartOut, "PASSED") {
					ds.Smart = "PASSED"
				} else if strings.Contains(smartOut, "FAILED") {
					ds.Smart = "FAILED"
				} else {
					ds.Smart = "unknown"
				}
			}
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
		fstype = "ext4"
	}
	if confirm != "yes" {
		http.Error(w, `{"error": "请加 confirm=yes 确认操作"}`, http.StatusBadRequest)
		return
	}
	// Security: no system disk
	if isSystemDisk(device) {
		http.Error(w, `{"error": "不允许操作系统盘"}`, http.StatusBadRequest)
		return
	}
	// Security: mountpoint must be under /data
	if !strings.HasPrefix(mountpoint, "/data/") && mountpoint != "/data" {
		http.Error(w, `{"error": "挂载点必须在 /data/ 下"}`, http.StatusBadRequest)
		return
	}

	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "root"
	}

	var steps []string

	// 1. Format
	out, err := common.SudoExec("mkfs."+fstype, "-F", device)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "格式化失败: %s"}`, out), http.StatusInternalServerError)
		return
	}
	steps = append(steps, "格式化 "+device+" 为 "+fstype)

	// 2. Get UUID
	uuidOut, _ := common.ExecOutput("blkid", "-s", "UUID", "-o", "value", device)
	uuid := strings.TrimSpace(uuidOut)

	// 3. Create mount point
	common.SudoExec("mkdir", "-p", mountpoint)
	steps = append(steps, "创建目录 "+mountpoint)

	// 4. Mount
	common.SudoExec("mount", device, mountpoint)
	steps = append(steps, "挂载 "+device+" 到 "+mountpoint)

	// 5. Write fstab for persistence
	if uuid != "" {
		fstabLine := fmt.Sprintf("UUID=%s %s %s defaults 0 2", uuid, mountpoint, fstype)
		// Read existing fstab, check if entry exists
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
			// Write via sudo tee
			cmd := exec.Command("sudo", "tee", "/etc/fstab")
			cmd.Stdin = strings.NewReader(content)
			cmd.Run()
			steps = append(steps, "写入 /etc/fstab 持久化 (UUID="+uuid+")")
		}
	}

	// 6. Chown
	common.SudoExec("chown", "-R", nasUser+":"+nasUser, mountpoint)
	steps = append(steps, "设置权限 "+nasUser+":"+nasUser)

	// 7. Optional: add to Samba
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
		"message": "快速配置完成",
		"steps":   steps,
		"device":  device,
		"mountpoint": mountpoint,
		"fstype":  fstype,
		"uuid":    uuid,
	})
}

// handleFstab returns current /etc/fstab content
func handleFstab(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		// Try sudo
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
	// Check mount status
	out, _ := common.ExecOutput("findmnt", "-n", "-o", "TARGET", device)
	mount := strings.TrimSpace(out)
	if mount == "/" || mount == "/boot" || mount == "/boot/efi" {
		return true
	}
	// Check by device name patterns
	if strings.Contains(device, "nvme0n1p1") || strings.Contains(device, "nvme0n1p2") {
		return true
	}
	// Check if any partition on this device mounts /
	// e.g. /dev/sda3 might be /
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
		// Also check children for unused partitions
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
