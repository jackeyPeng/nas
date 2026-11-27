package diskmgmt

import (
	"fmt"
	"strings"
)

// ═══════════════════════════════════════════════════
// consensus P0-A2：出厂重置擦盘集合收敛为面板纳管盘
// ═══════════════════════════════════════════════════
//
// 根因：reset.go(handleWizardReset)/stream.go(handleWizardResetStream) 都对
// getDataDisks()（返回所有非系统整盘）执行 mdadm --zero-superblock + wipefs -a。
// 专用整机场景下这等于"全部非系统盘"，但用户插一块外来 USB 备份盘（带数据，
// 从未被面板纳管）做重置时会被一起误擦。
//
// 修法：collectManagedWipeTargets 收集面板实际纳管的设备集合（PV、RAID 成员、
// /data/nas* 挂载点的源设备及其解析出的底层盘），reset 流程只对这个集合执行
// 擦除；集合外的盘完全不碰。

// mdstatMemberDisks 从 /proc/mdstat 文本中解析所有阵列（active 与 inactive
// 均纳管，见下方 consensus R2 Item C 说明）的成员盘设备路径（形如 "sdb[0]"
// "nvme0n1[1]"），统一补上 "/dev/" 前缀返回。纯函数，供单测。只需要成员盘
// 集合本身，不需要区分属于哪个阵列——reset 场景是"全部纳管阵列的全部成员
// 都要擦"，不像 pool delete 需要按单个阵列精确匹配。
//
// consensus R2 Item C 根因：此前只认含 " : active" 的行——inactive 阵列
// （成员盘仍带 RAID 超级块，常见于掉盘/未完全组装的阵列）不会进纳管集合，
// 重置后残留签名可能被开机自动重组（幽灵阵列）。
//
// 修法：改走既有 parseMdstatBlocks（mdstat_parser.go）——其块切分只按
// "md 名匹配 ^md\d+$" 识别阵列块，不区分 active/inactive（真实 mdstat 的
// inactive 头行形如 "md0 : inactive sdb[1](S) sda[0](S)"，MemberTokens 一样
// 能从 fields[3:] 正确切出）。从块的 MemberTokens 提取盘名，(F)/(S) 等标志
// 不影响归集——重置要擦的就是全部成员盘，不区分其当前角色。
func mdstatMemberDisks(mdstatContent string) []string {
	seen := map[string]bool{}
	var result []string
	for _, block := range parseMdstatBlocks(mdstatContent) {
		for _, tok := range block.MemberTokens {
			idx := strings.Index(tok, "[")
			if idx <= 0 {
				continue
			}
			name := tok[:idx]
			dev := "/dev/" + name
			if !seen[dev] {
				seen[dev] = true
				result = append(result, dev)
			}
		}
	}
	return result
}

// pvsListAll 解析 `pvs --noheadings -o pv_name,vg_name` 输出，取第一列（PV
// 设备路径）。纯函数。
//
// 本轮修复（issue #1）：所有实际调用方（reset.go、stream.go）传入的都是
// `pv_name,vg_name` 两列输出，但此函数此前按"整行就是一个设备路径"处理
// （只做 TrimSpace + HasPrefix("/dev/") 判断），对形如
// "  /dev/sda1   vg_nas" 的真实两列行会把整行（含 VG 名）当成一个设备
// 字符串塞进结果——既产出了形如 "/dev/sda1   vg_nas" 的非法设备名，又遗漏了
// 把该 PV 正确纳入 managedTargets 集合，污染 collectManagedWipeTargets 的
// 输出（被 reset.go/stream.go 用于"只擦纳管盘"的判定依据）。
//
// 修法：改用 strings.Fields 按空白切分取 fields[0]，与 pvsPVToVGMap /
// getCachedPVMappings（overview.go）保持同一套解析逻辑，避免同一份 pvs
// 输出在不同函数里被用不同方式误解析。只有空行、无法整行解析出字段的
// 行会被跳过（不会把半行垃圾当设备名）。
func pvsListAll(pvsOut string) []string {
	var result []string
	for _, line := range strings.Split(pvsOut, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		dev := fields[0]
		if strings.HasPrefix(dev, "/dev/") {
			result = append(result, dev)
		}
	}
	return result
}

// resolveMountSourceToPhysicalDevices 把一个 /data/nas* 挂载点的源设备
// （df/findmnt 的 SOURCE 列）解析成底层物理盘/分区集合：
//   - LV（/dev/mapper/vg-lv 或 /dev/vgname/lvname）→ 该 VG 的全部 PV
//   - MD 阵列（/dev/md0）→ 该阵列的全部成员盘（由 memberLookup 提供）
//   - 其它（整盘/分区直接挂载）→ 设备本身
//
// pvMappings: PV设备 → VG名；mdMembers: md设备 → 成员盘列表。均为预先查询好
// 传入的数据，函数本身是纯函数，不发起任何系统命令，便于单测注入 fixture。
func resolveMountSourceToPhysicalDevices(source string, pvMappings map[string]string, mdMembers map[string][]string) []string {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	// MD 阵列
	if strings.HasPrefix(source, "/dev/md") {
		if members, ok := mdMembers[source]; ok {
			return members
		}
		return []string{source}
	}
	// LVM：mapper 命名 /dev/mapper/vg-lv 或路径命名 /dev/vgname/lvname
	vgName := ""
	if strings.HasPrefix(source, "/dev/mapper/") {
		mapperName := strings.TrimPrefix(source, "/dev/mapper/")
		var vgList []string
		for _, vg := range pvMappings {
			vgList = append(vgList, vg)
		}
		vgName = matchVGForMapper(mapperName, vgList)
	} else if strings.HasPrefix(source, "/dev/") {
		rest := strings.TrimPrefix(source, "/dev/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 {
			// /dev/<vgname>/<lvname> —— 候选 VG 名就是第一段，但只有在
			// pvMappings 中确实存在该 VG 时才按 LVM 处理，否则当整盘/分区处理
			// （例如 /dev/sdb1 没有第二段，不会进入这个分支）。
			candidate := parts[0]
			for _, vg := range pvMappings {
				if vg == candidate {
					vgName = candidate
					break
				}
			}
		}
	}
	if vgName != "" {
		var members []string
		for pv, vg := range pvMappings {
			if vg == vgName {
				members = append(members, pv)
			}
		}
		if len(members) > 0 {
			return members
		}
	}
	// 整盘/分区直接挂载
	return []string{source}
}

// collectManagedWipeTargets 收集出厂重置应该擦除的设备集合（并集、去重），
// 在任何拆除动作之前调用。三条来源：
//
//	a) pvs 列出的全部 PV；
//	b) 活跃 MD 阵列的全部成员盘；
//	c) /data/nas* 前缀挂载点的源设备解析出的底层盘（LV→VG的PV、MD→成员、整盘/分区→自身）。
//
// 纯函数，三路输入都是预先查询好的文本/映射，不在函数内部发起任何系统命令，
// 便于单测注入「外来 USB 盘有数据签名但从未纳管」等 fixture。
func collectManagedWipeTargets(pvsOut, mdstatContent string, dataMounts []map[string]string) []string {
	set := map[string]bool{}
	var order []string
	add := func(dev string) {
		dev = strings.TrimSpace(dev)
		if dev == "" || set[dev] {
			return
		}
		set[dev] = true
		order = append(order, dev)
	}

	// a) 全部 PV
	for _, pv := range pvsListAll(pvsOut) {
		add(pv)
	}

	// b) 全部 MD 阵列成员盘
	mdMembersFlat := mdstatMemberDisks(mdstatContent)
	for _, dev := range mdMembersFlat {
		add(dev)
	}

	// 为 (c) 准备 pvMappings（pv→vg）与 mdMembers（md设备→成员）—— 从同一份
	// pvsOut/mdstatContent 重新按设备分组，不发起新的系统调用。
	pvMappings := pvsPVToVGMap(pvsOut)
	mdMembers := mdstatMembersByDevice(mdstatContent)

	// c) /data/nas* 挂载点源设备解析出的底层盘
	for _, m := range dataMounts {
		mount := m["mount"]
		if !strings.HasPrefix(mount, "/data/nas") && mount != "/data" {
			continue
		}
		for _, dev := range resolveMountSourceToPhysicalDevices(m["device"], pvMappings, mdMembers) {
			add(dev)
		}
	}

	return order
}

// pvsPVToVGMap 解析 `pvs --noheadings -o pv_name,vg_name` 形式的输出为 PV→VG 映射。
// 纯函数，供 collectManagedWipeTargets 内部复用（与 getCachedPVMappings 的真实
// 系统调用版本做相同的字段解析，但不发起命令，便于单测注入文本）。
func pvsPVToVGMap(pvsOut string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(pvsOut, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 {
			result[fields[0]] = fields[1]
		}
	}
	return result
}

// mdstatMembersByDevice 解析 /proc/mdstat 文本，返回 md设备路径 → 成员盘列表
// 的映射（如 "/dev/md0" → ["/dev/sda","/dev/sdb"]）。纯函数。
//
// consensus R2 Item C 范围扩展说明（报告申报）：任务书只点名
// mdstatMemberDisks，但本函数与它是同一个 " : active" 过滤器 bug 的另一个
// 实例，被 collectManagedWipeTargets 路径 (c) 用于 /data/nas* 挂载点源
// 设备是 MD 阵列时的成员解析。虽然 inactive 阵列通常不会被挂载（path (c)
// 命中它的概率低于 path (b) 的 mdstatMemberDisks），但两个函数现在用不同
// 逻辑解析同一份 mdstat 文本会造成行为不一致、维护陷阱，顺手一并改走
// parseMdstatBlocks，保持与 mdstatMemberDisks 同源同逻辑。
func mdstatMembersByDevice(mdstatContent string) map[string][]string {
	result := make(map[string][]string)
	for _, block := range parseMdstatBlocks(mdstatContent) {
		var members []string
		for _, tok := range block.MemberTokens {
			idx := strings.Index(tok, "[")
			if idx <= 0 {
				continue
			}
			members = append(members, "/dev/"+tok[:idx])
		}
		if len(members) > 0 {
			result["/dev/"+block.MdName] = members
		}
	}
	return result
}

// wipeManagedTargets 对纳管目标集合逐个执行 zero-superblock + wipefs，命中
// 系统盘则跳过该盘并记日志（不中止整个 reset —— 与 handlePoolDelete 的"拒绝
// 整个操作"语义不同，这里是尽力拆除，其余目标仍应继续清除）。返回 step 日志。
//
// 问题 1 补充修复：此前 mdadm --zero-superblock / wipefs -a 的返回值被
// common.SudoExec 原地丢弃——走到这一步时 pvremove/vgremove/mdadm --stop
// 等不可逆步骤已经成功（reset.go/stream.go 在调用本函数前已经让这些失败
// 立即 return），所以不适合让单个盘的擦除失败中止整个循环（其它盘的签名
// 该清的还是要清，否则更糟：LVM/RAID 元数据已经拆了，磁盘签名却没擦，
// 残留的旧文件系统签名反而可能让下次扫盘误把这块盘识别成"仍属于旧池"）。
// 但失败不能再悄无声息——现在每个失败的命令都如实写进 step 文案
// ("✗ 清除磁盘签名失败" + 具体 stderr/err)，调用方（reset.go 的 steps、
// stream.go 的 SSE Detail）会原样呈现给用户，不再假装"清除磁盘签名"
// 对这块盘也成功了。
func wipeManagedTargets(targets []string) []string {
	var steps []string
	for _, dev := range targets {
		if isSystemDisk(dev) {
			steps = append(steps, "跳过系统盘 "+dev)
			continue
		}
		var failures []string
		if out, err := sudoExecFn("/usr/sbin/mdadm", "--zero-superblock", dev); err != nil {
			failures = append(failures, fmt.Sprintf("mdadm --zero-superblock 失败: %v（输出: %s）", err, out))
		}
		if out, err := sudoExecFn("/usr/sbin/wipefs", "-a", dev); err != nil {
			failures = append(failures, fmt.Sprintf("wipefs -a 失败: %v（输出: %s）", err, out))
		}
		if len(failures) > 0 {
			steps = append(steps, fmt.Sprintf("✗ 清除磁盘签名 %s 失败，请人工检查: %s", dev, strings.Join(failures, "; ")))
		} else {
			steps = append(steps, "清除磁盘签名 "+dev)
		}
	}
	return steps
}

// intersectDevices 返回同时出现在 targets 和 allowed 中的设备路径（去重，
// 按 targets 的原始顺序），供"只对纳管集合里确实是 PV 的那些设备执行
// pvremove"场景使用——reset.go/stream.go 在 LVM 拆除步骤里用它把
// managedTargets（PV + RAID 成员盘 + 挂载点解析出的底层盘的并集）收窄到
// pvsListAll(pvsOutPre) 报告的真实 PV 子集，避免把纯 RAID 成员盘误当 PV
// 调用 pvremove。纯函数。
func intersectDevices(targets, allowed []string) []string {
	allowedSet := map[string]bool{}
	for _, a := range allowed {
		allowedSet[strings.TrimSpace(a)] = true
	}
	var result []string
	for _, t := range targets {
		if allowedSet[strings.TrimSpace(t)] {
			result = append(result, t)
		}
	}
	return result
}

// skipUnmanagedDisks 返回 getDataDisks() 中不在纳管目标集合内的盘，供日志记录
// 「跳过未纳管盘 <dev>」用——这些盘完全不会被 wipeManagedTargets 触碰。纯函数。
func skipUnmanagedDisks(dataDisks, managedTargets []string) []string {
	managedSet := map[string]bool{}
	for _, t := range managedTargets {
		managedSet[t] = true
	}
	var unmanaged []string
	for _, d := range dataDisks {
		if !managedSet[d] {
			unmanaged = append(unmanaged, d)
		}
	}
	return unmanaged
}

// ═══════════════════════════════════════════════════
// Item 1 修复：VG/LV/PV 删除与 RAID 停止收敛到纳管集合
// ═══════════════════════════════════════════════════
//
// 根因：handleWizardReset(reset.go)/handleWizardResetStream(stream.go) 在
// collectManagedWipeTargets 采集出 managedTargets 之后，第 3/4 步（删除
// VG/LV/PV）和第 5 步（停止 RAID）却完全不看这个集合——直接 `vgs`/`pvs`
// 枚举宿主机上*全部* VG/PV 执行 vgremove/lvremove/pvremove，`stopRAIDArrays`
// 对 /dev/md[0-9]* glob 全部停止。专用整机场景下这碰巧等于"全部"，但一旦
// 面板之外还有未纳管的 VG/PV/RAID（比如同机跑着另一套未被面板接管的 LVM，
// 或者外插了一块带 RAID 签名的 USB 盘），会被这两步无差别拆除/停止——
// 与 wipeManagedTargets 已经做到的"只擦纳管盘"精度不一致，是同一类 bug
// 在不同步骤里的另一个实例。
//
// 修法：managedVGNames 从 pvMappings（pvsPVToVGMap 的结果）里筛出"至少有
// 一个 PV 在 managedTargets 集合内"的 VG 名；managedMDDevices 从
// mdstatMembersByDevice 的结果里筛出"至少有一个成员盘在 managedTargets
// 集合内"的 md 设备路径。两者都是纯函数，输入是已经查询好的文本/映射，
// 不发起任何系统命令，便于单测注入 fixture 验证"未纳管的 VG/PV/md 不受影响"。

// managedVGNames 从 pvsOut（pvs --noheadings -o pv_name,vg_name 的原始输出）
// 解析出的 PV→VG 映射中，筛出至少有一个 PV 属于 managedTargets 集合的 VG
// 名称集合（去重，保持首次出现顺序）。
func managedVGNames(pvsOut string, managedTargets []string) []string {
	managedSet := map[string]bool{}
	for _, t := range managedTargets {
		managedSet[strings.TrimSpace(t)] = true
	}
	seen := map[string]bool{}
	var result []string
	// 按 pvsOut 原始行序遍历（而不是先转 map 再遍历 map），保证确定性输出
	// 顺序——map 遍历顺序随机，会让日志/测试输出不稳定。
	for _, line := range strings.Split(pvsOut, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pv, vg := fields[0], fields[1]
		if !managedSet[pv] {
			continue
		}
		if vg == "" || seen[vg] {
			continue
		}
		seen[vg] = true
		result = append(result, vg)
	}
	return result
}

// managedMDDevices 从 mdstatContent 解析出的 md设备→成员盘映射中，筛出至少
// 有一个成员盘属于 managedTargets 集合的 md 设备路径集合（去重，按
// parseMdstatBlocks 的块序）。
func managedMDDevices(mdstatContent string, managedTargets []string) []string {
	managedSet := map[string]bool{}
	for _, t := range managedTargets {
		managedSet[strings.TrimSpace(t)] = true
	}
	var result []string
	for _, block := range parseMdstatBlocks(mdstatContent) {
		mdDev := "/dev/" + block.MdName
		hit := false
		for _, tok := range block.MemberTokens {
			idx := strings.Index(tok, "[")
			if idx <= 0 {
				continue
			}
			if managedSet["/dev/"+tok[:idx]] {
				hit = true
				break
			}
		}
		if hit {
			result = append(result, mdDev)
		}
	}
	return result
}
