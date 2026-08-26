package store

import (
	"database/sql"
	"strings"
	"time"
)

type Member struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	APIURL     string `json:"api_url"`
	DNSAddr    string `json:"dns_addr"`
	SecretHash string `json:"-"`
	JoinedAt   int64  `json:"joined_at"`
	LastSeen   int64  `json:"last_seen"`
}

type JoinToken struct {
	ID        string
	TokenHash string
	ExpiresAt int64
	UsedAt    *int64
}

type ZoneRow struct {
	Origin       string `json:"origin"`
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	PersistPath  string `json:"persist_path"`
	TransferFrom string `json:"transfer_from,omitempty"`
	TransferTo   string `json:"transfer_to,omitempty"`
	Mutable      string `json:"mutable,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

type OIDCConfig struct {
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURL  string `json:"redirect_url"`
}

type Snapshot struct {
	Generation int64       `json:"generation"`
	Users      []User      `json:"users"`
	Tokens     []Token     `json:"tokens"`
	OIDC       *OIDCConfig `json:"oidc,omitempty"`
	Zones      []ZoneRow   `json:"zones"`
}

func (s *Store) InsertJoinToken(hash string, ttl time.Duration) (JoinToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := newID()
	if err != nil {
		return JoinToken{}, err
	}
	jt := JoinToken{ID: id, TokenHash: hash, ExpiresAt: time.Now().Add(ttl).Unix()}
	_, err = s.db.Exec(`INSERT INTO join_tokens(id, token_hash, expires_at) VALUES(?,?,?)`, jt.ID, jt.TokenHash, jt.ExpiresAt)
	return jt, err
}

func (s *Store) ConsumeJoinToken(hash string) (JoinToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var jt JoinToken
	var used sql.NullInt64
	err := s.db.QueryRow(`SELECT id, token_hash, expires_at, used_at FROM join_tokens WHERE token_hash = ?`, hash).
		Scan(&jt.ID, &jt.TokenHash, &jt.ExpiresAt, &used)
	if err != nil {
		return JoinToken{}, err
	}
	if used.Valid {
		return JoinToken{}, sql.ErrNoRows
	}
	if time.Now().Unix() > jt.ExpiresAt {
		return JoinToken{}, sql.ErrNoRows
	}
	now := nowUnix()
	if _, err := s.db.Exec(`UPDATE join_tokens SET used_at = ? WHERE id = ?`, now, jt.ID); err != nil {
		return JoinToken{}, err
	}
	jt.UsedAt = &now
	return jt, nil
}

func (s *Store) InsertMember(m Member) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		m.ID = id
	}
	now := nowUnix()
	if m.JoinedAt == 0 {
		m.JoinedAt = now
	}
	m.LastSeen = now
	_, err := s.db.Exec(`INSERT INTO cluster_members(id, name, api_url, dns_addr, secret_hash, joined_at, last_seen) VALUES(?,?,?,?,?,?,?)`,
		m.ID, m.Name, m.APIURL, m.DNSAddr, m.SecretHash, m.JoinedAt, m.LastSeen)
	return err
}

func (s *Store) ListMembers() ([]Member, error) {
	rows, err := s.db.Query(`SELECT id, name, api_url, dns_addr, secret_hash, joined_at, last_seen FROM cluster_members ORDER BY joined_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Name, &m.APIURL, &m.DNSAddr, &m.SecretHash, &m.JoinedAt, &m.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMemberBySecretHash(hash string) (Member, error) {
	var m Member
	err := s.db.QueryRow(`SELECT id, name, api_url, dns_addr, secret_hash, joined_at, last_seen FROM cluster_members WHERE secret_hash = ?`, hash).
		Scan(&m.ID, &m.Name, &m.APIURL, &m.DNSAddr, &m.SecretHash, &m.JoinedAt, &m.LastSeen)
	return m, err
}

func (s *Store) TouchMember(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE cluster_members SET last_seen = ? WHERE id = ?`, nowUnix(), id)
	return err
}

func (s *Store) DeleteMember(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM cluster_members WHERE id = ?`, id)
	return err
}

func (s *Store) UpsertZone(z ZoneRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if z.CreatedAt == 0 {
		z.CreatedAt = nowUnix()
	}
	_, err := s.db.Exec(`INSERT INTO zones(origin, kind, source, persist_path, transfer_from, transfer_to, mutable, created_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(origin) DO UPDATE SET kind=excluded.kind, source=excluded.source, persist_path=excluded.persist_path,
			transfer_from=excluded.transfer_from, transfer_to=excluded.transfer_to, mutable=excluded.mutable`,
		z.Origin, z.Kind, z.Source, z.PersistPath, z.TransferFrom, z.TransferTo, z.Mutable, z.CreatedAt)
	return err
}

func (s *Store) GetZone(origin string) (ZoneRow, error) {
	var z ZoneRow
	err := s.db.QueryRow(`SELECT origin, kind, source, persist_path, transfer_from, transfer_to, mutable, created_at FROM zones WHERE origin = ?`, origin).
		Scan(&z.Origin, &z.Kind, &z.Source, &z.PersistPath, &z.TransferFrom, &z.TransferTo, &z.Mutable, &z.CreatedAt)
	return z, err
}

func (s *Store) ListZones() ([]ZoneRow, error) {
	rows, err := s.db.Query(`SELECT origin, kind, source, persist_path, transfer_from, transfer_to, mutable, created_at FROM zones ORDER BY origin`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ZoneRow
	for rows.Next() {
		var z ZoneRow
		if err := rows.Scan(&z.Origin, &z.Kind, &z.Source, &z.PersistPath, &z.TransferFrom, &z.TransferTo, &z.Mutable, &z.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func (s *Store) DeleteZone(origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM zones WHERE origin = ?`, origin)
	return err
}

func (s *Store) UpsertOIDC(c OIDCConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO oidc_config(id, issuer, client_id, client_secret, redirect_url) VALUES(1,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET issuer=excluded.issuer, client_id=excluded.client_id, client_secret=excluded.client_secret, redirect_url=excluded.redirect_url`,
		c.Issuer, c.ClientID, c.ClientSecret, c.RedirectURL)
	return err
}

func (s *Store) GetOIDC() (OIDCConfig, error) {
	var c OIDCConfig
	err := s.db.QueryRow(`SELECT issuer, client_id, client_secret, redirect_url FROM oidc_config WHERE id = 1`).
		Scan(&c.Issuer, &c.ClientID, &c.ClientSecret, &c.RedirectURL)
	return c, err
}

func (s *Store) PutOIDCState(state, nonce string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO oidc_state(state, nonce, expires_at) VALUES(?,?,?)`, state, nonce, time.Now().Add(ttl).Unix())
	return err
}

func (s *Store) TakeOIDCState(state string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var nonce string
	var exp int64
	err := s.db.QueryRow(`SELECT nonce, expires_at FROM oidc_state WHERE state = ?`, state).Scan(&nonce, &exp)
	if err != nil {
		return "", err
	}
	_, _ = s.db.Exec(`DELETE FROM oidc_state WHERE state = ?`, state)
	if time.Now().Unix() > exp {
		return "", sql.ErrNoRows
	}
	return nonce, nil
}

func (s *Store) Snapshot() (Snapshot, error) {
	users, err := s.ListUsers()
	if err != nil {
		return Snapshot{}, err
	}
	tokens, err := s.ListTokens("")
	if err != nil {
		return Snapshot{}, err
	}
	zones, err := s.ListZones()
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Generation: s.Generation(), Users: users, Tokens: tokens, Zones: zones}
	if oidc, err := s.GetOIDC(); err == nil {
		snap.OIDC = &oidc
	}
	return snap, nil
}

func (s *Store) ApplySnapshot(snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM api_tokens`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM zones`); err != nil {
		return err
	}
	for _, u := range snap.Users {
		if _, err := tx.Exec(`INSERT INTO users(id, username, password_hash, role, disabled, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`,
			u.ID, u.Username, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAt, u.UpdatedAt); err != nil {
			return err
		}
	}
	for _, t := range snap.Tokens {
		var exp any
		if t.ExpiresAt != nil {
			exp = *t.ExpiresAt
		}
		if _, err := tx.Exec(`INSERT INTO api_tokens(id, user_id, name, token_hash, prefix, role, expires_at, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			t.ID, t.UserID, t.Name, t.TokenHash, t.Prefix, t.Role, exp, t.CreatedAt); err != nil {
			return err
		}
	}
	for _, z := range snap.Zones {
		if _, err := tx.Exec(`INSERT INTO zones(origin, kind, source, persist_path, transfer_from, transfer_to, mutable, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			z.Origin, z.Kind, z.Source, z.PersistPath, z.TransferFrom, z.TransferTo, z.Mutable, z.CreatedAt); err != nil {
			return err
		}
	}
	if snap.OIDC != nil {
		if _, err := tx.Exec(`INSERT INTO oidc_config(id, issuer, client_id, client_secret, redirect_url) VALUES(1,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET issuer=excluded.issuer, client_id=excluded.client_id, client_secret=excluded.client_secret, redirect_url=excluded.redirect_url`,
			snap.OIDC.Issuer, snap.OIDC.ClientID, snap.OIDC.ClientSecret, snap.OIDC.RedirectURL); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, MetaGeneration, snap.Generation); err != nil {
		return err
	}
	return tx.Commit()
}

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
