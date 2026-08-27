package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestReplaceApexNSRemovesOriginals(t *testing.T) {
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

	body, _ := json.Marshal(map[string]any{
		"name": "@",
		"type": "NS",
		"acl":  "",
		"records": []recordJSON{
			{Name: "@", Type: "NS", TTL: 300, Rdata: "ns1.dns.example.com."},
			{Name: "@", Type: "NS", TTL: 300, Rdata: "ns3.dns.example.com."},
		},
	})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/zones/example.com./records", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("replace apex NS: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records?name=@&type=NS", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list NS: %d %s", w.Code, w.Body.Bytes())
	}
	var out struct {
		Records []recordJSON `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 2 {
		t.Fatalf("apex NS count = %d %s, want 2", len(out.Records), w.Body.Bytes())
	}
	got := map[string]bool{}
	for _, rec := range out.Records {
		got[strings.ToLower(rec.Rdata)] = true
	}
	if !got["ns1.dns.example.com."] || !got["ns3.dns.example.com."] {
		t.Fatalf("replaced NS missing: %s", w.Body.Bytes())
	}
	if got["ns1.example.com."] {
		t.Fatalf("original NS still present: %s", w.Body.Bytes())
	}

	empty, _ := json.Marshal(map[string]any{
		"name": "@", "type": "NS", "acl": "", "records": []recordJSON{},
	})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/zones/example.com./records", bytes.NewReader(empty))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty apex NS: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestReplaceApexSOAUpdatesMname(t *testing.T) {
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

	body, _ := json.Marshal(map[string]any{
		"name": "@",
		"type": "SOA",
		"acl":  "",
		"records": []recordJSON{
			{Name: "@", Type: "SOA", TTL: 300, Rdata: "ns1.rwx.dev. hostmaster.example.com. 1 7200 600 86400 60"},
		},
	})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/zones/example.com./records", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("replace SOA: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records?name=@&type=SOA", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list SOA: %d %s", w.Code, w.Body.Bytes())
	}
	var out struct {
		Records []recordJSON `json:"records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 1 {
		t.Fatalf("SOA count = %d %s, want 1", len(out.Records), w.Body.Bytes())
	}
	if !strings.Contains(strings.ToLower(out.Records[0].Rdata), "ns1.rwx.dev.") {
		t.Fatalf("MNAME not updated: %s", w.Body.Bytes())
	}
	if strings.Contains(strings.ToLower(out.Records[0].Rdata), "ns1.example.com.") {
		t.Fatalf("original MNAME still present: %s", w.Body.Bytes())
	}

	empty, _ := json.Marshal(map[string]any{
		"name": "@", "type": "SOA", "acl": "", "records": []recordJSON{},
	})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/zones/example.com./records", bytes.NewReader(empty))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty SOA: %d %s", w.Code, w.Body.Bytes())
	}
}
