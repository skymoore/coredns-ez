package ixfr

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

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
