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
		httpClient:       &http.Client{},
		stop:             make(chan struct{}),
		tsig:             newTSIGHub(),
		filters:          newFilterEngine(),
		filterAllowLocal: true,
		xferHub:          newXferHub(),
		skipReload:       true,
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

func TestSqliteGhostZoneIsListedAndDeletable(t *testing.T) {
	a := testAdmin(t)
	if err := a.db.UpsertZone(store.ZoneRow{Origin: "sky.sc.", Kind: zonereg.KindPrimary, Source: zonereg.SourceAdmin}); err != nil {
		t.Fatal(err)
	}
	tok := loginToken(t, a)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("sky.sc.")) {
		t.Fatalf("ghost zone missing from list: %d %s", w.Code, w.Body.Bytes())
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/zones/sky.sc.", nil)
	del.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, del)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete ghost: %d %s", w.Code, w.Body.Bytes())
	}
	if _, err := a.db.GetZone("sky.sc."); err == nil {
		t.Fatal("sqlite zone row survived DELETE")
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
	if a.db.HasRecords("example.com.", "") == false {
		t.Fatal("create zone did not persist SOA/NS to sqlite")
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

func TestCreateZoneSetsMnameAndRname(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	zbody, _ := json.Marshal(map[string]string{
		"origin": "example.com.",
		"type":   "primary",
		"ns":     "ns1.rwx.dev.",
		"rname":  "sky@rwx.dev",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records?name=@&type=SOA", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list SOA: %d %s", w.Code, w.Body.Bytes())
	}
	body := string(w.Body.Bytes())
	if !bytes.Contains(w.Body.Bytes(), []byte("ns1.rwx.dev.")) {
		t.Fatalf("MNAME missing: %s", body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("sky.rwx.dev.")) {
		t.Fatalf("RNAME missing: %s", body)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("hostmaster.example.com.")) {
		t.Fatalf("default RNAME still present: %s", body)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records?name=@&type=NS", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("ns1.rwx.dev.")) {
		t.Fatalf("apex NS missing: %d %s", w.Code, w.Body.Bytes())
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

	r = httptest.NewRequest(http.MethodGet, "/api/v1/queries", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"recent"`)) {
		t.Fatalf("queries: %d %s", w.Code, w.Body.Bytes())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"series"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"range":"1h"`)) {
		t.Fatalf("queries default range: %s", w.Body.Bytes())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/queries?range=24h", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"range":"24h"`)) {
		t.Fatalf("queries 24h: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestSecondaryMutationsProxyWithSharedHMAC(t *testing.T) {
	primary := testAdmin(t)
	primSrv := httptest.NewServer(primary.mux)
	t.Cleanup(primSrv.Close)

	sec := testAdmin(t)
	sec.cfg.Role = roleSecondary
	if err := sec.db.SetMeta(store.MetaPrimaryURL, primSrv.URL); err != nil {
		t.Fatal(err)
	}

	tok := loginToken(t, sec)
	zbody, _ := json.Marshal(map[string]string{"origin": "proxy.test.", "type": "primary"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	sec.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unsynced identity should 401, got %d %s", w.Code, w.Body.Bytes())
	}

	snap, err := primary.db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.JWTHMAC == "" {
		t.Fatal("snapshot missing jwt_hmac")
	}
	if err := sec.db.ApplySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if err := sec.db.SetMeta(store.MetaPrimaryURL, primSrv.URL); err != nil {
		t.Fatal(err)
	}
	tok = loginToken(t, sec)
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	sec.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("shared hmac proxied create: %d %s", w.Code, w.Body.Bytes())
	}
	if !primary.db.HasRecords("proxy.test.", "") {
		t.Fatal("primary did not receive proxied zone create")
	}
}

func TestProxyTransferStaysOnSecondary(t *testing.T) {
	called := false
	prim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(599)
	}))
	t.Cleanup(prim.Close)

	sec := testAdmin(t)
	sec.cfg.Role = roleSecondary
	if err := sec.db.SetMeta(store.MetaPrimaryURL, prim.URL); err != nil {
		t.Fatal(err)
	}
	tok := loginToken(t, sec)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/zones/rwx.dev./transfer", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	sec.mux.ServeHTTP(w, r)
	if called {
		t.Fatal("zone transfer was proxied to the primary")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("local transfer: %d %s", w.Code, w.Body.Bytes())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("not a secondary")) {
		t.Fatalf("want local not-a-secondary, got %s", w.Body.Bytes())
	}
}

func TestProxyDoesNotForwardLogin(t *testing.T) {
	a := testAdmin(t)
	a.cfg.Role = roleSecondary
	if err := a.db.SetMeta(store.MetaPrimaryURL, "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code == http.StatusBadGateway || w.Code == http.StatusServiceUnavailable {
		t.Fatalf("login must stay on this node, got %d %s", w.Code, w.Body.Bytes())
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestAuthConfigPasswordFromMeta(t *testing.T) {
	a := testAdmin(t)
	a.cfg.Password = true
	if err := a.db.SetMeta(store.MetaPassword, "off"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"password":false`)) {
		t.Fatalf("meta password off: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestClusterConnectRequiresAdminWhenUsersExist(t *testing.T) {
	a := testAdmin(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/connect", bytes.NewReader([]byte(`{"url":"http://x","token":"t"}`)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth connect: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestClusterConnectStandalonePrimaryNotForbidden(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/connect", bytes.NewReader([]byte(`{"url":"http://127.0.0.1:1","token":"t"}`)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Fatalf("standalone primary must be allowed to join, got %d %s", w.Code, w.Body.Bytes())
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502 from unreachable primary, got %d %s", w.Code, w.Body.Bytes())
	}
}

func TestClusterConnectRejectedWhenHasSecondary(t *testing.T) {
	a := testAdmin(t)
	if _, err := a.db.InsertMember(store.Member{Name: "ns2", Role: store.MemberSecondary, SecretHash: "x"}); err != nil {
		t.Fatal(err)
	}
	tok := loginToken(t, a)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/connect", bytes.NewReader([]byte(`{"url":"http://x","token":"t"}`)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.Bytes())
	}
}

func TestInstallHTTPHandlerMissingField(t *testing.T) {
	if installHTTPHandler(nil, http.NotFoundHandler()) {
		t.Fatal("nil config must not install")
	}
}
