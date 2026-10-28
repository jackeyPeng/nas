package diskmgmt

import (
	"strings"

	"nas-panel/common"
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

// mdstatMemberDisks 从 /proc/mdstat 文本中解析所有活跃阵列的成员盘设备路径
// （形如 "sdb[0]" "nvme0n1[1]"），统一补上 "/dev/" 前缀返回。纯函数，供单测。
// 只需要成员盘集合本身，不需要区分属于哪个阵列——reset 场景是"全部纳管阵列
// 的全部成员都要擦"，不像 pool delete 需要按单个阵列精确匹配。
func mdstatMemberDisks(mdstatContent string) []string {
	seen := map[string]bool{}
	var result []string
	for _, line := range strings.Split(mdstatContent, "\n") {
		trimmed := strings.TrimSpace(line)
		// mdstat 阵列行形如: "md0 : active raid1 sdb[1] sda[0]"
		if !strings.Contains(trimmed, " : active") {
			continue
		}
		for _, tok := range strings.Fields(trimmed) {
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

// pvsListAll 从 `pvs --noheadings -o pv_name` 输出解析全部 PV 设备路径。纯函数。
func pvsListAll(pvsOut string) []string {
	var result []string
	for _, line := range strings.Split(pvsOut, "\n") {
		dev := strings.TrimSpace(line)
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
func mdstatMembersByDevice(mdstatContent string) map[string][]string {
	result := make(map[string][]string)
	for _, line := range strings.Split(mdstatContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, " : active") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		mdDev := "/dev/" + fields[0]
		var members []string
		for _, tok := range fields {
			idx := strings.Index(tok, "[")
			if idx <= 0 {
				continue
			}
			name := tok[:idx]
			if name == fields[0] {
				continue
			}
			members = append(members, "/dev/"+name)
		}
		if len(members) > 0 {
			result[mdDev] = members
		}
	}
	return result
}

// wipeManagedTargets 对纳管目标集合逐个执行 zero-superblock + wipefs，命中
// 系统盘则跳过该盘并记日志（不中止整个 reset —— 与 handlePoolDelete 的"拒绝
// 整个操作"语义不同，这里是尽力拆除，其余目标仍应继续清除）。返回 step 日志。
func wipeManagedTargets(targets []string) []string {
	var steps []string
	for _, dev := range targets {
		if isSystemDisk(dev) {
			steps = append(steps, "跳过系统盘 "+dev)
			continue
		}
		common.SudoExec("/usr/sbin/mdadm", "--zero-superblock", dev)
		common.SudoExec("/usr/sbin/wipefs", "-a", dev)
		steps = append(steps, "清除磁盘签名 "+dev)
	}
	return steps
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
