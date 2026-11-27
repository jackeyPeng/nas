package users

import (
	"fmt"
	"strconv"
	"strings"

	"nas-panel/common"
)

// privateDirQuota 查询私有目录配额。
// status: "ok" / "unsupported"(文件系统不支持 project quota) / "error"(查询失败) / "none"(无私有目录)
// 加固轮1 #4：查询失败不再伪装成"无限制"，由调用方按 status 呈现。
func privateDirQuota(username string) (usedGB float64, limitGB int, status, reason string) {
	// 主目录在 /data/nas1/username
	// 需要找到挂载点
	mountPoint := findMountPoint("/data/nas1/" + username)
	if mountPoint == "" {
		return 0, 0, "none", "找不到私有目录挂载点"
	}

	// 能力探测：ext4 或没开 prjquota 的 xfs 上配额根本不生效
	if st := common.MountQuotaSupport(mountPoint); !st.Supported {
		return 0, 0, "unsupported", st.Reason
	}

	projName := "home_" + username
	out, err := common.SudoOutput("/usr/sbin/xfs_quota", "-x", "-c", "report -p -N", mountPoint)
	if err != nil {
		return 0, 0, "error", fmt.Sprintf("xfs_quota report 失败: %v", err)
	}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == projName {
			if used, err := strconv.ParseFloat(fields[1], 64); err == nil {
				usedGB = used / 1024 / 1024
			}
			if hard, err := strconv.Atoi(fields[3]); err == nil {
				limitGB = hard / 1024 / 1024
			}
			return usedGB, limitGB, "ok", ""
		}
	}
	return 0, 0, "ok", "" // 无该 project = 未设配额（真·无限制）
}

// dirExistsFn/findMountPointFn 默认指向真实 dirExists/findMountPoint（分别
// 跑真实 sudo test -d、df）；测试注入替身，避免 setPrivateDirQuota 的单测
// 依赖真实 /data/nas1/<user> 路径和 sudo 环境。
var (
	dirExistsFn      = dirExists
	findMountPointFn = findMountPoint
)

// setPrivateDirQuota 设置私有目录配额
func setPrivateDirQuota(username string, quotaGB int) error {
	privDir := "/data/nas1/" + username
	if !dirExistsFn(privDir) {
		return fmt.Errorf("私有目录 %s 不存在", privDir)
	}

	mountPoint := findMountPointFn(privDir)
	if mountPoint == "" {
		return fmt.Errorf("找不到 %s 所在的挂载点", privDir)
	}

	projName := "home_" + username

	if quotaGB <= 0 {
		// 移除配额。
		//
		// 修复顺序 bug（与 diskmgmt/quota.go 的 Item 4 同一根因）：此前的顺序是
		// "先删 /etc/projects、/etc/projid 里的映射，再 project -C"，但
		// `xfs_quota -x -c "project -C <name>" <mountPoint>` 的 <name> 要靠
		// /etc/projid 解析成 project ID 才能定位——先删映射会让这个名字在
		// xfs_quota 眼里已经不存在，`project -C` 可能因解析不到而失败或
		// 什么都不清除，导致文件系统上的 project 配额实际从未被清除，却
		// 还以为已经清理完毕。同时旧代码把两步的返回值全部丢弃（裸调用，
		// 不接 error），无论成功失败都向调用方返回 nil。
		//
		// 现在倒过来，且两步的错误都向上传播：
		// 1) 先 project -C 清除文件系统上的配额——映射还健在，名字能正常
		//    解析。失败则直接返回错误、不触碰映射文件，等同于"回到调用前"，
		//    可安全重试。
		// 2) project -C 成功后再删映射；若这一步失败，文件系统上的配额已经
		//    真的清除（无限制状态已达成），只是映射文件残留孤儿记录——如实
		//    报告这个部分失败，不伪造"全部成功"。
		if out, err := sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c",
			fmt.Sprintf("project -C %s", projName), mountPoint); err != nil {
			return fmt.Errorf("清除文件系统上的 project 配额失败（映射未改动，可安全重试）: %s: %v", out, err)
		}
		if err := removeProjectEntry(projName); err != nil {
			return fmt.Errorf("文件系统上的 project 配额已清除，但删除配额映射失败（%s/%s 中可能残留孤儿映射，建议人工检查或重试）: %v",
				quotaProjectsFile, quotaProjidFile, err)
		}
		return nil
	}

	// 能力探测：文件系统不支持 project quota 时明确报错，不静默失效（加固轮1 #4）
	if st := common.MountQuotaSupport(mountPoint); !st.Supported {
		return fmt.Errorf("配额不可用: %s", st.Reason)
	}

	// 查找或创建 project ID
	projID := findProjectID(projName)
	if projID < 0 {
		projID = getNextProjectID()
		// 写入 /etc/projects：标准格式 id:path（XFS 要求 ID 在前）
		entry := fmt.Sprintf("%d:%s\n", projID, privDir)
		if err := appendToFile(quotaProjectsFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
		}
		// 写入 /etc/projid
		entry = fmt.Sprintf("%s:%d\n", projName, projID)
		if err := appendToFile(quotaProjidFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjidFile, err)
		}
	} else {
		if err := updateProjectPath(projName, projID, privDir); err != nil {
			return fmt.Errorf("更新 %s 失败: %v", quotaProjectsFile, err)
		}
	}

	// 初始化 project
	out, err := sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c",
		fmt.Sprintf("project -s %s", projName), mountPoint)
	if err != nil {
		return fmt.Errorf("xfs_quota project 失败: %s: %v", out, err)
	}

	// 设置硬限制
	limitKB := quotaGB * 1024 * 1024
	out, err = sudoExecFn("/usr/sbin/xfs_quota", "-x", "-c",
		fmt.Sprintf("limit -p bhard=%dk %s", limitKB, projName), mountPoint)
	if err != nil {
		return fmt.Errorf("xfs_quota limit 失败: %s: %v", out, err)
	}

	return nil
}

// findMountPoint 查找路径所在的挂载点
func findMountPoint(path string) string {
	out, err := common.ExecOutput("df", "--output=target", path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) >= 2 {
		return strings.TrimSpace(lines[1])
	}
	return ""
}

// --- 从 diskmgmt/quota.go 复制的辅助函数 ---
//
// sudoExecFn/execOutputFn/safeWriteFileFn 默认指向 common 包的真实实现；
// 测试注入替身以避免真实 sudo/xfs_quota//etc 文件读写（issue #4 修复：
// setPrivateDirQuota 此前完全无法脱离真实 root/xfs_quota 环境测试）。
var (
	sudoExecFn      = common.SudoExec
	execOutputFn    = common.ExecOutput
	safeWriteFileFn = common.SafeWriteFile
)

// quotaProjectsFile / quotaProjidFile 默认指向真实 /etc/projects、/etc/projid；
// 测试注入指向 t.TempDir() 下的假文件，避免读写真实系统配置。
var (
	quotaProjectsFile = "/etc/projects"
	quotaProjidFile   = "/etc/projid"
)

const quotaBaseID = 1000

func getNextProjectID() int {
	data, err := execOutputFn("cat", quotaProjidFile)
	if err != nil {
		return quotaBaseID
	}
	maxID := quotaBaseID - 1
	for _, line := range strings.Split(data, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 2 {
			if id, err := strconv.Atoi(parts[1]); err == nil && id > maxID {
				maxID = id
			}
		}
	}
	return maxID + 1
}

func findProjectID(name string) int {
	data, err := execOutputFn("cat", quotaProjidFile)
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(data, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 2 && parts[0] == name {
			if id, err := strconv.Atoi(parts[1]); err == nil {
				return id
			}
		}
	}
	return -1
}

// removeProjectEntry：/etc/projects 标准格式是 id:path，没有 name，先从 /etc/projid 查出 id
// 再按 id 匹配删除；解析沿用 common.ParseProjectsLine，顺带归一化历史脏数据。
// issue #4 修复：写入失败必须向上传播，不能被调用方静默吞掉（此前裸调用不接
// 返回值，无论映射文件是否真的写成功都对外报告"已取消"）。
func removeProjectEntry(name string) error {
	id := findProjectID(name)

	if data, err := execOutputFn("cat", quotaProjectsFile); err == nil {
		var lines []string
		for _, line := range strings.Split(data, "\n") {
			lineID, _, lineName, ok := common.ParseProjectsLine(line)
			if ok && ((id >= 0 && lineID == id) || (lineName != "" && lineName == name)) {
				continue
			}
			lines = append(lines, line)
		}
		if err := safeWriteFileFn(quotaProjectsFile, strings.Join(lines, "\n")); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
		}
	}
	if data, err := execOutputFn("cat", quotaProjidFile); err == nil {
		var lines []string
		for _, line := range strings.Split(data, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), name+":") {
				lines = append(lines, line)
			}
		}
		if err := safeWriteFileFn(quotaProjidFile, strings.Join(lines, "\n")); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjidFile, err)
		}
	}
	return nil
}

// updateProjectPath：按 id 匹配，同时把历史脏数据行归一化为标准 id:path 格式
func updateProjectPath(name string, id int, newPath string) error {
	data, err := execOutputFn("cat", quotaProjectsFile)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(data, "\n") {
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

func appendToFile(path, content string) error {
	existing, _ := execOutputFn("cat", path)
	newContent := existing
	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += content
	return safeWriteFileFn(path, newContent)
}
