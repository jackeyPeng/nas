package users

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"nas-panel/common"
)

// dirExists 判断目录是否存在
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// privateDirQuota 查询私有目录配额
func privateDirQuota(username string) (usedGB float64, limitGB int) {
	// 私有目录在 /data/private/username
	// 需要找到挂载点
	mountPoint := findMountPoint("/data/private")
	if mountPoint == "" {
		return 0, 0
	}

	projName := "private_" + username
	out, err := common.SudoOutput("/usr/sbin/xfs_quota", "-x", "-c", "report -p -N", mountPoint)
	if err != nil {
		return 0, 0
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
			return usedGB, limitGB
		}
	}
	return 0, 0
}

// setPrivateDirQuota 设置私有目录配额
func setPrivateDirQuota(username string, quotaGB int) error {
	privDir := "/data/private/" + username
	if !dirExists(privDir) {
		return fmt.Errorf("私有目录 %s 不存在", privDir)
	}

	mountPoint := findMountPoint(privDir)
	if mountPoint == "" {
		return fmt.Errorf("找不到 %s 所在的挂载点", privDir)
	}

	projName := "private_" + username

	if quotaGB <= 0 {
		// 移除配额
		removeProjectEntry(projName)
		common.SudoExec("/usr/sbin/xfs_quota", "-x", "-c",
			fmt.Sprintf("project -C %s", projName), mountPoint)
		return nil
	}

	// 查找或创建 project ID
	projID := findProjectID(projName)
	if projID < 0 {
		projID = getNextProjectID()
		// 写入 /etc/projects
		entry := fmt.Sprintf("%s:%d:%s\n", projName, projID, privDir)
		if err := appendToFile(quotaProjectsFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjectsFile, err)
		}
		// 写入 /etc/projid
		entry = fmt.Sprintf("%s:%d\n", projName, projID)
		if err := appendToFile(quotaProjidFile, entry); err != nil {
			return fmt.Errorf("写入 %s 失败: %v", quotaProjidFile, err)
		}
	} else {
		updateProjectPath(projName, projID, privDir)
	}

	// 初始化 project
	out, err := common.SudoExec("/usr/sbin/xfs_quota", "-x", "-c",
		fmt.Sprintf("project -s %s", projName), mountPoint)
	if err != nil {
		return fmt.Errorf("xfs_quota project 失败: %s: %v", out, err)
	}

	// 设置硬限制
	limitKB := quotaGB * 1024 * 1024
	out, err = common.SudoExec("/usr/sbin/xfs_quota", "-x", "-c",
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

const (
	quotaProjectsFile = "/etc/projects"
	quotaProjidFile   = "/etc/projid"
	quotaBaseID       = 1000
)

func getNextProjectID() int {
	data, err := common.ExecOutput("cat", quotaProjidFile)
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
	data, err := common.ExecOutput("cat", quotaProjidFile)
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

func removeProjectEntry(name string) {
	if data, err := common.ExecOutput("cat", quotaProjectsFile); err == nil {
		var lines []string
		for _, line := range strings.Split(data, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), name+":") {
				lines = append(lines, line)
			}
		}
		common.SafeWriteFile(quotaProjectsFile, strings.Join(lines, "\n"))
	}
	if data, err := common.ExecOutput("cat", quotaProjidFile); err == nil {
		var lines []string
		for _, line := range strings.Split(data, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), name+":") {
				lines = append(lines, line)
			}
		}
		common.SafeWriteFile(quotaProjidFile, strings.Join(lines, "\n"))
	}
}

func updateProjectPath(name string, id int, newPath string) {
	data, err := common.ExecOutput("cat", quotaProjectsFile)
	if err != nil {
		return
	}
	var lines []string
	for _, line := range strings.Split(data, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 3 && parts[0] == name {
			lines = append(lines, fmt.Sprintf("%s:%d:%s", name, id, newPath))
		} else {
			lines = append(lines, line)
		}
	}
	common.SafeWriteFile(quotaProjectsFile, strings.Join(lines, "\n"))
}

func appendToFile(path, content string) error {
	existing, _ := common.ExecOutput("cat", path)
	newContent := existing
	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += content
	return common.SafeWriteFile(path, newContent)
}
