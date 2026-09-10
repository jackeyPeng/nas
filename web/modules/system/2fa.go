package system

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"nas-panel/common"
)

// twoFA 是面板两步验证的状态文件结构。
// enabled=false 且 secret 非空 = 已生成密钥、待验证码确认（pending）。
type twoFA struct {
	Secret  string `json:"secret"`
	Enabled bool   `json:"enabled"`
}

func twoFAPath() string {
	dir := "/opt/nas/data"
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "2fa.json")
}

func load2FA() (twoFA, bool) {
	var s twoFA
	data, err := os.ReadFile(twoFAPath())
	if err != nil {
		return s, false
	}
	if json.Unmarshal(data, &s) != nil {
		return s, false
	}
	return s, true
}

func save2FA(s twoFA) error {
	data, _ := json.Marshal(s)
	return os.WriteFile(twoFAPath(), data, 0600)
}

// TOTPEnabled 报告两步验证是否已启用（供登录流程调用）。
func TOTPEnabled() bool {
	s, ok := load2FA()
	return ok && s.Enabled && s.Secret != ""
}

// VerifyLoginTOTP 校验登录时的 TOTP 验证码。
func VerifyLoginTOTP(code string) bool {
	s, ok := load2FA()
	if !ok || !s.Enabled || s.Secret == "" {
		return false
	}
	return common.VerifyTOTP(s.Secret, code)
}

// ── API handlers ──────────────────────────────────────────

func handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	s, ok := load2FA()
	common.JSONResponse(w, map[string]interface{}{
		"enabled": ok && s.Enabled,
		"pending": ok && !s.Enabled && s.Secret != "",
	})
}

func handle2FASetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	secret, err := common.GenerateTOTPSecret()
	if err != nil {
		http.Error(w, `{"error":"密钥生成失败"}`, http.StatusInternalServerError)
		return
	}
	if err := save2FA(twoFA{Secret: secret, Enabled: false}); err != nil {
		http.Error(w, `{"error":"状态保存失败"}`, http.StatusInternalServerError)
		return
	}
	nasUser, _ := common.ReadEnvFile(common.GetEnvFilePath(), "NAS_USER")
	if nasUser == "" {
		nasUser = "admin"
	}
	otpauthURL := "otpauth://totp/Z1-NAS:" + nasUser + "?secret=" + secret + "&issuer=Z1-NAS"
	common.JSONResponse(w, map[string]interface{}{
		"secret":      secret,
		"otpauth_url": otpauthURL,
	})
}

func handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	s, ok := load2FA()
	if !ok || s.Secret == "" {
		http.Error(w, `{"error":"请先初始化两步验证"}`, http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	if !common.VerifyTOTP(s.Secret, code) {
		http.Error(w, `{"error":"验证码错误"}`, http.StatusBadRequest)
		return
	}
	s.Enabled = true
	if err := save2FA(s); err != nil {
		http.Error(w, `{"error":"状态保存失败"}`, http.StatusInternalServerError)
		return
	}
	common.LogAudit("system", "启用两步验证", "SYSTEM", "/api/2fa/enable", "2FA enabled", "success", "")
	common.JSONResponse(w, map[string]interface{}{"enabled": true})
}

func handle2FADisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	s, ok := load2FA()
	if !ok || !s.Enabled {
		http.Error(w, `{"error":"两步验证未启用"}`, http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	if !common.VerifyTOTP(s.Secret, code) {
		http.Error(w, `{"error":"验证码错误"}`, http.StatusBadRequest)
		return
	}
	os.Remove(twoFAPath())
	common.LogAudit("system", "禁用两步验证", "SYSTEM", "/api/2fa/disable", "2FA disabled", "success", "")
	common.JSONResponse(w, map[string]interface{}{"enabled": false})
}
