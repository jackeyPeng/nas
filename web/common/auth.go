package common

import (
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
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
			return
		}

		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		_, err := VerifyToken(tokenStr)
		if err != nil {
			http.Error(w, `{"error": "invalid token"}`, http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
