package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/skymoore/coredns-plugins/api/store"
	dnsupdatepersist "github.com/skymoore/coredns-plugins/dns-update-persistent"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
)

func testAPI(t *testing.T) *API {
	t.Helper()
	t.Cleanup(zonereg.ResetForTest)
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	hash, err := hashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(store.User{Username: "admin", PasswordHash: hash, Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	a := &API{
		cfg:       coreConfig{Role: rolePrimary, Data: dir, DB: filepath.Join(dir, "api.sqlite")},
		db:        st,
		primaries: map[string]*dnsupdatepersist.UpdatePersist{},
		stop:      make(chan struct{}),
	}
	a.mux = a.routes()
	t.Cleanup(func() { close(a.stop) })
	return a
}

func TestDenyByDefaultAndLogin(t *testing.T) {
	a := testAPI(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("zones without auth: %d", w.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("health: %d", w.Code)
	}

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.Bytes())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("token: %v %s", err, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	r.Header.Set("Authorization", "Bearer "+out.Token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("zones with jwt: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/dns-query", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK && bytes.Contains(w.Body.Bytes(), []byte("coredns-api")) {
		t.Fatal("mux must not claim /dns-query")
	}
}

func TestCreateZoneAndRecord(t *testing.T) {
	a := testAPI(t)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)

	zbody, _ := json.Marshal(map[string]string{"origin": "example.com.", "type": "primary"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}

	rec, _ := json.Marshal(recordJSON{Name: "www.example.com.", Type: "A", TTL: 60, Rdata: "192.0.2.10"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(rec))
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("add record: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records?name=www.example.com.&type=A", nil)
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("192.0.2.10")) {
		t.Fatalf("list records: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestLastAdminDeleteRejected(t *testing.T) {
	a := testAPI(t)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)
	u, err := a.db.GetUserByName("admin")
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+u.ID, nil)
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete last admin: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestAPITokenAuth(t *testing.T) {
	a := testAPI(t)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)

	tbody, _ := json.Marshal(map[string]string{"name": "ci", "role": "viewer"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/tokens", bytes.NewReader(tbody))
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", w.Code, w.Body.Bytes())
	}
	var created struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Secret == "" {
		t.Fatal("missing secret")
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	r.Header.Set("Authorization", "Bearer "+created.Secret)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("token auth: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestMuxDoesNotRegisterDNSQuery(t *testing.T) {
	a := testAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/dns-query", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("unexpected 200 on /dns-query: %s", w.Body.Bytes())
	}
}

func TestClusterConnectRejectedOnPrimary(t *testing.T) {
	a := testAPI(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/connect", bytes.NewReader([]byte(`{"url":"http://x","token":"t"}`)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.Bytes())
	}
}

func TestInstallHTTPHandlerMissingField(t *testing.T) {
	if installHTTPHandler(nil, http.NotFoundHandler()) {
		t.Fatal("nil config must not install")
	}
}
