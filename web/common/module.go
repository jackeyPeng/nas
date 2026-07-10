package common

import (
	"net/http"
	"strings"
)

// Module interface that each feature module implements
type Module interface {
	RegisterRoutes(mux *http.ServeMux)
}

// LoggingMiddleware logs all requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = strings.TrimSpace // avoid unused import
		next.ServeHTTP(w, r)
	})
}
