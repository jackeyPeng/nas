package diskmgmt

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nas-panel/common"

	_ "modernc.org/sqlite"
)

var (
	configDB     *sql.DB
	configDBOnce sync.Once
)

// configDBPath returns the path to the config metadata database
func configDBPath() string {
	dir := "/opt/nas/data"
	if d := os.Getenv("NAS_DATA_DIR"); d != "" {
		dir = d
	}
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "folders.db")
}

// initConfigDB opens the SQLite metadata database and seeds it from existing folders
func initConfigDB() *sql.DB {
	configDBOnce.Do(func() {
		var err error
		configDB, err = sql.Open("sqlite", configDBPath())
		if err != nil {
			log.Printf("[CONFIG_SYNC] 无法打开元数据库: %v", err)
			return
		}
		configDB.Exec(`CREATE TABLE IF NOT EXISTS folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			path TEXT NOT NULL UNIQUE,
			pool TEXT NOT NULL,
			permission TEXT NOT NULL DEFAULT 'readwrite',
			valid_users TEXT NOT NULL DEFAULT '',
			write_users TEXT NOT NULL DEFAULT '',
			recycle_bin INTEGER NOT NULL DEFAULT 0,
			samba_share INTEGER NOT NULL DEFAULT 1,
			nfs_export INTEGER NOT NULL DEFAULT 0,
			quota_gb INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`)
		// 迁移：老库补 write_users 列（幂等，检测列是否存在）
		migrateWriteUsersColumn(configDB)
		log.Printf("[CONFIG_SYNC] 元数据库已初始化: %s", configDBPath())

		// Seed from existing folders if table is empty
		var count int
		configDB.QueryRow("SELECT COUNT(*) FROM folders").Scan(&count)
		if count == 0 {
			seedFromFileSystem(configDB)
		}
	})
	return configDB
}

// seedFromFileSystem scans /data for existing folders and populates metadata
func seedFromFileSystem(db *sql.DB) {
	mounts := getExistingDataMounts()
	for _, m := range mounts {
		entries, err := os.ReadDir(m["mount"])
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "#recycle" {
				continue
			}
			path := filepath.Join(m["mount"], entry.Name())
			name := entry.Name()

			// Check Samba
			smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
			smbMap := parseSambaShares(smbConf)
			smb, hasSamba := smbMap[path]
			perm := "readwrite"
			validUsers := ""
			writeUsers := ""
			recycle := false
			samba := hasSamba
			if hasSamba {
				if smb["read_only"] == "yes" {
					perm = "readonly"
				}
				validUsers = smb["valid_users"]
				writeUsers = smb["write_list"]
				recycle = hasRecycleBin(smbConf, name)
			}

			// Check NFS
			nfs := isNFSExported(path)

			_, err = db.Exec(`INSERT OR IGNORE INTO folders (name, path, pool, permission, valid_users, write_users, recycle_bin, samba_share, nfs_export)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				name, path, m["mount"], perm, validUsers, writeUsers, boolToInt(recycle), boolToInt(samba), boolToInt(nfs))
			if err != nil {
				log.Printf("[CONFIG_SYNC] seed 失败 %s: %v", name, err)
			} else {
				log.Printf("[CONFIG_SYNC] seed: %s (samba=%v nfs=%v)", name, samba, nfs)
			}
		}
	}
	log.Printf("[CONFIG_SYNC] seed 完成")
}

// FolderMeta represents a folder's metadata stored in SQLite
type FolderMeta struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Pool       string `json:"pool"`
	Permission string `json:"permission"`
	ValidUsers string `json:"valid_users"`
	WriteUsers string `json:"write_users"`
	RecycleBin bool   `json:"recycle_bin"`
	SambaShare bool   `json:"samba_share"`
	NFSExport  bool   `json:"nfs_export"`
	QuotaGB    int    `json:"quota_gb"`
	CreatedAt  string `json:"created_at"`
}

// migrateWriteUsersColumn adds the write_users column to an existing folders
// table if it is missing (idempotent, safe to run on every startup).
func migrateWriteUsersColumn(db *sql.DB) {
	rows, err := db.Query(`PRAGMA table_info(folders)`)
	if err != nil {
		log.Printf("[CONFIG_SYNC] 检查 folders 列失败: %v", err)
		return
	}
	defer rows.Close()
	hasWriteUsers := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == "write_users" {
			hasWriteUsers = true
		}
	}
	if hasWriteUsers {
		return
	}
	if _, err := db.Exec(`ALTER TABLE folders ADD COLUMN write_users TEXT NOT NULL DEFAULT ''`); err != nil {
		log.Printf("[CONFIG_SYNC] 添加 write_users 列失败: %v", err)
		return
	}
	log.Printf("[CONFIG_SYNC] 已迁移: folders 表新增 write_users 列")
}

// SyncFolderMeta ensures the metadata table matches the file system
// Called after create/edit/delete folder operations
func SyncFolderMeta(name, folderPath, pool, permission, validUsers, writeUsers string, samba, nfs, recycle bool, quotaGB int) {
	db := initConfigDB()
	if db == nil {
		return
	}

	// Upsert: insert or update
	_, err := db.Exec(`INSERT INTO folders (name, path, pool, permission, valid_users, write_users, samba_share, nfs_export, recycle_bin, quota_gb, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name, pool=excluded.pool, permission=excluded.permission,
			valid_users=excluded.valid_users, write_users=excluded.write_users,
			samba_share=excluded.samba_share,
			nfs_export=excluded.nfs_export, recycle_bin=excluded.recycle_bin,
			quota_gb=excluded.quota_gb, updated_at=datetime('now')`,
		name, folderPath, pool, permission, validUsers, writeUsers, boolToInt(samba), boolToInt(nfs), boolToInt(recycle), quotaGB)
	if err != nil {
		log.Printf("[CONFIG_SYNC] 同步元数据失败: %v", err)
	}
}

// RemoveFolderMeta removes a folder from metadata
func RemoveFolderMeta(folderPath string) {
	db := initConfigDB()
	if db == nil {
		return
	}
	db.Exec("DELETE FROM folders WHERE path = ?", folderPath)
}

// GetAllFolderMeta returns all folder metadata
func GetAllFolderMeta() []FolderMeta {
	db := initConfigDB()
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT id, name, path, pool, permission, valid_users, write_users, recycle_bin, samba_share, nfs_export, quota_gb, created_at FROM folders ORDER BY id")
	if err != nil {
		log.Printf("[CONFIG_SYNC] 查询元数据失败: %v", err)
		return nil
	}
	defer rows.Close()

	var result []FolderMeta
	for rows.Next() {
		var m FolderMeta
		var rb, smb, nfs int
		rows.Scan(&m.ID, &m.Name, &m.Path, &m.Pool, &m.Permission, &m.ValidUsers, &m.WriteUsers, &rb, &smb, &nfs, &m.QuotaGB, &m.CreatedAt)
		m.RecycleBin = rb != 0
		m.SambaShare = smb != 0
		m.NFSExport = nfs != 0
		result = append(result, m)
	}
	return result
}

// ═══════════════════════════════════════════════════════════
// 配置生成引擎
// ═══════════════════════════════════════════════════════════

const managedStart = "# === Z1 MANAGED SHARES START ==="
const managedEnd = "# === Z1 MANAGED SHARES END ==="

// GenerateSambaConfig generates the managed section of smb.conf from metadata
func GenerateSambaConfig() error {
	metas := GetAllFolderMeta()
	if metas == nil {
		return fmt.Errorf("无法读取元数据")
	}

	// Build the managed section
	var sb strings.Builder
	sb.WriteString(managedStart + "\n")
	nasUser := getNASUser()
	for _, m := range metas {
		if !m.SambaShare {
			continue
		}
		users := m.ValidUsers
		if users == "" {
			users = nasUser
		}

		writeMode, writeList := smbShareParams(m, nasUser)

		sb.WriteString(fmt.Sprintf(`
[%s]
   path = %s
   browseable = yes
   %s
%s   valid users = %s
   create mask = 0775
   directory mask = 0775
   force user = %s
   force group = %s
`, m.Name, m.Path, writeMode, writeList, users, nasUser, nasUser))

		if m.RecycleBin {
			sb.WriteString(`   vfs objects = recycle
   recycle:repository = #recycle
   recycle:keeptree = yes
   recycle:versions = yes
   recycle:touch = yes
`)
		}
	}
	sb.WriteString(managedEnd + "\n")

	managedBlock := sb.String()

	// Read current smb.conf
	current, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")

	// Replace or append managed section
	newConf := replaceManagedBlock(current, managedBlock)
	if newConf == current {
		return nil // No change
	}

	// Write back
	common.SafeWriteFile("/etc/samba/smb.conf", newConf)
	log.Printf("[CONFIG_SYNC] Samba 配置已更新 (%d 个共享)", len(metas))
	return nil
}

// GenerateNFSConfig generates the managed section of /etc/exports from metadata
func GenerateNFSConfig() error {
	metas := GetAllFolderMeta()
	if metas == nil {
		return fmt.Errorf("无法读取元数据")
	}

	var sb strings.Builder
	sb.WriteString(managedStart + "\n")
	subnet := detectLANSubnet()
	for _, m := range metas {
		if !m.NFSExport {
			continue
		}
		opts := "rw,sync,no_subtree_check,no_root_squash"
		if m.Permission == "readonly" {
			opts = "ro,sync,no_subtree_check"
		}
		sb.WriteString(fmt.Sprintf("%s %s(%s)\n", m.Path, subnet, opts))
	}
	sb.WriteString(managedEnd + "\n")

	managedBlock := sb.String()

	current, _ := common.SudoOutput("cat", "/etc/exports")
	newConf := replaceManagedBlock(current, managedBlock)
	if newConf == current {
		return nil
	}

	common.SafeWriteFile("/etc/exports", newConf)
	common.SudoExec("exportfs", "-a")
	log.Printf("[CONFIG_SYNC] NFS 配置已更新")
	return nil
}

// SyncAllConfigs regenerates all configs from metadata and reloads services
func SyncAllConfigs() error {
	var errs []string
	changed := false

	if err := GenerateSambaConfig(); err != nil {
		errs = append(errs, "Samba: "+err.Error())
	} else {
		changed = true
	}

	if err := GenerateNFSConfig(); err != nil {
		errs = append(errs, "NFS: "+err.Error())
	} else {
		changed = true
	}

	if changed {
		reloadServices()
	}

	if len(errs) > 0 {
		return fmt.Errorf("配置同步失败: %s", strings.Join(errs, "; "))
	}
	return nil
}

// reloadServices reloads all services after config changes
func reloadServices() {
	// Samba: graceful reload (no disconnect)
	if out, err := common.SudoOutput("systemctl", "reload", "smbd"); err != nil {
		log.Printf("[CONFIG_SYNC] smbd reload 失败: %s", out)
		// Fallback: restart
		common.SudoExec("systemctl", "restart", "smbd")
		log.Printf("[CONFIG_SYNC] smbd 已重启")
	} else {
		log.Printf("[CONFIG_SYNC] smbd 已重载配置")
	}

	// NFS: exportfs -a already called in GenerateNFSConfig, no restart needed

	// FTP: reload config
	if isServiceActive("vsftpd") {
		common.SudoExec("systemctl", "reload", "vsftpd")
		log.Printf("[CONFIG_SYNC] vsftpd 已重载")
	}

	// WebDAV: restart (rclone doesn't support reload)
	if isServiceActive("rclone-webdav") {
		common.SudoExec("systemctl", "restart", "rclone-webdav")
		log.Printf("[CONFIG_SYNC] rclone-webdav 已重启")
	}

	// S3: restart (rclone doesn't support reload)
	if isServiceActive("rclone-s3") {
		common.SudoExec("systemctl", "restart", "rclone-s3")
		log.Printf("[CONFIG_SYNC] rclone-s3 已重启")
	}

	// Record reload timestamp
	markReloaded()
}

// isServiceActive checks if a systemd service is active
func isServiceActive(name string) bool {
	out, err := common.SudoOutput("systemctl", "is-active", name)
	return err == nil && strings.TrimSpace(out) == "active"
}

// verifyServiceConfig checks if a running service matches the config on disk
func verifyServiceConfig() []string {
	var issues []string

	// Compare config mtime against last reload timestamp
	reloadFile := "/opt/nas/data/.last_reload"
	reloadStamp, _ := common.SudoOutput("cat", reloadFile)
	reloadStamp = strings.TrimSpace(reloadStamp)

	configMtime, _ := common.SudoOutput("stat", "-c", "%Y", "/etc/samba/smb.conf")
	configMtime = strings.TrimSpace(configMtime)

	if reloadStamp == "" {
		// No reload recorded yet, skip check
		return nil
	}

	if configMtime > reloadStamp {
		issues = append(issues, "Samba 配置文件已更新但服务可能未重载，请点击修复配置")
	}

	return issues
}

// markReloaded writes the current config mtime as the last reload timestamp
func markReloaded() {
	reloadFile := "/opt/nas/data/.last_reload"
	configMtime, _ := common.SudoOutput("stat", "-c", "%Y", "/etc/samba/smb.conf")
	common.SafeWriteFile(reloadFile, strings.TrimSpace(configMtime))
}

// ═══════════════════════════════════════════════════════════
// 一致性检查
// ═══════════════════════════════════════════════════════════

// ConfigIssues represents configuration consistency problems
type ConfigIssues struct {
	HasIssues bool     `json:"has_issues"`
	Issues    []string `json:"issues"`
}

// CheckConfigConsistency compares file system + metadata + config files
func CheckConfigConsistency() ConfigIssues {
	var issues ConfigIssues

	metas := GetAllFolderMeta()

	// 1. Check: folders in metadata but directory missing
	for _, m := range metas {
		if _, err := os.Stat(m.Path); os.IsNotExist(err) {
			issues.HasIssues = true
			issues.Issues = append(issues.Issues, fmt.Sprintf("文件夹 %s 在元数据中存在但目录已丢失: %s（已自动清理）", m.Name, m.Path))
			// Auto-cleanup: remove stale metadata entry
			RemoveFolderMeta(m.Path)
		}
	}

	// 2. Check: folders on disk but not in metadata
	scanOrphanDirs := func(mountPoint string) {
		entries, err := os.ReadDir(mountPoint)
		if err != nil {
			return
		}
		metaMap := make(map[string]bool)
		for _, m := range metas {
			metaMap[m.Path] = true
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "#recycle" {
				continue
			}
			fullPath := filepath.Join(mountPoint, entry.Name())
			if !metaMap[fullPath] {
				issues.HasIssues = true
				issues.Issues = append(issues.Issues, fmt.Sprintf("目录 %s 存在但未在面板中管理", fullPath))
			}
		}
	}

	// Scan all /data mount points
	mounts := getExistingDataMounts()
	for _, m := range mounts {
		scanOrphanDirs(m["mount"])
	}

	// 3. Check: smb.conf managed section matches metadata
	smbConf, _ := common.SudoOutput("cat", "/etc/samba/smb.conf")
	expectedSamba := generateManagedBlock(metas, "samba")
	hasSambaBlock := strings.Contains(smbConf, managedStart)
	if len(metas) > 0 && !hasSambaBlock {
		issues.HasIssues = true
		issues.Issues = append(issues.Issues, "Samba 配置缺少 Z1 托管段标记")
	}
	// If no managed folders but block still exists, flag as stale
	if len(metas) == 0 && hasSambaBlock {
		issues.HasIssues = true
		issues.Issues = append(issues.Issues, "Samba 配置存在残留的 Z1 托管段标记（无托管文件夹）")
	}

	// 4. Check: exports managed section matches metadata
	exports, _ := common.SudoOutput("cat", "/etc/exports")
	expectedNFS := generateManagedBlock(metas, "nfs")
	hasNFSBlock := strings.Contains(exports, managedStart)
	if len(metas) > 0 && !hasNFSBlock {
		issues.HasIssues = true
		issues.Issues = append(issues.Issues, "NFS 配置缺少 Z1 托管段标记")
	}
	if len(metas) == 0 && hasNFSBlock {
		issues.HasIssues = true
		issues.Issues = append(issues.Issues, "NFS 配置存在残留的 Z1 托管段标记（无托管文件夹）")
	}

	// 5. Check: services have loaded the latest config
	serviceIssues := verifyServiceConfig()
	if len(serviceIssues) > 0 {
		issues.HasIssues = true
		issues.Issues = append(issues.Issues, serviceIssues...)
	}

	_ = expectedSamba
	_ = expectedNFS

	return issues
}

// ═══════════════════════════════════════════════════════════
// 辅助函数
// ═══════════════════════════════════════════════════════════

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// smbShareParams 决定单个 SMB 共享的权限模式。
// 返回 (writeMode, writeList)：
//
//	write_users 非空 → 按用户粒度：read only = yes + write list（write list 覆盖 read only）
//	write_users 为空 → 兼容旧数据：文件夹级 permission 决定 read only / writable
func smbShareParams(m FolderMeta, nasUser string) (string, string) {
	writeList := ""
	writeMode := "writable = yes"
	if strings.TrimSpace(m.WriteUsers) != "" {
		writeMode = "read only = yes"
		writeList = "   write list = " + m.WriteUsers + "\n"
	} else if m.Permission == "readonly" {
		writeMode = "read only = yes"
	}
	return writeMode, writeList
}

// getNASUser returns the NAS service account name used for force user/group.
func getNASUser() string {
	user, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if user == "" {
		user = os.Getenv("NAS_USER")
	}
	if user == "" {
		user = "root"
	}
	return user
}

// detectLANSubnet 返回主网卡的局域网网段（CIDR 形式，如 192.168.1.0/24）
// 用于 NFS 导出，替代原来硬编码的 192.168.0.0/16
func detectLANSubnet() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "192.168.0.0/16"
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			continue
		}
		network := ip4.Mask(ipnet.Mask)
		return fmt.Sprintf("%s/%d", network.String(), ones)
	}
	return "192.168.0.0/16"
}

// replaceManagedBlock replaces or inserts the managed block in config content.
// Strips any existing managed block (everything between the first START marker and
// the last END marker, plus orphaned markers), then appends the fresh block.
func replaceManagedBlock(current, managedBlock string) string {
	startIdx := strings.Index(current, managedStart)
	endIdx := strings.LastIndex(current, managedEnd)

	var body string
	switch {
	case startIdx >= 0 && endIdx >= startIdx:
		// Normal case: strip [first START .. last END] inclusive.
		body = current[:startIdx] + current[endIdx+len(managedEnd):]
	case startIdx >= 0:
		// Orphan START without END: strip from START to EOF.
		body = current[:startIdx]
	case endIdx >= 0:
		// Orphan END without START: strip the stray marker.
		body = current[:endIdx] + current[endIdx+len(managedEnd):]
	default:
		body = current
	}

	// Remove managed shares from body (they'll be regenerated in the managed block)
	metas := GetAllFolderMeta()
	for _, m := range metas {
		body = removeShareFromBody(body, m)
	}

	// Clean up extra whitespace
	body = strings.TrimRight(body, "\n")

	return body + "\n\n" + strings.TrimRight(managedBlock, "\n") + "\n"
}

// removeShareFromBody removes a managed share from the config body (non-managed area)
func removeShareFromBody(body string, m FolderMeta) string {
	lines := strings.Split(body, "\n")
	var result []string

	// For smb.conf: remove [sharename] sections
	skipSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sectionName := trimmed[1 : len(trimmed)-1]
			if sectionName == m.Name {
				skipSection = true
				continue
			}
			skipSection = false
		}
		if skipSection {
			continue
		}
		// For exports: remove lines starting with the path
		if strings.HasPrefix(trimmed, m.Path) {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// generateManagedBlock generates a preview of the managed block for comparison
func generateManagedBlock(metas []FolderMeta, configType string) string {
	// This is a simplified version for comparison
	var sb strings.Builder
	sb.WriteString(managedStart + "\n")
	for _, m := range metas {
		if configType == "samba" && m.SambaShare {
			sb.WriteString(fmt.Sprintf("[%s]\n", m.Name))
		} else if configType == "nfs" && m.NFSExport {
			sb.WriteString(fmt.Sprintf("%s ...\n", m.Path))
		}
	}
	sb.WriteString(managedEnd + "\n")
	return sb.String()
}

// ═══════════════════════════════════════════════════════════
// API Handlers
// ═══════════════════════════════════════════════════════════

// handleConfigCheck returns consistency issues
func handleConfigCheck(w http.ResponseWriter, r *http.Request) {
	issues := CheckConfigConsistency()
	common.JSONResponse(w, map[string]interface{}{
		"has_issues": issues.HasIssues,
		"issues":     issues.Issues,
	})
}

// handleConfigSync regenerates all configs from metadata
func handleConfigSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if err := SyncAllConfigs(); err != nil {
		common.JSONResponse(w, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	common.JSONResponse(w, map[string]interface{}{
		"message": "配置同步完成",
	})
}
