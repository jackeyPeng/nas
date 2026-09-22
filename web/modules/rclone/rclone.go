package rclone

import (
	"bufio"
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
	Direction   string `json:"direction"` // upload(本地→远端) | download(远端→本地)，默认 upload
	Source      string `json:"source"`    // 本地路径，如 /data/nas1/docs
	Remote      string `json:"remote"`    // 远端名称
	DestPath    string `json:"dest_path"` // 远端路径，如 backup/docs
	Mode        string `json:"mode"`      // sync | copy | bisync
	Schedule    string `json:"schedule"`  // cron 表达式，空表示手动
	Bandwidth   int    `json:"bandwidth"` // KB/s，0=不限
	Transfers   int    `json:"transfers"` // 并发数，默认 4
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
	// 面板重启时，上次运行中被强杀的任务会永远停在 running —— 启动时归一为 failed
	tasks := loadTasks()
	dirty := false
	for i := range tasks {
		if tasks[i].LastResult == "running" {
			tasks[i].LastResult = "failed"
			tasks[i].LastMessage = "面板重启，任务中断"
			dirty = true
		}
	}
	if dirty {
		saveTasks(tasks)
	}
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
	mux.HandleFunc("GET /api/rclone/remotes/{name}", common.AuthMiddleware(handleGetRemote))
	mux.HandleFunc("PUT /api/rclone/remotes/{name}", common.AuthMiddleware(handleUpdateRemote))
	mux.HandleFunc("POST /api/rclone/remotes/test", common.AuthMiddleware(handleTestRemote))
	mux.HandleFunc("GET /api/rclone/shared-dirs", common.AuthMiddleware(handleSharedDirs))
	mux.HandleFunc("POST /api/rclone/mkdir", common.AuthMiddleware(handleMkdir))

	// 同步任务
	mux.HandleFunc("GET /api/rclone/tasks", common.AuthMiddleware(handleListTasks))
	mux.HandleFunc("POST /api/rclone/tasks", common.AuthMiddleware(handleCreateTask))
	mux.HandleFunc("PUT /api/rclone/tasks/{id}", common.AuthMiddleware(handleUpdateTask))
	mux.HandleFunc("DELETE /api/rclone/tasks/{id}", common.AuthMiddleware(handleDeleteTask))
	mux.HandleFunc("POST /api/rclone/tasks/{id}/run", common.AuthMiddleware(handleRunTask))
	mux.HandleFunc("POST /api/rclone/tasks/{id}/toggle", common.AuthMiddleware(handleToggleTask))
	mux.HandleFunc("GET /api/rclone/tasks/{id}/progress", common.AuthMiddleware(handleTaskProgress))

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
	localTargets := aliasRemoteTargets()
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
		// alias 指向本地路径的，展示为 local
		if rtype == "alias" && strings.HasPrefix(localTargets[name], localRemotePrefix) {
			rtype = "local"
		}
		remotes = append(remotes, Remote{
			Name:      name,
			Type:      rtype,
			SecretSet: true, // 能列出来说明已配置
		})
	}
	common.JSONResponse(w, map[string]interface{}{"remotes": remotes})
}

var validRemoteName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// localRemotePrefix 是 alias backend 指向本地路径的前缀（:local:<绝对路径>）。
const localRemotePrefix = ":local:"

// toBackendRemote 将前端类型+配置转换为 rclone 实际 backend。
// rclone 的 local backend 不支持 root/local_path 配置项（其根路径由路径本身表达，
// config create 传入的路径参数会被静默忽略，导致 remote 指向 $HOME）。
// 因此 local 类型改用 alias backend 指向本地绝对路径（:local:<path>）。
// 返回 (实际类型, 实际配置, error)；local 但缺 local_path 时返回 error。
func toBackendRemote(rtype string, config map[string]string) (string, map[string]string, error) {
	if rtype != "local" {
		return rtype, config, nil
	}
	p := strings.TrimSpace(config["local_path"])
	if p == "" {
		return "", nil, fmt.Errorf("local 类型需要 local_path")
	}
	return "alias", map[string]string{"remote": localRemotePrefix + p}, nil
}

// toFrontendRemote 将 rclone 实际 backend 映射回前端类型+配置。
// alias 且 remote 以 :local: 开头 → 前端 "local"，local_path = 去掉前缀的路径。
func toFrontendRemote(rtype string, config map[string]string) (string, map[string]string) {
	if rtype == "alias" && strings.HasPrefix(config["remote"], localRemotePrefix) {
		return "local", map[string]string{"local_path": strings.TrimPrefix(config["remote"], localRemotePrefix)}
	}
	return rtype, config
}

// aliasRemoteTargets 解析 config 里 alias 类型 remote 的 remote 字段（name → target）。
// 用于 list 时把「指向本地路径的 alias」显示为 local 类型。
func aliasRemoteTargets() map[string]string {
	conf := getRcloneConf()
	data, err := common.SudoOutput("cat", conf)
	if err != nil {
		return nil
	}
	targets := map[string]string{}
	var cur string
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			cur = strings.Trim(trimmed, "[]")
			continue
		}
		if cur == "" || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if strings.TrimSpace(parts[0]) == "remote" {
			targets[cur] = strings.TrimSpace(parts[1])
		}
	}
	return targets
}

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

	// local 类型 → alias backend 转换（rclone local backend 不支持路径配置项）
	var cerr error
	rtype, config, cerr = toBackendRemote(rtype, config)
	if cerr != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, cerr.Error()), http.StatusBadRequest)
		return
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

// sensitiveKeys 脱敏的敏感字段
var sensitiveKeys = map[string]bool{
	"secret_access_key": true, "pass": true, "password": true,
	"client_secret": true, "token": true, "key": true,
}

// handleGetRemote 返回单个远端的配置（敏感字段脱敏为 "********"）
func handleGetRemote(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	conf := getRcloneConf()
	data, err := common.SudoOutput("cat", conf)
	if err != nil {
		http.Error(w, `{"error":"读取 rclone 配置失败"}`, http.StatusInternalServerError)
		return
	}
	// 解析 ini 段落
	section := "[" + name + "]"
	inSection := false
	cfg := map[string]string{}
	rtype := ""
	found := false
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == section {
			inSection = true
			found = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inSection = false
			continue
		}
		if !inSection || !strings.Contains(trimmed, "=") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "type" {
			rtype = val
		}
		if sensitiveKeys[key] {
			if val != "" {
				cfg[key] = "********"
			}
		} else {
			cfg[key] = val
		}
	}
	if !found {
		http.Error(w, `{"error":"远端不存在"}`, http.StatusNotFound)
		return
	}
	// alias 指向本地路径的映射回前端 "local" 类型
	rtype, cfg = toFrontendRemote(rtype, cfg)
	common.JSONResponse(w, map[string]interface{}{
		"name":   name,
		"type":   rtype,
		"config": cfg,
	})
}

// handleUpdateRemote 更新远端配置（留空的敏感字段保持不变）
func handleUpdateRemote(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	r.ParseForm()
	args := []string{"config", "update", name}
	count := 0
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "rc_") || len(vals) == 0 {
			continue
		}
		k := strings.TrimPrefix(key, "rc_")
		v := vals[0]
		// 留空或占位符 = 不修改该字段
		if v == "" || v == "********" {
			continue
		}
		// local 类型：local_path 转成 alias backend 的 remote 字段
		if k == "local_path" {
			args = append(args, "remote="+localRemotePrefix+v)
		} else {
			args = append(args, k+"="+v)
		}
		count++
	}
	if count == 0 {
		common.JSONResponse(w, map[string]interface{}{"message": "没有需要修改的字段"})
		return
	}
	cmd := rcloneCmdSudo(args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, string(out)), http.StatusInternalServerError)
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": fmt.Sprintf("远端 %s 已更新", name),
		"output":  string(out),
	})
}

// handleMkdir 在共享目录下创建子目录
func handleMkdir(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	dir := strings.TrimSpace(r.FormValue("dir")) // 必须是共享目录白名单内
	sub := strings.TrimSpace(r.FormValue("sub")) // 子路径，如 backup/2026
	if dir == "" || sub == "" {
		http.Error(w, `{"error":"dir 和 sub 必填"}`, http.StatusBadRequest)
		return
	}
	if !isUnderSharedDir(dir) {
		http.Error(w, `{"error":"dir 必须是已配置的共享目录"}`, http.StatusBadRequest)
		return
	}
	// 防穿越：不允许 ..、绝对路径、首尾斜杠
	if strings.Contains(sub, "..") || strings.HasPrefix(sub, "/") || strings.Contains(sub, "\\") {
		http.Error(w, `{"error":"子路径不能包含 .. 或以 / 开头"}`, http.StatusBadRequest)
		return
	}
	sub = strings.Trim(sub, "/")
	full := filepath.Join(dir, sub)
	// 二次确认拼出来的路径仍在共享目录内
	if !isUnderSharedDir(full) {
		http.Error(w, `{"error":"目标路径超出共享目录范围"}`, http.StatusBadRequest)
		return
	}
	out, err := common.SudoOutput("mkdir", "-p", full)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"创建失败: %s: %v"}`, out, err), http.StatusInternalServerError)
		return
	}
	// 属主对齐父共享目录（保持 Samba 可写）
	common.SudoExec("bash", "-c", fmt.Sprintf("chown --reference=%s %s", dir, full))
	common.JSONResponse(w, map[string]interface{}{
		"message": "目录已创建",
		"path":    full,
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
		if _, ok := r.Form["schedule"]; ok {
			tasks[i].Schedule = r.FormValue("schedule") // 允许清空（回到手动）
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

// taskProgress 保存正在运行（或刚结束）任务的实时进度，供 /progress 接口轮询。
type taskProgress struct {
	mu          sync.Mutex
	TaskID      string
	Running     bool
	StartTime   string
	Percent     int
	Speed       string
	ETA         string
	Transferred string
	Total       string
	StatsLine   string
	OutputTail  []string // 最近 40 行输出
	startedAt   time.Time
	full        strings.Builder // 完整输出（写日志用）
	fullLen     int
}

const maxFullOutput = 512 * 1024

var progressMap sync.Map // taskID -> *taskProgress

// statsRe 匹配 rclone --stats-one-line 的进度行。实测本机 rclone 输出形如：
// "2026-09-22 16:50:41 INFO  :    50.024 MiB / 76.294 MiB, 66%, 2.106 MiB/s, ETA 12s"
// （无 Transferred: 前缀）；带 --stats-one-line-date 或旧版本时可能带前缀，故 Transferred: 设为可选。
var statsRe = regexp.MustCompile(`(?:Transferred:\s*)?([0-9.]+\s?[KMGTPE]?i?B)\s*/\s*([0-9.]+\s?[KMGTPE]?i?B),\s*([0-9]+)%,\s*([^,]*),\s*ETA\s*(\S+)`)

func (p *taskProgress) pushLine(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fullLen < maxFullOutput {
		p.full.WriteString(line + "\n")
		p.fullLen += len(line) + 1
	}
	p.OutputTail = append(p.OutputTail, line)
	if len(p.OutputTail) > 40 {
		p.OutputTail = p.OutputTail[len(p.OutputTail)-40:]
	}
	if m := statsRe.FindStringSubmatch(line); m != nil {
		p.Transferred = strings.TrimSpace(m[1])
		p.Total = strings.TrimSpace(m[2])
		p.Percent, _ = strconv.Atoi(m[3])
		p.Speed = strings.TrimSpace(m[4])
		p.ETA = m[5]
		p.StatsLine = line
	}
}

// progressView 是 /progress 接口的 JSON 快照。
type progressView struct {
	TaskID      string   `json:"task_id"`
	Running     bool     `json:"running"`
	StartTime   string   `json:"start_time"`
	Elapsed     string   `json:"elapsed"`
	Percent     int      `json:"percent"`
	Speed       string   `json:"speed"`
	ETA         string   `json:"eta"`
	Transferred string   `json:"transferred"`
	Total       string   `json:"total"`
	StatsLine   string   `json:"stats_line"`
	OutputTail  []string `json:"output_tail"`
}

func (p *taskProgress) view() progressView {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := progressView{
		TaskID:      p.TaskID,
		Running:     p.Running,
		StartTime:   p.StartTime,
		Percent:     p.Percent,
		Speed:       p.Speed,
		ETA:         p.ETA,
		Transferred: p.Transferred,
		Total:       p.Total,
		StatsLine:   p.StatsLine,
		OutputTail:  append([]string{}, p.OutputTail...),
	}
	if !p.startedAt.IsZero() {
		v.Elapsed = formatElapsed(time.Since(p.startedAt))
	}
	return v
}

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// setTaskRunState 更新任务的 LastRun/LastResult/LastMessage 并落盘。
func setTaskRunState(id, result, runTime, message string) {
	tasks := loadTasks()
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].LastRun = runTime
			tasks[i].LastResult = result
			tasks[i].LastMessage = message
			saveTasks(tasks)
			return
		}
	}
}

// handleTaskProgress 返回任务实时进度（运行中或最近一次运行结果）。
func handleTaskProgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	p, ok := progressMap.Load(id)
	if !ok {
		common.JSONResponse(w, progressView{TaskID: id, OutputTail: []string{}})
		return
	}
	common.JSONResponse(w, p.(*taskProgress).view())
}

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
	// 立即把任务标为运行中（手动 run 和定时调度两条路径都覆盖），
	// 前端任务列表马上能看到「运行中」徽标，运行按钮同时禁用。
	setTaskRunState(task.ID, "running", start.Format("2006-01-02 15:04:05"), "")
	logEntry := TaskLog{
		ID:        generateID(),
		TaskID:    task.ID,
		TaskName:  task.Name,
		StartTime: start.Format("2006-01-02 15:04:05"),
	}

	// 进度追踪（运行中 + 结束后保留最后一份快照供前端轮询）
	prog := &taskProgress{TaskID: task.ID, Running: true, StartTime: logEntry.StartTime, startedAt: start, OutputTail: []string{}}
	progressMap.Store(task.ID, prog)

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

	// 流式执行：边读输出边更新进度，不再等进程结束才拿到日志
	cmd := rcloneCmdSudo(args...)
	var runErr error
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		runErr = err
	} else {
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			runErr = err
		} else {
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				prog.pushLine(sc.Text())
			}
			runErr = cmd.Wait()
		}
	}

	prog.mu.Lock()
	fullOutput := prog.full.String()
	prog.Running = false
	prog.mu.Unlock()

	logEntry.EndTime = time.Now().Format("2006-01-02 15:04:05")
	logEntry.Output = fullOutput

	// 更新任务状态
	var result, message string
	if runErr != nil {
		message = runErr.Error()
		// rclone 失败时把输出里的第一条 ERROR 行并入 message，前端不用展开日志就能看到原因
		for _, line := range prog.view().OutputTail {
			if strings.Contains(line, "ERROR") || strings.Contains(line, "Failed to") {
				message = strings.TrimSpace(line)
				break
			}
		}
		result = "failed"
		common.EmitEvent("rclone", common.EventError, "rclone.sync_failed",
			map[string]interface{}{"task": task.Name, "remote": task.Remote, "direction": task.Direction},
			runErr.Error())
	} else {
		result, message = "success", "同步完成"
		common.EmitEvent("rclone", common.EventSuccess, "rclone.sync_ok",
			map[string]interface{}{"task": task.Name, "remote": task.Remote, "direction": task.Direction},
			"")
	}
	logEntry.Result = result
	logEntry.Message = message
	setTaskRunState(task.ID, result, logEntry.EndTime, message)

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
