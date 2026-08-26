package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQualifyName(t *testing.T) {
	origin := "example.com."
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "example.com.", false},
		{"@", "example.com.", false},
		{"www", "www.example.com.", false},
		{"WWW", "www.example.com.", false},
		{"www.example.com", "www.example.com.", false},
		{"www.example.com.", "www.example.com.", false},
		{"example.com", "example.com.", false},
		{"example.com.", "example.com.", false},
		{"_sip._tcp", "_sip._tcp.example.com.", false},
		{"*", "*.example.com.", false},
		{"other.com.", "", true},
		{"mail.other.com.", "", true},
	}
	for _, tc := range cases {
		got, err := qualifyName(tc.in, origin)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("qualifyName(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("qualifyName(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("qualifyName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRelativeRecordAndPatch(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	zbody, _ := json.Marshal(map[string]string{"origin": "example.com.", "type": "primary"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}

	rec, _ := json.Marshal(recordJSON{Name: "www", Type: "A", TTL: 60, Rdata: "192.0.2.10"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(rec))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("add relative record: %d %s", w.Code, w.Body.Bytes())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("www.example.com.")) {
		t.Fatalf("add relative record did not expand name: %s", w.Body.Bytes())
	}

	apex, _ := json.Marshal(recordJSON{Name: "@", Type: "A", TTL: 60, Rdata: "192.0.2.1"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(apex))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("add apex @: %d %s", w.Code, w.Body.Bytes())
	}

	outside, _ := json.Marshal(recordJSON{Name: "evil.com.", Type: "A", TTL: 60, Rdata: "192.0.2.9"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(outside))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("outside-zone name: %d %s", w.Code, w.Body.Bytes())
	}

	patch, _ := json.Marshal(map[string]recordJSON{
		"old": {Name: "www", Type: "A", TTL: 60, Rdata: "192.0.2.10"},
		"new": {Name: "www", Type: "A", TTL: 120, Rdata: "192.0.2.11"},
	})
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/zones/example.com./records", bytes.NewReader(patch))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("patch record: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records?name=www&type=A", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.Bytes())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("192.0.2.11")) {
		t.Fatalf("patched rdata missing: %s", w.Body.Bytes())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("192.0.2.10")) {
		t.Fatalf("old rdata still present: %s", w.Body.Bytes())
	}
}
