package diagnostics

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"

	_ "modernc.org/sqlite"
)

var (
	db        *sql.DB
	dbMu      sync.Mutex
	runMu     sync.Mutex
	isRunning bool // only one long task at a time
)

// DiagnosticItem defines a diagnostic check
type DiagnosticItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Duration    string `json:"duration"` // "2分钟", "4小时"
	Enabled     bool   `json:"enabled"`
	Schedule    string `json:"schedule"` // "daily", "weekly", "monthly", "manual"
	LastRun     string `json:"last_run"`
	LastResult  string `json:"last_result"` // "pass", "fail", "warn", "running", ""
	LastDetail  string `json:"last_detail"`
	Progress    int    `json:"progress"` // 0-100
	Running     bool   `json:"running"`
}

// Config holds global diagnostic settings
type Config struct {
	TimeWindowStart string `json:"time_window_start"` // "02:00"
	TimeWindowEnd   string `json:"time_window_end"`   // "06:00"
	TempLimit       int    `json:"temp_limit"`         // 55
	IOLimit         int    `json:"io_limit"`           // 70
	ScrubSpeedMax   int    `json:"scrub_speed_max"`    // 100000 KB/s
}

// HistoryEntry is a record of a diagnostic run
type HistoryEntry struct {
	ID        int    `json:"id"`
	ItemID    string `json:"item_id"`
	ItemName  string `json:"item_name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Result    string `json:"result"`
	Detail    string `json:"detail"`
}

// DefaultItems returns the three diagnostic items
func DefaultItems() []DiagnosticItem {
	return []DiagnosticItem{
		{
			ID: "smart_short", Name: "SMART 短检",
			Description: "磁盘固件内置快速自检，检测 imminent failure 风险",
			Duration: "~2分钟", Enabled: true, Schedule: "weekly",
		},
		{
			ID: "smart_long", Name: "SMART 长检",
			Description: "全盘表面读取扫描，检测坏道和弱扇区",
			Duration: "~4小时", Enabled: false, Schedule: "monthly",
		},
		{
			ID: "raid_scrub", Name: "RAID 数据清理",
			Description: "校验 RAID 阵列所有数据的完整性，自动修复静默损坏",
			Duration: "~2小时", Enabled: false, Schedule: "monthly",
		},
	}
}

// DefaultConfig returns default config
func DefaultConfig() Config {
	return Config{
		TimeWindowStart: "02:00",
		TimeWindowEnd:   "06:00",
		TempLimit:       55,
		IOLimit:         70,
		ScrubSpeedMax:   100000,
	}
}

// RegisterRoutes registers diagnostic routes
func RegisterRoutes(mux *http.ServeMux) {
	initDB()

	mux.HandleFunc("/api/diagnostics/status", common.AuthMiddleware(handleStatus))
	mux.HandleFunc("/api/diagnostics/run", common.AuthMiddleware(handleRun))
	mux.HandleFunc("/api/diagnostics/history", common.AuthMiddleware(handleHistory))
	mux.HandleFunc("/api/diagnostics/config", common.AuthMiddleware(handleConfig))

	// Start background scheduler
	go schedulerLoop()
}

func initDB() {
	dbPath := "/opt/nas/data/diagnostics.db"
	os.MkdirAll(filepath.Dir(dbPath), 0755)
	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		// Fallback to current dir
		db, _ = sql.Open("sqlite", "diagnostics.db")
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id TEXT NOT NULL,
		item_name TEXT NOT NULL,
		start_time TEXT NOT NULL,
		end_time TEXT,
		result TEXT,
		detail TEXT
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS item_state (
		item_id TEXT PRIMARY KEY,
		enabled INTEGER DEFAULT 1,
		schedule TEXT DEFAULT 'weekly',
		last_run TEXT,
		last_result TEXT,
		last_detail TEXT
	)`)
}

// ═══════════════════════════════════════
// Status API
// ═══════════════════════════════════════

func handleStatus(w http.ResponseWriter, r *http.Request) {
	items := DefaultItems()

	// Load state from DB
	for i := range items {
		row := db.QueryRow("SELECT enabled, schedule, last_run, last_result, last_detail FROM item_state WHERE item_id = ?", items[i].ID)
		var enabled int
		var schedule, lastRun, lastResult, lastDetail sql.NullString
		if err := row.Scan(&enabled, &schedule, &lastRun, &lastResult, &lastDetail); err == nil {
			items[i].Enabled = enabled == 1
			if schedule.Valid {
				items[i].Schedule = schedule.String
			}
			if lastRun.Valid {
				items[i].LastRun = lastRun.String
			}
			if lastResult.Valid {
				items[i].LastResult = lastResult.String
			}
			if lastDetail.Valid {
				items[i].LastDetail = lastDetail.String
			}
		}

		// Check if currently running
		items[i].Running = isRunning && items[i].ID == currentRunningItem
		if items[i].Running {
			items[i].LastResult = "running"
			items[i].Progress = currentProgress
		}
	}

	common.JSONResponse(w, map[string]interface{}{
		"items": items,
	})
}

// ═══════════════════════════════════════
// Run API
// ═══════════════════════════════════════

var (
	currentRunningItem string
	currentProgress    int
)

func handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	itemID := r.FormValue("item_id")
	if itemID == "" {
		http.Error(w, `{"error":"item_id required"}`, http.StatusBadRequest)
		return
	}

	// Check if already running
	runMu.Lock()
	if isRunning {
		runMu.Unlock()
		http.Error(w, `{"error":"已有诊断任务正在运行，请等待完成"}`, http.StatusConflict)
		return
	}

	// Check temperature
	cfg := loadConfig()
	if !checkTemperature(cfg.TempLimit) {
		runMu.Unlock()
		http.Error(w, fmt.Sprintf(`{"error":"磁盘温度超过 %d°C，已跳过诊断"}`, cfg.TempLimit), http.StatusServiceUnavailable)
		return
	}

	// Check IO usage
	if !checkIOUsage(cfg.IOLimit) {
		runMu.Unlock()
		http.Error(w, fmt.Sprintf(`{"error":"磁盘 IO 使用率超过 %d%%，已跳过诊断"}`, cfg.IOLimit), http.StatusServiceUnavailable)
		return
	}

	// Check time window for long tasks
	if itemID == "smart_long" || itemID == "raid_scrub" {
		if !inTimeWindow(cfg.TimeWindowStart, cfg.TimeWindowEnd) {
			runMu.Unlock()
			http.Error(w, `{"error":"长耗时诊断仅允许在时间窗口内运行"}`, http.StatusServiceUnavailable)
			return
		}
	}

	isRunning = true
	currentRunningItem = itemID
	currentProgress = 0
	runMu.Unlock()

	// Run in background
	go func() {
		defer func() {
			runMu.Lock()
			isRunning = false
			currentRunningItem = ""
			currentProgress = 0
			runMu.Unlock()
		}()

		result, detail := runDiagnostic(itemID, cfg)

		// Save to history
		now := time.Now().Format("2006-01-02 15:04:05")
		itemName := ""
		for _, it := range DefaultItems() {
			if it.ID == itemID {
				itemName = it.Name
				break
			}
		}
		db.Exec("INSERT INTO history (item_id, item_name, start_time, end_time, result, detail) VALUES (?, ?, ?, ?, ?, ?)",
			itemID, itemName, now, time.Now().Format("2006-01-02 15:04:05"), result, detail)

		// Update item state
		db.Exec(`INSERT OR REPLACE INTO item_state (item_id, enabled, schedule, last_run, last_result, last_detail)
			VALUES (?, 1, (SELECT schedule FROM item_state WHERE item_id = ?), ?, ?, ?)`,
			itemID, itemID, now, result, detail)

		// Clean old history (keep 500)
		db.Exec("DELETE FROM history WHERE id NOT IN (SELECT id FROM history ORDER BY id DESC LIMIT 500)")
	}()

	common.JSONResponse(w, map[string]interface{}{
		"message": "诊断任务已开始",
		"item_id": itemID,
	})
}

// ═══════════════════════════════════════
// History API
// ═══════════════════════════════════════

func handleHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, item_id, item_name, start_time, COALESCE(end_time,''), COALESCE(result,''), COALESCE(detail,'') FROM history ORDER BY id DESC LIMIT 100")
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"history": []HistoryEntry{}})
		return
	}
	defer rows.Close()

	var history []HistoryEntry
	for rows.Next() {
		var h HistoryEntry
		rows.Scan(&h.ID, &h.ItemID, &h.ItemName, &h.StartTime, &h.EndTime, &h.Result, &h.Detail)
		history = append(history, h)
	}
	if history == nil {
		history = []HistoryEntry{}
	}
	common.JSONResponse(w, map[string]interface{}{"history": history})
}

// ═══════════════════════════════════════
// Config API
// ═══════════════════════════════════════

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		common.JSONResponse(w, map[string]interface{}{
			"config": loadConfig(),
			"items":  loadItemStates(),
		})
		return
	}

	if r.Method == http.MethodPost {
		// Save global config
		if v := r.FormValue("time_window_start"); v != "" {
			db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('time_window_start', ?)", v)
		}
		if v := r.FormValue("time_window_end"); v != "" {
			db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('time_window_end', ?)", v)
		}
		if v := r.FormValue("temp_limit"); v != "" {
			db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('temp_limit', ?)", v)
		}
		if v := r.FormValue("io_limit"); v != "" {
			db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('io_limit', ?)", v)
		}
		if v := r.FormValue("scrub_speed_max"); v != "" {
			db.Exec("INSERT OR REPLACE INTO config (key, value) VALUES ('scrub_speed_max', ?)", v)
		}

		// Save item states
		if itemID := r.FormValue("item_id"); itemID != "" {
			enabled := r.FormValue("enabled")
			schedule := r.FormValue("schedule")
			if enabled != "" || schedule != "" {
				enabledVal := "1"
				if enabled == "false" || enabled == "0" {
					enabledVal = "0"
				}
				scheduleVal := schedule
				if scheduleVal == "" {
					scheduleVal = "weekly"
				}
				db.Exec(`INSERT OR REPLACE INTO item_state (item_id, enabled, schedule) VALUES (?, ?, ?)`,
					itemID, enabledVal, scheduleVal)
			}
		}

		common.JSONResponse(w, map[string]interface{}{
			"message": "配置已保存",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func loadConfig() Config {
	cfg := DefaultConfig()
	rows, _ := db.Query("SELECT key, value FROM config")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			switch k {
			case "time_window_start":
				cfg.TimeWindowStart = v
			case "time_window_end":
				cfg.TimeWindowEnd = v
			case "temp_limit":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.TempLimit = n
				}
			case "io_limit":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.IOLimit = n
				}
			case "scrub_speed_max":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.ScrubSpeedMax = n
				}
			}
		}
	}
	return cfg
}

func loadItemStates() []DiagnosticItem {
	items := DefaultItems()
	for i := range items {
		row := db.QueryRow("SELECT enabled, schedule, last_run, last_result, last_detail FROM item_state WHERE item_id = ?", items[i].ID)
		var enabled int
		var schedule, lastRun, lastResult, lastDetail sql.NullString
		if err := row.Scan(&enabled, &schedule, &lastRun, &lastResult, &lastDetail); err == nil {
			items[i].Enabled = enabled == 1
			if schedule.Valid {
				items[i].Schedule = schedule.String
			}
			if lastRun.Valid {
				items[i].LastRun = lastRun.String
			}
			if lastResult.Valid {
				items[i].LastResult = lastResult.String
			}
			if lastDetail.Valid {
				items[i].LastDetail = lastDetail.String
			}
		}
	}
	return items
}

// ═══════════════════════════════════════
// Diagnostic execution
// ═══════════════════════════════════════

func runDiagnostic(itemID string, cfg Config) (result, detail string) {
	switch itemID {
	case "smart_short":
		return runSMARTShort()
	case "smart_long":
		return runSMARTLong()
	case "raid_scrub":
		return runRAIDScrub(cfg)
	default:
		return "fail", "未知诊断项"
	}
}

func runSMARTShort() (string, string) {
	disks := getDataDisks()
	if len(disks) == 0 {
		return "warn", "未找到数据盘"
	}

	var results []string
	allPass := true
	for _, disk := range disks {
		out, err := common.SudoExec("smartctl", "-t", "short", disk)
		if err != nil {
			results = append(results, fmt.Sprintf("%s: 触发失败 - %s", disk, out))
			allPass = false
			continue
		}
		// Wait for test to complete (short test ~2 min)
		for i := 0; i < 30; i++ {
			time.Sleep(5 * time.Second)
			runMu.Lock()
			currentProgress = (i + 1) * 100 / 30
			runMu.Unlock()

			statusOut, _ := common.SudoOutput("smartctl", "-l", "selftest", disk)
			if strings.Contains(statusOut, "Completed without error") {
				results = append(results, fmt.Sprintf("%s: 通过", disk))
				break
			}
			if strings.Contains(statusOut, "Completed with read failure") || strings.Contains(statusOut, "failed") {
				results = append(results, fmt.Sprintf("%s: 失败", disk))
				allPass = false
				break
			}
		}
	}
	if allPass {
		return "pass", strings.Join(results, "; ")
	}
	return "fail", strings.Join(results, "; ")
}

func runSMARTLong() (string, string) {
	disks := getDataDisks()
	if len(disks) == 0 {
		return "warn", "未找到数据盘"
	}

	var results []string
	for _, disk := range disks {
		out, err := common.SudoExec("smartctl", "-t", "long", disk)
		if err != nil {
			results = append(results, fmt.Sprintf("%s: 触发失败 - %s", disk, out))
			continue
		}
		results = append(results, fmt.Sprintf("%s: 已触发，后台运行中", disk))
	}
	// Long test runs in background - we just trigger it
	return "pass", strings.Join(results, "; ") + "（长检在后台运行，请稍后查看结果）"
}

func runRAIDScrub(cfg Config) (string, string) {
	// Find MD devices
	mdMatches, _ := filepath.Glob("/dev/md[0-9]*")

	var mdDevices []string
	for _, dev := range mdMatches {
		base := filepath.Base(dev)
		// Only plain md devices (no partitions like md0p1)
		if len(base) >= 3 && base[2] >= '0' && base[2] <= '9' {
			mdDevices = append(mdDevices, dev)
		}
	}

	if len(mdDevices) == 0 {
		return "warn", "未找到 RAID 设备"
	}

	// Set speed limit
	for _, dev := range mdDevices {
		speedFile := fmt.Sprintf("/sys/block/%s/md/sync_speed_max", filepath.Base(dev))
		common.SudoExec("bash", "-c", fmt.Sprintf("echo %d > %s 2>/dev/null", cfg.ScrubSpeedMax, speedFile))
	}

	var results []string
	for _, dev := range mdDevices {
		actionFile := fmt.Sprintf("/sys/block/%s/md/sync_action", filepath.Base(dev))
		common.SudoExec("bash", "-c", fmt.Sprintf("echo check > %s 2>/dev/null", actionFile))

		// Monitor progress
		for i := 0; i < 360; i++ { // max 30 min polling
			time.Sleep(5 * time.Second)

			// Check if still in time window
			if !inTimeWindow(cfg.TimeWindowStart, cfg.TimeWindowEnd) {
				common.SudoExec("bash", "-c", fmt.Sprintf("echo idle > %s 2>/dev/null", actionFile))
				results = append(results, fmt.Sprintf("%s: 超出时间窗口，已暂停", dev))
				return "warn", strings.Join(results, "; ")
			}

			// Check IO usage
			if !checkIOUsage(cfg.IOLimit) {
				common.SudoExec("bash", "-c", fmt.Sprintf("echo idle > %s 2>/dev/null", actionFile))
				results = append(results, fmt.Sprintf("%s: IO 使用率过高，已暂停", dev))
				return "warn", strings.Join(results, "; ")
			}

			// Read progress
			mdstatOut, _ := os.ReadFile("/proc/mdstat")
			mdstat := string(mdstatOut)
			if !strings.Contains(mdstat, "check") && !strings.Contains(mdstat, "resync") && !strings.Contains(mdstat, "reshape") {
				results = append(results, fmt.Sprintf("%s: 完成", dev))
				break
			}

			runMu.Lock()
			currentProgress = (i + 1) * 100 / 360
			if currentProgress > 99 {
				currentProgress = 99
			}
			runMu.Unlock()
		}
	}
	if len(results) == 0 {
		return "pass", "所有 RAID 设备清理完成"
	}
	return "pass", strings.Join(results, "; ")
}

// ═══════════════════════════════════════
// Safety checks
// ═══════════════════════════════════════

func checkTemperature(limit int) bool {
	disks := getDataDisks()
	for _, disk := range disks {
		// Read via smartctl
		out, _ := common.SudoOutput("smartctl", "-A", disk)
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "Temperature_Celsius") {
				fields := strings.Fields(line)
				if len(fields) >= 10 {
					if temp, err := strconv.Atoi(fields[9]); err == nil && temp > limit {
						return false
					}
				}
			}
		}
	}
	return true
}

func checkIOUsage(limit int) bool {
	// Check iostat for disk utilization
	out, _ := common.ExecOutput("iostat", "-x", "-d", "1", "1")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "sd") || strings.Contains(line, "nvme") {
			fields := strings.Fields(line)
			if len(fields) >= 14 {
				utilStr := strings.TrimSuffix(fields[13], "%")
				if util, err := strconv.ParseFloat(utilStr, 64); err == nil && util > float64(limit) {
					return false
				}
			}
		}
	}
	return true
}

func inTimeWindow(start, end string) bool {
	now := time.Now()
	h, _, _ := now.Clock()
	startH, _ := strconv.Atoi(strings.Split(start, ":")[0])
	endH, _ := strconv.Atoi(strings.Split(end, ":")[0])
	if startH <= endH {
		return h >= startH && h < endH
	}
	return h >= startH || h < endH // overnight window
}

func getDataDisks() []string {
	var disks []string
	// Use lsblk to find non-system disks
	out, _ := common.ExecOutput("lsblk", "-nd", "-o", "NAME,TYPE,MOUNTPOINT")
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "disk" {
			continue
		}
		name := fields[0]
		if name == "sr0" || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
			continue
		}
		// Check if system disk
		dev := "/dev/" + name
		mpOut, _ := common.ExecOutput("lsblk", "-nlo", "MOUNTPOINT", dev)
		isSystem := false
		for _, mp := range strings.Split(mpOut, "\n") {
			mp = strings.TrimSpace(mp)
			if mp == "/" || mp == "/boot" || mp == "/boot/efi" {
				isSystem = true
				break
			}
		}
		if !isSystem {
			disks = append(disks, dev)
		}
	}
	return disks
}

// ═══════════════════════════════════════
// Background scheduler
// ═══════════════════════════════════════

func schedulerLoop() {
	for {
		time.Sleep(60 * time.Second) // Check every minute

		runMu.Lock()
		if isRunning {
			runMu.Unlock()
			continue
		}
		runMu.Unlock()

		cfg := loadConfig()
		items := loadItemStates()
		now := time.Now()

		for _, item := range items {
			if !item.Enabled {
				continue
			}
			if item.Schedule == "manual" {
				continue
			}

			// Check if due
			if !isDue(item, now) {
				continue
			}

			// Check time window for long tasks
			if item.ID == "smart_long" || item.ID == "raid_scrub" {
				if !inTimeWindow(cfg.TimeWindowStart, cfg.TimeWindowEnd) {
					continue
				}
			}

			// Check safety
			if !checkTemperature(cfg.TempLimit) || !checkIOUsage(cfg.IOLimit) {
				continue
			}

			// Run it
			runMu.Lock()
			isRunning = true
			currentRunningItem = item.ID
			currentProgress = 0
			runMu.Unlock()

			go func(it DiagnosticItem, c Config) {
				defer func() {
					runMu.Lock()
					isRunning = false
					currentRunningItem = ""
					currentProgress = 0
					runMu.Unlock()
				}()

				result, detail := runDiagnostic(it.ID, c)
				now := time.Now().Format("2006-01-02 15:04:05")
				db.Exec("INSERT INTO history (item_id, item_name, start_time, end_time, result, detail) VALUES (?, ?, ?, ?, ?, ?)",
					it.ID, it.Name, now, time.Now().Format("2006-01-02 15:04:05"), result, detail)
				db.Exec("UPDATE item_state SET last_run = ?, last_result = ?, last_detail = ? WHERE item_id = ?",
					now, result, detail, it.ID)
			}(item, cfg)
		}
	}
}

func isDue(item DiagnosticItem, now time.Time) bool {
	if item.LastRun == "" {
		return true
	}
	lastRun, err := time.Parse("2006-01-02 15:04:05", item.LastRun)
	if err != nil {
		return true
	}
	switch item.Schedule {
	case "daily":
		return now.Sub(lastRun) >= 24*time.Hour
	case "weekly":
		return now.Sub(lastRun) >= 7*24*time.Hour
	case "monthly":
		return now.Sub(lastRun) >= 30*24*time.Hour
	default:
		return false
	}
}