package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestQuoteTXT(t *testing.T) {
	if quoteTXT(`abc`) != `"abc"` {
		t.Fatalf("quote: %q", quoteTXT("abc"))
	}
	if unquoteTXT(`"abc"`) != "abc" {
		t.Fatalf("unquote quoted")
	}
	if unquoteTXT("abc") != "abc" {
		t.Fatalf("unquote bare")
	}
	if quoteTXT(`"abc"`) != `"abc"` {
		t.Fatalf("quote already-quoted: %q", quoteTXT(`"abc"`))
	}
}

func TestMatchZone(t *testing.T) {
	zones := []string{"rwx.dev.", "k8s.rwx.dev."}
	got, err := matchZone("_acme-challenge.app.k8s.rwx.dev.", "", zones)
	if err != nil || got != "k8s.rwx.dev." {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = matchZone("_acme-challenge.rwx.dev.", "", zones)
	if err != nil || got != "rwx.dev." {
		t.Fatalf("apex got %q err %v", got, err)
	}
	got, err = matchZone("x.example.com.", "example.com", zones)
	if err != nil || got != "example.com." {
		t.Fatalf("configured got %q err %v", got, err)
	}
	if _, err := matchZone("other.org.", "", zones); err == nil {
		t.Fatal("expected no match")
	}
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(jsonRaw(`{"serverUrl":"http://192.168.8.53:8080","tokenSecretRef":{"name":"coredns-api-token","key":"token"},"ttl":60}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "http://192.168.8.53:8080" || cfg.secret().Name != "coredns-api-token" || cfg.ttl() != 60 {
		t.Fatalf("%+v", cfg)
	}
	cfg, err = loadConfig(jsonRaw(`{"serverUrl":"http://ns","authTokenSecretRef":{"name":"t","key":"api-token"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.secret().Key != "api-token" || cfg.ttl() != 60 {
		t.Fatalf("alias %+v", cfg)
	}
	if _, err := loadConfig(jsonRaw(`{}`)); err == nil {
		t.Fatal("expected error")
	}
}

func jsonRaw(s string) *extapi.JSON {
	return &extapi.JSON{Raw: []byte(s)}
}

type ezMem struct {
	mu      sync.Mutex
	zones   []string
	records map[string][]recordJSON
	posts   int
	deletes int
}

func newEZMem(zones ...string) *ezMem {
	return &ezMem{zones: zones, records: map[string][]recordJSON{}}
}

func (m *ezMem) stats() (posts, deletes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.posts, m.deletes
}

func (m *ezMem) handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v1/zones", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var zs []map[string]string
		for _, z := range m.zones {
			zs = append(zs, map[string]string{"origin": z})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"zones": zs})
	})
	mux.HandleFunc("/api/v1/zones/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/zones/")
		origin, _, _ := strings.Cut(path, "/records")
		origin = canonicalOrigin(origin)
		switch r.Method {
		case http.MethodGet:
			name := r.URL.Query().Get("name")
			m.mu.Lock()
			defer m.mu.Unlock()
			var out []recordJSON
			for _, rec := range m.records[origin] {
				if name != "" && canonicalOrigin(rec.Name) != canonicalOrigin(name) {
					continue
				}
				out = append(out, rec)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"records": out})
		case http.MethodPost:
			var rec recordJSON
			_ = json.NewDecoder(r.Body).Decode(&rec)
			m.mu.Lock()
			m.records[origin] = append(m.records[origin], rec)
			m.posts++
			m.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(rec)
		case http.MethodDelete:
			b, _ := io.ReadAll(r.Body)
			var rec recordJSON
			_ = json.Unmarshal(b, &rec)
			m.mu.Lock()
			defer m.mu.Unlock()
			keep := m.records[origin][:0]
			for _, have := range m.records[origin] {
				if canonicalOrigin(have.Name) == canonicalOrigin(rec.Name) && unquoteTXT(have.Rdata) == unquoteTXT(rec.Rdata) {
					m.deletes++
					continue
				}
				keep = append(keep, have)
			}
			m.records[origin] = keep
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func TestClientPresentCleanUp(t *testing.T) {
	mem := newEZMem("rwx.dev.")
	srv := httptest.NewServer(mem.handler())
	t.Cleanup(srv.Close)
	cli := newClient(srv.URL, "secret-token")
	ctx := context.Background()
	fqdn := "_acme-challenge.test.rwx.dev."
	if err := cli.PutTXT(ctx, "rwx.dev.", fqdn, "tok1", 60); err != nil {
		t.Fatal(err)
	}
	if err := cli.PutTXT(ctx, "rwx.dev.", fqdn, "tok1", 60); err != nil {
		t.Fatal(err)
	}
	posts, _ := mem.stats()
	if posts != 1 {
		t.Fatalf("idempotent present: posts=%d", posts)
	}
	ok, err := cli.HasTXT(ctx, "rwx.dev.", fqdn, "tok1")
	if err != nil || !ok {
		t.Fatalf("has %v %v", ok, err)
	}
	if err := cli.DeleteTXT(ctx, "rwx.dev.", fqdn, "tok1"); err != nil {
		t.Fatal(err)
	}
	ok, err = cli.HasTXT(ctx, "rwx.dev.", fqdn, "tok1")
	if err != nil || ok {
		t.Fatalf("after delete has %v %v", ok, err)
	}
}

func TestSolverPresentCleanUp(t *testing.T) {
	mem := newEZMem("rwx.dev.")
	srv := httptest.NewServer(mem.handler())
	t.Cleanup(srv.Close)
	s := &solver{
		dial: func(base, token string) *ezClient {
			if token != "secret-token" {
				t.Fatalf("token %q", token)
			}
			return newClient(base, token)
		},
		tokenFn: func(context.Context, solverConfig, string) (string, error) {
			return "secret-token", nil
		},
	}
	ch := challenge(srv.URL, "_acme-challenge.test.rwx.dev.", "", "abc")
	if err := s.Present(ch); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanUp(ch); err != nil {
		t.Fatal(err)
	}
	posts, deletes := mem.stats()
	if posts != 1 || deletes != 1 {
		t.Fatalf("posts=%d deletes=%d", posts, deletes)
	}
}

func challenge(serverURL, fqdn, zone, key string) *v1alpha1.ChallengeRequest {
	cfg := jsonRaw(`{"serverUrl":"` + serverURL + `","tokenSecretRef":{"name":"coredns-api-token","key":"token"}}`)
	return &v1alpha1.ChallengeRequest{
		ResourceNamespace: "cert-manager",
		ResolvedFQDN:      fqdn,
		ResolvedZone:      zone,
		Key:               key,
		Config:            cfg,
	}
}
