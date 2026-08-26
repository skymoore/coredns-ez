package admin

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *Admin) handleBackup(w http.ResponseWriter, r *http.Request) {
	if err := a.db.Checkpoint(); err != nil {
		log.Warningf("backup checkpoint: %v", err)
	}
	name := fmt.Sprintf("coredns-ez-%s.zip", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	zw := zip.NewWriter(w)
	defer zw.Close()

	dbPath := a.db.Path()
	if dbPath != "" {
		if err := zipFile(zw, dbPath, "admin.sqlite"); err != nil {
			log.Warningf("backup sqlite: %v", err)
		}
		for _, suf := range []string{"-wal", "-shm"} {
			p := dbPath + suf
			if _, err := os.Stat(p); err == nil {
				_ = zipFile(zw, p, "admin.sqlite"+suf)
			}
		}
	}
	if a.cfg.Data != "" {
		_ = zipDir(zw, a.cfg.Data, "zones")
	}
	if conf := corefilePath(); conf != "" {
		if _, err := os.Stat(conf); err == nil {
			_ = zipFile(zw, conf, "Corefile")
		}
		tlsDir := filepath.Join(filepath.Dir(conf), "tls")
		if st, err := os.Stat(tlsDir); err == nil && st.IsDir() {
			_ = zipDir(zw, tlsDir, "tls")
		}
	}
	a.db.Audit(actorFrom(r).Username, "backup.create", "", name)
}

func corefilePath() string {
	args := os.Args
	for i, a := range args {
		if a == "-conf" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "-conf=") {
			return strings.TrimPrefix(a, "-conf=")
		}
	}
	return "Corefile"
}

func zipFile(zw *zip.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(st)
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func zipDir(zw *zip.Writer, root, prefix string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		return zipFile(zw, path, filepath.ToSlash(filepath.Join(prefix, rel)))
	})
}
