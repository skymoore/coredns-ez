package admin

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// maxJSONBody bounds decoded request bodies. Sized for bulk record/import
// payloads (tens of thousands of records) without letting a client grow the
// heap without limit.
const maxJSONBody = 4 << 20

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// internalError logs the full error with request-id correlation and writes a
// constant copy, so gorm/sqlite detail never reaches callers.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	rid, _ := r.Context().Value(middleware.RequestIDKey).(string)
	log.Errorf("%s %s rid=%s: %v", r.Method, r.URL.Path, rid, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody+1))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
