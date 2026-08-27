package store

import (
	"database/sql"
	"strings"
)

func canonicalDNSSECOrigin(origin string) string {
	return strings.ToLower(strings.TrimSpace(origin))
}

func (s *Store) InsertDNSSECKey(k DNSSECKey) (DNSSECKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k.ID == "" {
		id, err := newID()
		if err != nil {
			return DNSSECKey{}, err
		}
		k.ID = id
	}
	k.Origin = canonicalDNSSECOrigin(k.Origin)
	if k.CreatedAt == 0 {
		k.CreatedAt = nowUnix()
	}
	_, err := s.db.Exec(`INSERT INTO dnssec_keys(id, origin, key_tag, algorithm, flags, public, private, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		k.ID, k.Origin, k.KeyTag, k.Algorithm, k.Flags, k.Public, k.Private, k.CreatedAt)
	return k, err
}

func (s *Store) ListDNSSECKeys() ([]DNSSECKey, error) {
	rows, err := s.db.Query(`SELECT id, origin, key_tag, algorithm, flags, public, private, created_at FROM dnssec_keys ORDER BY origin, key_tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNSSECKey
	for rows.Next() {
		var k DNSSECKey
		if err := rows.Scan(&k.ID, &k.Origin, &k.KeyTag, &k.Algorithm, &k.Flags, &k.Public, &k.Private, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if out == nil {
		out = []DNSSECKey{}
	}
	return out, rows.Err()
}

func (s *Store) GetDNSSECKeys(origin string) ([]DNSSECKey, error) {
	origin = canonicalDNSSECOrigin(origin)
	rows, err := s.db.Query(`SELECT id, origin, key_tag, algorithm, flags, public, private, created_at FROM dnssec_keys WHERE origin = ? ORDER BY key_tag`, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNSSECKey
	for rows.Next() {
		var k DNSSECKey
		if err := rows.Scan(&k.ID, &k.Origin, &k.KeyTag, &k.Algorithm, &k.Flags, &k.Public, &k.Private, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if out == nil {
		out = []DNSSECKey{}
	}
	return out, rows.Err()
}

func (s *Store) DeleteDNSSECKeys(origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM dnssec_keys WHERE origin = ?`, canonicalDNSSECOrigin(origin))
	return err
}

func (s *Store) DNSSECOriginSet() map[string]bool {
	rows, err := s.db.Query(`SELECT DISTINCT origin FROM dnssec_keys`)
	if err != nil {
		return map[string]bool{}
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return out
		}
		out[o] = true
	}
	return out
}

func applyDNSSECKeysTx(tx *sql.Tx, keys []DNSSECKey) error {
	if keys == nil {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM dnssec_keys`); err != nil {
		return err
	}
	for _, k := range keys {
		if k.CreatedAt == 0 {
			k.CreatedAt = nowUnix()
		}
		if _, err := tx.Exec(`INSERT INTO dnssec_keys(id, origin, key_tag, algorithm, flags, public, private, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			k.ID, k.Origin, k.KeyTag, k.Algorithm, k.Flags, k.Public, k.Private, k.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}
