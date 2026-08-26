package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/skymoore/coredns-ez/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-ez/dns-update-persistent"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

func testAdmin(t *testing.T) *Admin {
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
	a := &Admin{
		cfg:              coreConfig{Role: rolePrimary, Data: dir, DB: filepath.Join(dir, "api.sqlite"), Password: true},
		db:               st,
		primaries:        map[string]*dnsupdatepersist.UpdatePersist{},
		views:            map[string]map[string]*dnsupdatepersist.UpdatePersist{},
		stop:             make(chan struct{}),
		tsig:             newTSIGHub(),
		filters:          newFilterEngine(),
		filterAllowLocal: true,
		xferHub:          newXferHub(),
	}
	a.mux = a.routes()
	t.Cleanup(func() { close(a.stop) })
	return a
}

func TestDenyByDefaultAndLogin(t *testing.T) {
	a := testAdmin(t)

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
	if w.Code == http.StatusOK && bytes.Contains(w.Body.Bytes(), []byte("coredns-admin")) {
		t.Fatal("mux must not claim /dns-query")
	}
}

func TestCreateZoneAndRecord(t *testing.T) {
	a := testAdmin(t)
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
	a := testAdmin(t)
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
	a := testAdmin(t)
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
	a := testAdmin(t)
	r := httptest.NewRequest(http.MethodPost, "/dns-query", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("unexpected 200 on /dns-query: %s", w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/dns-query", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("GET /dns-query must not be the SPA: %s", w.Body.Bytes())
	}
}

func loginToken(t *testing.T, a *Admin) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.Bytes())
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("token: %v %s", err, w.Body.Bytes())
	}
	return tok.Token
}

func TestAuthConfigAndPasswordOff(t *testing.T) {
	a := testAdmin(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"password":true`)) {
		t.Fatalf("default config: %d %s", w.Code, w.Body.Bytes())
	}

	a.cfg.Password = false
	r = httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"password":false`)) {
		t.Fatalf("password off config: %d %s", w.Code, w.Body.Bytes())
	}

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("password login when disabled: %d %s", w.Code, w.Body.Bytes())
	}

	a.cfg.OIDC = &oidcSettings{
		Issuer:      "https://idp.example.com",
		ButtonText:  "Sign in with IdP",
		ButtonImage: "https://idp.example.com/logo.svg",
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("oidc config: %d %s", w.Code, w.Body.Bytes())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"oidc":true`)) ||
		!bytes.Contains(w.Body.Bytes(), []byte(`"oidc_button_text":"Sign in with IdP"`)) ||
		!bytes.Contains(w.Body.Bytes(), []byte(`"oidc_button_image":"https://idp.example.com/logo.svg"`)) {
		t.Fatalf("oidc button fields: %s", w.Body.Bytes())
	}
}

func TestSPAAndJSONIndex(t *testing.T) {
	a := testAdmin(t)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /: %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !bytes.Contains([]byte(ct), []byte("text/html")) && !bytes.Contains(w.Body.Bytes(), []byte("<html")) {
		t.Fatalf("GET / should be HTML, got %s %s", ct, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/zones", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("<html")) {
		t.Fatalf("SPA fallback /zones: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1", nil)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"ui"`)) {
		t.Fatalf("JSON index: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestMetricsAndAudit(t *testing.T) {
	a := testAdmin(t)
	a.db.Audit("admin", "zone.create", "example.com.", "test")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("metrics unauth: %d", w.Code)
	}

	tok := loginToken(t, a)
	r = httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"series"`)) {
		t.Fatalf("metrics: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("zone.create")) {
		t.Fatalf("audit: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestClusterConnectRejectedOnPrimary(t *testing.T) {
	a := testAdmin(t)
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
