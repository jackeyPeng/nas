package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// getMonitorStatus returns current monitoring check results
func getMonitorStatus() map[string]interface{} {
	result := map[string]interface{}{}
	info := getSystemInfo()

	// Disk usage (detailed)
	result["disk_usage"] = getDiskUsagePct()
	result["disk_used"] = info.DiskUsed
	result["disk_total"] = info.DiskTotal

	// Memory usage (detailed)
	result["mem_usage"] = getMemUsagePct()
	result["mem_used"] = info.MemUsed
	result["mem_total"] = info.MemTotal

	// CPU load (detailed)
	result["cpu_load"] = getCPULoad()
	result["cpu_cores"] = info.CPUCores

	// Services down count
	services := getServices()
	downCount := 0
	for _, svc := range services {
		if svc["active"] != "active" {
			downCount++
		}
	}
	result["services_down"] = downCount
	result["services_total"] = len(services)

	// Configured channels
	result["channels"] = getAlertChannels()

	// Alert state
	result["alert_state"] = getAlertStateList()

	// Network traffic
	result["network"] = getNetworkTraffic()

	return result
}

// getDiskUsagePct returns disk usage percentage without %
func getDiskUsagePct() string {
	out, err := exec.Command("df", "/data").Output()
	if err != nil {
		out, _ = exec.Command("df", "/").Output()
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return "0"
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return "0"
	}
	pct := strings.TrimSuffix(fields[4], "%")
	return pct
}

// getMemUsagePct returns memory usage percentage without %
func getMemUsagePct() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "0"
	}
	var total, avail float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "MemTotal:":
			total = val
		case "MemAvailable:":
			avail = val
		}
	}
	if total == 0 {
		return "0"
	}
	pct := (total - avail) * 100 / total
	return fmt.Sprintf("%.1f", pct)
}

// getCPULoad returns 1-minute load average
func getCPULoad() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "0"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "0"
	}
	return fields[0]
}

// getAlertChannels checks which alert channels are configured
func getAlertChannels() []map[string]interface{} {
	envPath := getEnvFilePath()
	channels := []map[string]interface{}{}

	checks := []struct {
		name string
		key  string
	}{
		{"钉钉", "ALERT_DINGTALK_WEBHOOK"},
		{"Telegram", "ALERT_TELEGRAM_TOKEN"},
		{"Bark", "ALERT_BARK_KEY"},
		{"Email", "ALERT_SMTP_HOST"},
	}

	for _, c := range checks {
		val, _ := readEnvFile(envPath, c.key)
		channels = append(channels, map[string]interface{}{
			"name":       c.name,
			"configured": val != "",
		})
	}

	return channels
}

// getAlertStateList reads alert state file and returns as list
func getAlertStateList() []map[string]string {
	// Try /var/lib/nas-monitor first
	stateFile := "/var/lib/nas-monitor/alerts.state"
	data, err := os.ReadFile(stateFile)
	if err != nil {
		// Fallback to home directory
		home, _ := os.UserHomeDir()
		stateFile = home + "/.nas-monitor/alerts.state"
		data, err = os.ReadFile(stateFile)
		if err != nil {
			return []map[string]string{}
		}
	}

	var result []map[string]string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			ts, _ := strconv.ParseInt(parts[1], 10, 64)
			result = append(result, map[string]string{
				"key":       parts[0],
				"timestamp": parts[1],
				"time":      formatTimestamp(ts),
			})
		}
	}
	return result
}

// formatTimestamp converts unix timestamp to readable time
func formatTimestamp(ts int64) string {
	if ts == 0 {
		return ""
	}
	// Simple formatting without time package dependency
	t := ts
	hours := t / 3600
	mins := (t % 3600) / 60
	return fmt.Sprintf("%d小时%d分钟前", hours, mins)
}

// getNetworkTraffic reads /proc/net/dev twice (1s apart) to get real-time rates
func getNetworkTraffic() []map[string]interface{} {
	data1, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return []map[string]interface{}{}
	}

	// Sleep 1 second to measure rate
	time.Sleep(time.Second)

	data2, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return []map[string]interface{}{}
	}

	return parseNetDev(string(data1), string(data2))
}

// parseNetDev parses two /proc/net/dev snapshots and returns per-interface rates
func parseNetDev(snap1, snap2 string) []map[string]interface{} {
	ifaces1 := parseNetDevLines(snap1)
	ifaces2 := parseNetDevLines(snap2)

	var result []map[string]interface{}
	for name, vals2 := range ifaces2 {
		vals1, ok := ifaces1[name]
		if !ok {
			continue
		}
		// Skip loopback
		if name == "lo" {
			continue
		}

		rxBytes := vals2[0] - vals1[0]
		txBytes := vals2[8] - vals1[8]

		result = append(result, map[string]interface{}{
			"interface": name,
			"rx_rate":   fmt.Sprintf("%.1f", float64(rxBytes)/1024.0),     // KB/s
			"tx_rate":   fmt.Sprintf("%.1f", float64(txBytes)/1024.0),     // KB/s
			"rx_total":  fmt.Sprintf("%.2f", float64(vals2[0])/1024/1024), // MB total
			"tx_total":  fmt.Sprintf("%.2f", float64(vals2[8])/1024/1024), // MB total
		})
	}
	return result
}

// parseNetDevLines parses /proc/net/dev into map[interface][]uint64 (16 fields)
func parseNetDevLines(data string) map[string][]uint64 {
	result := map[string][]uint64{}
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Inter") || strings.HasPrefix(line, "face") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(strings.TrimSpace(parts[1]))
		if len(fields) < 16 {
			continue
		}
		vals := make([]uint64, 16)
		for i, f := range fields[:16] {
			vals[i], _ = strconv.ParseUint(f, 10, 64)
		}
		result[name] = vals
	}
	return result
}

// getEnvFilePath returns the .env file path
func getEnvFilePath() string {
	if _, err := os.Stat("/opt/nas/.env"); err == nil {
		return "/opt/nas/.env"
	}
	return ""
}

// getAlertConfig reads all ALERT_* variables from .env
func getAlertConfig() map[string]string {
	envPath := getEnvFilePath()
	if envPath == "" {
		return map[string]string{}
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		return map[string]string{}
	}

	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ALERT_") {
			continue
		}
		// Skip commented lines
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

// saveAlertConfig writes ALERT_* variables back to .env, preserving non-ALERT
// lines and comments. Needs sudo to write to /opt/nas/.env.
func saveAlertConfig(values map[string]string) error {
	envPath := getEnvFilePath()
	if envPath == "" {
		return fmt.Errorf(".env file not found")
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}

	// Separate ALERT_ lines from non-ALERT lines
	var nonAlertLines []string
	alertKeys := make(map[string]bool)
	for k := range values {
		alertKeys[k] = true
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ALERT_") && !strings.HasPrefix(trimmed, "#") {
			continue // Will be rewritten
		}
		nonAlertLines = append(nonAlertLines, line)
	}

	// Build new content
	var builder strings.Builder
	for _, line := range nonAlertLines {
		builder.WriteString(line + "\n")
	}

	// Add alert config section
	builder.WriteString("\n# ═══ 告警通知配置 ═══\n")

	// Define order and labels
	alertFields := []struct {
		key   string
		label string
	}{
		{"ALERT_DINGTALK_WEBHOOK", "钉钉 Webhook"},
		{"ALERT_DINGTALK_SECRET", "钉钉加签密钥"},
		{"ALERT_TELEGRAM_TOKEN", "Telegram Token"},
		{"ALERT_TELEGRAM_CHAT_ID", "Telegram Chat ID"},
		{"ALERT_BARK_KEY", "Bark Key"},
		{"ALERT_BARK_SERVER", "Bark Server"},
		{"ALERT_SMTP_HOST", "SMTP 服务器"},
		{"ALERT_SMTP_PORT", "SMTP 端口"},
		{"ALERT_SMTP_USER", "SMTP 用户名"},
		{"ALERT_SMTP_PASS", "SMTP 密码"},
		{"ALERT_SMTP_FROM", "SMTP 发件人"},
		{"ALERT_SMTP_TO", "SMTP 收件人"},
		{"ALERT_DISK_THRESHOLD", "磁盘告警阈值"},
		{"ALERT_MEM_THRESHOLD", "内存告警阈值"},
		{"ALERT_LOAD_THRESHOLD", "负载告警阈值"},
	}

	for _, f := range alertFields {
		val := values[f.key]
		if val != "" {
			builder.WriteString(fmt.Sprintf("%s=%s\n", f.key, val))
		} else {
			builder.WriteString(fmt.Sprintf("#%s=\n", f.key))
		}
	}

	// Write using sudo (nas-panel runs as non-root)
	content := builder.String()
	cmd := exec.Command("sudo", "tee", envPath)
	cmd.Stdin = strings.NewReader(content)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
