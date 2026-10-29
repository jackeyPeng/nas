package diskmgmt

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════
// E7：RAID 配置持久化链路修复
// ═══════════════════════════════════════════════════
//
// 根因（真机实锤，Debian 13 测试机，RAID1 池，2026-10-28/29）：
// stream.go setupRAIDStream 第 6 步流式建池（setup-stream?mode=raid1，面板
// 唯一实际入口）写 /etc/mdadm/mdadm.conf 时：
//  1. `mdadm --detail --scan` 与 `cat mdadm.conf` 的 error 都被 `_` 吞掉——
//     scan 失败时 mdadmConf 为空仍 SafeWriteFile + stepDone("保存 RAID 配置")，
//     假成功；
//  2. 没有 update-initramfs 调用——conf 写对了，initramfs 里的旧版本不会
//     更新，早期装配（initrd 阶段）仍按旧 conf 走，阵列以 md127 装配而非
//     conf 里写的 md0（名称漂移实锤：重启后 /proc/mdstat 是 md127，fstab
//     按 UUID 挂载未受影响，但 conf 与现实不一致）；
//  3. 没有写后验证——SafeWriteFile 失败（权限/磁盘满）同样会被当成功报告；
//  4. 没有 UUID 去重——同一阵列重复跑建池流程会在 conf 里叠加多条 ARRAY 行。
//
// 修法：persistRAIDConfig 把"取 ARRAY 行 → 读现有 conf → UUID 去重替换/
// 追加 → 落盘 → 写后验证 → update-initramfs"串成一条不吞错的链路，stream.go
// 与 wizard.go 的 RAID 建池路径都改调用它。
//
// 执行器注入：包内此前没有 runner 注入模式（既有测试都是纯函数表驱动，如
// raid_sysfs.go 的 buildRAIDStatusFromSysfs 直接喂 map）。本轮引入最小的
// 函数型 runner 参数 cmdRunner，生产路径传 common.SudoOutput 的包装，测试
// 传 stub——不改动 common 包，不影响其他调用点。

// cmdRunner 是 persistRAIDConfig 的执行器注入点：生产路径包一层
// common.SudoOutput，测试传 stub 函数模拟各命令的输出/失败。
type cmdRunner func(name string, args ...string) (string, error)

// sudoRunner 是 cmdRunner 的生产实现，直接转发给 common.SudoOutput。
func sudoRunner(name string, args ...string) (string, error) {
	return common.SudoOutput(name, args...)
}

// raidConfWriter 是落盘步骤的执行器注入点，默认转发给 common.SafeWriteFile。
// SafeWriteFile 不是 SudoOutput/SudoExec 的薄包装（内部有直写优先 + sudo cp
// 兜底两条路径），不能直接塞进 cmdRunner 的 (string, ...string) 签名，单独
// 开一个包级变量 stub（与现有 rcloneRestartCmd 同款模式，见 config_sync.go/
// regression_test.go），测试替换掉它即可验证 persistRAIDConfig/resetRAIDConfig
// 的全部分支，不触碰真实文件系统、不发起真实 sudo 调用。
var raidConfWriter = common.SafeWriteFile

const mdadmConfPath = "/etc/mdadm/mdadm.conf"

// arrayLineRe 匹配 mdadm.conf / `mdadm --detail --scan` 输出里的 ARRAY 行，
// 并把该阵列的 UUID 捕获出来（大小写不敏感，mdadm 输出的 UUID 本身是小写
// 十六进制+冒号分隔，但做个兜底）。形如：
//
//	ARRAY /dev/md0 metadata=1.2 name=nas:0 UUID=1234abcd:5678efgh:90ab1234:cdef5678
var arrayLineRe = regexp.MustCompile(`(?i)^ARRAY\s+\S+.*\bUUID=(\S+)`)

// arrayPrefixRe 匹配任意 ARRAY 行（大小写不敏感的 "ARRAY" 前缀，词边界），
// 不要求带 UUID 字段——出厂重置等场景需要删除手写的、仅含 name=/devices=
// 等字段而没有 UUID= 的 ARRAY 行，这类行用 extractArrayUUID 判定命中不了。
var arrayPrefixRe = regexp.MustCompile(`(?i)^ARRAY\b`)

// isArrayLine 判断一行是否是 ARRAY 行（大小写不敏感的 "ARRAY" 前缀，词
// 边界——"ARRAYX foo" 这类前缀陷阱不误判）。不要求带 UUID= 字段，供
// stripArrayLines 等需要删除任意形态 ARRAY 行的场景使用；extractArrayUUID
// 仍只认带 UUID= 的行，供 upsert/写后验证等按 UUID 匹配的逻辑使用，两者
// 判据不混用。
func isArrayLine(line string) bool {
	return arrayPrefixRe.MatchString(strings.TrimSpace(line))
}

// extractArrayUUID 从单条 ARRAY 行提取 UUID（小写归一化，mdadm 大小写不敏感
// 但统一小写方便比较）。非 ARRAY 行或没有 UUID 字段返回 ok=false。
func extractArrayUUID(line string) (uuid string, ok bool) {
	m := arrayLineRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}

// stripCR 去掉行尾 \r（CRLF 容错），不影响纯 LF 文本。
func stripCR(line string) string {
	return strings.TrimRight(line, "\r")
}

// findArrayLineByUUID 在 mdadm --detail --scan 的输出（可能含多行，如
// --scan 不带设备参数时会把全部已装配阵列都列出来）中找到 UUID 与
// targetUUID 匹配的那一行。targetUUID 为空或找不到匹配返回 ok=false。
func findArrayLineByUUID(scanOutput, targetUUID string) (line string, ok bool) {
	targetUUID = strings.ToLower(strings.TrimSpace(targetUUID))
	if targetUUID == "" {
		return "", false
	}
	for _, raw := range strings.Split(scanOutput, "\n") {
		l := stripCR(raw)
		if uuid, hasUUID := extractArrayUUID(l); hasUUID && uuid == targetUUID {
			return strings.TrimSpace(l), true
		}
	}
	return "", false
}

// upsertArrayLine 把 newLine（一条 ARRAY 行）合并进现有 mdadm.conf 内容：
// 已有同 UUID 的 ARRAY 行 → 原地替换该行（覆盖设备名漂移场景，如
// md0→md127）；没有 → 追加到文件末尾。非 ARRAY 行（HOMEHOST/MAILADDR/
// 注释/空行等）原样保留、顺序不变。
//
// 纯函数，供表驱动单测直接验证替换/追加两条路径，不发起任何系统调用。
func upsertArrayLine(existingConf, newLine string) string {
	newUUID, ok := extractArrayUUID(newLine)
	lines := splitConfLines(existingConf)

	if ok {
		for i, l := range lines {
			if uuid, hasUUID := extractArrayUUID(l); hasUUID && uuid == newUUID {
				lines[i] = strings.TrimSpace(newLine)
				return joinConfLines(lines)
			}
		}
	}
	lines = append(lines, strings.TrimSpace(newLine))
	return joinConfLines(lines)
}

// splitConfLines 把 conf 内容按行拆分，过滤掉因 TrimRight 产生的末尾空行
// （但保留文件中间原有的空行，不改变非目标内容的排布）。CRLF 容错。
func splitConfLines(conf string) []string {
	if strings.TrimSpace(conf) == "" {
		return nil
	}
	raw := strings.Split(conf, "\n")
	lines := make([]string, 0, len(raw))
	for i, l := range raw {
		l = stripCR(l)
		if i == len(raw)-1 && l == "" {
			// 末尾因文件以 \n 结尾产生的空字符串，不作为一行保留，
			// 由 joinConfLines 统一补回尾部换行。
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// joinConfLines 把行切片拼回文件内容，统一以单个 \n 结尾。
func joinConfLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// stripArrayLines 从 mdadm.conf 内容中删除所有 ARRAY 行，保留其余内容
// （HOMEHOST/MAILADDR/注释/空行等 Debian 默认行不动）。文件为空或没有
// ARRAY 行时原样返回（但仍做 CRLF 归一化）。供 reset.go/stream.go 出厂
// 重置复用，不再把整个 mdadm.conf 清空。
//
// Fix D（E7 审查回炉 Minor4）：删除判据从"extractArrayUUID 命中"放宽为
// isArrayLine（ARRAY 前缀即删）——此前以 UUID= 字段命中为删除条件，不带
// UUID= 的手写 ARRAY 行（仅 name=/devices=... ）出厂重置后会残留。
//
// 纯函数，表驱动单测覆盖：多 ARRAY/无 ARRAY/空文件/CRLF/无UUID的ARRAY行/
// "ARRAYX" 前缀陷阱不误删。
func stripArrayLines(conf string) string {
	if strings.TrimSpace(conf) == "" {
		return conf
	}
	lines := splitConfLines(conf)
	var kept []string
	for _, l := range lines {
		if isArrayLine(l) {
			continue
		}
		kept = append(kept, l)
	}
	return joinConfLines(kept)
}

// persistRAIDConfig 把 mdDev（如 "/dev/md0"）对应阵列的 ARRAY 行持久化进
// /etc/mdadm/mdadm.conf，并尝试更新 initramfs。runner 为空时使用生产实现
// sudoRunner（测试传 stub）。
//
// 失败语义：
//   - scan 失败，或 scan 输出里找不到 mdDev 对应 UUID 的 ARRAY 行 → 返回
//     error，调用方必须报 stepFailed，不得假成功；
//   - 写后验证失败（落盘后再次 cat，目标 UUID 的 ARRAY 行不在文件里）→
//     返回 error；
//   - update-initramfs 失败 → 不 fatal，返回值里带 warning 文案（非 error），
//     调用方应把 warning 带进 step 文案/日志，不得静默吞掉。
//
// 返回 (warning, err)：err != nil 时 warning 无意义（调用方应只看 err）；
// err == nil 时 warning 非空表示 initramfs 更新失败但 conf 已正确落盘。
func persistRAIDConfig(runner cmdRunner, mdDev string) (warning string, err error) {
	if runner == nil {
		runner = sudoRunner
	}

	// a. 取 ARRAY 行：用 --detail --scan <mdDev>，只对该设备生成一行，
	// 避免把系统上其他阵列的 ARRAY 行也混进扫描结果里再去过滤。
	scanOut, scanErr := runner("/usr/sbin/mdadm", "--detail", "--scan", mdDev)
	if scanErr != nil {
		return "", fmt.Errorf("mdadm --detail --scan %s 失败: %w", mdDev, scanErr)
	}
	targetUUID, hasUUID := findArrayUUIDForScan(scanOut)
	if !hasUUID {
		return "", fmt.Errorf("mdadm --detail --scan %s 输出中未找到 ARRAY/UUID 行: %q", mdDev, strings.TrimSpace(scanOut))
	}
	newLine, found := findArrayLineByUUID(scanOut, targetUUID)
	if !found {
		return "", fmt.Errorf("mdadm --detail --scan %s 输出中未找到 UUID=%s 对应的 ARRAY 行", mdDev, targetUUID)
	}

	// b. 读现有 conf —— cat 失败（文件不存在/权限）按空文件处理但留痕，
	// 不让读取失败悄悄变成"从空白重写"的假象之外无任何线索。Fix A（E7 审查
	// 回炉 Important）：cat 失败时若文件实际存在（瞬时失败/权限问题），按
	// 空文件重建会用仅含新 ARRAY 行的内容整文件覆盖，HOMEHOST/MAILADDR/
	// 其他阵列行全丢——必须留日志痕迹，并把警告带给调用方（SSE/steps），
	// 不能像此前那样静默吞错。
	existingConf, catErr := runner("cat", mdadmConfPath)
	catWarning := ""
	if catErr != nil {
		log.Printf("[RAID_PERSIST] 读取 %s 失败，按空文件重建（若文件实际存在，其原有内容将被覆盖）: %v", mdadmConfPath, catErr)
		catWarning = fmt.Sprintf("读取 %s 失败，按空文件重建（若文件实际存在，其原有内容可能被覆盖）: %v", mdadmConfPath, catErr)
		existingConf = ""
	}

	// c. UUID 去重：已有同 UUID 的 ARRAY 行 → 替换；无 → 追加。
	newConf := upsertArrayLine(existingConf, newLine)

	// d. 落盘。
	if writeErr := raidConfWriter(mdadmConfPath, newConf); writeErr != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", mdadmConfPath, writeErr)
	}

	// e. 写后验证：再 cat 一次，确认目标 UUID 的 ARRAY 行真的落盘了。
	verifyConf, verifyErr := runner("cat", mdadmConfPath)
	if verifyErr != nil {
		return "", fmt.Errorf("写后验证失败（读取 %s 出错）: %w", mdadmConfPath, verifyErr)
	}
	if _, ok := findArrayLineByUUID(verifyConf, targetUUID); !ok {
		return "", fmt.Errorf("写后验证失败：%s 中未找到 UUID=%s 对应的 ARRAY 行", mdadmConfPath, targetUUID)
	}

	// f. update-initramfs：失败不 fatal，但必须把警告带出去，不能静默。
	// Fix A：catWarning（若有）与 initramfs 警告可共存，分号拼接，不互相覆盖。
	if out, initErr := runner("update-initramfs", "-u"); initErr != nil {
		initWarning := fmt.Sprintf("RAID 配置已保存，但 update-initramfs -u 失败（重启后阵列可能以其他设备名装配）: %s: %v", out, initErr)
		if catWarning != "" {
			return catWarning + "; " + initWarning, nil
		}
		return initWarning, nil
	}

	return catWarning, nil
}

// findArrayUUIDForScan 从 scan 输出中取第一条 ARRAY 行的 UUID（--detail
// --scan <mdDev> 正常只会输出该设备自己的一行；多行时取第一条，容忍极端
// 场景下 mdadm 版本差异带的额外输出行）。
func findArrayUUIDForScan(scanOutput string) (uuid string, ok bool) {
	for _, raw := range strings.Split(scanOutput, "\n") {
		if u, hasUUID := extractArrayUUID(stripCR(raw)); hasUUID {
			return u, true
		}
	}
	return "", false
}

// resetRAIDConfig 用于出厂重置：只删 mdadm.conf 里的 ARRAY 行，保留其余
// 内容（HOMEHOST/MAILADDR 等 Debian 默认行不动）。文件不存在/读取失败时
// 视为无需处理（没有可清的 ARRAY 行，也没有可保留的默认行，直接跳过，
// 不写入任何内容——避免用空文件覆盖一个实际存在但读取瞬时失败的 conf）。
// 删完若内容有变化，尝试 update-initramfs -u；失败只返回 warning，不
// 阻断重置流程调用方。
//
// 返回 (warning, err)：err 非 nil 表示落盘失败（调用方应据此报告重置步骤
// 失败，而不是像此前那样无条件 append "清除 RAID 配置" 成功文案）。
func resetRAIDConfig(runner cmdRunner) (warning string, err error) {
	if runner == nil {
		runner = sudoRunner
	}

	existingConf, catErr := runner("cat", mdadmConfPath)
	if catErr != nil {
		// 文件不存在/读取失败：没有可清理的内容，跳过写入，不是失败。
		return "", nil
	}

	newConf := stripArrayLines(existingConf)
	if newConf == existingConf {
		return "", nil
	}

	if writeErr := raidConfWriter(mdadmConfPath, newConf); writeErr != nil {
		return "", fmt.Errorf("写入 %s 失败: %w", mdadmConfPath, writeErr)
	}

	if out, initErr := runner("update-initramfs", "-u"); initErr != nil {
		return fmt.Sprintf("RAID 配置已清理，但 update-initramfs -u 失败（旧 initramfs 可能残留已删阵列的 ARRAY 行）: %s: %v", out, initErr), nil
	}
	return "", nil
}
