package dnsupdatepersist

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/miekg/dns"
)

// persistWrite is the on-disk replace used after a mutating UPDATE. Tests
// reassign it to inject write failures without depending on filesystem
// permissions.
var persistWrite = writeZoneFile

func (d *UpdatePersist) persistUpdated(rrs []dns.RR) error {
	if d.seedPath == "" {
		return fmt.Errorf("no file path configured")
	}
	start := time.Now()
	err := persistWrite(d.seedPath, d.Zone, rrs)
	writeDuration.WithLabelValues(d.Zone).Observe(time.Since(start).Seconds())
	return err
}

func writeZoneFile(path, origin string, rrs []dns.RR) error {
	if soaOf(rrs) == nil {
		return fmt.Errorf("no SOA")
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}

	f, err := os.CreateTemp(dir, ".persist-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	if err := writeRecords(f, origin, rrs); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	if err := syncDir(dir); err != nil {
		return err
	}
	log.Infof("Persisted %s to %s serial=%d", origin, path, soaOf(rrs).Serial)
	return nil
}

func writeRecords(w io.Writer, origin string, rrs []dns.RR) error {
	soa := soaOf(rrs)
	header := fmt.Sprintf("; persisted by coredns dns-update-persistent origin=%s serial=%d\n", origin, soa.Serial)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if err := writeRR(w, soa); err != nil {
		return err
	}
	for _, rr := range rrs {
		if rr == soa {
			continue
		}
		if _, ok := rr.(*dns.SOA); ok {
			continue
		}
		if err := writeRR(w, rr); err != nil {
			return err
		}
	}
	return nil
}

func writeRR(w io.Writer, rr dns.RR) error {
	if _, err := io.WriteString(w, rr.String()); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
