package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skymoore/coredns-ez/admin/store"
)

func TestNormalizeTransferAddr(t *testing.T) {
	got, err := normalizeTransferAddr("203.0.113.10")
	if err != nil || got != "203.0.113.10:53" {
		t.Fatalf("got %q %v", got, err)
	}
	got, err = normalizeTransferAddr("203.0.113.10:5300")
	if err != nil || got != "203.0.113.10:5300" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := normalizeTransferAddr("*"); err == nil {
		t.Fatal("*")
	}
	if _, err := normalizeTransferAddr("10.0.0.0/8"); err == nil {
		t.Fatal("cidr")
	}
	if _, err := normalizeTransferAddr("ns1.example.com"); err == nil {
		t.Fatal("name")
	}
}

func TestTransferHTTP(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)

	body, _ := json.Marshal(map[string]any{"to": []string{"203.0.113.10", "198.51.100.20:53"}})
	r := httptest.NewRequest(http.MethodPut, "/api/v1/transfer", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.Bytes())
	}
	var out struct {
		To []string `json:"to"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.To) != 2 || out.To[0] != "203.0.113.10:53" || out.To[1] != "198.51.100.20:53" {
		t.Fatalf("to %+v", out.To)
	}

	bad, _ := json.Marshal(map[string]any{"to": []string{"*"}})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/transfer", bytes.NewReader(bad))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("star: %d %s", w.Code, w.Body.Bytes())
	}

	added, err := a.db.AddTransferTo("203.0.113.10:53")
	if err != nil || added {
		t.Fatalf("dup add %v %v", added, err)
	}
	a.appendTransferAddr("192.0.2.8")
	got := a.db.TransferTo()
	if len(got) != 3 {
		t.Fatalf("after append %+v", got)
	}
}

func TestTransferSnapshot(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/api.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SetTransferTo([]string{"192.0.2.1:53"}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil || len(snap.TransferTo) != 1 || snap.TransferTo[0] != "192.0.2.1:53" {
		t.Fatalf("%+v %v", snap.TransferTo, err)
	}
	s2, err := store.Open(t.TempDir() + "/b.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.ApplySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if got := s2.TransferTo(); len(got) != 1 || got[0] != "192.0.2.1:53" {
		t.Fatalf("replica %+v", got)
	}
}
