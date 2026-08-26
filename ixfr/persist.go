package ixfr

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

func writeJournalFile(path, origin string, history int, current uint32, incs []increment) error {
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

	f, err := os.CreateTemp(dir, ".ixfr-*")
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

	if err := writeJournal(f, origin, history, current, incs); err != nil {
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
	return syncDir(dir)
}

func writeJournal(w io.Writer, origin string, history int, current uint32, incs []increment) error {
	if _, err := fmt.Fprintf(w, "; ixfr-journal origin=%s current=%d history=%d\n", origin, current, history); err != nil {
		return err
	}
	for _, inc := range incs {
		if _, err := fmt.Fprintf(w, "$INCREMENT %d %d\n", inc.oldSerial, inc.newSerial); err != nil {
			return err
		}
		for _, rr := range inc.deleted {
			if _, err := fmt.Fprintf(w, "- %s\n", rr.String()); err != nil {
				return err
			}
		}
		for _, rr := range inc.added {
			if _, err := fmt.Fprintf(w, "+ %s\n", rr.String()); err != nil {
				return err
			}
		}
	}
	return nil
}

func readJournalFile(path string) ([]increment, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return parseJournal(f)
}

func parseJournal(r io.Reader) ([]increment, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	var incs []increment
	var cur *increment
	lineNo := 0
	flush := func() {
		if cur != nil {
			incs = append(incs, *cur)
			cur = nil
		}
	}
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "$INCREMENT") {
			flush()
			fields := strings.Fields(line)
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: expected $INCREMENT old new", lineNo)
			}
			oldS, err1 := strconv.ParseUint(fields[1], 10, 32)
			newS, err2 := strconv.ParseUint(fields[2], 10, 32)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("line %d: bad serials", lineNo)
			}
			cur = &increment{oldSerial: uint32(oldS), newSerial: uint32(newS)}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: RR outside $INCREMENT", lineNo)
		}
		if len(line) < 3 || (line[0] != '+' && line[0] != '-') || line[1] != ' ' {
			return nil, fmt.Errorf("line %d: expected '+ RR' or '- RR'", lineNo)
		}
		rr, err := dns.NewRR(line[2:])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if line[0] == '-' {
			cur.deleted = append(cur.deleted, rr)
		} else {
			cur.added = append(cur.added, rr)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return incs, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
