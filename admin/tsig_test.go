package admin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/admin/store"
)

func TestTSIGKeyCRUD(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)

	body, _ := json.Marshal(map[string]string{"name": "updater.example.com", "algorithm": "hmac-sha256"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tsig-keys", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.Bytes())
	}
	var created store.TSIGKey
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "updater.example.com." || created.Algorithm != "hmac-sha256" || created.Secret == "" {
		t.Fatalf("created %+v", created)
	}
	if a.tsig.Snapshot()[created.Name] != created.Secret {
		t.Fatal("hub missing key")
	}

	dup := httptest.NewRequest(http.MethodPost, "/api/v1/tsig-keys", bytes.NewReader(body))
	dup.Header.Set("Authorization", "Bearer "+tok)
	dup.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, dup)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dup: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/tsig-keys", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(created.Name)) {
		t.Fatalf("list: %d %s", w.Code, w.Body.Bytes())
	}

	msg := []byte("tsig-mac-input")
	ts := &dns.TSIG{Hdr: dns.RR_Header{Name: created.Name}, Algorithm: dns.HmacSHA256}
	mac, err := a.tsig.Generate(msg, ts)
	if err != nil || len(mac) == 0 {
		t.Fatalf("generate: %v", err)
	}
	ts.MAC = hex.EncodeToString(mac)
	if err := a.tsig.Verify(msg, ts); err != nil {
		t.Fatalf("verify: %v", err)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/tsig-keys/"+created.ID, nil)
	del.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, del)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.Bytes())
	}
	if _, ok := a.tsig.Snapshot()[created.Name]; ok {
		t.Fatal("hub still has deleted key")
	}
}
