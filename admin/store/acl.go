package store

import (
	"database/sql"
	"fmt"
	"net"
	"regexp"
	"strings"
)

var aclNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type ACL struct {
	ID         string   `json:"id" gorm:"primaryKey"`
	Name       string   `json:"name" gorm:"uniqueIndex;not null"`
	Networks   []string `json:"networks" gorm:"-"`
	NetworkCSV string   `json:"-" gorm:"column:networks;type:text;not null"`
	Position   int      `json:"position" gorm:"not null"`
	CreatedAt  int64    `json:"created_at" gorm:"column:created_at;not null;autoCreateTime:false"`
}

type ZoneView struct {
	Origin string `json:"origin" gorm:"primaryKey"`
	ACL    string `json:"acl" gorm:"primaryKey"`
	Path   string `json:"path,omitempty" gorm:"column:persist_path;not null;default:''"`
	Data   []byte `json:"data,omitempty" gorm:"-"`
}

func ValidACLName(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "", "public", "default", "all":
		return fmt.Errorf("acl name %q is reserved", name)
	}
	if !aclNameRe.MatchString(name) {
		return fmt.Errorf("acl name must be lowercase alphanumeric with hyphens")
	}
	return nil
}

func NormalizeCIDRs(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if ip := net.ParseIP(raw); ip != nil {
				if ip.To4() != nil {
					raw += "/32"
				} else {
					raw += "/128"
				}
			}
		}
		_, cidr, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid network %q", raw)
		}
		s := cidr.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func NormalizeNetworks(in []string) ([]string, error) {
	out, err := NormalizeCIDRs(in)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one network is required")
	}
	return out, nil
}

func (a ACL) Contains(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range a.Networks {
		_, cidr, err := net.ParseCIDR(n)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Store) ListACLs() ([]ACL, error) {
	rows, err := s.db.Query(`SELECT id, name, networks, position, created_at FROM acls ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ACL
	for rows.Next() {
		var a ACL
		var nets string
		if err := rows.Scan(&a.ID, &a.Name, &nets, &a.Position, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Networks = SplitCSV(nets)
		out = append(out, a)
	}
	if out == nil {
		out = []ACL{}
	}
	return out, rows.Err()
}

func (s *Store) GetACLByName(name string) (ACL, error) {
	var a ACL
	var nets string
	err := s.db.QueryRow(`SELECT id, name, networks, position, created_at FROM acls WHERE name = ?`, strings.ToLower(name)).
		Scan(&a.ID, &a.Name, &nets, &a.Position, &a.CreatedAt)
	a.Networks = SplitCSV(nets)
	return a, err
}

func (s *Store) InsertACL(a ACL) (ACL, error) {
	if err := ValidACLName(a.Name); err != nil {
		return ACL{}, err
	}
	nets, err := NormalizeNetworks(a.Networks)
	if err != nil {
		return ACL{}, err
	}
	a.Name = strings.ToLower(strings.TrimSpace(a.Name))
	a.Networks = nets
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		id, err := newID()
		if err != nil {
			return ACL{}, err
		}
		a.ID = id
	}
	if a.CreatedAt == 0 {
		a.CreatedAt = nowUnix()
	}
	if a.Position == 0 {
		_ = s.db.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM acls`).Scan(&a.Position)
	}
	_, err = s.db.Exec(`INSERT INTO acls(id, name, networks, position, created_at) VALUES(?,?,?,?,?)`,
		a.ID, a.Name, JoinCSV(a.Networks), a.Position, a.CreatedAt)
	return a, err
}

func (s *Store) UpdateACL(name string, newName string, networks []string, position *int) (ACL, error) {
	cur, err := s.GetACLByName(name)
	if err != nil {
		return ACL{}, err
	}
	if networks != nil {
		nets, err := NormalizeNetworks(networks)
		if err != nil {
			return ACL{}, err
		}
		cur.Networks = nets
	}
	if position != nil {
		cur.Position = *position
	}
	rename := ""
	if newName != "" {
		newName = strings.ToLower(strings.TrimSpace(newName))
		if newName != cur.Name {
			if err := ValidACLName(newName); err != nil {
				return ACL{}, err
			}
			rename = newName
			cur.Name = newName
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rename != "" {
		if _, err := s.db.Exec(`UPDATE acls SET name = ?, networks = ?, position = ? WHERE id = ?`,
			cur.Name, JoinCSV(cur.Networks), cur.Position, cur.ID); err != nil {
			return ACL{}, err
		}
		_, err = s.db.Exec(`UPDATE zone_views SET acl = ? WHERE acl = ?`, rename, strings.ToLower(name))
		if err != nil {
			return cur, err
		}
		_, err = s.db.Exec(`UPDATE records SET view = ? WHERE view = ?`, rename, strings.ToLower(name))
		return cur, err
	}
	_, err = s.db.Exec(`UPDATE acls SET networks = ?, position = ? WHERE id = ?`,
		JoinCSV(cur.Networks), cur.Position, cur.ID)
	return cur, err
}

func (s *Store) DeleteACL(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM acls WHERE name = ?`, strings.ToLower(name))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpsertZoneView(v ZoneView) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO zone_views(origin, acl, persist_path) VALUES(?,?,?)
		ON CONFLICT(origin, acl) DO UPDATE SET persist_path = excluded.persist_path`,
		v.Origin, v.ACL, v.Path)
	return err
}

func (s *Store) ListZoneViews() ([]ZoneView, error) {
	rows, err := s.db.Query(`SELECT origin, acl, persist_path FROM zone_views ORDER BY origin, acl`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ZoneView
	for rows.Next() {
		var v ZoneView
		if err := rows.Scan(&v.Origin, &v.ACL, &v.Path); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if out == nil {
		out = []ZoneView{}
	}
	return out, rows.Err()
}

func (s *Store) ListZoneViewsFor(origin string) ([]ZoneView, error) {
	rows, err := s.db.Query(`SELECT origin, acl, persist_path FROM zone_views WHERE origin = ? ORDER BY acl`, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ZoneView
	for rows.Next() {
		var v ZoneView
		if err := rows.Scan(&v.Origin, &v.ACL, &v.Path); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DeleteZoneViews(origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM zone_views WHERE origin = ?`, origin)
	return err
}

func (s *Store) DeleteZoneViewsForACL(acl string) ([]ZoneView, error) {
	views, err := s.ListZoneViews()
	if err != nil {
		return nil, err
	}
	var gone []ZoneView
	for _, v := range views {
		if v.ACL == acl {
			gone = append(gone, v)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.db.Exec(`DELETE FROM records WHERE view = ?`, acl); err != nil {
		return gone, err
	}
	_, err = s.db.Exec(`DELETE FROM zone_views WHERE acl = ?`, acl)
	return gone, err
}

func applyACLsTx(tx *sql.Tx, acls []ACL) error {
	if acls == nil {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM acls`); err != nil {
		return err
	}
	for _, a := range acls {
		if a.CreatedAt == 0 {
			a.CreatedAt = nowUnix()
		}
		if _, err := tx.Exec(`INSERT INTO acls(id, name, networks, position, created_at) VALUES(?,?,?,?,?)`,
			a.ID, a.Name, JoinCSV(a.Networks), a.Position, a.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}
