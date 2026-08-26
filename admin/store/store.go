package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS api_tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL,
  role TEXT NOT NULL,
  expires_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oidc_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  issuer TEXT NOT NULL,
  client_id TEXT NOT NULL,
  client_secret TEXT NOT NULL,
  redirect_url TEXT NOT NULL,
  button_text TEXT NOT NULL DEFAULT '',
  button_image TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS oidc_state (
  state TEXT PRIMARY KEY,
  nonce TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS cluster_members (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  api_url TEXT NOT NULL,
  dns_addr TEXT NOT NULL,
  secret_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'secondary',
  joined_at INTEGER NOT NULL,
  last_seen INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS join_tokens (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at INTEGER NOT NULL,
  used_at INTEGER
);
CREATE TABLE IF NOT EXISTS zones (
  origin TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  source TEXT NOT NULL,
  persist_path TEXT NOT NULL,
  transfer_from TEXT,
  transfer_to TEXT,
  mutable TEXT,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  origin TEXT,
  detail TEXT
);
CREATE TABLE IF NOT EXISTS acls (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  networks TEXT NOT NULL,
  position INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS zone_views (
  origin TEXT NOT NULL,
  acl TEXT NOT NULL,
  persist_path TEXT NOT NULL,
  PRIMARY KEY (origin, acl)
);
CREATE TABLE IF NOT EXISTS tsig_keys (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  algorithm TEXT NOT NULL,
  secret TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS filter_feeds (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  action TEXT NOT NULL,
  url TEXT NOT NULL,
  sync TEXT NOT NULL,
  interval_seconds INTEGER NOT NULL DEFAULT 86400,
  last_sync_at INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  last_count INTEGER NOT NULL DEFAULT 0,
  etag TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS filter_feeds_action_url ON filter_feeds(action, url);
CREATE TABLE IF NOT EXISTS filter_rules (
  id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  pattern TEXT NOT NULL,
  kids_only INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(action, pattern, kids_only, source)
);
`

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	MetaNodeID       = "node_id"
	MetaRole         = "role"
	MetaClusterID    = "cluster_id"
	MetaJWTHMAC      = "jwt_hmac"
	MetaAdvertise    = "advertise_dns"
	MetaPrimaryURL   = "primary_url"
	MetaMemberSec    = "member_secret"
	MetaMemberID     = "member_id"
	MetaGeneration   = "snapshot_generation"
	MetaTransferTo   = "transfer_to"
	MetaPassword     = "password"
	MetaCorefileHash = "corefile_hash"
	MetaNodeName     = "node_name"

	MemberPrimary   = "primary"
	MemberSecondary = "secondary"
)

// Store is the SQLite identity and inventory database.
type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	path string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureMeta(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Path() string { return s.path }

func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (s *Store) migrate() error {
	// Existing DBs created the members table before role existed.
	_, _ = s.db.Exec(`ALTER TABLE cluster_members ADD COLUMN role TEXT NOT NULL DEFAULT 'secondary'`)
	_, _ = s.db.Exec(`ALTER TABLE oidc_config ADD COLUMN button_text TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE oidc_config ADD COLUMN button_image TEXT NOT NULL DEFAULT ''`)
	if err := s.dedupeFilterFeeds(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS filter_feeds_action_url ON filter_feeds(action, url)`); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureMeta() error {
	if _, err := s.Meta(MetaNodeID); err == nil {
		return nil
	}
	id, err := randomHex(16)
	if err != nil {
		return err
	}
	hmac, err := randomHex(32)
	if err != nil {
		return err
	}
	now := map[string]string{
		MetaNodeID:     id,
		MetaJWTHMAC:    hmac,
		MetaGeneration: "0",
	}
	for k, v := range now {
		if err := s.SetMeta(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Meta(k string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, k).Scan(&v)
	return v, err
}

func (s *Store) SetMeta(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

func (s *Store) BumpGeneration() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var g int64
	_ = s.db.QueryRow(`SELECT CAST(v AS INTEGER) FROM meta WHERE k = ?`, MetaGeneration).Scan(&g)
	g++
	_, err := s.db.Exec(`INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, MetaGeneration, fmt.Sprintf("%d", g))
	return g, err
}

func (s *Store) Generation() int64 {
	var g int64
	_ = s.db.QueryRow(`SELECT CAST(v AS INTEGER) FROM meta WHERE k = ?`, MetaGeneration).Scan(&g)
	return g
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func nowUnix() int64 { return time.Now().Unix() }

func newID() (string, error) { return randomHex(16) }
