package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nas-panel/common"

	_ "modernc.org/sqlite"
)

// 凭证保险箱 v2：零知识模型。
//
// 加密/解密全部在浏览器端（web/frontend/zkvault.min.js，PBKDF2-SHA256 + AES-256-GCM）。
// 服务器只存 salt + verification_hash + 密文，永不接触明文、口令或派生密钥——
// 审计/事件日志同样绝不记录这些字段。
//
// legacy 三件套（/api/vault/legacy*）是旧版服务端加密模型（密钥由 NAS_PASS 派生）
// 的迁移期兼容：仅用于把旧条目导出明文供前端用新口令重新加密，之后 purge 清空旧表。

const (
	// 旧版 legacy 表密钥派生参数（与旧版 vault.go 行为一致，不变更，保证能解密存量数据）
	legacyPBKDF2Iterations = 100000
	legacyKeyLen           = 32

	// 新版保险箱常量
	minIterations = 100000
	maxIterations = 1000000
	maxFailed     = 5
	lockDuration  = 30 * time.Minute

	maxNameLen  = 200
	maxFieldLen = 200
	maxEncLen   = 128 * 1024 // 128KB
	maxNotesLen = 200
)

var (
	saltPattern       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	verifyHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var (
	vaultDB     *sql.DB
	vaultDBOnce sync.Once
	vaultDBMu   sync.Mutex
)

// nowFunc 可在测试中替换，避免 sleep 依赖真实时间。
var nowFunc = time.Now

func vaultDBPath() string {
	dir := "/opt/nas/data"
	if d := os.Getenv("NAS_DATA_DIR"); d != "" {
		dir = d
	}
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "vault.db")
}

func initVaultDB() *sql.DB {
	vaultDBOnce.Do(func() {
		var err error
		vaultDB, err = common.OpenSQLite(vaultDBPath())
		if err != nil {
			log.Printf("[VAULT] 打开数据库失败: %v", err)
			return
		}
		// legacy 表（v1，保留作迁移兼容）
		vaultDB.Exec(`CREATE TABLE IF NOT EXISTS vault (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			category TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			salt TEXT NOT NULL,
			nonce TEXT NOT NULL,
			encrypted TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`)
		// v2 零知识模型
		vaultDB.Exec(`CREATE TABLE IF NOT EXISTS vault_meta (
			owner TEXT PRIMARY KEY,
			salt TEXT NOT NULL,
			verify_hash TEXT NOT NULL,
			iterations INTEGER NOT NULL,
			failed INTEGER NOT NULL DEFAULT 0,
			locked_until TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		)`)
		vaultDB.Exec(`CREATE TABLE IF NOT EXISTS vault_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner TEXT NOT NULL,
			name TEXT NOT NULL,
			category TEXT DEFAULT '',
			url TEXT DEFAULT '',
			username TEXT DEFAULT '',
			notes_preview TEXT DEFAULT '',
			encrypted TEXT NOT NULL,
			created_at TEXT,
			updated_at TEXT
		)`)
		vaultDB.Exec(`CREATE INDEX IF NOT EXISTS idx_vault_items_owner ON vault_items(owner)`)
		os.Chmod(vaultDBPath(), 0600)
	})
	return vaultDB
}

// ResetForTest 关闭并重置数据库单例，仅供测试在不同 t.TempDir() 间切换时使用。
func ResetForTest() {
	vaultDBMu.Lock()
	defer vaultDBMu.Unlock()
	if vaultDB != nil {
		vaultDB.Close()
	}
	vaultDB = nil
	vaultDBOnce = sync.Once{}
}

// SetNowForTest 替换时间源，仅供测试使用。
func SetNowForTest(f func() time.Time) {
	if f == nil {
		nowFunc = time.Now
		return
	}
	nowFunc = f
}

// ── legacy 密码学（纯标准库，搬自旧版 vault.go，不变更） ─────────────────────

func pbkdf2SHA256(password string, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, []byte(password))
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var dk []byte
	for i := 1; i <= numBlocks; i++ {
		prf.Reset()
		prf.Write(salt)
		prf.Write([]byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)})
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for j := 1; j < iter; j++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for k := range t {
				t[k] ^= u[k]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

func decryptAESGCM(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// ── 路由 ─────────────────────────────

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/vault/status", common.AuthMiddleware(handleVaultStatus))
	mux.HandleFunc("/api/vault/setup", common.AuthMiddleware(handleVaultSetup))
	mux.HandleFunc("/api/vault/unlock", common.AuthMiddleware(handleVaultUnlock))
	mux.HandleFunc("/api/vault/items", common.AuthMiddleware(handleVaultItems))
	mux.HandleFunc("/api/vault/create", common.AuthMiddleware(handleVaultItemCreate))
	mux.HandleFunc("/api/vault/update", common.AuthMiddleware(handleVaultItemUpdate))
	mux.HandleFunc("/api/vault/delete", common.AuthMiddleware(handleVaultItemDelete))
	mux.HandleFunc("/api/vault/change-passphrase", common.AuthMiddleware(handleVaultChangePassphrase))

	// legacy（迁移兼容）
	mux.HandleFunc("/api/vault/legacy", common.AuthMiddleware(handleLegacyList))
	mux.HandleFunc("/api/vault/legacy/reveal", common.AuthMiddleware(handleLegacyReveal))
	mux.HandleFunc("/api/vault/legacy/purge", common.AuthMiddleware(handleLegacyPurge))
}

// ── 状态 ─────────────────────────────

type vaultMeta struct {
	Salt        string
	VerifyHash  string
	Iterations  int
	Failed      int
	LockedUntil string // RFC3339，可为空
}

func getVaultMeta(db *sql.DB, owner string) (*vaultMeta, error) {
	var m vaultMeta
	var lockedUntil sql.NullString
	err := db.QueryRow(`SELECT salt, verify_hash, iterations, failed, locked_until FROM vault_meta WHERE owner = ?`, owner).
		Scan(&m.Salt, &m.VerifyHash, &m.Iterations, &m.Failed, &lockedUntil)
	if err != nil {
		return nil, err
	}
	if lockedUntil.Valid {
		m.LockedUntil = lockedUntil.String
	}
	return &m, nil
}

// isLocked 判断当前是否在锁定期内（lockedUntil 为空或已过期则不算锁定）。
func isLocked(lockedUntil string, now time.Time) bool {
	if lockedUntil == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lockedUntil)
	if err != nil {
		// fail-safe：字段损坏/无法解析时视为锁定中（审查 M3），宁可多锁不可漏放
		return true
	}
	return now.Before(t)
}

func countVaultItems(db *sql.DB, owner string) int {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM vault_items WHERE owner = ?`, owner).Scan(&n)
	return n
}

func countLegacyItems(db *sql.DB) int {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM vault`).Scan(&n)
	return n
}

func handleVaultStatus(w http.ResponseWriter, r *http.Request) {
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	resp := map[string]interface{}{
		"setup":        false,
		"iterations":   0,
		"item_count":   countVaultItems(db, owner),
		"legacy_count": countLegacyItems(db),
	}
	meta, err := getVaultMeta(db, owner)
	if err == nil {
		resp["setup"] = true
		resp["iterations"] = meta.Iterations
		// salt 本身不是秘密（公开的 PBKDF2 参数），浏览器端派生密钥必须拿到它，
		// 绝不能返回 verify_hash（那才是需要保密、仅用于比对的派生校验值）。
		resp["salt"] = meta.Salt
		if isLocked(meta.LockedUntil, nowFunc()) {
			resp["locked_until"] = meta.LockedUntil
		}
	}
	common.JSONResponse(w, resp)
}

// ── setup ─────────────────────────────

func validSetupParams(salt, verifyHash string, iterations int) error {
	if !saltPattern.MatchString(salt) {
		return fmt.Errorf("salt 格式非法（需 32 位十六进制）")
	}
	if !verifyHashPattern.MatchString(verifyHash) {
		return fmt.Errorf("verify_hash 格式非法（需 64 位十六进制）")
	}
	if iterations < minIterations || iterations > maxIterations {
		return fmt.Errorf("iterations 必须在 %d - %d 之间", minIterations, maxIterations)
	}
	return nil
}

func handleVaultSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	if _, err := getVaultMeta(db, owner); err == nil {
		http.Error(w, `{"error":"保险箱已初始化"}`, http.StatusConflict)
		return
	}

	salt := r.FormValue("salt")
	verifyHash := r.FormValue("verify_hash")
	iterations, _ := strconv.Atoi(r.FormValue("iterations"))
	if err := validSetupParams(salt, verifyHash, iterations); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	_, err := db.Exec(`INSERT INTO vault_meta (owner, salt, verify_hash, iterations, failed, locked_until) VALUES (?, ?, ?, ?, 0, NULL)`,
		owner, salt, verifyHash, iterations)
	if err != nil {
		// 并发双 setup 的 TOCTOU（审查 M7）：主键冲突归一为 409，而非 500
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, `{"error":"保险箱已初始化"}`, http.StatusConflict)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "初始化失败: "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "VAULT", "初始化保险箱", "", "success")
	common.EmitEvent("vault", common.EventSuccess, "vault.setup", map[string]interface{}{"owner": owner}, "")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// ── unlock ─────────────────────────────

func handleVaultUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	meta, err := getVaultMeta(db, owner)
	if err != nil {
		http.Error(w, `{"error":"保险箱未初始化"}`, http.StatusBadRequest)
		return
	}

	now := nowFunc()
	if isLocked(meta.LockedUntil, now) {
		t, _ := time.Parse(time.RFC3339, meta.LockedUntil)
		remaining := int(t.Sub(now).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		fmt.Fprintf(w, `{"error":"保险箱已锁定，请稍后再试","locked_remaining_seconds":%d}`, remaining)
		return
	}

	verifyHash := r.FormValue("verify_hash")
	match := subtle.ConstantTimeCompare([]byte(verifyHash), []byte(meta.VerifyHash)) == 1

	if !match {
		// 原子自增（审查 I1）：并发失败请求不再共享旧计数，防止稀释 5 次锁定阈值
		if _, err := db.Exec(`UPDATE vault_meta SET failed = failed + 1 WHERE owner = ?`, owner); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, "更新失败计数出错: "+err.Error()), http.StatusInternalServerError)
			return
		}
		var failed int
		if err := db.QueryRow(`SELECT failed FROM vault_meta WHERE owner = ?`, owner).Scan(&failed); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, "读取失败计数出错: "+err.Error()), http.StatusInternalServerError)
			return
		}
		if failed >= maxFailed {
			lockedUntil := now.Add(lockDuration).Format(time.RFC3339)
			db.Exec(`UPDATE vault_meta SET failed = 0, locked_until = ? WHERE owner = ?`, lockedUntil, owner)
			common.LogAuditRequest(r, "VAULT", "解锁失败触发锁定", "", "failed")
			common.EmitEvent("vault", common.EventWarn, "vault.locked", map[string]interface{}{"owner": owner}, "")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusLocked)
			fmt.Fprintf(w, `{"error":"连续失败次数过多，已锁定 30 分钟","locked_remaining_seconds":%d}`, int(lockDuration.Seconds()))
			return
		}
		common.LogAuditRequest(r, "VAULT", "解锁失败", "", "failed")
		attemptsLeft := maxFailed - failed
		http.Error(w, fmt.Sprintf(`{"error":"口令错误","attempts_left":%d}`, attemptsLeft), http.StatusUnauthorized)
		return
	}

	db.Exec(`UPDATE vault_meta SET failed = 0 WHERE owner = ?`, owner)
	common.LogAuditRequest(r, "VAULT", "解锁保险箱", "", "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// ── items CRUD（owner 行级隔离） ─────────────────────────────

type VaultItemV2 struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	URL          string `json:"url"`
	Username     string `json:"username"`
	NotesPreview string `json:"notes_preview"`
	Encrypted    string `json:"encrypted"`
	UpdatedAt    string `json:"updated_at"`
}

func handleVaultItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		common.JSONResponse(w, map[string]interface{}{"items": []VaultItemV2{}})
		return
	}
	rows, err := db.Query(`SELECT id, name, category, url, username, notes_preview, encrypted, updated_at
		FROM vault_items WHERE owner = ? ORDER BY id DESC`, owner)
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"items": []VaultItemV2{}})
		return
	}
	defer rows.Close()
	items := []VaultItemV2{}
	for rows.Next() {
		var it VaultItemV2
		var updatedAt sql.NullString
		if err := rows.Scan(&it.ID, &it.Name, &it.Category, &it.URL, &it.Username, &it.NotesPreview, &it.Encrypted, &updatedAt); err == nil {
			it.UpdatedAt = updatedAt.String
			items = append(items, it)
		}
	}
	common.JSONResponse(w, map[string]interface{}{"items": items})
}

// validItemFields 校验新增/更新条目的通用字段长度约束。
func validItemFields(name, category, url, username, notesPreview, encrypted string) error {
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("名称不能为空且不超过 %d 字符", maxNameLen)
	}
	if encrypted == "" || len(encrypted) > maxEncLen {
		return fmt.Errorf("加密内容不能为空且不超过 %d 字节", maxEncLen)
	}
	if len(category) > maxFieldLen || len(url) > maxFieldLen || len(username) > maxFieldLen {
		return fmt.Errorf("字段长度超出限制（最长 %d 字符）", maxFieldLen)
	}
	if len(notesPreview) > maxNotesLen {
		return fmt.Errorf("备注摘要不超过 %d 字符", maxNotesLen)
	}
	return nil
}

func handleVaultItemCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	if _, err := getVaultMeta(db, owner); err != nil {
		http.Error(w, `{"error":"保险箱未初始化"}`, http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	category := r.FormValue("category")
	url := r.FormValue("url")
	username := r.FormValue("username")
	notesPreview := r.FormValue("notes_preview")
	encrypted := r.FormValue("encrypted")
	if err := validItemFields(name, category, url, username, notesPreview, encrypted); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	now := nowFunc().Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO vault_items (owner, name, category, url, username, notes_preview, encrypted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		owner, name, category, url, username, notesPreview, encrypted, now, now)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "保存失败: "+err.Error()), http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "VAULT", "新增条目", "name="+name, "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

func handleVaultItemUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, `{"error":"id 非法"}`, http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	category := r.FormValue("category")
	url := r.FormValue("url")
	username := r.FormValue("username")
	notesPreview := r.FormValue("notes_preview")
	encrypted := r.FormValue("encrypted")
	if err := validItemFields(name, category, url, username, notesPreview, encrypted); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	now := nowFunc().Format(time.RFC3339)
	res, err := db.Exec(`UPDATE vault_items SET name=?, category=?, url=?, username=?, notes_preview=?, encrypted=?, updated_at=?
		WHERE id = ? AND owner = ?`,
		name, category, url, username, notesPreview, encrypted, now, id, owner)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "更新失败: "+err.Error()), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, `{"error":"条目不存在"}`, http.StatusNotFound)
		return
	}
	common.LogAuditRequest(r, "VAULT", "更新条目", fmt.Sprintf("id=%d", id), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

func handleVaultItemDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, `{"error":"id 非法"}`, http.StatusBadRequest)
		return
	}
	res, err := db.Exec(`DELETE FROM vault_items WHERE id = ? AND owner = ?`, id, owner)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "删除失败: "+err.Error()), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, `{"error":"条目不存在"}`, http.StatusNotFound)
		return
	}
	common.LogAuditRequest(r, "VAULT", "删除条目", fmt.Sprintf("id=%d", id), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// ── change-passphrase（事务性：meta + 全部条目一起更新，任一失败整体回滚） ──

type changePassItem struct {
	ID        int    `json:"id"`
	Encrypted string `json:"encrypted"`
}

// parseChangePassItems 解析 items JSON 数组字符串 [{"id":1,"encrypted":"..."}]。
func parseChangePassItems(raw string) ([]changePassItem, error) {
	if raw == "" {
		return nil, nil
	}
	var items []changePassItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func handleVaultChangePassphrase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	if _, err := getVaultMeta(db, owner); err != nil {
		http.Error(w, `{"error":"保险箱未初始化"}`, http.StatusBadRequest)
		return
	}

	salt := r.FormValue("salt")
	verifyHash := r.FormValue("verify_hash")
	iterations, _ := strconv.Atoi(r.FormValue("iterations"))
	if err := validSetupParams(salt, verifyHash, iterations); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	items, err := parseChangePassItems(r.FormValue("items"))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "items 格式非法: "+err.Error()), http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, `{"error":"开启事务失败"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// 覆盖校验（审查 I4）：提交条目必须恰好覆盖 owner 全部条目，
	// 防止旧标签页用过期缓存重加密导致其它设备新增的条目永久无法解密
	var total int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM vault_items WHERE owner = ?`, owner).Scan(&total); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "统计条目失败: "+err.Error()), http.StatusInternalServerError)
		return
	}
	if len(items) != total {
		http.Error(w, fmt.Sprintf(`{"error":"条目数不一致（提交 %d / 服务器 %d），请刷新后重试"}`, len(items), total), http.StatusBadRequest)
		return
	}

	for _, it := range items {
		if len(it.Encrypted) == 0 || len(it.Encrypted) > maxEncLen {
			http.Error(w, `{"error":"加密内容非法"}`, http.StatusBadRequest)
			return
		}
		res, err := tx.Exec(`UPDATE vault_items SET encrypted = ? WHERE id = ? AND owner = ?`, it.Encrypted, it.ID, owner)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, "更新条目失败: "+err.Error()), http.StatusInternalServerError)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// 条目不属于 owner（或不存在）→ 整体回滚，不改口令
			http.Error(w, fmt.Sprintf(`{"error":"条目 %d 不属于当前用户，操作已取消"}`, it.ID), http.StatusForbidden)
			return
		}
	}

	if _, err := tx.Exec(`UPDATE vault_meta SET salt = ?, verify_hash = ?, iterations = ?, failed = 0, locked_until = NULL WHERE owner = ?`,
		salt, verifyHash, iterations, owner); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, "更新口令失败: "+err.Error()), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, `{"error":"提交事务失败"}`, http.StatusInternalServerError)
		return
	}
	common.LogAuditRequest(r, "VAULT", "修改保险箱口令", fmt.Sprintf("items=%d", len(items)), "success")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}

// ── legacy（迁移兼容） ─────────────────────────────

type LegacyVaultItem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

func handleLegacyList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	db := initVaultDB()
	if db == nil {
		common.JSONResponse(w, map[string]interface{}{"items": []LegacyVaultItem{}})
		return
	}
	rows, err := db.Query("SELECT id, name, category, note, created_at FROM vault ORDER BY id")
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"items": []LegacyVaultItem{}})
		return
	}
	defer rows.Close()
	items := []LegacyVaultItem{}
	for rows.Next() {
		var it LegacyVaultItem
		if rows.Scan(&it.ID, &it.Name, &it.Category, &it.Note, &it.CreatedAt) == nil {
			items = append(items, it)
		}
	}
	common.JSONResponse(w, map[string]interface{}{"items": items})
}

// handleLegacyReveal 服务端解密旧版条目回明文——仅此一处，属迁移期兼容。
// 前端拿到明文后立即用新保险箱密钥重新加密为新条目，不在服务端落地新密文。
func handleLegacyReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	id := r.FormValue("id")
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	var saltB64, nonceB64, encB64 string
	err := db.QueryRow("SELECT salt, nonce, encrypted FROM vault WHERE id = ?", id).Scan(&saltB64, &nonceB64, &encB64)
	if err != nil {
		http.Error(w, `{"error":"凭据不存在"}`, http.StatusNotFound)
		return
	}
	salt, _ := base64.StdEncoding.DecodeString(saltB64)
	nonce, _ := base64.StdEncoding.DecodeString(nonceB64)
	enc, _ := base64.StdEncoding.DecodeString(encB64)
	key := pbkdf2SHA256(common.GetNasPass(), salt, legacyPBKDF2Iterations, legacyKeyLen)
	plain, err := decryptAESGCM(key, nonce, enc)
	if err != nil {
		http.Error(w, `{"error":"解密失败（管理密码可能已变更）"}`, http.StatusInternalServerError)
		return
	}
	// 迁移专用接口：不记录明文，只记录发生了一次迁移期查看
	common.LogAuditRequest(r, "VAULT", "迁移查看旧版凭据", "id="+id, "success")
	common.JSONResponse(w, map[string]interface{}{"secret": string(plain)})
}

func handleLegacyPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	owner := common.RequestUsername(r)
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	if _, err := getVaultMeta(db, owner); err != nil {
		http.Error(w, `{"error":"新保险箱尚未初始化，不能清空旧版数据"}`, http.StatusBadRequest)
		return
	}
	db.Exec(`DELETE FROM vault`)
	common.LogAuditRequest(r, "VAULT", "清空旧版保险箱", "", "success")
	common.EmitEvent("vault", common.EventWarn, "vault.purged", map[string]interface{}{"owner": owner}, "")
	common.JSONResponse(w, map[string]interface{}{"ok": true})
}
