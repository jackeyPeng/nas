# 任务：diskmgmt 第二轮 — 存储安全 P0 残项 + 专用整机定位收敛

你在 ~/soft/nas 仓库工作。Go module 在 `web/`。产品定位已拍板：**专用 NAS 整机**（数据盘由面板独占管理，一机一主池）。以下 file:line 由 parent 对 commit 8609240 之后的 HEAD 核实过；代码现实优先于本文，动手前先通读列出的源文件。

## 硬约束（违反 = 返工）

- 禁止读取仓库外任何文件（~/go/pkg/mod、/tmp 实验等一律禁止）——一次 external_directory 拒绝即整轮作废。测试只用 `go test`（t.TempDir() 可用）。
- 零 CGO、零新依赖。
- 后端只改 `web/modules/diskmgmt/`；前端只改 `web/frontend/app.js`、`web/frontend/index.html`、i18n 语言包（如动 i18n 必须跑 `python3 scripts/check-i18n.py` 保证 zh/en parity）。禁止碰 third_party/。
- 不要全仓 gofmt；改动文件 gofmt 干净即可。
- 任务书缺口或矛盾写进报告申报，不得静默绕过。
- 两个提交，commit message 原样使用（见各部分末尾）。报告写仓库根 `TASK-REPORT.md`（覆盖旧内容，不 git add）。

## 构建与测试

```
cd web && GOPROXY=https://goproxy.cn,direct go build ./... && go vet ./modules/diskmgmt/ && go test ./modules/diskmgmt/ -count=1
```

已核实事实（勿再查证）：`common.SudoExec/SudoOutput/ExecOutput(name string, args ...string) (string, error)`。前端 app.js 的 `diskMkdir/diskMount/diskUnmount/diskFormat` 四个方法在 index.html 中零引用（死 UI 代码，无按钮调用）。真机 fstab 行是 `UUID=xxx /data/nas1 xfs defaults,prjquota 0 2` 形态（writeFstab wizard.go:466 生成）。LVM 池 device 经 detectPoolDevice 归一为 `/dev/vg_nas` 后才进 extractVGName，真实删池链路通。

---

# 提交 A：安全修复（4 项）

## A1 — RAID1 resync 超时后禁止继续扩文件系统

现状：`raid_expand.go:300` `waitForResync` 为 void，`for i:=0;i<600;i++` 2s 一轮最多等 10 分钟，超时后静默返回；调用方（raid_expand.go:177 附近）无条件 `stepDone("数据同步完成")` 并继续 growFilesystem——阵列还在重构就扩文件系统。

改法：`waitForResync` 返回 `error`（或 bool）：正常完成返回 nil；600 轮超时返回非 nil（信息含「阵列同步未在 10 分钟内完成」）。调用方超时则 sendRAIDExpandProgress 发 error 事件（Detail 说明：已中止，文件系统未扩展；阵列仍在后台同步属正常，等同步完成后可用「扩展文件系统」入口（/api/disk/raid/expand-fs）单独补跑）并 return，不再 stepDone、不再 growFilesystem。

## A2 — RAID0 不提供扩容/换盘入口

现状：前端 `raidPools` getter（app.js:725）按 `type` 前缀 raid 过滤，raid0 池会进「替换盘」下拉与扩容路由；后端 handleRAIDExpandStream/handleReplaceDisk 未见显式 level 检查（自行核实实际行为）。

改法：
- 后端：handleRAIDExpandStream 与 handleReplaceDisk 在拿锁前显式读取阵列 level（mdadm --detail 或 /sys/block/mdX/md/level），raid0 直接 error 事件/JSON 400 拒绝（文案说明 RAID0 无冗余、不支持扩容/换盘）。若核实后发现后端已有等效拒绝，报告说明并保持不重复加。
- 前端：替换盘池下拉与扩容按钮对 `type==='raid0'` 的池隐藏/禁用（含提示文案），从 raidPools 消费侧过滤，别改 raidPools getter 本身（其他消费方可能要全量 raid 列表——自行 grep 确认后选择最小改法）。

## A3 — 删池时未挂载池的 fstab UUID 行残留

现状：`filterFstabForDeletedPool(fstab, deletedMounts, lvmVgName)`（pool.go，commit 8609240 新增）判据 (a) 挂载点精确匹配、(b) LVM 设备前缀。真机 fstab 是 UUID 行：池处于挂载态时 (a) 可命中；**池未挂载时删除**（findmnt 空 → deletedMountPoints 空）UUID 行残留 → 下次开机 systemd 等不到设备进 emergency mode。

改法：签名扩为 `filterFstabForDeletedPool(fstab string, deletedMounts []string, deletedUUIDs []string, lvmVgName string) string`，新增判据 (c)：`fields[0] == "UUID="+uuid`（uuid ∈ deletedUUIDs）。handlePoolDelete 在拆除前采集 UUID：
- lvm 分支：对每个 lvPath 在 lvremove 之前 `lsblk -no UUID <lvPath>`；
- raid 分支：mdadm --stop 之前 `blkid -s UUID -o value <poolDevice>`；
- single 分支：wipefs 之前同 blkid。
采集失败（空值）跳过该条即可，不阻断删除。更新既有 `TestFilterFstabForDeletedPool` 并新增 UUID 判据子用例（含：未挂载池只剩 UUID 行也被删、其他池 UUID 行保留）。

同函数两个顺手修（独立审查 Minor findings，与 A3 同区域）：
- pool.go:510 附近 `pvsOut, _ := common.SudoOutput("/usr/sbin/pvs", ...)` 错误被丢弃——pvs 失败时 memberPVs 为空、vgremove 照常执行会静默复现幽灵盘。改为接 err：非 nil 时中止删除（http.Error JSON 500，说明无法确认 VG 成员、为安全拒绝删除）或至少记入 steps 并跳过 vgremove——二选一，报告说明选择理由。
- pool.go:493 附近 `mountPoint := strings.TrimSpace(mntOut)`——findmnt 对多挂载点（bind mount）LV 输出多行，脏值会破坏判据(a)与 RemoveFolderMetaByMountPrefix。改为取首行（strings.SplitN(mntOut, "\n", 2)[0] 再 TrimSpace）。

## A3b — pool_extend 步骤名 running/done 失配（独立审查 Minor）

现状：pool_extend.go:96-120 `stepRunning("初始化物理卷 (pvcreate)")` vs `stepDone("初始化物理卷")`、`stepRunning("扩展逻辑卷 (lvextend)")` vs `stepDone("扩展逻辑卷")`——前端按名称配对 running→done，失配导致 running 条目永不置 done、另推一条 done。改法：统一每对 stepRunning/stepDone 的名称字符串（保留带命令名的写法即可），逐对 grep 核对。

## A4 — detectPoolDevice 的 VG 子串兜底会前缀误命中

现状：`overview.go` detectPoolDevice（约 :537-560）对 /dev/mapper/<name> 遍历 vgs 输出，循环体内先试前缀规则、**紧接着同一轮就做 `strings.Contains(mapperName, vg)` 子串兜底**——若 VG 列表里 "vg" 排在 "vg_nas" 前面，mapper "vg_nas-data" 会在第一轮被子串兜底误判成 VG "vg"。

改法：把匹配逻辑抽成纯函数 `matchVGForMapper(mapperName string, vgList []string) string`：第一遍全体 VG 只做精确前缀规则（`vgEscaped+"-"` 或 `vg+"-"` 前缀，vgEscaped 是 `-`→`--` 转义），无命中才第二遍做子串兜底；仍无命中返回 ""。detectPoolDevice 改为调用它（vgs exec 留在原函数）。单测覆盖：正常 mapper、VG 名含连字符（--转义）、"vg"/"vg_nas" 前缀冲突顺序（两种排列都要对）、无匹配返回空。

## A5 — 「含数据但不属于任何池」的盘在存储总览页消失（独立审查 Minor，Fix 1a 的展示缺口）

现状：Fix 1a 后，带未挂载数据分区的盘 Type=data、正确脱离 FreeDisks，但 overview.go:366-380 的分桶 else 分支对它 `findDiskPoolDevice` 返回空 → Status 被强写 "pool_member"、Pool 为空、且未追加进任何桶 → 存储总览页看不到这块盘（只有 /disk/status 磁盘列表页可见）。这正是第二份外部评审说的 foreign（含存量数据、未纳管）类别缺失。

改法（最小范围）：
- 后端 overview.go：else 分支中 `poolDev == ""` 时不再强写 pool_member——`ds.Status = "unmanaged"`，并追加进 StorageOverview 新字段 `UnmanagedDisks []DiskSummary`（**必须 `make([]DiskSummary, 0)` 初始化，nil slice 序列化成 null 会崩前端 .length**，这是本仓库已知坑）。JSON tag 用 `unmanaged_disks`。
- 前端：存储管理页「物理磁盘」tab（disks）新增一小节「⚠️ 未纳管磁盘（含存量数据）」渲染 `storageOverview.unmanaged_disks || []`（卡片样式沿用 free_disks 虚线卡但边框用 warn 色，提示文案：该盘含已有数据/分区，不能直接建池；如需复用请先备份并显式擦除）。i18n 键 zh/en 同步（storage.unmanaged_title / storage.unmanaged_hint 之类），跑 check-i18n.py。空数组时整节不渲染（x-show/template x-if 守卫，注意 null 守卫用 `|| []`）。
- 仪表盘 disk bay 等其他消费方不动（自行 grep unmanaged 确认无遗漏消费点，报告列出）。

**提交 A message（原样）：**
```
fix(diskmgmt): 存储安全P0残项 — resync超时禁扩fs/RAID0拒绝扩容换盘/删池UUID行清理与pvs查错/VG匹配前缀误命中/未纳管盘可见
```

---

# 提交 B：专用整机定位收敛（3 项）

## B1 — 低级别磁盘 API 关进调试开关

现状：`diskmgmt.go:27-30` 注册 /api/disk/format、/api/disk/mount、/api/disk/unmount、/api/disk/mkdir，仅认证保护；前端四个对应方法（app.js:1895-1950 附近 diskMkdir/diskMount/diskUnmount/diskFormat）在 index.html 零引用，是死代码。

改法：
- 后端：四个路由包一层守卫（如 `debugDiskGate(next)`，请求时读 `os.Getenv("NAS_DISK_DEBUG") == "1"` 才放行，否则 403 + JSON `{"error":"低级别磁盘接口已禁用（调试模式未开启）"}`）。不删 handler 本体。将来「显式擦除」领域功能（S2）会重新产品化 format。
- 前端：删除四个死方法 diskMkdir/diskMount/diskUnmount/diskFormat（grep 确认 index.html 与其他 JS 无引用后删；app.js 内如有互相调用一并清）。
- i18n：若四个方法关联的语言包键成为死键，本次不删（键清理需人工核对，报告中列出即可）。

## B2 — separate 多池模式移出创建主流程

现状：options.go 的 raid_options 仍含 separate；handleWizardSetupStream 接受 mode=separate；setupSeparateStream（stream.go:495 附近）创建 /data/nas1..N 多池——与「一机一主池」定位冲突。

改法：
- options.go：从 raid_options 输出中移除 separate 条目（前端向导数据驱动，自动不再显示）。
- stream.go：handleWizardSetupStream 对 mode=separate 发 error 事件（「separate 多池模式已下线，单池模型请使用 merge/RAID」）并 return；删除 setupSeparateStream 函数及 validateMode/getModeStepCount 的 separate 分支。
- app.js：清理向导链路对 separate 的引用（如 wizUsableText/wizUsableGB 的 separate case，2149 行附近注释；grep 'separate' 逐个确认属于向导创建链路才删）。
- **不动**存量 separate 机器的管理路径：pool 删除的 "single" 分支、overview 展示、fstab 等保持原样。
- 若 i18n 有 separate 专属键（storage.raid_name_separate 等）：保留键本体（历史机器 UI 可能仍渲染池类型标签），仅当向导创建链路引用被删导致死键时报告列出，不删键。跑 check-i18n.py 确认 parity。

## B3 — RAID1 加成员不叫「扩容」

现状：RAID1 的 expand 流程实际是「加镜像盘」，容量基本不变，UI 与文案统称「扩容」误导。

改法：前端 RAID 扩容对话框（openRAIDExpand 消费侧，app.js:2430 附近 + index.html 对应区块）按 raidType 区分文案：raid1 显示「添加镜像盘」（说明：提高冗余，容量不变），raid5/6 保持「扩容」。新文案走 i18n 键（zh/en 同步，跑 check-i18n.py）。后端不动。

**提交 B message（原样）：**
```
feat(diskmgmt): 专用整机定位收敛 — 低级别磁盘API调试开关/下线separate多池创建/RAID1加镜像文案区分
```

---

## 报告（TASK-REPORT.md，覆盖写，不入库）

每项改动摘要与文件、A2 后端现状核实结论、新增/修改测试清单与 `go test -v` 实际 PASS 计数、check-i18n.py 输出、发现的死键清单（只列不删）、偏离与申报。
