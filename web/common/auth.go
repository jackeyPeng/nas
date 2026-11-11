package common

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtSecret []byte
var nasPass string

// InitAuth initializes the JWT secret
func InitAuth(secret string) {
	jwtSecret = []byte(secret)
}

// SetNasPass stores the NAS password for runtime updates
func SetNasPass(pass string) {
	nasPass = pass
}

// GetNasPass returns the current NAS password
func GetNasPass() string {
	return nasPass
}

// UpdateNasPass updates the password at runtime (called after password change)
func UpdateNasPass(pass string) {
	nasPass = pass
}

// DefaultPass 出厂默认密码（首次登录后强制修改）
const DefaultPass = "Nas-Test-2026"

// IsDefaultPass 当前面板密码是否仍是出厂默认密码
func IsDefaultPass() bool {
	return subtle.ConstantTimeCompare([]byte(nasPass), []byte(DefaultPass)) == 1
}

// CreateToken creates a JWT token for a user
func CreateToken(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// VerifyToken verifies a JWT token and returns the username
func VerifyToken(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid claims")
	}

	sub, ok := claims["sub"].(string)
	if !ok {
		return "", fmt.Errorf("invalid subject")
	}

	return sub, nil
}

// AuthMiddleware checks JWT token
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !ValidTokenFromRequest(r) {
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ValidTokenFromRequest 校验请求 Authorization: Bearer <JWT> 头，返回 token 是否有效。
// 供需要在非 AuthMiddleware 包裹的 handler 内复用同一校验逻辑的场景调用
// （如 /api/system/check 这类公开路由内部的分支放行判断）。
func ValidTokenFromRequest(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	tokenStr := strings.TrimPrefix(auth, "Bearer ")
	_, err := VerifyToken(tokenStr)
	return err == nil
}

// AdminMiddleware 要求调用者登录且用户名 == NAS_USER（面板管理员）。
// 单用户架构下，面板登录账号就是管理员；其他 Linux 系统用户不是面板账号，
// 他们的 token（若将来有）也拿不到管理权限。
func AdminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !ValidTokenFromRequest(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if RequestUsername(r) != GetNASUser() {
			http.Error(w, `{"error":"forbidden: admin required"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// IsAdmin 判定当前请求是否来自面板管理员（已登录且用户名 == NAS_USER）。
// 供 handler 内部在同一路由里需要区分「管理员」与「本人」权限时复用。
func IsAdmin(r *http.Request) bool {
	if !ValidTokenFromRequest(r) {
		return false
	}
	return RequestUsername(r) == GetNASUser()
}
