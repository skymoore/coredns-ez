package store

import "strings"

func (s *Store) Audit(actor, action, origin, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`INSERT INTO audit(at, actor, action, origin, detail) VALUES(?,?,?,?,?)`, nowUnix(), actor, action, origin, detail)
}

func SplitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func JoinCSV(v []string) string { return strings.Join(v, ",") }
