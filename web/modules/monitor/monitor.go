package monitor

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"nas-panel/common"
	"nas-panel/modules/dashboard"
)

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/monitor", common.AuthMiddleware(handleMonitor))
	mux.HandleFunc("/api/alert-config", common.AuthMiddleware(handleAlertConfig))
}

func handleMonitor(w http.ResponseWriter, r *http.Request) {
	common.JSONResponse(w, getMonitorStatus())
}

func handleAlertConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		config := getAlertConfig()
		common.JSONResponse(w, map[string]interface{}{"config": config})
		return
	}
	if r.Method == http.MethodPost {
		r.ParseForm()
		values := map[string]string{}
		for key, vals := range r.Form {
			if !strings.HasPrefix(key, "ALERT_") {
				continue
			}
			if len(vals) > 0 {
				values[key] = strings.TrimSpace(vals[0])
			}
		}
		if len(values) == 0 {
			http.Error(w, `{"error": "no ALERT_* values provided"}`, http.StatusBadRequest)
			return
		}
		err := saveAlertConfig(values)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "告警配置已保存", "saved": values})
	}
}

func getMonitorStatus() map[string]interface{} {
	result := map[string]interface{}{}
	info := dashboard.GetSystemInfo()
	services := dashboard.GetServices()

	result["disk_usage"] = getDiskUsagePct()
	result["disk_used"] = info.DiskUsed
	result["disk_total"] = info.DiskTotal
	result["disk_info"] = getDiskDetails()
	result["inode_info"] = getInodeInfo()
	result["lvm_info"] = getLVMInfo()

	result["mem_usage"] = getMemUsagePct()
	result["mem_used"] = info.MemUsed
	result["mem_total"] = info.MemTotal
	result["mem_detail"] = getMemDetail()

	result["cpu_load"] = getCPULoad()
	result["cpu_cores"] = info.CPUCores
	result["cpu_info"] = getCPUDetail()

	downCount := 0
	for _, svc := range services {
		if svc["active"] != "active" {
			downCount++
		}
	}
	result["services_down"] = downCount
	result["services_total"] = len(services)
	result["process_count"] = getProcessCount()
	result["top_procs"] = getTopMemProcs()
	result["logged_in_users"] = getLoggedInUsers()
	result["system_errors"] = getSystemErrors()
	result["channels"] = getAlertChannels()
	result["alert_state"] = getAlertStateList()
	result["network"] = getNetworkTraffic()
	return result
}

func getDiskUsagePct() string {
	out, _ := common.ExecOutput("df", "/data")
	if out == "" {
		out, _ = common.ExecOutput("df", "/")
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return "0"
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return "0"
	}
	return strings.TrimSuffix(fields[4], "%")
}

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
	return fmt.Sprintf("%.1f", (total-avail)*100/total)
}

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

func getAlertChannels() []map[string]interface{} {
	envPath := common.GetEnvFilePath()
	channels := []map[string]interface{}{}
	checks := []struct{ name, key string }{
		{"钉钉", "ALERT_DINGTALK_WEBHOOK"},
		{"Telegram", "ALERT_TELEGRAM_TOKEN"},
		{"Bark", "ALERT_BARK_KEY"},
		{"Email", "ALERT_SMTP_HOST"},
	}
	for _, c := range checks {
		val, _ := common.ReadEnvFile(envPath, c.key)
		channels = append(channels, map[string]interface{}{"name": c.name, "configured": val != ""})
	}
	return channels
}

func getAlertStateList() []map[string]string {
	stateFile := "/var/lib/nas-monitor/alerts.state"
	data, err := os.ReadFile(stateFile)
	if err != nil {
		home, _ := os.UserHomeDir()
		data, err = os.ReadFile(home + "/.nas-monitor/alerts.state")
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
				"key": parts[0], "timestamp": parts[1],
				"time": fmt.Sprintf("%d小时%d分钟前", ts/3600, (ts%3600)/60),
			})
		}
	}
	return result
}

func getProcessCount() string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "0"
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			if _, err := strconv.Atoi(e.Name()); err == nil {
				count++
			}
		}
	}
	return fmt.Sprintf("%d", count)
}

func getLoggedInUsers() []map[string]string {
	out, err := common.ExecOutput("who")
	if err != nil {
		return []map[string]string{}
	}
	var users []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		user := map[string]string{"user": fields[0], "tty": fields[1], "date": fields[2] + " " + fields[3]}
		if len(fields) >= 6 {
			user["from"] = strings.Trim(fields[len(fields)-1], "()")
		}
		users = append(users, user)
	}
	return users
}

func getSystemErrors() string {
	out, _ := common.SudoOutput("journalctl", "-p", "err", "-n", "10", "--no-pager", "--since", "24 hours ago")
	return out
}

func getDiskDetails() string {
	out, _ := common.ExecOutput("lsblk", "-o", "NAME,SIZE,TYPE,MOUNTPOINT,MODEL,ROTA")
	return out
}

func getInodeInfo() string {
	out, _ := common.ExecOutput("df", "-i", "/data")
	if out == "" {
		out, _ = common.ExecOutput("df", "-i", "/")
	}
	return out
}

func getLVMInfo() string {
	var result string
	if out, err := common.SudoOutput("pvs", "--noheadings", "-o", "pv_name,vg_name,pv_size,pv_free"); err == nil && len(strings.TrimSpace(out)) > 0 {
		result += "物理卷 (PV):\n" + out + "\n"
	}
	if out, err := common.SudoOutput("vgs", "--noheadings", "-o", "vg_name,vg_size,vg_free"); err == nil && len(strings.TrimSpace(out)) > 0 {
		result += "卷组 (VG):\n" + out + "\n"
	}
	if out, err := common.SudoOutput("lvs", "--noheadings", "-o", "lv_name,vg_name,lv_size,lv_path"); err == nil && len(strings.TrimSpace(out)) > 0 {
		result += "逻辑卷 (LV):\n" + out + "\n"
	}
	if result == "" {
		return "无 LVM 配置"
	}
	return result
}

func getMemDetail() string {
	out, _ := common.ExecOutput("free", "-h")
	return out
}

func getCPUDetail() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	var result string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "model name") || strings.Contains(line, "cache size") || strings.Contains(line, "cpu MHz") {
			if result == "" || !strings.Contains(result, strings.Split(line, ":")[0]) {
				result += line + "\n"
			}
		}
	}
	if gov, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"); err == nil {
		result += "governor: " + strings.TrimSpace(string(gov)) + "\n"
	}
	return result
}

func getTopMemProcs() []map[string]string {
	out, err := exec.Command("ps", "aux", "--sort=-%mem").Output()
	if err != nil {
		return []map[string]string{}
	}
	lines := strings.Split(string(out), "\n")
	var result []map[string]string
	for i := 1; i < len(lines) && len(result) < 10; i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		fields := strings.Fields(l)
		if len(fields) < 11 {
			continue
		}
		result = append(result, map[string]string{
			"user": fields[0], "pid": fields[1], "cpu": fields[2],
			"mem": fields[3], "rss": fields[5],
			"command": strings.Join(fields[10:], " "),
		})
	}
	return result
}

func getNetworkTraffic() []map[string]interface{} {
	data1, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return []map[string]interface{}{}
	}
	time.Sleep(time.Second)
	data2, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return []map[string]interface{}{}
	}
	return parseNetDev(string(data1), string(data2))
}

func parseNetDev(snap1, snap2 string) []map[string]interface{} {
	ifaces1 := parseNetDevLines(snap1)
	ifaces2 := parseNetDevLines(snap2)
	var result []map[string]interface{}
	for name, vals2 := range ifaces2 {
		vals1, ok := ifaces1[name]
		if !ok || name == "lo" {
			continue
		}
		rxBytes := vals2[0] - vals1[0]
		txBytes := vals2[8] - vals1[8]
		result = append(result, map[string]interface{}{
			"interface": name,
			"rx_rate":   fmt.Sprintf("%.1f", float64(rxBytes)/1024.0),
			"tx_rate":   fmt.Sprintf("%.1f", float64(txBytes)/1024.0),
			"rx_total":  fmt.Sprintf("%.2f", float64(vals2[0])/1024/1024),
			"tx_total":  fmt.Sprintf("%.2f", float64(vals2[8])/1024/1024),
		})
	}
	return result
}

func parseNetDevLines(data string) map[string][]uint64 {
	result := map[string][]uint64{}
	for _, line := range strings.Split(data, "\n") {
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

func getAlertConfig() map[string]string {
	envPath := common.GetEnvFilePath()
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
		if !strings.HasPrefix(line, "ALERT_") || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func saveAlertConfig(values map[string]string) error {
	envPath := common.GetEnvFilePath()
	if envPath == "" {
		return fmt.Errorf(".env file not found")
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}
	var nonAlertLines []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ALERT_") && !strings.HasPrefix(trimmed, "#") {
			continue
		}
		nonAlertLines = append(nonAlertLines, line)
	}
	var builder strings.Builder
	for _, line := range nonAlertLines {
		builder.WriteString(line + "\n")
	}
	builder.WriteString("\n# ═══ 告警通知配置 ═══\n")
	alertFields := []struct{ key, label string }{
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
	cmd := exec.Command("sudo", "tee", envPath)
	cmd.Stdin = strings.NewReader(builder.String())
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
