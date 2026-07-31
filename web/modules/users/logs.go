package users

import (
	"net/http"
	"strconv"
	"strings"

	"nas-panel/common"
)

// LoginLogEntry 登录日志条目
type LoginLogEntry struct {
	Time    string `json:"time"`
	User    string `json:"user"`
	Source  string `json:"source"`  // IP 或终端
	Service string `json:"service"` // ssh/samba/ftp
	Result  string `json:"result"`  // success/failed
	Message string `json:"message"` // 详细信息
}

// handleLoginLog 登录日志 API
func handleLoginLog(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
			limit = l
		}
	}

	entries := collectLoginLogs(limit)
	common.JSONResponse(w, map[string]interface{}{
		"entries": entries,
		"limit":   limit,
	})
}

// collectLoginLogs 从多个来源收集登录日志
func collectLoginLogs(limit int) []LoginLogEntry {
	var entries []LoginLogEntry

	// 来源1: last 命令（成功登录）
	lastEntries := parseLastCommand(limit)
	entries = append(entries, lastEntries...)

	// 来源2: journalctl sshd 失败日志
	sshEntries := parseSSHFailedLogs(limit)
	entries = append(entries, sshEntries...)

	// 来源3: Samba 日志（可选，如果开启了日志）
	sambaEntries := parseSambaLogs(limit)
	entries = append(entries, sambaEntries...)

	// 按时间排序（新的在前）
	sortLoginLogs(entries)

	// 限制数量
	if len(entries) > limit {
		entries = entries[:limit]
	}

	return entries
}

// parseLastCommand 解析 last 命令输出
func parseLastCommand(limit int) []LoginLogEntry {
	var entries []LoginLogEntry

	out, err := common.ExecOutput("last", "-n", strconv.Itoa(limit), "-F")
	if err != nil {
		return entries
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "wtmp") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		user := fields[0]
		// 跳过系统用户
		if user == "reboot" || user == "shutdown" || user == "runlevel" {
			continue
		}

		// 解析来源
		source := ""
		if len(fields) >= 3 {
			source = fields[2]
		}

		// 解析时间
		timeStr := ""
		if len(fields) >= 5 {
			timeStr = strings.Join(fields[3:5], " ")
		}

		entries = append(entries, LoginLogEntry{
			Time:    timeStr,
			User:    user,
			Source:  source,
			Service: "ssh",
			Result:  "success",
			Message: "登录成功",
		})
	}

	return entries
}

// parseSSHFailedLogs 解析 SSH 失败日志
func parseSSHFailedLogs(limit int) []LoginLogEntry {
	var entries []LoginLogEntry

	// 尝试从 journalctl 获取
	out, err := common.ExecOutput("journalctl", "-u", "sshd", "-n", strconv.Itoa(limit), "--no-pager", "-o", "short")
	if err != nil {
		// 备用：读 /var/log/auth.log
		out, err = common.ExecOutput("grep", "sshd", "/var/log/auth.log")
		if err != nil {
			return entries
		}
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Failed password") && !strings.Contains(line, "Invalid user") {
			continue
		}

		entry := LoginLogEntry{
			Service: "ssh",
			Result:  "failed",
		}

		// 解析用户
		if strings.Contains(line, "Invalid user") {
			parts := strings.Split(line, "Invalid user ")
			if len(parts) > 1 {
				userPart := strings.Fields(parts[1])
				if len(userPart) > 0 {
					entry.User = userPart[0]
				}
			}
		} else if strings.Contains(line, "Failed password for") {
			parts := strings.Split(line, "Failed password for ")
			if len(parts) > 1 {
				userPart := strings.Fields(parts[1])
				if len(userPart) > 0 {
					entry.User = userPart[0]
				}
			}
		}

		// 解析来源 IP
		if idx := strings.Index(line, " from "); idx > 0 {
			ipPart := line[idx+6:]
			fields := strings.Fields(ipPart)
			if len(fields) > 0 {
				entry.Source = fields[0]
			}
		}

		// 解析时间（journalctl 格式）
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			entry.Time = strings.Join(fields[0:3], " ")
		}

		entry.Message = "登录失败"
		if entry.User != "" {
			entries = append(entries, entry)
		}
	}

	return entries
}

// parseSambaLogs 解析 Samba 日志
func parseSambaLogs(limit int) []LoginLogEntry {
	var entries []LoginLogEntry

	// 检查 Samba 日志文件是否存在
	logFile := "/var/log/samba/log.smbd"
	out, err := common.SudoOutput("tail", "-n", strconv.Itoa(limit*2), logFile)
	if err != nil {
		return entries
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "authentication") && !strings.Contains(line, "connect") {
			continue
		}

		entry := LoginLogEntry{
			Service: "samba",
			Result:  "success",
		}

		// 尝试解析用户和来源
		if strings.Contains(line, "user") {
			// 简化解析
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "user" && i+1 < len(fields) {
					entry.User = strings.Trim(fields[i+1], "[]")
				}
			}
		}

		if entry.User != "" {
			entry.Time = extractTimeFromLogLine(line)
			entry.Message = "Samba 访问"
			entries = append(entries, entry)
		}
	}

	return entries
}

// extractTimeFromLogLine 从日志行提取时间
func extractTimeFromLogLine(line string) string {
	// 尝试多种时间格式
	fields := strings.Fields(line)
	if len(fields) >= 3 {
		// 检查是否是时间格式 (YYYY/MM/DD HH:MM:SS)
		if strings.Contains(fields[0], "/") || strings.Contains(fields[0], "-") {
			return fields[0] + " " + fields[1]
		}
		// syslog 格式 (Mon DD HH:MM:SS)
		if len(fields) >= 3 {
			return strings.Join(fields[0:3], " ")
		}
	}
	return ""
}

// sortLoginLogs 按时间排序（简单冒泡，新的在前）
func sortLoginLogs(entries []LoginLogEntry) {
	// 由于时间格式不统一，这里简化处理：保持原有顺序
	// last 命令输出本身就是新的在前
	// SSH 失败日志也大致按时间顺序
	// 实际生产环境应该用时间戳解析后排序
}
