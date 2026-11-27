package diskmgmt

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"nas-panel/common"
)

// ═══════════════════════════════════════════════════════════
// 池删除持久化恢复状态（Requirement 2）
//
// 问题：handlePoolDelete 此前完全没有持久化的"池/操作状态模型"——所有池
// 信息都来自实时 vgs/pvs/lvs/mdstat 查询。第一条不可逆命令（vgremove /
// mdadm --stop）成功执行后，如果进程在后续"尽力清理成员盘残留签名"的
// 循环中途被杀（断电、面板崩溃、OOM），没有任何落盘记录说明：
//   - 这次删除涉及哪些成员设备（memberPVs/memberDisks）
//   - 哪些设备已经清理完、哪些还没处理
//   - 不可逆步骤是否已经越过
// 之前的"补救策略"（pvFailures/diskFailures 列表 + HTTP 500 详细文案）
// 只是把失败原因精确报告给**当次 HTTP 响应的调用方**；一旦进程重启，
// 这份信息随请求对象一起消失，不构成"持久化恢复计划"。
//
// 修复：在 switch 分支内第一条不可逆命令（vgremove/mdadm --stop/
// 单盘wipefs）执行之前，把 "池类型 + 设备 + 名称 + 成员设备列表 + 成员
// 稳定身份（UUID/序列号）+ 阶段" 写入 folders.db 的新表
// pool_delete_recovery（与 pending_ops/operation_logs 同一个
// initConfigDB() 单例连接，复用既有持久化基础设施，不新增数据库文件）。
// 阶段随删除推进更新；全部步骤成功或者收尾阶段的"部分失败"已经把具体
// 残留设备通过 HTTP 响应报告给调用方后，才清除该记录。进程崩溃重启后，
// GetPoolDeleteRecoveries() 能查到未清除的记录，
// /api/disk/pool/delete/recovery 把它暴露出来，管理员可以照着记录里的
// 成员设备及其 UUID/序列号手动核实/续做清理，而不是面对一个"查不到任何
// 池、也不知道哪些盘残留签名"的黑箱。
//
// ─── 本轮复查修复的 4 个问题（parent review）───────────────────────────
//
// 1) 身份不稳定：此前 Members 只存 /dev/sdX 这类易变路径——重启/重新
//    插拔后内核可能把同一块物理盘重新编号为别的 /dev/sdX，恢复记录据此
//    指向的设备可能已经是另一块盘。现在在 recordPoolDeleteStart 之前
//    由调用方（pool.go）对每个成员设备采集 UUID（blkid）与序列号
//    （getDiskSerial，smartctl），连同路径一起落盘为 MemberIdentities。
//    【验收复查再修正】最初的实现把"UUID 或序列号任一存在"当作身份
//    确认充分条件，但 UUID 是该设备即将被清除的文件系统/RAID 超级块
//    内容的属性，不是设备本身的稳定标识——对"即将被 wipefs 的设备"
//    而言，拿它现在的内容身份当作设备身份本身就是错的，设备一清空
//    UUID 就失效，且理论上可以在不同物理盘上重现相同 UUID。现在改为
//    要求序列号必须存在作为身份基线，UUID/WWN 仅作补充信息；任一设备
//    拿不到序列号，fail-closed 拒绝整个删除（见 captureDeviceIdentities）。
//
// 2) "上报即清除"误当"已交付"：此前 handlePoolDelete 在
//    jsonError/JSONResponse 写响应之后自动调用 clearPoolDeleteRecovery，
//    理由是"stdlib 没有客户端已收到的信号，写响应之后清除已经是能做到
//    的最强保证"。【验收复查推翻此设计】这个理由本身站不住：写
//    ResponseWriter 成功不代表字节真的送达了客户端——TCP 连接可能已经
//    断开、反向代理可能已经超时放弃、浏览器可能已经关闭标签页，Go 的
//    net/http 对这些情况通常仍然让 Write 调用"成功"返回。换句话说，
//    "尽力保证"本质上还是在用一个不可靠的信号自动做出不可逆的清除
//    决定——这和最初要解决的"崩溃后记录丢失"问题是同一类风险，只是
//    换了一个更隐蔽的触发条件。现在改为：无论成功、部分失败还是配置
//    清理失败，handlePoolDelete 都不再自动清除记录；记录只能通过新增
//    的显式确认接口 confirmPoolDeleteRecovery/
//    /api/disk/pool/delete/recovery/confirm 由管理员在核实磁盘层面
//    确实已经处理完毕后手动清除——必须是人的动作，不能是任何自动化的
//    "响应写完了所以认为已经交付"推断。
//
// 3) 查询错误当"零记录"：GetPoolDeleteRecoveries 此前 db.Query 出错时
//    返回 nil，与"确实没有恢复记录"完全无法区分，调用方
//    （handlePoolDeleteRecoveryStatus）会向管理员展示一个看起来"一切
//    正常"的空列表，而真实情况是数据库读取失败、无法确认是否有被中断
//    的删除。现在返回 (nil, error)，调用方据此 fail-closed：查询失败时
//    必须明确告知"无法确认"，不能呈现为"没有问题"。
//
// 4) 阶段更新错误被吞掉：updatePoolDeletePhase 此前只记日志，返回值
//    留给调用方的信息是"反正已经执行过不可逆命令了，中止也没意义"——
//    这个判断本身没错（阶段更新失败不应该、也不能撤销已经执行的
//    vgremove/mdadm --stop），但吞掉错误意味着调用方完全不知道阶段
//    字段可能已经与实际不一致，无法在响应/日志里如实反映这一点。现在
//    返回 error，调用方把阶段更新失败也计入 steps/日志，让管理员知道
//    "恢复记录的阶段字段可能不准确，需要以磁盘层面实际状态为准"，而不是
//    误以为阶段字段绝对可靠。
// ═══════════════════════════════════════════════════════════

// DeviceIdentity 记录一个成员设备在落盘恢复记录时的稳定身份——UUID（文件
// 系统超级块 UUID，或 RAID 成员的 linux_raid_member UUID，经 blkid 读取）
// 与序列号（经 smartctl 读取）。Path 仍然保留（排障时仍然有用），但
// 恢复/核实时应该优先以 UUID/Serial 匹配实际设备，而不是信任 /dev/sdX
// 路径在重启/重新插拔后仍然指向同一块物理盘。
type DeviceIdentity struct {
	Path   string `json:"path"`
	UUID   string `json:"uuid,omitempty"`
	Serial string `json:"serial,omitempty"`
	WWN    string `json:"wwn,omitempty"`
}

// PoolDeleteRecovery 记录一次池删除操作在越过不可逆步骤时的现场。
type PoolDeleteRecovery struct {
	ID         int    `json:"id"`
	PoolType   string `json:"pool_type"`
	PoolDevice string `json:"pool_device"`
	PoolName   string `json:"pool_name"`
	// Members 是 JSON 数组字符串（成员 PV 或成员盘设备路径），删除前一次性
	// 落盘，之后不再更新——即使清理过程中部分成员已处理完，恢复场景下
	// 管理员需要看到的是"这次删除涉及的全部成员"，而不是动态收缩的列表。
	// 保留该字段是为了兼容旧记录/简单排障场景；新记录的权威身份信息在
	// MemberIdentities 里（含 UUID/Serial，不止是易变的路径）。
	Members string `json:"members"`
	// MemberIdentities 是 []DeviceIdentity 的 JSON 数组字符串，每个成员
	// 设备的稳定身份（UUID/序列号）+ 采集时的路径。恢复/核实时应以此为
	// 准，而不是 Members 里的裸路径。
	MemberIdentities string `json:"member_identities"`
	// Phase 描述已越过的不可逆阶段："vg_removed" / "array_stopped" / "wiped"，
	// 用于恢复时判断该设备集合目前处于哪种中间状态（VG/阵列元数据已经
	// 不存在，但成员盘上的旧签名是否已清除未知）。
	Phase string `json:"phase"`
	// PhaseUpdateWarning 非空时说明最近一次 updatePoolDeletePhase 写入
	// 失败——Phase 字段的值可能落后于磁盘层面实际已经越过的阶段，核实时
	// 不能把 Phase 当作绝对可靠的现场判据，必须以磁盘层面实际状态复核。
	PhaseUpdateWarning string `json:"phase_update_warning,omitempty"`
	CreatedAt          string `json:"created_at"`
}

func ensurePoolDeleteRecoverySchema(db *sql.DB) {
	db.Exec(`CREATE TABLE IF NOT EXISTS pool_delete_recovery (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		pool_type TEXT NOT NULL,
		pool_device TEXT NOT NULL,
		pool_name TEXT NOT NULL,
		members TEXT NOT NULL DEFAULT '[]',
		member_identities TEXT NOT NULL DEFAULT '[]',
		phase TEXT NOT NULL DEFAULT '',
		phase_update_warning TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	// 迁移：老记录（上一轮实现）没有 member_identities / phase_update_warning
	// 列，幂等补列，已有行留空（Phase 字段的"可能不准确"语义不适用于迁移前
	// 的历史行——它们没有 warning 列，不代表百分百可靠，只是没有这个判据）。
	addColumnIfMissing(db, "pool_delete_recovery", "member_identities", "TEXT NOT NULL DEFAULT '[]'")
	addColumnIfMissing(db, "pool_delete_recovery", "phase_update_warning", "TEXT NOT NULL DEFAULT ''")
}

// addColumnIfMissing 幂等地给已存在的表补一列（sqlite 没有
// "ADD COLUMN IF NOT EXISTS"，需要先查 PRAGMA table_info 判断列是否已存在）。
func addColumnIfMissing(db *sql.DB, table, column, def string) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == column {
			return
		}
	}
	db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
}

// captureDeviceIdentityFn 采集单个成员设备的稳定身份（UUID + 序列号 +
// WWN），测试可替换此 seam 避免真的调用 blkid/smartctl。生产实现见
// captureDeviceIdentity。
var captureDeviceIdentityFn = captureDeviceIdentity

// parentBlockDeviceFn 把分区设备路径解析为其所在的整盘设备路径（如
// /dev/sdb1 → /dev/sdb；整盘设备本身原样返回，如 /dev/sdb → /dev/sdb）。
// 测试可替换此 seam。生产实现见 parentBlockDevice。
var parentBlockDeviceFn = parentBlockDevice

// parentBlockDevice 用 `lsblk -no PKNAME` 查询设备的父块设备名。smartctl
// 的序列号读取针对整盘设计——对分区节点（如 /dev/sdb1）执行
// `smartctl -i` 通常直接失败或者读不到任何内容，不能作为"这块分区所在
// 物理盘序列号"的可靠来源。lsblk PKNAME 字段对分区返回其父盘的裸设备名
// （无前缀），对整盘设备返回空字符串——此时说明传入的本身就是整盘，直接
// 原样使用。PKNAME 查询失败（lsblk 执行出错）时保守返回原路径，让上层
// smartctl 调用去真实验证这个路径能否读到序列号（宁可采集失败 fail-closed
// 拒绝删除，也不要在"父设备解析失败"时静默放过身份校验）。
func parentBlockDevice(devPath string) string {
	out, err := execOutputFn("lsblk", "-no", "PKNAME", devPath)
	if err != nil {
		return devPath
	}
	pk := strings.TrimSpace(out)
	if pk == "" {
		return devPath
	}
	return "/dev/" + pk
}

// captureDeviceIdentity 对单个成员设备路径（如 /dev/sdb 或 /dev/sdb1）
// 采集稳定身份：
//   - UUID：blkid -s UUID（对格式化过的 LV/单盘命中文件系统 UUID；对裸的
//     RAID 成员盘命中 mdadm 写入的 linux_raid_member 超级块 UUID——两种
//     情况下都是绑定在该设备当前内容上的稳定标识，不随 /dev/sdX 重新
//     编号而改变）。blkid 直接对传入的设备路径（分区或整盘）操作，不需要
//     父设备解析——分区和整盘各自有独立的文件系统/RAID 超级块 UUID。
//   - 序列号：读硬件序列号，物理盘级别的身份。【验收复查修正两个问题】
//     (a) 分区→父设备映射：serial 是整盘属性，对分区节点直接跑
//     `smartctl -i` 通常读不到（smartctl 面向整盘设计），之前实现对
//     "/dev/sdb1" 这样的分区路径只是裸去掉 "/dev/" 前缀再拼回去，仍然
//     是分区路径本身，不会先解析出父盘 /dev/sdb。现在先用
//     parentBlockDeviceFn（lsblk PKNAME）解析出实际应该查询序列号的整盘
//     设备，再对该整盘路径读序列号。
//     (b) 绕过只读展示缓存：此前通过 getDiskSerial 走 cachedSmartctl（30s
//     TTL，专为 overview/status 等高频轮询只读展示设计），在身份采集这条
//     安全关键路径上复用这个缓存意味着：如果同一 /dev/sdX 路径在 TTL
//     窗口内被热插拔替换成另一块物理盘（旧盘拔出、新盘插入复用同一个
//     内核设备号），这里可能读到的是缓存里旧盘的序列号，而真正将被
//     wipefs/mdadm --zero-superblock 销毁签名的是新插入的物理盘——
//     恢复记录里存的身份和实际被销毁的设备不是同一块盘，完全违背"持久化
//     恢复记录身份必须可靠"的前提。现在直接调用 sudoOutputFn("smartctl",
//     "-i", ...) 绕开 cachedSmartctl，每次都是不缓存的实时查询，采集时刻
//     读到的序列号必然对应当前实际插在该路径上的物理盘。
//
// 都采不到时返回的 DeviceIdentity 只有 Path，调用方
// （captureDeviceIdentities）据此判定"这个设备没有任何可靠身份"。
func captureDeviceIdentity(devPath string) DeviceIdentity {
	id := DeviceIdentity{Path: devPath}
	if uuidOut, err := sudoOutputFn("blkid", "-s", "UUID", "-o", "value", devPath); err == nil {
		id.UUID = strings.TrimSpace(uuidOut)
	}
	serialDev := parentBlockDeviceFn(devPath)
	// 直接调用未缓存的 smartctl -i（不走 cachedSmartctl），确保读到的是
	// 采集时刻该路径上实际物理盘的序列号，不是只读展示缓存里可能已经
	// 过期（指向被热插拔替换前的旧盘）的值。
	if serialOut, err := sudoOutputFn("smartctl", "-i", serialDev); err == nil {
		id.Serial = parseSmartctlSerial(serialOut)
	}
	if wwnOut, err := sudoOutputFn("lsblk", "-no", "WWN", devPath); err == nil {
		id.WWN = strings.TrimSpace(wwnOut)
	}
	return id
}

// parseSmartctlSerial 从 `smartctl -i` 的输出里提取 "Serial Number:" 行的
// 值。抽成独立函数复用 getDiskSerial 已有的解析逻辑，避免重复实现。
func parseSmartctlSerial(out string) string {
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

// captureDeviceIdentities 对一组成员设备路径逐个采集稳定身份。任一设备
// 既无 UUID 也无序列号（WWN 很多消费级盘/分区本身就没有，不作为必要
// 条件），说明这个设备除了易变的 /dev/sdX 路径之外没有任何可靠身份可以
// 落盘——如果据此继续执行不可逆命令，恢复记录里存的仍然只是路径，等同于
// 没有修复"身份不稳定"这个问题。此时返回 error，调用方必须 fail-closed
// 拒绝整个删除，不能"部分设备有身份、部分没有"就放行。
// 验收复查发现：UUID 是即将被 wipefs/pvremove/mdadm --zero-superblock 抹除
// 的那块盘**当前内容**的文件系统/RAID 超级块 UUID——它描述的是"这块设备上
// 现在装的是什么"，不是设备本身的稳定身份。一旦该设备被清除签名或者被
// 重新格式化，UUID 就不再指向任何东西；换一块盘用 dd 写入同样内容也能
// 产生相同 UUID。拿一个即将被销毁的易变属性当作恢复记录里"这是哪块设备"
// 的唯一依据，本质上和只存 /dev/sdX 路径一样不可靠——只是看起来更像一个
// "身份"。序列号（smartctl 读取的硬件序列号）才是绑定在物理设备本身、
// 不随分区表/文件系统内容变化的标识，所以现在要求 Serial 必须存在，
// 作为身份判定的基线；UUID/WWN 仅作为补充信息保留（排障时仍有参考价值，
// 但不能单独作为"设备身份已确认"的依据）。任一设备拿不到序列号，说明
// 没有可靠身份可以记录，fail-closed 拒绝整个删除。
func captureDeviceIdentities(devices []string) ([]DeviceIdentity, error) {
	ids := make([]DeviceIdentity, 0, len(devices))
	for _, dev := range devices {
		id := captureDeviceIdentityFn(dev)
		if id.Serial == "" {
			return nil, fmt.Errorf("设备 %s 无法读取硬件序列号（smartctl），仅凭文件系统/RAID 超级块 UUID 不足以作为可靠的持久化身份——该 UUID 绑定在即将被清除的内容上，设备清空后即失效，无法建立可靠的持久化身份记录", dev)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// recordPoolDeleteStart 在第一条不可逆命令之前写入恢复记录，返回记录 ID
// 供后续 updatePoolDeletePhase/clearPoolDeleteRecovery 使用。写入失败
// （数据库不可用）或任一成员设备的稳定身份采集失败时返回 error——调用方
// 必须据此 fail-closed，不能在"连恢复记录都写不进去/记录里只有易变路径
// 没有可靠身份"的情况下继续执行不可逆命令（否则崩溃后连"这次删除涉及
// 哪些设备"都无法可靠重建，等同于完全没有持久化）。
func recordPoolDeleteStart(poolType, poolDevice, poolName string, members []string) (int64, error) {
	identities, idErr := captureDeviceIdentities(members)
	if idErr != nil {
		return 0, fmt.Errorf("采集成员设备稳定身份失败: %w", idErr)
	}
	db := initPendingDB()
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化，无法建立持久化恢复记录")
	}
	ensurePoolDeleteRecoverySchema(db)
	membersJSON, err := json.Marshal(members)
	if err != nil {
		return 0, fmt.Errorf("成员列表序列化失败: %w", err)
	}
	identitiesJSON, err := json.Marshal(identities)
	if err != nil {
		return 0, fmt.Errorf("成员身份序列化失败: %w", err)
	}
	res, err := db.Exec(`INSERT INTO pool_delete_recovery (pool_type, pool_device, pool_name, members, member_identities, phase) VALUES (?, ?, ?, ?, ?, ?)`,
		poolType, poolDevice, poolName, string(membersJSON), string(identitiesJSON), "started")
	if err != nil {
		return 0, fmt.Errorf("写入持久化恢复记录失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("无法获取恢复记录 ID: %w", err)
	}
	log.Printf("[POOL_DELETE_RECOVERY] 记录已创建 id=%d type=%s device=%s members=%s identities=%s", id, poolType, poolDevice, string(membersJSON), string(identitiesJSON))
	return id, nil
}

// updatePoolDeletePhase 在越过某个不可逆步骤后更新阶段标记（如 VG 已删除/
// 阵列已停止）。返回 error 而不是只记日志——此时不可逆命令已经执行成功，
// 调用方不应该、也不能据此撤销已经执行的操作，但必须知道"阶段字段这次
// 没能成功写入，记录里的 Phase 可能落后于磁盘层面实际状态"，才能如实地
// 把这个局限性体现在响应/日志里（而不是悄悄假装阶段字段永远可靠）。
// 更新失败时额外尝试把 PhaseUpdateWarning 写成功，便于恢复时的人工核实；
// 这一步本身失败也只记日志，不再二次返回 error（避免无限递归式的"更新
// 失败的更新也失败"处理）。
func updatePoolDeletePhase(id int64, phase string) error {
	if id == 0 {
		return nil
	}
	db := initPendingDB()
	if db == nil {
		return fmt.Errorf("数据库未初始化，无法更新恢复记录阶段")
	}
	if _, err := db.Exec(`UPDATE pool_delete_recovery SET phase = ? WHERE id = ?`, phase, id); err != nil {
		log.Printf("[POOL_DELETE_RECOVERY] 更新阶段失败 id=%d phase=%s: %v", id, phase, err)
		warnErr := fmt.Errorf("更新持久化恢复记录阶段失败（阶段字段可能不准确，id=%d phase=%s）: %w", id, phase, err)
		if _, werr := db.Exec(`UPDATE pool_delete_recovery SET phase_update_warning = ? WHERE id = ?`, warnErr.Error(), id); werr != nil {
			log.Printf("[POOL_DELETE_RECOVERY] 写入阶段更新警告也失败 id=%d: %v", id, werr)
		}
		return warnErr
	}
	return nil
}

// clearPoolDeleteRecovery 从数据库删除一条恢复记录。【验收复查后收紧】
// 这个函数本身不再由 handlePoolDelete 自动调用——无论删除结果是完全
// 成功、部分失败还是配置清理失败，写 HTTP 响应都不构成"已确认交付给
// 调用方"：连接可能已经断开、响应可能从未真正送达客户端。现在只有
// confirmPoolDeleteRecovery（管理员显式核实之后触发的动作）会调用它；
// 保留为内部函数供该入口和测试复用。
func clearPoolDeleteRecovery(id int64) {
	if id == 0 {
		return
	}
	db := initPendingDB()
	if db == nil {
		return
	}
	if _, err := db.Exec(`DELETE FROM pool_delete_recovery WHERE id = ?`, id); err != nil {
		log.Printf("[POOL_DELETE_RECOVERY] 清除记录失败 id=%d: %v", id, err)
	}
}

// confirmPoolDeleteRecovery 由管理员在人工核实磁盘层面状态之后显式触发，
// 清除一条指定 id 的恢复记录。与 handlePoolDeleteRecoveryStatus（只读
// 展示）严格分离：状态查询接口永远不会产生副作用、永远不会执行任何
// wipe 相关操作；本函数也只做数据库删除，不做任何磁盘操作——清除记录
// 的前提（磁盘层面是否真的已经处理干净）完全由管理员自行核实，面板不
// 代替人做这个判断，也不会因为"确认清除"这个动作触发任何新的破坏性
// 命令。id 不存在或已被清除时返回 error，调用方据此明确告知管理员，而
// 不是模糊地返回成功。
func confirmPoolDeleteRecovery(id int64) error {
	if id <= 0 {
		return fmt.Errorf("无效的恢复记录 id: %d", id)
	}
	db := initPendingDB()
	if db == nil {
		return fmt.Errorf("数据库未初始化，无法确认清除恢复记录")
	}
	ensurePoolDeleteRecoverySchema(db)
	res, err := db.Exec(`DELETE FROM pool_delete_recovery WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("清除恢复记录失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("无法确认恢复记录是否已清除: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("恢复记录 id=%d 不存在或已被清除", id)
	}
	log.Printf("[POOL_DELETE_RECOVERY] 管理员已确认清除记录 id=%d", id)
	return nil
}

// handlePoolDeleteRecoveryConfirm 是清除池删除恢复记录的唯一入口——POST-only，
// 要求显式传入 id，且必须是管理员在核实磁盘层面状态之后的主动操作。与
// handlePoolDeleteRecoveryStatus（GET，只读展示，不产生任何副作用）严格
// 区分：本接口只删除数据库记录，不执行、也不触发任何 wipe/wipefs/mdadm/
// lvremove 等破坏性命令——它面对的"身份确认"场景可能涉及一个已经不完整、
// 不可信的设备集合（Requirement 3），所以这里的唯一职责是数据库记录的
// 生命周期管理，绝不能变成针对未知身份设备的二次破坏性操作入口。
func handlePoolDeleteRecoveryConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idStr := r.FormValue("id")
	if idStr == "" {
		jsonError(w, "缺少 id 参数", http.StatusBadRequest)
		return
	}
	var id int64
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
		jsonError(w, "id 参数无效", http.StatusBadRequest)
		return
	}
	if err := confirmPoolDeleteRecovery(id); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.LogAuditRequest(r, "STORAGE", "确认清除池删除恢复记录", fmt.Sprintf("id=%d", id), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// GetPoolDeleteRecoveries 返回所有尚未清除的池删除恢复记录——即进程在
// 越过不可逆步骤之后、结果报告给调用方之前被中断的删除操作。供
// /api/disk/pool/delete/recovery 及运维排查使用。
//
// 返回 error 而不是把查询失败悄悄当成"零记录"——db.Query 本身失败（数据库
// 损坏/磁盘只读/连接已关闭等）时，调用方如果把 nil 切片和 nil error 同等
// 对待，会把"无法确认是否有被中断的删除"误报成"确认没有被中断的删除"，
// 这正是本轮复查要修复的 fail-open 问题。调用方必须对 error fail-closed：
// 明确告知管理员"无法确认"，不能呈现为一切正常的空列表。
func GetPoolDeleteRecoveries() ([]PoolDeleteRecovery, error) {
	db := initPendingDB()
	if db == nil {
		return nil, fmt.Errorf("数据库未初始化，无法查询持久化恢复记录")
	}
	ensurePoolDeleteRecoverySchema(db)
	rows, err := db.Query(`SELECT id, pool_type, pool_device, pool_name, members, member_identities, phase, phase_update_warning, created_at FROM pool_delete_recovery ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询持久化恢复记录失败: %w", err)
	}
	defer rows.Close()
	var out []PoolDeleteRecovery
	for rows.Next() {
		var rec PoolDeleteRecovery
		if err := rows.Scan(&rec.ID, &rec.PoolType, &rec.PoolDevice, &rec.PoolName, &rec.Members, &rec.MemberIdentities, &rec.Phase, &rec.PhaseUpdateWarning, &rec.CreatedAt); err != nil {
			// 单行解析失败同样 fail-closed：不能跳过这一行假装它不存在
			// （rows.Scan 出错通常意味着列类型/schema 异常，比"零记录"更
			// 值得警觉，继续返回已经扫描成功的行会让管理员误以为列表完整）。
			return nil, fmt.Errorf("解析持久化恢复记录失败: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历持久化恢复记录失败: %w", err)
	}
	return out, nil
}

// handlePoolDeleteRecoveryStatus 暴露未清除的池删除恢复记录，供面板在
// 启动后或管理员手动检查时发现"上次删除可能被中断，以下设备需要人工核实"。
// 查询失败时返回 500 + 明确的"无法确认"文案，不能退化成一个看起来空空如也
// 的 recoveries:[] 列表——那会让管理员误以为"没有被中断的删除"，而真实
// 情况是数据库当前不可读，两者必须能被调用方区分开。
func handlePoolDeleteRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	recs, err := GetPoolDeleteRecoveries()
	if err != nil {
		jsonError(w, fmt.Sprintf("无法确认是否存在被中断的池删除操作（查询持久化恢复记录失败），请勿认为数据安全，需要人工检查数据库: %s", err.Error()), http.StatusInternalServerError)
		return
	}
	if recs == nil {
		recs = []PoolDeleteRecovery{}
	}
	common.JSONResponse(w, map[string]interface{}{
		"recoveries": recs,
		"count":      len(recs),
	})
}

// poolDeleteRecoveryNowFn 测试注入 seam（时间戳相关逻辑如需要可用）。
var poolDeleteRecoveryNowFn = time.Now
