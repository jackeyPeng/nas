package common

import (
	"net/http"
)

// Module interface that each feature module implements
type Module interface {
	RegisterRoutes(mux *http.ServeMux)
}
