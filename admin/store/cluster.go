package store

import (
	"database/sql"
	"time"
)

type Member struct {
	ID         string `json:"id" gorm:"primaryKey"`
	Name       string `json:"name" gorm:"not null"`
	APIURL     string `json:"api_url" gorm:"column:api_url;not null"`
	DNSAddr    string `json:"dns_addr" gorm:"column:dns_addr;not null"`
	SecretHash string `json:"-" gorm:"column:secret_hash;not null"`
	Role       string `json:"role" gorm:"not null;default:secondary"`
	JoinedAt   int64  `json:"joined_at" gorm:"column:joined_at;not null;autoCreateTime:false"`
	LastSeen   int64  `json:"last_seen" gorm:"column:last_seen;not null;autoUpdateTime:false"`
}

type JoinToken struct {
	ID        string `gorm:"primaryKey"`
	TokenHash string `gorm:"column:token_hash;uniqueIndex;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
	UsedAt    *int64 `gorm:"column:used_at"`
}

type ZoneRow struct {
	Origin       string `json:"origin" gorm:"primaryKey"`
	Kind         string `json:"kind" gorm:"not null"`
	Source       string `json:"source" gorm:"not null"`
	PersistPath  string `json:"persist_path,omitempty" gorm:"column:persist_path;not null;default:''"`
	TransferFrom string `json:"transfer_from,omitempty" gorm:"column:transfer_from"`
	TransferTo   string `json:"transfer_to,omitempty" gorm:"column:transfer_to"`
	Mutable      string `json:"mutable,omitempty"`
	CreatedAt    int64  `json:"created_at" gorm:"column:created_at;not null;autoCreateTime:false"`
}

type OIDCConfig struct {
	ID           int    `json:"-" gorm:"primaryKey"`
	Issuer       string `json:"issuer" gorm:"not null"`
	ClientID     string `json:"client_id" gorm:"column:client_id;not null"`
	ClientSecret string `json:"client_secret,omitempty" gorm:"column:client_secret;not null"`
	RedirectURL  string `json:"redirect_url" gorm:"column:redirect_url;not null"`
	ButtonText   string `json:"button_text,omitempty" gorm:"column:button_text;not null;default:''"`
	ButtonImage  string `json:"button_image,omitempty" gorm:"column:button_image;not null;default:''"`
}

type Snapshot struct {
	Generation   int64             `json:"generation"`
	Password     *bool             `json:"password,omitempty"`
	Users        []User            `json:"users"`
	Tokens       []Token           `json:"tokens"`
	OIDC         *OIDCConfig       `json:"oidc,omitempty"`
	Zones        []ZoneRow         `json:"zones"`
	Members      []Member          `json:"members"`
	ACLs         []ACL             `json:"acls"`
	TSIGKeys     []TSIGKey         `json:"tsig_keys,omitempty"`
	FilterFeeds  []FilterFeed      `json:"filter_feeds,omitempty"`
	FilterRules  []FilterRule      `json:"filter_rules,omitempty"`
	TransferTo   []string          `json:"transfer_to"`
	Recursion    []string          `json:"recursion"`
	Corefile     string            `json:"corefile,omitempty"`
	CorefileHash string            `json:"corefile_hash,omitempty"`
	CoreFiles    map[string][]byte `json:"core_files,omitempty"`
	Views        []ZoneView        `json:"views,omitempty"`
	Records      []Record          `json:"records,omitempty"`
	JWTHMAC      string            `json:"jwt_hmac,omitempty"`
	DNSSECKeys   []DNSSECKey       `json:"dnssec_keys,omitempty"`
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

func (s *Store) InsertMember(m Member) (Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID == "" {
		id, err := newID()
		if err != nil {
			return Member{}, err
		}
		m.ID = id
	}
	if m.Role == "" {
		m.Role = MemberSecondary
	}
	now := nowUnix()
	if m.JoinedAt == 0 {
		m.JoinedAt = now
	}
	m.LastSeen = now
	_, err := s.db.Exec(`INSERT INTO cluster_members(id, name, api_url, dns_addr, secret_hash, role, joined_at, last_seen) VALUES(?,?,?,?,?,?,?,?)`,
		m.ID, m.Name, m.APIURL, m.DNSAddr, m.SecretHash, m.Role, m.JoinedAt, m.LastSeen)
	return m, err
}

func scanMember(scanner interface{ Scan(dest ...any) error }) (Member, error) {
	var m Member
	err := scanner.Scan(&m.ID, &m.Name, &m.APIURL, &m.DNSAddr, &m.SecretHash, &m.Role, &m.JoinedAt, &m.LastSeen)
	if err == nil && m.Role == "" {
		m.Role = MemberSecondary
	}
	return m, err
}

func (s *Store) GetMember(id string) (Member, error) {
	return scanMember(s.db.QueryRow(`SELECT id, name, api_url, dns_addr, secret_hash, role, joined_at, last_seen FROM cluster_members WHERE id = ?`, id))
}

func (s *Store) ListMembers() ([]Member, error) {
	rows, err := s.db.Query(`SELECT id, name, api_url, dns_addr, secret_hash, role, joined_at, last_seen FROM cluster_members ORDER BY CASE role WHEN 'primary' THEN 0 ELSE 1 END, joined_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []Member{}
	}
	return out, rows.Err()
}

func (s *Store) GetMemberBySecretHash(hash string) (Member, error) {
	return scanMember(s.db.QueryRow(`SELECT id, name, api_url, dns_addr, secret_hash, role, joined_at, last_seen FROM cluster_members WHERE secret_hash = ?`, hash))
}

// UpsertRosterMember inserts or updates public roster fields. Secret hashes
// are left untouched. changed is true when a replica should pull a new snapshot.
func (s *Store) UpsertRosterMember(m Member) (changed bool, err error) {
	existing, err := s.GetMember(m.ID)
	if err != nil {
		if err != sql.ErrNoRows {
			return false, err
		}
		_, err = s.InsertMember(m)
		return err == nil, err
	}
	if m.APIURL == "" {
		m.APIURL = existing.APIURL
	}
	if m.DNSAddr == "" {
		m.DNSAddr = existing.DNSAddr
	}
	if m.Name == "" {
		m.Name = existing.Name
	}
	if m.Role == "" {
		m.Role = existing.Role
	}
	same := existing.Name == m.Name && existing.APIURL == m.APIURL && existing.DNSAddr == m.DNSAddr && existing.Role == m.Role
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(`UPDATE cluster_members SET name=?, api_url=?, dns_addr=?, role=?, last_seen=? WHERE id=?`,
		m.Name, m.APIURL, m.DNSAddr, m.Role, nowUnix(), m.ID)
	if err != nil {
		return false, err
	}
	return !same, nil
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
	if _, err := s.db.Exec(`DELETE FROM records WHERE origin = ?`, origin); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM ixfr_journals WHERE origin = ?`, origin); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM zone_views WHERE origin = ?`, origin); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM dnssec_keys WHERE origin = ?`, origin); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM zones WHERE origin = ?`, origin)
	return err
}

func (s *Store) UpsertOIDC(c OIDCConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO oidc_config(id, issuer, client_id, client_secret, redirect_url, button_text, button_image) VALUES(1,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET issuer=excluded.issuer, client_id=excluded.client_id, client_secret=excluded.client_secret, redirect_url=excluded.redirect_url, button_text=excluded.button_text, button_image=excluded.button_image`,
		c.Issuer, c.ClientID, c.ClientSecret, c.RedirectURL, c.ButtonText, c.ButtonImage)
	return err
}

func (s *Store) GetOIDC() (OIDCConfig, error) {
	var c OIDCConfig
	err := s.db.QueryRow(`SELECT issuer, client_id, client_secret, redirect_url, button_text, button_image FROM oidc_config WHERE id = 1`).
		Scan(&c.Issuer, &c.ClientID, &c.ClientSecret, &c.RedirectURL, &c.ButtonText, &c.ButtonImage)
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
	members, err := s.ListMembers()
	if err != nil {
		return Snapshot{}, err
	}
	acls, err := s.ListACLs()
	if err != nil {
		return Snapshot{}, err
	}
	keys, err := s.ListTSIGKeys()
	if err != nil {
		return Snapshot{}, err
	}
	feeds, err := s.ListFilterFeeds()
	if err != nil {
		return Snapshot{}, err
	}
	rules, err := s.ListFilterRules("", "")
	if err != nil {
		return Snapshot{}, err
	}
	recs, err := s.ListAllRecords()
	if err != nil {
		return Snapshot{}, err
	}
	dkeys, err := s.ListDNSSECKeys()
	if err != nil {
		return Snapshot{}, err
	}
	to := s.TransferTo()
	recurs := s.Recursion()
	snap := Snapshot{Generation: s.Generation(), Users: users, Tokens: tokens, Zones: zones, Members: members, ACLs: acls, TSIGKeys: keys, FilterFeeds: feeds, FilterRules: rules, TransferTo: to, Recursion: recurs, Records: recs, DNSSECKeys: dkeys}
	if oidc, err := s.GetOIDC(); err == nil {
		snap.OIDC = &oidc
	}
	if hmac, err := s.Meta(MetaJWTHMAC); err == nil {
		snap.JWTHMAC = hmac
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
		if _, err := tx.Exec(`INSERT INTO oidc_config(id, issuer, client_id, client_secret, redirect_url, button_text, button_image) VALUES(1,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET issuer=excluded.issuer, client_id=excluded.client_id, client_secret=excluded.client_secret, redirect_url=excluded.redirect_url, button_text=excluded.button_text, button_image=excluded.button_image`,
			snap.OIDC.Issuer, snap.OIDC.ClientID, snap.OIDC.ClientSecret, snap.OIDC.RedirectURL, snap.OIDC.ButtonText, snap.OIDC.ButtonImage); err != nil {
			return err
		}
	}
	if snap.Members != nil {
		if _, err := tx.Exec(`DELETE FROM cluster_members`); err != nil {
			return err
		}
		for _, m := range snap.Members {
			role := m.Role
			if role == "" {
				role = MemberSecondary
			}
			if m.JoinedAt == 0 {
				m.JoinedAt = nowUnix()
			}
			if _, err := tx.Exec(`INSERT INTO cluster_members(id, name, api_url, dns_addr, secret_hash, role, joined_at, last_seen) VALUES(?,?,?,?,?,?,?,?)`,
				m.ID, m.Name, m.APIURL, m.DNSAddr, m.SecretHash, role, m.JoinedAt, m.LastSeen); err != nil {
				return err
			}
		}
	}
	if err := applyACLsTx(tx, snap.ACLs); err != nil {
		return err
	}
	if err := applyTSIGKeysTx(tx, snap.TSIGKeys); err != nil {
		return err
	}
	if err := applyFiltersTx(tx, snap.FilterFeeds, snap.FilterRules); err != nil {
		return err
	}
	if err := applyRecordsTx(tx, snap.Records); err != nil {
		return err
	}
	if err := applyDNSSECKeysTx(tx, snap.DNSSECKeys); err != nil {
		return err
	}
	if snap.TransferTo != nil {
		if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, MetaTransferTo, JoinCSV(snap.TransferTo)); err != nil {
			return err
		}
	}
	if snap.Recursion != nil {
		if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, MetaRecursion, JoinCSV(snap.Recursion)); err != nil {
			return err
		}
	}
	if snap.JWTHMAC != "" {
		if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, MetaJWTHMAC, snap.JWTHMAC); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, MetaGeneration, snap.Generation); err != nil {
		return err
	}
	return tx.Commit()
}
