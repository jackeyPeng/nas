package diskmgmt

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"nas-panel/common"
)

// QuotaManager handles XFS project quotas for shared folders
// Uses /etc/projects and /etc/projid to map folder paths to project IDs

const (
	quotaBaseID = 1000 // start project IDs from 1000 to avoid system conflicts
)

// quotaProjectsFile / quotaProjidFile 默认指向真实 /etc/projects、/etc/projid；
// 测试注入指向 t.TempDir() 下的假文件，避免读写真实系统配置（Item 7 修复）。
var (
	quotaProjectsFile = "/etc/projects"
	quotaProjidFile   = "/etc/projid"
)

// quotaMu 串行化 setFolderQuota 的「查找/创建 project ID + 读写
// /etc/projects、/etc/projid」临界区（Item 7 修复）。
//
// 问题：findProjectID 读一次 projid，未命中时 getNextProjectID 再读一次同一
// 文件算出"下一个 ID"再 append；两个不同文件夹并发调用 setFolderQuota 时，
// 可能同时读到同一个 maxID 并算出相同的"下一个 ID"，导致两个文件夹的配额
// 互相覆盖（同一 projID 映射到不同 path，xfs_quota project -s 以最后写入的
// /etc/projects 条目为准）。removeProjectEntry/updateProjectPath 同样读写
// 这两个文件，必须纳入同一把锁。
//
// 参照 config_sync.go 的 syncAllMu：单管理员场景锁竞争可忽略，用普通
// sync.Mutex 整体串行化即可，不需要更细粒度的锁。
var quotaMu sync.Mutex

// getNextProjectID finds the next available project ID
func getNextProjectID() int {
	data, err := os.ReadFile(quotaProjidFile)
	if err != nil {
		return quotaBaseID
	}
	maxID := quotaBaseID - 1
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 2 {
			if id, err := strconv.Atoi(parts[1]); err == nil && id > maxID {
				maxID = id
			}
		}
	}
	return maxID + 1
}

// projectName generates a project name from pool name and folder name
func projectName(poolName, folderName string) string {
	// Sanitize: only alphanumeric and underscore
	name := poolName + "_" + folderName
	var result []rune
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result = append(result, r)
		}
	}
	if len(result) == 0 {
		return "folder_" + folderName
	}
	return string(result)
}

// setFolderQuota sets XFS project quota on a folder
// quotaGB: 0 = remove quota (unlimited), >0 = hard limit in GB
func setFolderQuota(mountPoint, folderPath, poolName, folderName string, quotaGB int) error {
	projName := projectName(poolName, folderName)

	// Item 7 修复：查找/创建 projectID 与读写 /etc/projects、/etc/projid 必须
	// 整体串行化，否则并发调用会算出相同的"下一个 ID"并互相覆盖映射。
	quotaMu.Lock()
	defer quotaMu.Unlock()

	if quotaGB <= 0 {
		// Remove quota: clear the filesystem-level project quota, then
		// remove the /etc/projects、/etc/projid 映射。
		//
		// Item 3 修复：此前 removeProjectEntry 的错误被直接忽略（原调用是裸的
		// removeProjectEntry(projName)，不接返回值），且 xfs_quota project -C
		// 的返回值也被直接丢弃——无论映射文件和文件系统上的 project 配额是否
		// 真的清除，都向调用方返回 nil（"成功"）。现在两步的错误都向上传播。
		//
		// Item 4 修复（顺序 bug）：此前的顺序是"先删映射，再 project -C"，
		// 但 `xfs_quota -x -c "project -C <name>" <mountPoint>` 的 <name> 是
		// project **名字**，xfs_quota 要靠 /etc/projid 把这个名字解析成
		// project ID 才能定位到要清除哪个 project（与 `project -s` 用同一套
		// 名字解析机制）。如果先把 /etc/projects、/etc/projid 里的映射删了，
		// 这个名字在 xfs_quota 眼里就已经不存在，`project -C projName`
		// 会因为解析不到名字而失败（或者什么都不清除却返回看似正常的结果，
		// 取决于 xfs_quota 版本），导致文件系统上的 project 配额实际从未被
		// 清除，而我们还以为已经清理完毕。
		//
		// 现在倒过来：
		//
		// 1) 先用 project -C 清除文件系统上的 project 配额——此时映射还
		//    健在，名字能正常解析到 ID。如果这一步失败，直接返回错误、
		//    不触碰映射文件，状态等同于"还原到调用前"（清除失败=什么都
		//    没变，可以安全重试，不会把映射删了却留下文件系统上清不掉的
		//    project）。
		//
		// 2) project -C 成功后再删映射文件；如果这一步失败，文件系统上的
		//    project 配额已经被清除（真正的"无限制"状态），只是 /etc/projects、
		//    /etc/projid 里还残留着旧的 name/path 映射——这是一个相对温和的
		//    不一致（配额确实已经移除，只是记账文件没跟上），比旧顺序下
		//    "映射没了但配额还在生效"更安全：旧顺序的失败模式是用户以为
		//    配额已取消、实际仍受限；新顺序的失败模式是多了一条孤儿映射，
		//    下次对同一 projName 调用 setFolderQuota 时 findProjectID 会
		//    命中这条孤儿记录并按"已存在"逻辑更新 path 续用，不会造成数据
		//    安全问题，顶多是 projID 复用了旧编号——仍按原样如实报错，不
		//    伪造"回滚成功"的假象，提示需要人工检查/重试 removeProjectEntry。
		if out, err := sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c", fmt.Sprintf("project -C %s", projName), mountPoint); err != nil {
			return fmt.Errorf("清除文件系统上的 project 配额失败（映射未改动，可安全重试）: %s: %v", out, err)
		}
		if err := removeProjectEntry(projName); err != nil {
			return fmt.Errorf("文件系统上的 project 配额已清除，但删除配额映射失败（%s/%s 中可能残留孤儿映射，建议人工检查或重试）: %v",
				quotaProjectsFile, quotaProjidFile, err)
		}
		return nil
	}

	// 能力探测：文件系统不支持 project quota 时明确报错，不静默失效（加固轮1 #4）
	if st := mountQuotaSupportFn(mountPoint); !st.Supported {
		return fmt.Errorf("配额不可用: %s", st.Reason)
	}

	// Find or create project ID
	//
	// Item D 修复：此前任一步骤失败都是直接 return err，但前面已经成功写入的
	// /etc/projects、/etc/projid 条目、以及已经在文件系统上生效的 xfs_quota
	// project 都原样留在原地——报告调用方"失败"，实际上留下了一个半成品状态
	// （例如 /etc/projects 有映射但 /etc/projid 没有；或者 project 已经在目录
	// 上生效但没有 limit，等于一个"无限制"的配额）。现在每个失败分支都主动
	// 回滚已完成的步骤，保证 setFolderQuota 要么整体成功、要么把状态还原到
	// 调用前（fail-closed），不留半成品。
	projID := findProjectID(projName)
	createdNewEntries := false
	if projID < 0 {
		projID = getNextProjectID()
		// Add to /etc/projects: 标准格式 id:path（XFS 要求 ID 在前）
		entry := fmt.Sprintf("%d:%s\n", projID, folderPath)
		if err := appendToFile(quotaProjectsFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
		}
		// Add to /etc/projid: name:id
		entry = fmt.Sprintf("%s:%d\n", projName, projID)
		if err := appendToFile(quotaProjidFile, entry); err != nil {
			// /etc/projects 已经写入但 /etc/projid 失败：回滚刚写入的
			// /etc/projects 条目，避免留下一个在 projid 里找不到名字的
			// 孤儿映射。此时 /etc/projid 里从未写入过这个 name，只能按
			// 已知的 projID 直接删除，不能走 removeProjectEntry(name)
			// （它要先从 projid 查 id，这里必然查不到）。
			if rbErr := removeProjectsEntryByID(projID); rbErr != nil {
				return fmt.Errorf("写入 %s 失败: %v（回滚 %s 中的孤儿条目也失败: %v，请人工检查）",
					quotaProjidFile, err, quotaProjectsFile, rbErr)
			}
			return fmt.Errorf("写入 %s 失败: %v", quotaProjidFile, err)
		}
		createdNewEntries = true
	} else {
		// Update path in /etc/projects if changed
		if err := updateProjectPath(projName, projID, folderPath); err != nil {
			return fmt.Errorf("更新 %s 失败: %v", quotaProjectsFile, err)
		}
	}

	// Initialize project on the directory
	out, err := sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c", fmt.Sprintf("project -s %s", projName), mountPoint)
	if err != nil {
		// project -s 失败：如果本次调用是新建的映射条目（projid 刚才还不
		// 存在），把刚写入的 /etc/projects、/etc/projid 一并回滚，不留下
		// 文件里有映射但系统上从未生效的半成品；已存在的映射（更新路径场景）
		// 不回滚路径更新，只报告失败——旧的 project 配置仍然有效，不需要
		// 撤销本来就存在的状态。
		if createdNewEntries {
			if rbErr := removeProjectEntry(projName); rbErr != nil {
				return fmt.Errorf("xfs_quota project 失败: %s: %v（回滚映射条目也失败: %v，请人工检查 %s/%s）",
					out, err, rbErr, quotaProjectsFile, quotaProjidFile)
			}
		}
		return fmt.Errorf("xfs_quota project 失败: %s: %v", out, err)
	}

	// Set hard limit (bhard = block hard limit, in KB)
	limitKB := quotaGB * 1024 * 1024
	out, err = sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c",
		fmt.Sprintf("limit -p bhard=%dk %s", limitKB, projName), mountPoint)
	if err != nil {
		// limit 设置失败：project 已经在目录上生效（上一步成功），但没有
		// 上限——等于一个被标记为"已配额"却实际无限制的目录，对用户更危险。
		// 清掉刚生效的 project（project -C），回到"未配额"的已知状态；
		// 新建的映射条目也一并回滚。清理失败只追加警告，不掩盖原始错误。
		if _, clearErr := sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c", fmt.Sprintf("project -C %s", projName), mountPoint); clearErr != nil {
			err = fmt.Errorf("%v（回滚 project 也失败: %v，/etc/projects 和 /etc/projid 中的映射已保留以便重试，请人工检查 %s 上的 project 状态）", err, clearErr, mountPoint)
		} else if createdNewEntries {
			if rbErr := removeProjectEntry(projName); rbErr != nil {
				err = fmt.Errorf("%v（project 已回滚，但清理映射条目失败: %v，请人工检查 %s/%s）", err, rbErr, quotaProjectsFile, quotaProjidFile)
			}
		}
		return fmt.Errorf("xfs_quota limit 失败: %s: %v", out, err)
	}

	return nil
}

// getFolderQuota reads current quota for a folder from xfs_quota report
func getFolderQuota(mountPoint, poolName, folderName string) (usedGB float64, limitGB int, err error) {
	projName := projectName(poolName, folderName)

	out, err := common.SudoOutput("/usr/sbin/xfs_quota", "-x", "-c", "report -p -N", mountPoint)
	if err != nil {
		return 0, 0, err
	}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == projName {
			// fields: name, used(KB), soft, hard
			if used, err := strconv.ParseFloat(fields[1], 64); err == nil {
				usedGB = used / 1024 / 1024
			}
			if hard, err := strconv.Atoi(fields[3]); err == nil {
				limitGB = hard / 1024 / 1024
			}
			return usedGB, limitGB, nil
		}
	}
	return 0, 0, nil // no quota set
}

// findProjectID looks up project ID by name from /etc/projid
func findProjectID(name string) int {
	data, err := os.ReadFile(quotaProjidFile)
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 2 && parts[0] == name {
			if id, err := strconv.Atoi(parts[1]); err == nil {
				return id
			}
		}
	}
	return -1
}

// removeProjectsEntryByID 仅从 /etc/projects 按已知 id 删除对应行，不经过
// /etc/projid 做 name→id 查找。
//
// Item D 修复：setFolderQuota 在 /etc/projects 写入成功、/etc/projid 写入
// 失败时需要回滚刚写入的 /etc/projects 孤儿条目——但此时 /etc/projid 里从未
// 出现过这个 name，removeProjectEntry 内部的 findProjectID(name) 必然返回
// -1，按 name 做兜底匹配又因为标准 id:path 行没有 name 字段而匹配不到，
// 导致孤儿条目删不掉。这里用调用方已经手上的 id 直接按 id 删除，绕开这个
// 查找链路。
func removeProjectsEntryByID(id int) error {
	data, err := os.ReadFile(quotaProjectsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 %s 失败: %v", quotaProjectsFile, err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		lineID, _, _, ok := common.ParseProjectsLine(line)
		if ok && lineID == id {
			continue
		}
		lines = append(lines, line)
	}
	if err := safeWriteFileFn(quotaProjectsFile, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
	}
	return nil
}

// removeProjectEntry removes entries from /etc/projects and /etc/projid
// /etc/projects 标准格式是 id:path，没有 name，所以先从 /etc/projid 查出 id 再按 id 匹配删除。
// 解析沿用 common.ParseProjectsLine，顺带归一化历史脏数据（name:id:path → id:path）。
// Item 7 修复：写入失败必须向上传播，不能被调用方静默吞掉——否则配额映射
// 文件与 xfs_quota 实际状态不一致却对用户报告"已取消"。
func removeProjectEntry(name string) error {
	id := findProjectID(name)

	// Remove from /etc/projects（按 id 匹配；id<0 说明 projid 里也没有，整行按 name 兜底匹配脏数据）
	if data, err := os.ReadFile(quotaProjectsFile); err == nil {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			lineID, _, lineName, ok := common.ParseProjectsLine(line)
			if ok && ((id >= 0 && lineID == id) || (lineName != "" && lineName == name)) {
				continue
			}
			lines = append(lines, line)
		}
		if err := safeWriteFileFn(quotaProjectsFile, strings.Join(lines, "\n")); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取 %s 失败: %v", quotaProjectsFile, err)
	}
	// Remove from /etc/projid
	if data, err := os.ReadFile(quotaProjidFile); err == nil {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), name+":") {
				lines = append(lines, line)
			}
		}
		if err := safeWriteFileFn(quotaProjidFile, strings.Join(lines, "\n")); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjidFile, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取 %s 失败: %v", quotaProjidFile, err)
	}
	return nil
}

// updateProjectPath updates the path in /etc/projects for an existing project（按 id 匹配，
// 同时把历史脏数据行归一化为标准 id:path 格式）
func updateProjectPath(name string, id int, newPath string) error {
	data, err := os.ReadFile(quotaProjectsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 %s 失败: %v", quotaProjectsFile, err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		lineID, _, lineName, ok := common.ParseProjectsLine(line)
		if ok && (lineID == id || (lineName != "" && lineName == name)) {
			lines = append(lines, fmt.Sprintf("%d:%s", id, newPath))
		} else {
			lines = append(lines, line)
		}
	}
	if err := safeWriteFileFn(quotaProjectsFile, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
	}
	return nil
}

// appendToFile appends a line to a file (creates if not exists)
func appendToFile(path, content string) error {
	existing, _ := os.ReadFile(path)
	newContent := string(existing)
	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += content
	return safeWriteFileFn(path, newContent)
}

// handleFolderQuota handles GET (read quota) and POST (set quota) for a folder
func handleFolderQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		poolName := r.URL.Query().Get("pool")
		folderName := r.URL.Query().Get("folder")
		mountPoint := r.URL.Query().Get("mountpoint")
		if poolName == "" || folderName == "" || mountPoint == "" {
			jsonError(w, "pool, folder, mountpoint required", http.StatusBadRequest)
			return
		}

		usedGB, limitGB, err := getFolderQuota(mountPoint, poolName, folderName)
		if err != nil {
			// 查询失败不再伪装成"无限制"（加固轮1 #4）
			if st := mountQuotaSupportFn(mountPoint); !st.Supported {
				common.JSONResponse(w, map[string]interface{}{
					"folder":       folderName,
					"quota_status": "unsupported",
					"reason":       st.Reason,
				})
				return
			}
			common.JSONResponse(w, map[string]interface{}{
				"folder":       folderName,
				"quota_status": "error",
				"reason":       err.Error(),
			})
			return
		}

		common.JSONResponse(w, map[string]interface{}{
			"folder":       folderName,
			"used_gb":      fmt.Sprintf("%.1f", usedGB),
			"limit_gb":     limitGB,
			"unlimited":    limitGB == 0,
			"quota_status": "ok",
		})
		return
	}

	if r.Method == http.MethodPost {
		poolName := r.FormValue("pool")
		folderName := r.FormValue("folder")
		mountPoint := r.FormValue("mountpoint")
		folderPath := r.FormValue("path")
		quotaStr := r.FormValue("quota_gb")

		if poolName == "" || folderName == "" || mountPoint == "" || folderPath == "" {
			jsonError(w, "pool, folder, mountpoint, path required", http.StatusBadRequest)
			return
		}

		quotaGB := 0
		if quotaStr != "" {
			var err error
			quotaGB, err = strconv.Atoi(quotaStr)
			if err != nil || quotaGB < 0 {
				jsonError(w, "quota_gb must be a non-negative integer (0=unlimited)", http.StatusBadRequest)
				return
			}
		}

		if err := setFolderQuota(mountPoint, folderPath, poolName, folderName, quotaGB); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		msg := fmt.Sprintf("文件夹 %s 配额已设置为 %dGB", folderName, quotaGB)
		if quotaGB == 0 {
			msg = fmt.Sprintf("文件夹 %s 配额已取消（无限制）", folderName)
		}
		common.JSONResponse(w, map[string]interface{}{"message": msg})
		return
	}

	jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
}
