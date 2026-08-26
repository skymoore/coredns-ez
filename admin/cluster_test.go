package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

type snapPrimary struct{ origin, path string }

func (s snapPrimary) Origin() string            { return s.origin }
func (s snapPrimary) Source() string            { return zonereg.SourceCorefile }
func (s snapPrimary) Records() []dns.RR         { return nil }
func (s snapPrimary) Apply(_, _ []dns.RR) error { return nil }
func (s snapPrimary) Path() string              { return s.path }

func TestPublicAPIURLSkipsLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/connect", nil)
	r.Host = "127.0.0.1:8080"
	if u := publicAPIURL(r, "", "192.168.8.54:53"); u != "http://192.168.8.54:8080" {
		t.Fatalf("got %q", u)
	}
	r.Host = "192.168.8.54:8080"
	if u := publicAPIURL(r, "", ""); u != "http://192.168.8.54:8080" {
		t.Fatalf("host %q", u)
	}
	if u := publicAPIURL(r, "http://127.0.0.1:8080", "192.168.8.54:53"); u != "http://192.168.8.54:8080" {
		t.Fatalf("explicit loopback %q", u)
	}
}

func TestFullSnapshotIncludesZonereg(t *testing.T) {
	a := testAdmin(t)
	if err := zonereg.RegisterPrimary(snapPrimary{origin: "corefile.example.", path: "/etc/coredns/db.corefile.example"}); err != nil {
		t.Fatal(err)
	}
	snap, err := a.fullSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Password == nil || !*snap.Password {
		t.Fatal("password flag")
	}
	found := false
	for _, z := range snap.Zones {
		if z.Origin == "corefile.example." {
			found = true
		}
	}
	if !found {
		t.Fatalf("zones %+v", snap.Zones)
	}
}

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
	secID := ""
	for _, m := range got.Members {
		if m.Role == store.MemberSecondary {
			secID = m.ID
			if m.Name != "ns2" {
				t.Fatalf("join name %q", m.Name)
			}
		}
	}
	patch, _ := json.Marshal(map[string]string{"name": "ns3.dns.rwx.dev"})
	pr := httptest.NewRequest(http.MethodPatch, "/api/v1/cluster/members/"+secID, bytes.NewReader(patch))
	pr.Header.Set("Authorization", "Bearer "+token)
	pr.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, pr)
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.Bytes())
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range got.Members {
		if m.ID == secID && m.Name == "ns3.dns.rwx.dev" {
			found = true
		}
	}
	if !found {
		t.Fatalf("renamed roster %+v", got.Members)
	}

	if err := a.db.SetMeta(store.MetaNodeName, "ns1.dns.rwx.dev"); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Members {
		if m.Role == store.MemberPrimary && m.Name != "ns1.dns.rwx.dev" {
			t.Fatalf("primary name %q", m.Name)
		}
	}
	var sawPrimary, sawSecondary bool
	for _, m := range got.Members {
		if m.Role == store.MemberPrimary {
			sawPrimary = true
			if m.Self != true {
				t.Fatalf("primary should be self: %+v", m)
			}
		}
		if m.Role == store.MemberSecondary && m.Name == "ns3.dns.rwx.dev" {
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
