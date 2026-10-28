package diskmgmt

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ═══════════════════════════════════════════════════
// consensus R2 Item A：RAID 状态链 sysfs 优先采集
// ═══════════════════════════════════════════════════
//
// 根因：getRAIDStatus 与 raid_expand.go 的 waitForResync/handleRAIDReshapeStatus/
// handleRAIDExpandFS 全部依赖 /proc/mdstat 文本解析——mdstat 是给人看的格式化
// 输出，字段语义松散（如本文件下方 sync_action/mdstat "recovery" 的命名不一致）。
// 内核在 /sys/block/<md>/md/ 下暴露的是结构化标量文件，字段稳定、无需正则抠
// 文本进度条，应优先使用；mdstat 解析（mdstat_parser.go）保留作为 sysfs 不可用
// 时（容器/旧内核/权限问题）的兼容回退，不删除。
//
// 真机 fixture（10.216.10.57，md0 raid1 2×50G，Debian 13，2026-10-28 实采）：
//   - 干净态：level=raid1 array_state=clean degraded=0 sync_action=idle
//     sync_completed=none sync_speed=none reshape_position=none rd0=rd1=in_sync
//   - 降级态：array_state 仍是 clean（不能信 array_state 判健康）、degraded=1、
//     sync_action=idle、仅 rd0 存在（failed 成员的 rd* 目录消失）；
//     mdstat 同刻 [2/1] [U_]。
//   - 重建态：degraded=1 sync_action=recover（sysfs 值）/ mdstat 文本是
//     "recovery"——两者命名不同，映射时不能混用；sync_completed 格式
//     "<done> / <total>"（KB）；sync_speed 单位 K/sec。
//   - 恢复完成：degraded=0 sync_action=idle array_state=clean。

// raidSysfsScalarFiles 是 /sys/block/<md>/md/ 下要读取的标量文件名列表。
var raidSysfsScalarFiles = []string{
	"level", "array_state", "degraded", "raid_disks", "sync_action",
	"sync_completed", "sync_speed", "reshape_position", "last_sync_action",
}

// readRAIDSysfsValues 读取 /sys/block/<mdName>/md/ 下的标量文件与 rd*/state
// 成员表。mdName 不带 /dev/ 前缀（如 "md0"）。目录不存在（sysfs 不可用/非
// Linux md 环境）时返回 ok=false，调用方应回退 mdstat 路径。
//
// vals 的 key 是 raidSysfsScalarFiles 中的文件名，value 是文件内容 trim 后的
// 原始字符串（未解析，留给纯函数 buildRAIDStatusFromSysfs 处理，便于单测注入）。
// members 的 key 是 "rd0"/"rd1"/...，value 是对应 state 文件内容
// （"in_sync"/"spare"/"faulty" 等）——注意 failed 成员的 rd* 目录在内核里会
// 直接消失，不会出现 state=faulty 的情况（真机实测，降级态只剩 rd0）。
func readRAIDSysfsValues(mdName string) (vals map[string]string, members map[string]string, ok bool) {
	mdDir := filepath.Join("/sys/block", mdName, "md")
	info, err := os.Stat(mdDir)
	if err != nil || !info.IsDir() {
		return nil, nil, false
	}

	vals = make(map[string]string)
	for _, name := range raidSysfsScalarFiles {
		data, err := os.ReadFile(filepath.Join(mdDir, name))
		if err != nil {
			continue
		}
		vals[name] = strings.TrimSpace(string(data))
	}

	members = make(map[string]string)
	entries, err := os.ReadDir(mdDir)
	if err == nil {
		for _, e := range entries {
			if !rdDirRe.MatchString(e.Name()) {
				continue
			}
			stateData, err := os.ReadFile(filepath.Join(mdDir, e.Name(), "state"))
			if err != nil {
				continue
			}
			members[e.Name()] = strings.TrimSpace(string(stateData))
		}
	}

	return vals, members, true
}

// enumerateSysfsMDNames 枚举 /sys/block/md* 下匹配 ^md\d+$ 的 md 设备名
// （不带 /dev/ 前缀），按名称排序返回，便于稳定输出顺序。sysfs 不可用
// （glob 无匹配）时返回空切片——调用方据此判定要不要回退 mdstat。
func enumerateSysfsMDNames() []string {
	matches, _ := filepath.Glob("/sys/block/md*/md")
	var names []string
	for _, m := range matches {
		// m 形如 "/sys/block/md0/md"；取上一级目录名作为 md 名。
		name := filepath.Base(filepath.Dir(m))
		if mdNameRe.MatchString(name) {
			names = append(names, name)
		}
	}
	return names
}

// rdDirRe 匹配成员目录名 "rd0"/"rd1"/...（/sys/block/<md>/md/ 下每个当前
// 成员一个目录，失败成员目录会直接消失，不会留下 faulty 状态可读）。
var rdDirRe = regexp.MustCompile(`^rd\d+$`)

// parseSyncCompleted 解析 sysfs sync_completed 文件内容。格式 "<done> / <total>"
// （单位 KB）或 "none"（无进行中的同步）。畸形输入统一返回 ok=false，调用方
// 应把 ok=false 当作"无法计算百分比"处理，不强行给出误导性的 0%/100%。
func parseSyncCompleted(raw string) (done, total int64, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "none" {
		return 0, 0, false
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	d, errD := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	t, errT := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if errD != nil || errT != nil || t <= 0 {
		return 0, 0, false
	}
	return d, t, true
}

// syncCompletedPercent 把 parseSyncCompleted 的结果格式化为现有 "N.N%" 字符串
// 形态（与 mdstat 路径的 SyncPercent 字段形态保持一致，供前端复用同一渲染）。
// 无法计算时返回空串。
func syncCompletedPercent(raw string) string {
	done, total, ok := parseSyncCompleted(raw)
	if !ok {
		return ""
	}
	pct := float64(done) / float64(total) * 100
	return strconv.FormatFloat(pct, 'f', 1, 64) + "%"
}

// raidHealthFromCounts 推导 Health 字段："degraded"（degraded>0 或有失败盘）、
// "failed"（array_state=inactive 或 degraded==raid_disks，即全部成员失效）、
// 否则 "normal"。
//
// 语义陷阱（真机实测）：降级时 array_state 仍是 "clean"——不能信 array_state
// 判断健康，必须看 degraded 计数；这是本次修法的核心纠偏点。
func raidHealthFromCounts(arrayState string, degraded, raidDisks, failedDisks int) string {
	if arrayState == "inactive" || (raidDisks > 0 && degraded >= raidDisks) {
		return "failed"
	}
	if degraded > 0 || failedDisks > 0 {
		return "degraded"
	}
	return "normal"
}

// buildRAIDStatusFromSysfs 纯函数：把 readRAIDSysfsValues 的输出构造为
// RAIDStatus。供表驱动单测直接注入真机 fixture map，不发起任何系统调用。
//
// 字段语义：
//   - Level：sysfs 的 "level" 文件（如 "raid1"），与 mdstat 路径的 "1" 数字
//     形态不同——RAIDStatus.Level 历史上就是 mdstat 抽出的纯数字（见
//     extractRAIDLevel），这里统一转换成同样的数字字符串形态，保持 json
//     字段向后兼容。
//   - Health/Activity：新增字段，见上方 raidHealthFromCounts 和本函数内
//     sync_action 直接透传逻辑。
//   - State：兼容派生——degraded/failed → "degraded"；sync_action != idle →
//     "resyncing"；否则沿用 array_state 原值（如 "clean"/"active"）。
//   - SyncPercent：从 sync_completed 解析，"N.N%" 形态，与 mdstat 路径的
//     百分比字符串形态一致（但计算来源/单位不同——sysfs 是 KB 计数的
//     done/total，mdstat 是文本里现成的百分比，两者各自内部一致，不要
//     互相替换验证）。
//   - Disks/ActiveDisks/SpareDisks/FailedDisks：从 rd* 成员表的 state 值
//     归类；degraded 态下 failed 成员的 rd* 目录消失，不会被错误计入任何
//     桶——这与 mdstat 路径靠 (F)/(S) token 标志不同源，但两条路径对同一
//     真实状态应给出一致的桶计数（fixture 已验证）。
func buildRAIDStatusFromSysfs(mdName string, vals, members map[string]string) RAIDStatus {
	ra := RAIDStatus{Device: "/dev/" + mdName}

	levelRaw := vals["level"]
	ra.Level = extractRAIDLevel(levelRaw)

	degraded := atoiSafe(vals["degraded"])
	raidDisks := atoiSafe(vals["raid_disks"])
	arrayState := vals["array_state"]
	syncAction := vals["sync_action"]

	for _, state := range members {
		ra.Disks++
		switch state {
		case "spare":
			ra.SpareDisks++
		case "faulty":
			ra.FailedDisks++
		default:
			// in_sync / writemostly / write-mostly 等都算活跃成员
			ra.ActiveDisks++
		}
	}
	// degraded 态下 failed 成员的 rd* 目录会从 sysfs 消失（真机实测），
	// 此时 FailedDisks 从成员表里数不出来，用 raid_disks - 实际 rd* 数量
	// 兜底补足，避免"降级了但 FailedDisks 恒为 0"。
	if degraded > 0 && ra.FailedDisks == 0 && raidDisks > 0 && raidDisks > ra.Disks {
		ra.FailedDisks = raidDisks - ra.Disks
	}

	ra.Health = raidHealthFromCounts(arrayState, degraded, raidDisks, ra.FailedDisks)
	ra.Activity = syncAction

	switch {
	case ra.Health == "degraded" || ra.Health == "failed":
		ra.State = "degraded"
	case syncAction != "" && syncAction != "idle":
		ra.State = "resyncing"
	default:
		ra.State = arrayState
	}

	if syncAction != "" && syncAction != "idle" {
		if pct := syncCompletedPercent(vals["sync_completed"]); pct != "" {
			ra.SyncPercent = pct
		}
	}

	return ra
}

// atoiSafe 把字符串解析为 int，失败返回 0（sysfs 标量文件缺失/畸形时的
// 保守兜底，不 panic）。
func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// ═══════════════════════════════════════════════════
// raid_expand.go 三处调用点共用的 sysfs 轮询/估算纯函数
// ═══════════════════════════════════════════════════

// sysfsResyncDone 判断 sync_action 是否代表"同步已完成"（idle 即完成；
// resync/recover/reshape/check/repair 均代表仍在进行）。供 waitForResync
// 的 sysfs 轮询分支使用，命名独立成纯函数方便单测注入 sync_action 序列。
func sysfsResyncDone(syncAction string) bool {
	return syncAction == "" || syncAction == "idle"
}

// sysfsCleanStreakResult 纯函数：给定一段 sync_action 观测序列，模拟
// waitForResync 的"连续 N 轮 idle 才算完成"判定逻辑，返回是否在序列内达成
// 完成判定及达成时的轮次下标（-1 表示序列结束仍未达成，调用方应继续轮询/
// 超时）。requiredStreak 对应生产代码里的 requiredCleanStreak=3。
//
// 这是 waitForResync 真实轮询循环（依赖 time.Sleep + os.ReadFile，无法在
// 不改生产签名的前提下注入文件系统 mock）的判定核心抽取版，供单测验证
// "观察到非 idle 后 streak 清零重新累计"这一行为，不依赖真实时钟/sysfs。
func sysfsCleanStreakResult(actions []string, requiredStreak int) (done bool, atIndex int) {
	streak := 0
	for i, a := range actions {
		if sysfsResyncDone(a) {
			streak++
			if streak >= requiredStreak {
				return true, i
			}
		} else {
			streak = 0
		}
	}
	return false, -1
}

// reshapeETAMinutes 由 (total-done)/speed 估算剩余分钟数，一位小数。
// speed<=0 或 total<=done 时返回 ok=false（调用方应把 eta 置空，不展示
// 误导性的"0.0"或负数）。done/total 单位 KB（sync_completed 格式），
// speed 单位 K/sec（sync_speed 格式）——分钟 = (total-done)/speed/60。
func reshapeETAMinutes(done, total int64, speedKPerSec float64) (eta string, ok bool) {
	if speedKPerSec <= 0 || total <= done {
		return "", false
	}
	remainingKB := float64(total - done)
	minutes := remainingKB / speedKPerSec / 60
	return strconv.FormatFloat(minutes, 'f', 1, 64), true
}

// parseSyncSpeed 解析 sysfs sync_speed 文件内容（"<N>" 或 "none"），返回
// K/sec 浮点值。"none"/畸形输入返回 ok=false。
func parseSyncSpeed(raw string) (speed float64, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "none" {
		return 0, false
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
