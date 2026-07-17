package diskmgmt

import (
	"fmt"
	"strings"
)

// RaidOption represents a RAID/storage configuration option
type RaidOption struct {
	ID          string `json:"id"`           // single, merge, raid0, raid1, raid5, raid6, separate
	Name        string `json:"name"`         // LVM单盘, 容量合并, RAID0, RAID1, RAID5, RAID6, 独立模式
	Icon        string `json:"icon"`         // 💾 📦 ⚡ 🛡️ 🔒 🔒🔒 📁
	Safety      string `json:"safety"`       // low, medium, high, veryhigh
	SafetyText  string `json:"safety_text"`  // 无冗余, 基本安全, 安全, 极高安全
	UsableRatio string `json:"usable_ratio"` // 100%, 50%, (n-1)/n, (n-2)/n
	UsableSize  string `json:"usable_size"`  // 100G, 50G
	Description string `json:"description"`
	Warning     string `json:"warning"`      // 注意事项
	Recommended bool   `json:"recommended"`  // 是否推荐
	MinDisks    int    `json:"min_disks"`    // 最少磁盘数
	MaxDisks    int    `json:"max_disks"`    // 0=无限制
}

// getRaidOptions calculates available RAID options based on disk count and sizes
// diskCount: number of unused disks
// minSizeGB: smallest disk size in GB (for capacity calculation)
func getRaidOptions(diskCount int, minSizeGB float64) []RaidOption {
	var options []RaidOption

	if diskCount == 0 {
		return options
	}

	// All disks have same size for simplicity; if mixed, use min size
	totalSize := minSizeGB * float64(diskCount)

	// 1. LVM Single (1 disk) — for future expansion
	if diskCount >= 1 {
		usable := minSizeGB
		opt := RaidOption{
			ID:          "single",
			Name:        "LVM 单盘",
			Icon:        "💾",
			Safety:      "low",
			SafetyText:  "无冗余",
			UsableRatio: "100%",
			UsableSize:  formatSize(usable),
			Description: "单块磁盘用 LVM 方式管理，方便后期加盘扩容",
			Warning:     "磁盘故障数据丢失，建议定期备份",
			Recommended: diskCount == 1,
			MinDisks:    1,
			MaxDisks:    1,
		}
		options = append(options, opt)
	}

	// 2. LVM Merge / JBOD (2+ disks) — capacity first, expandable
	if diskCount >= 2 {
		usable := totalSize
		opt := RaidOption{
			ID:          "merge",
			Name:        "容量合并 (LVM)",
			Icon:        "📦",
			Safety:      "low",
			SafetyText:  "无冗余",
			UsableRatio: "100%",
			UsableSize:  formatSize(usable),
			Description: "多块磁盘合并为一个大存储空间，支持后期加盘扩容",
			Warning:     "任一磁盘故障，所有数据丢失。适合存放可恢复的媒体文件",
			Recommended: false,
			MinDisks:    2,
			MaxDisks:    0,
		}
		options = append(options, opt)
	}

	// 3. RAID0 (2+ disks) — speed, no safety
	if diskCount >= 2 {
		usable := totalSize
		opt := RaidOption{
			ID:          "raid0",
			Name:        "RAID0 条带",
			Icon:        "⚡",
			Safety:      "low",
			SafetyText:  "无冗余",
			UsableRatio: "100%",
			UsableSize:  formatSize(usable),
			Description: "数据分散写入多块磁盘，读写速度最快",
			Warning:     "⚠️ 任一磁盘故障，所有数据全部丢失！不建议存放重要数据",
			Recommended: false,
			MinDisks:    2,
			MaxDisks:    0,
		}
		options = append(options, opt)
	}

	// 4. RAID1 (2 disks) — mirror safety
	if diskCount >= 2 {
		usable := minSizeGB // mirror, capacity = 1 disk
		isRecommended := diskCount == 2
		opt := RaidOption{
			ID:          "raid1",
			Name:        "RAID1 镜像",
			Icon:        "🛡️",
			Safety:      "high",
			SafetyText:  "安全",
			UsableRatio: "50%",
			UsableSize:  formatSize(usable),
			Description: "两块磁盘互为镜像，一块坏了数据不丢",
			Warning:     "容量减半，但安全性高。适合存放重要文档、照片",
			Recommended: isRecommended,
			MinDisks:    2,
			MaxDisks:    2, // RAID1 standard is 2 disks
		}
		options = append(options, opt)
	}

	// 5. RAID5 (3+ disks) — balance of capacity and safety
	if diskCount >= 3 {
		usable := minSizeGB * float64(diskCount-1) // n-1 disks usable
		ratio := fmt.Sprintf("%d/%d", diskCount-1, diskCount)
		opt := RaidOption{
			ID:          "raid5",
			Name:        "RAID5",
			Icon:        "🔒",
			Safety:      "high",
			SafetyText:  "安全",
			UsableRatio: ratio,
			UsableSize:  formatSize(usable),
			Description: "数据和校验信息分散存储，坏1块盘不丢数据，容量利用率高",
			Warning:     "坏1块盘时可降级运行，需及时更换。重建过程较慢",
			Recommended: diskCount >= 3,
			MinDisks:    3,
			MaxDisks:    0,
		}
		options = append(options, opt)
	}

	// 6. RAID6 (4+ disks) — double parity, very safe
	if diskCount >= 4 {
		usable := minSizeGB * float64(diskCount-2) // n-2 disks usable
		ratio := fmt.Sprintf("%d/%d", diskCount-2, diskCount)
		opt := RaidOption{
			ID:          "raid6",
			Name:        "RAID6",
			Icon:        "🔒🔒",
			Safety:      "veryhigh",
			SafetyText:  "极高安全",
			UsableRatio: ratio,
			UsableSize:  formatSize(usable),
			Description: "双重校验，同时坏2块盘数据都不丢",
			Warning:     "容量利用率略低于RAID5，写入有一定性能损失。适合存放不可恢复的重要数据",
			Recommended: false,
			MinDisks:    4,
			MaxDisks:    0,
		}
		options = append(options, opt)
	}

	// 7. Independent / Separate (2+ disks) — each disk standalone
	if diskCount >= 2 {
		opt := RaidOption{
			ID:          "separate",
			Name:        "独立模式",
			Icon:        "📁",
			Safety:      "medium",
			SafetyText:  "各盘独立",
			UsableRatio: "100%",
			UsableSize:  formatSize(totalSize),
			Description: "每块磁盘独立一个存储空间，互不影响",
			Warning:     "坏一块只丢该盘数据，不影响其他盘。管理稍复杂",
			Recommended: false,
			MinDisks:    2,
			MaxDisks:    0,
		}
		options = append(options, opt)
	}

	return options
}

// getMinDiskSizeGB extracts the smallest disk size in GB from unused disks
func getMinDiskSizeGB(disks []WizardDisk) float64 {
	var minSize float64
	for _, d := range disks {
		size := parseSizeToGB(d.Size)
		if minSize == 0 || size < minSize {
			minSize = size
		}
	}
	return minSize
}

// safetyColor returns CSS color for safety level
func safetyColor(level string) string {
	switch level {
	case "low":
		return "#dc2626"
	case "medium":
		return "#f59e0b"
	case "high":
		return "#16a34a"
	case "veryhigh":
		return "#2563eb"
	default:
		return "#6b7280"
	}
}

// safetyBg returns CSS background color for safety level
func safetyBg(level string) string {
	switch level {
	case "low":
		return "#fee2e2"
	case "medium":
		return "#fef3c7"
	case "high":
		return "#dcfce7"
	case "veryhigh":
		return "#dbeafe"
	default:
		return "#f3f4f6"
	}
}

// ensure strings import is used
var _ = strings.TrimSpace
