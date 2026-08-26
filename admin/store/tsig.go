package store

import (
	"database/sql"
	"fmt"
	"strings"
)

const (
	TSIGAlgSHA1   = "hmac-sha1"
	TSIGAlgSHA224 = "hmac-sha224"
	TSIGAlgSHA256 = "hmac-sha256"
	TSIGAlgSHA384 = "hmac-sha384"
	TSIGAlgSHA512 = "hmac-sha512"
)

// TSIGKey is a named HMAC secret used for RFC 2845 TSIG (nsupdate, AXFR/IXFR).
type TSIGKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"`
	Secret    string `json:"secret,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

func NormalizeTSIGAlg(alg string) (string, error) {
	alg = strings.ToLower(strings.Trim(strings.TrimSpace(alg), "."))
	if alg == "" {
		return TSIGAlgSHA256, nil
	}
	switch alg {
	case TSIGAlgSHA1, TSIGAlgSHA224, TSIGAlgSHA256, TSIGAlgSHA384, TSIGAlgSHA512:
		return alg, nil
	default:
		return "", fmt.Errorf("algorithm must be hmac-sha1, hmac-sha256, hmac-sha384, or hmac-sha512")
	}
}

func (s *Store) ListTSIGKeys() ([]TSIGKey, error) {
	rows, err := s.db.Query(`SELECT id, name, algorithm, secret, created_at FROM tsig_keys ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TSIGKey{}
	for rows.Next() {
		var k TSIGKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Algorithm, &k.Secret, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) GetTSIGKey(id string) (TSIGKey, error) {
	var k TSIGKey
	err := s.db.QueryRow(`SELECT id, name, algorithm, secret, created_at FROM tsig_keys WHERE id = ?`, id).
		Scan(&k.ID, &k.Name, &k.Algorithm, &k.Secret, &k.CreatedAt)
	return k, err
}

func (s *Store) CreateTSIGKey(k TSIGKey) (TSIGKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k.ID == "" {
		id, err := newID()
		if err != nil {
			return TSIGKey{}, err
		}
		k.ID = id
	}
	if k.CreatedAt == 0 {
		k.CreatedAt = nowUnix()
	}
	_, err := s.db.Exec(`INSERT INTO tsig_keys(id, name, algorithm, secret, created_at) VALUES(?,?,?,?,?)`,
		k.ID, k.Name, k.Algorithm, k.Secret, k.CreatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return TSIGKey{}, fmt.Errorf("key %q already exists", k.Name)
		}
		return TSIGKey{}, err
	}
	return k, nil
}

func (s *Store) DeleteTSIGKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM tsig_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func applyTSIGKeysTx(tx *sql.Tx, keys []TSIGKey) error {
	if keys == nil {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM tsig_keys`); err != nil {
		return err
	}
	for _, k := range keys {
		if k.CreatedAt == 0 {
			k.CreatedAt = nowUnix()
		}
		if k.ID == "" {
			id, err := newID()
			if err != nil {
				return err
			}
			k.ID = id
		}
		if _, err := tx.Exec(`INSERT INTO tsig_keys(id, name, algorithm, secret, created_at) VALUES(?,?,?,?,?)`,
			k.ID, k.Name, k.Algorithm, k.Secret, k.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}
