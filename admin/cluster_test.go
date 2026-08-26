package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skymoore/coredns-ez/admin/store"
)

func TestClusterRosterIncludesPrimary(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Host = "ns1.example:8443"
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get cluster: %d %s", w.Code, w.Body.Bytes())
	}
	var got struct {
		Members []clusterMemberJSON `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 || got.Members[0].Role != store.MemberPrimary || !got.Members[0].Self {
		t.Fatalf("want this primary in roster, got %+v", got.Members)
	}
	if got.Members[0].APIURL != "http://ns1.example:8443" {
		t.Fatalf("primary api_url %q", got.Members[0].APIURL)
	}

	mint := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/join-tokens", bytes.NewReader([]byte(`{"ttl":"1h"}`)))
	mint.Header.Set("Authorization", "Bearer "+token)
	mint.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, mint)
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("join token: %d %s", w.Code, w.Body.Bytes())
	}

	joinBody, _ := json.Marshal(map[string]string{
		"token": tok.Token, "name": "ns2", "api_url": "http://ns2.example:8443", "dns_addr": "192.0.2.20:53",
	})
	join := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/join", bytes.NewReader(joinBody))
	join.Header.Set("Content-Type", "application/json")
	join.Host = "ns1.example:8443"
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, join)
	if w.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", w.Code, w.Body.Bytes())
	}
	var joined struct {
		MemberID string         `json:"member_id"`
		Snapshot store.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &joined); err != nil {
		t.Fatal(err)
	}
	if joined.MemberID == "" || len(joined.Snapshot.Members) != 2 {
		t.Fatalf("join snapshot roster: %+v", joined)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("roster after join: %+v", got.Members)
	}
	var sawPrimary, sawSecondary bool
	for _, m := range got.Members {
		if m.Role == store.MemberPrimary {
			sawPrimary = true
			if m.Self != true {
				t.Fatalf("primary should be self: %+v", m)
			}
		}
		if m.Role == store.MemberSecondary && m.Name == "ns2" {
			sawSecondary = true
		}
	}
	if !sawPrimary || !sawSecondary {
		t.Fatalf("missing roles in %+v", got.Members)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/cluster/members/"+got.Members[0].ID, nil)
	del.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, del)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete primary: %d %s", w.Code, w.Body.Bytes())
	}
}
