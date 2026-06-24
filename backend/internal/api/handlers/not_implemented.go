package handlers

import (
	"net/http"
)

// NotImplemented is a placeholder handler for routes not yet built.
func NotImplemented(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
