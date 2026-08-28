package admin

import (
	"encoding/json"
	"io"
	"net/http"
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

func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody+1))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
