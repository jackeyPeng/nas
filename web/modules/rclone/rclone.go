package rclone

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"
)

// ---------- 数据模型 ----------

type Remote struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Provider string `json:"provider,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// 敏感字段不返回给前端
	SecretSet bool `json:"secret_set"`
}

type SyncTask struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Direction   string `json:"direction"`    // upload(本地→远端) | download(远端→本地)，默认 upload
	Source      string `json:"source"`       // 本地路径，如 /data/nas1/docs
	Remote      string `json:"remote"`       // 远端名称
	DestPath    string `json:"dest_path"`    // 远端路径，如 backup/docs
	Mode        string `json:"mode"`         // sync | copy | bisync
	Schedule    string `json:"schedule"`     // cron 表达式，空表示手动
	Bandwidth   int    `json:"bandwidth"`    // KB/s，0=不限
	Transfers   int    `json:"transfers"`    // 并发数，默认 4
	Enabled     bool   `json:"enabled"`
	LastRun     string `json:"last_run,omitempty"`
	LastResult  string `json:"last_result,omitempty"` // success | failed | running
	LastMessage string `json:"last_message,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type TaskLog struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	TaskName  string `json:"task_name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Result    string `json:"result"` // success | failed
	Message   string `json:"message"`
	Output    string `json:"output"`
}

// ---------- 持久化 ----------

var (
	dataDir   = "/opt/nas/data/rclone"
	tasksFile = dataDir + "/tasks.json"
	logsFile  = dataDir + "/logs.json"
	mu        sync.RWMutex
)

func init() {
	os.MkdirAll(dataDir, 0755)
}

func loadTasks() []SyncTask {
	mu.RLock()
	defer mu.RUnlock()
	var tasks []SyncTask
	data, err := os.ReadFile(tasksFile)
	if err != nil {
		return []SyncTask{}
	}
	json.Unmarshal(data, &tasks)
	return tasks
}

func saveTasks(tasks []SyncTask) error {
	mu.Lock()
	defer mu.Unlock()
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	tmp := tasksFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, tasksFile)
}

func loadLogs() []TaskLog {
	mu.RLock()
	defer mu.RUnlock()
	var logs []TaskLog
	data, err := os.ReadFile(logsFile)
	if err != nil {
		return []TaskLog{}
	}
	json.Unmarshal(data, &logs)
	return logs
}

func appendLog(log TaskLog) {
	mu.Lock()
	defer mu.Unlock()
	logs := []TaskLog{}
	data, err := os.ReadFile(logsFile)
	if err == nil {
		json.Unmarshal(data, &logs)
	}
	// 只保留最近 200 条
	if len(logs) >= 200 {
		logs = logs[len(logs)-199:]
	}
	logs = append(logs, log)
	data, _ = json.MarshalIndent(logs, "", "  ")
	os.WriteFile(logsFile, data, 0644)
}

// ---------- rclone 命令封装 ----------

var rcloneConfPath = "/root/.config/rclone/rclone.conf"

// getRcloneConf 返回 rclone 配置文件路径
func getRcloneConf() string {
	if _, err := os.Stat(rcloneConfPath); err == nil {
		return rcloneConfPath
	}
	// fallback: 当前用户 home
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "rclone", "rclone.conf")
}

// rcloneCmd 构建带配置文件参数的 rclone 命令
func rcloneCmd(args ...string) *exec.Cmd {
	conf := getRcloneConf()
	fullArgs := append([]string{"--config", conf}, args...)
	return exec.Command("rclone", fullArgs...)
}

// rcloneCmdSudo 以 sudo 运行 rclone（需要访问 /data 等 root 目录）
func rcloneCmdSudo(args ...string) *exec.Cmd {
	conf := getRcloneConf()
	fullArgs := append([]string{"--config", conf}, args...)
	return exec.Command("sudo", append([]string{"rclone"}, fullArgs...)...)
}

// ---------- API Handlers ----------

func RegisterRoutes(mux *http.ServeMux) {
	// Remote 管理
	mux.HandleFunc("GET /api/rclone/remotes", common.AuthMiddleware(handleListRemotes))
	mux.HandleFunc("POST /api/rclone/remotes", common.AuthMiddleware(handleCreateRemote))
	mux.HandleFunc("DELETE /api/rclone/remotes/{name}", common.AuthMiddleware(handleDeleteRemote))
	mux.HandleFunc("POST /api/rclone/remotes/test", common.AuthMiddleware(handleTestRemote))
	mux.HandleFunc("GET /api/rclone/shared-dirs", common.AuthMiddleware(handleSharedDirs))

	// 同步任务
	mux.HandleFunc("GET /api/rclone/tasks", common.AuthMiddleware(handleListTasks))
	mux.HandleFunc("POST /api/rclone/tasks", common.AuthMiddleware(handleCreateTask))
	mux.HandleFunc("PUT /api/rclone/tasks/{id}", common.AuthMiddleware(handleUpdateTask))
	mux.HandleFunc("DELETE /api/rclone/tasks/{id}", common.AuthMiddleware(handleDeleteTask))
	mux.HandleFunc("POST /api/rclone/tasks/{id}/run", common.AuthMiddleware(handleRunTask))
	mux.HandleFunc("POST /api/rclone/tasks/{id}/toggle", common.AuthMiddleware(handleToggleTask))

	// 日志
	mux.HandleFunc("GET /api/rclone/logs", common.AuthMiddleware(handleListLogs))
	mux.HandleFunc("DELETE /api/rclone/logs", common.AuthMiddleware(handleClearLogs))

	// 状态
	mux.HandleFunc("GET /api/rclone/status", common.AuthMiddleware(handleStatus))
}

// handleStatus 返回 rclone 是否安装、版本、配置文件路径
func handleStatus(w http.ResponseWriter, r *http.Request) {
	out, err := exec.Command("rclone", "version").Output()
	installed := err == nil
	version := ""
	if installed {
		lines := strings.Split(string(out), "\n")
		if len(lines) > 0 {
			version = strings.TrimSpace(lines[0])
		}
	}
	common.JSONResponse(w, map[string]interface{}{
		"installed": installed,
		"version":   version,
		"conf_path": getRcloneConf(),
	})
}

// ---------- Remote 管理 ----------

func handleListRemotes(w http.ResponseWriter, r *http.Request) {
	cmd := rcloneCmd("listremotes", "--long")
	out, err := cmd.Output()
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"remotes": []Remote{}})
		return
	}
	var remotes []Remote
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 格式: "name: type"
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		rtype := strings.TrimSpace(parts[1])
		remotes = append(remotes, Remote{
			Name:      name,
			Type:      rtype,
			SecretSet: true, // 能列出来说明已配置
		})
	}
	common.JSONResponse(w, map[string]interface{}{"remotes": remotes})
}

var validRemoteName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func handleCreateRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	rtype := strings.TrimSpace(r.FormValue("type"))
	if name == "" || rtype == "" {
		http.Error(w, `{"error":"name and type required"}`, http.StatusBadRequest)
		return
	}
	if !validRemoteName.MatchString(name) {
		http.Error(w, `{"error":"name must be alphanumeric with - or _"}`, http.StatusBadRequest)
		return
	}

	// 收集所有 rclone 配置参数（以 rc_ 开头的 form 字段）
	config := map[string]string{}
	for key, vals := range r.Form {
		if strings.HasPrefix(key, "rc_") && len(vals) > 0 {
			config[strings.TrimPrefix(key, "rc_")] = vals[0]
		}
	}

	// 构建 rclone config create 命令
	args := []string{"config", "create", name, rtype}
	for k, v := range config {
		args = append(args, k+"="+v)
	}

	cmd := rcloneCmdSudo(args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, string(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("远端 %s 已创建", name),
		"output":  string(out),
	})
}

func handleDeleteRemote(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	cmd := rcloneCmdSudo("config", "delete", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, string(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{"message": fmt.Sprintf("远端 %s 已删除", name)})
}

func handleTestRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	// 用 lsd 测试连接（只列目录，不传输）
	cmd := rcloneCmdSudo("lsd", name+":")
	out, err := cmd.CombinedOutput()
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{
			"ok":      false,
			"message": "连接失败",
			"error":   string(out),
		})
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"ok":      true,
		"message": "连接成功",
		"output":  string(out),
	})
}

// ---------- 同步任务 ----------

// listSharedDirs 从 smb.conf 解析已配置共享的本地路径清单
func listSharedDirs() []string {
	smbConf, err := common.SudoOutput("cat", "/etc/samba/smb.conf")
	if err != nil {
		return []string{}
	}
	var dirs []string
	seen := map[string]bool{}
	var currentName string
	for _, line := range strings.Split(smbConf, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			currentName = strings.Trim(trimmed, "[]")
			continue
		}
		if currentName == "" || currentName == "global" || currentName == "homes" ||
			currentName == "printers" || currentName == "print$" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "path") && strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			p := strings.TrimSpace(parts[1])
			if p != "" && !seen[p] {
				seen[p] = true
				dirs = append(dirs, p)
			}
		}
	}
	return dirs
}

// handleSharedDirs 返回可选的本地共享目录清单（前端下拉用）
func handleSharedDirs(w http.ResponseWriter, r *http.Request) {
	common.JSONResponse(w, map[string]interface{}{"dirs": listSharedDirs()})
}

// isUnderSharedDir 校验路径是否在某个已配置共享目录范围内（等于共享路径或为其子路径）
func isUnderSharedDir(p string) bool {
	p = filepath.Clean(p)
	for _, dir := range listSharedDirs() {
		dir = filepath.Clean(dir)
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

func handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := loadTasks()
	// 按创建时间倒序
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt > tasks[j].CreatedAt
	})
	common.JSONResponse(w, map[string]interface{}{"tasks": tasks})
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	direction := strings.TrimSpace(r.FormValue("direction"))
	source := strings.TrimSpace(r.FormValue("source"))
	remote := strings.TrimSpace(r.FormValue("remote"))
	destPath := strings.TrimSpace(r.FormValue("dest_path"))
	mode := strings.TrimSpace(r.FormValue("mode"))
	schedule := strings.TrimSpace(r.FormValue("schedule"))
	bandwidth, _ := strconv.Atoi(r.FormValue("bandwidth"))
	transfers, _ := strconv.Atoi(r.FormValue("transfers"))
	if transfers <= 0 {
		transfers = 4
	}

	if name == "" || source == "" || remote == "" || destPath == "" {
		http.Error(w, `{"error":"name, source, remote, dest_path required"}`, http.StatusBadRequest)
		return
	}
	if mode != "sync" && mode != "copy" && mode != "bisync" {
		mode = "sync"
	}
	if direction != "download" {
		direction = "upload"
	}
	// 安全检查：source 必须是绝对路径
	if !strings.HasPrefix(source, "/") {
		http.Error(w, `{"error":"source must be absolute path"}`, http.StatusBadRequest)
		return
	}
	// 安全检查：source 不能是系统目录
	forbidden := []string{"/etc", "/bin", "/sbin", "/usr", "/boot", "/proc", "/sys", "/dev"}
	for _, f := range forbidden {
		if strings.HasPrefix(source, f) {
			http.Error(w, `{"error":"source cannot be system directory"}`, http.StatusBadRequest)
			return
		}
	}
	// 白名单校验：source 必须在已配置共享目录范围内
	if !isUnderSharedDir(source) {
		http.Error(w, `{"error":"本地路径必须在已配置的共享目录范围内，请从下拉列表选择"}`, http.StatusBadRequest)
		return
	}

	task := SyncTask{
		ID:        generateID(),
		Name:      name,
		Direction: direction,
		Source:    source,
		Remote:    remote,
		DestPath:  destPath,
		Mode:      mode,
		Schedule:  schedule,
		Bandwidth: bandwidth,
		Transfers: transfers,
		Enabled:   true,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}

	tasks := loadTasks()
	tasks = append(tasks, task)
	if err := saveTasks(tasks); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	common.JSONResponse(w, map[string]interface{}{
		"message": "任务已创建",
		"task":    task,
	})
}

func handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	r.ParseForm()
	tasks := loadTasks()
	for i, t := range tasks {
		if t.ID != id {
			continue
		}
		if v := r.FormValue("direction"); v == "upload" || v == "download" {
			tasks[i].Direction = v
		}
		if v := r.FormValue("name"); v != "" {
			tasks[i].Name = v
		}
		if v := r.FormValue("source"); v != "" {
			if !isUnderSharedDir(v) {
				http.Error(w, `{"error":"本地路径必须在已配置的共享目录范围内"}`, http.StatusBadRequest)
				return
			}
			tasks[i].Source = v
		}
		if v := r.FormValue("remote"); v != "" {
			tasks[i].Remote = v
		}
		if v := r.FormValue("dest_path"); v != "" {
			tasks[i].DestPath = v
		}
		if v := r.FormValue("mode"); v != "" {
			tasks[i].Mode = v
		}
		if v := r.FormValue("schedule"); v != "" {
			tasks[i].Schedule = v
		}
		if v := r.FormValue("bandwidth"); v != "" {
			tasks[i].Bandwidth, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("transfers"); v != "" {
			tasks[i].Transfers, _ = strconv.Atoi(v)
		}
		if err := saveTasks(tasks); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		common.JSONResponse(w, map[string]interface{}{"message": "任务已更新", "task": tasks[i]})
		return
	}
	http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
}

func handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	tasks := loadTasks()
	for i, t := range tasks {
		if t.ID == id {
			tasks = append(tasks[:i], tasks[i+1:]...)
			saveTasks(tasks)
			common.JSONResponse(w, map[string]interface{}{"message": "任务已删除"})
			return
		}
	}
	http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
}

func handleToggleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	tasks := loadTasks()
	for i, t := range tasks {
		if t.ID == id {
			tasks[i].Enabled = !t.Enabled
			saveTasks(tasks)
			status := "启用"
			if !tasks[i].Enabled {
				status = "禁用"
			}
			common.JSONResponse(w, map[string]interface{}{
				"message": fmt.Sprintf("任务已%s", status),
				"enabled": tasks[i].Enabled,
			})
			return
		}
	}
	http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
}

// ---------- 任务执行 ----------

var runningTasks = sync.Map{} // taskID -> bool

func handleRunTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}

	tasks := loadTasks()
	var task *SyncTask
	for i := range tasks {
		if tasks[i].ID == id {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}

	// 检查是否已在运行
	if _, running := runningTasks.LoadOrStore(id, true); running {
		http.Error(w, `{"error":"task already running"}`, http.StatusConflict)
		return
	}

	// 异步执行
	go executeTask(*task)

	common.JSONResponse(w, map[string]interface{}{
		"message": "任务已开始执行",
		"task_id": id,
	})
}

func executeTask(task SyncTask) {
	defer runningTasks.Delete(task.ID)

	start := time.Now()
	logEntry := TaskLog{
		ID:        generateID(),
		TaskID:    task.ID,
		TaskName:  task.Name,
		StartTime: start.Format("2006-01-02 15:04:05"),
	}

	// 构建 rclone 命令
	args := []string{}
	switch task.Mode {
	case "sync":
		args = append(args, "sync")
	case "copy":
		args = append(args, "copy")
	case "bisync":
		args = append(args, "bisync")
	default:
		args = append(args, "sync")
	}

	// 源和目标（按方向交换）
	localPath := task.Source
	remotePath := fmt.Sprintf("%s:%s", task.Remote, task.DestPath)
	var src, dst string
	if task.Direction == "download" {
		src, dst = remotePath, localPath
	} else {
		src, dst = localPath, remotePath
	}
	args = append(args, src, dst)

	// 选项
	if task.Bandwidth > 0 {
		args = append(args, "--bwlimit", fmt.Sprintf("%dk", task.Bandwidth))
	}
	if task.Transfers > 0 {
		args = append(args, "--transfers", strconv.Itoa(task.Transfers))
	}
	args = append(args, "--stats", "5s", "--stats-one-line", "-v")

	cmd := rcloneCmdSudo(args...)
	out, err := cmd.CombinedOutput()

	logEntry.EndTime = time.Now().Format("2006-01-02 15:04:05")
	logEntry.Output = string(out)

	// 更新任务状态
	tasks := loadTasks()
	for i := range tasks {
		if tasks[i].ID == task.ID {
			tasks[i].LastRun = logEntry.EndTime
			if err != nil {
				tasks[i].LastResult = "failed"
				tasks[i].LastMessage = err.Error()
				logEntry.Result = "failed"
				logEntry.Message = err.Error()
			} else {
				tasks[i].LastResult = "success"
				tasks[i].LastMessage = "同步完成"
				logEntry.Result = "success"
				logEntry.Message = "同步完成"
			}
			saveTasks(tasks)
			break
		}
	}

	appendLog(logEntry)
}

// ---------- 日志 ----------

func handleListLogs(w http.ResponseWriter, r *http.Request) {
	logs := loadLogs()
	// 按时间倒序
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].StartTime > logs[j].StartTime
	})
	// 只返回最近 50 条，输出截断
	if len(logs) > 50 {
		logs = logs[:50]
	}
	for i := range logs {
		if len(logs[i].Output) > 2000 {
			logs[i].Output = logs[i].Output[:2000] + "\n... (truncated)"
		}
	}
	common.JSONResponse(w, map[string]interface{}{"logs": logs})
}

func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	os.Remove(logsFile)
	common.JSONResponse(w, map[string]interface{}{"message": "日志已清空"})
}
