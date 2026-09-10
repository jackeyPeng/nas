package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"nas-panel/common"

	_ "modernc.org/sqlite"
)

// 凭证保险箱：AES-256-GCM 加密存储敏感凭据，密钥由管理密码(NAS_PASS)经 PBKDF2 派生。
// 纯标准库实现，无第三方依赖。

var (
	vaultDB     *sql.DB
	vaultDBOnce sync.Once
)

func vaultDBPath() string {
	dir := "/opt/nas/data"
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "vault.db")
}

func initVaultDB() *sql.DB {
	vaultDBOnce.Do(func() {
		var err error
		vaultDB, err = sql.Open("sqlite", vaultDBPath())
		if err != nil {
			log.Printf("[VAULT] 打开数据库失败: %v", err)
			return
		}
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
		os.Chmod(vaultDBPath(), 0600)
	})
	return vaultDB
}

// ── 密码学（纯标准库） ─────────────────────────────

// pbkdf2SHA256 实现 PBKDF2-HMAC-SHA256。
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

func encryptAESGCM(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
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

// ── API ─────────────────────────────

type VaultItem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/vault", common.AuthMiddleware(handleVaultList))
	mux.HandleFunc("/api/vault/create", common.AuthMiddleware(handleVaultCreate))
	mux.HandleFunc("/api/vault/reveal", common.AuthMiddleware(handleVaultReveal))
	mux.HandleFunc("/api/vault/delete", common.AuthMiddleware(handleVaultDelete))
}

func handleVaultList(w http.ResponseWriter, r *http.Request) {
	db := initVaultDB()
	if db == nil {
		common.JSONResponse(w, map[string]interface{}{"items": []VaultItem{}})
		return
	}
	rows, err := db.Query("SELECT id, name, category, note, created_at FROM vault ORDER BY id")
	if err != nil {
		common.JSONResponse(w, map[string]interface{}{"items": []VaultItem{}})
		return
	}
	defer rows.Close()
	var items []VaultItem
	for rows.Next() {
		var it VaultItem
		rows.Scan(&it.ID, &it.Name, &it.Category, &it.Note, &it.CreatedAt)
		items = append(items, it)
	}
	if items == nil {
		items = []VaultItem{}
	}
	common.JSONResponse(w, map[string]interface{}{"items": items})
}

func handleVaultCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	name := r.FormValue("name")
	secret := r.FormValue("secret")
	category := r.FormValue("category")
	note := r.FormValue("note")
	if name == "" || secret == "" {
		http.Error(w, `{"error":"名称和凭据不能为空"}`, http.StatusBadRequest)
		return
	}
	db := initVaultDB()
	if db == nil {
		http.Error(w, `{"error":"数据库不可用"}`, http.StatusInternalServerError)
		return
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	key := pbkdf2SHA256(common.GetNasPass(), salt, 100000, 32)
	nonce, ct, err := encryptAESGCM(key, []byte(secret))
	if err != nil {
		http.Error(w, `{"error":"加密失败"}`, http.StatusInternalServerError)
		return
	}
	_, err = db.Exec(`INSERT INTO vault (name, category, note, salt, nonce, encrypted) VALUES (?, ?, ?, ?, ?, ?)`,
		name, category, note,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ct))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"保存失败: %v"}`, err), http.StatusInternalServerError)
		return
	}
	common.LogAudit("system", "新增凭据", "VAULT", "/api/vault/create", "name="+name, "success", "")
	common.JSONResponse(w, map[string]interface{}{"message": "凭据已保存"})
}

func handleVaultReveal(w http.ResponseWriter, r *http.Request) {
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
	key := pbkdf2SHA256(common.GetNasPass(), salt, 100000, 32)
	plain, err := decryptAESGCM(key, nonce, enc)
	if err != nil {
		http.Error(w, `{"error":"解密失败（管理密码可能已变更）"}`, http.StatusInternalServerError)
		return
	}
	common.LogAudit("system", "查看凭据", "VAULT", "/api/vault/reveal", "id="+id, "success", "")
	common.JSONResponse(w, map[string]interface{}{"secret": string(plain)})
}

func handleVaultDelete(w http.ResponseWriter, r *http.Request) {
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
	db.Exec("DELETE FROM vault WHERE id = ?", id)
	common.LogAudit("system", "删除凭据", "VAULT", "/api/vault/delete", "id="+id, "success", "")
	common.JSONResponse(w, map[string]interface{}{"message": "凭据已删除"})
}
