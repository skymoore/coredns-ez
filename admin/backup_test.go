package admin

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupZip(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)
	if err := os.WriteFile(filepath.Join(a.cfg.Data, "db.example.test"), []byte("zone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/backup", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("backup: %d %s", w.Code, w.Body.Bytes())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
	}
	if !have["admin.sqlite"] || !have["zones/db.example.test"] {
		t.Fatalf("zip names %v", have)
	}
}

func TestVersionNewer(t *testing.T) {
	if !versionNewer("1.14.8", "1.14.7") || versionNewer("1.14.7", "1.14.7") || versionNewer("1.13.0", "1.14.7") {
		t.Fatal("compare")
	}
	if !versionNewer("v1.15.0", "1.14.7") {
		t.Fatal("v prefix")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coredns")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(path, []byte("new-binary")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary" {
		t.Fatalf("got %q", got)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(bak) != "old" {
		t.Fatalf("bak %q", bak)
	}
}

func TestCanWriteDir(t *testing.T) {
	dir := t.TempDir()
	if err := canWriteDir(dir); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		t.Skip("root can write a 0555 directory")
	}
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := canWriteDir(ro); err == nil {
		t.Fatal("expected unwritable dir")
	}
}

func TestSupervisedRestart(t *testing.T) {
	t.Setenv("INVOCATION_ID", "unit-test")
	if !supervisedRestart() {
		t.Fatal("INVOCATION_ID should mean systemd will restart us")
	}
}
