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

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
	MetaRecursion    = "recursion_nets"
	MetaPassword     = "password"
	MetaCorefileHash = "corefile_hash"
	MetaNodeName     = "node_name"
	MetaPrimaryDNS   = "primary_dns"

	MemberPrimary   = "primary"
	MemberSecondary = "secondary"
)

// Store is the SQLite identity, zone, and record database.
type Store struct {
	gdb  *gorm.DB
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

	// Do not put foreign_keys=1 in the DSN. AutoMigrate rebuilds tables on
	// SQLite; with FKs on, DROP users CASCADE-deletes api_tokens (DNS-01
	// webhook secrets vanish on restart). GORM's migrator flag is not enough
	// because the DSN pragma is applied on every connection.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	s := &Store{gdb: gdb, db: sqlDB, path: path}
	if err := s.dedupeFilterFeeds(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := gdb.AutoMigrate(schemaModels()...); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := s.ensureMeta(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return s, nil
}

// Gorm returns the GORM handle. Schema lives in the models passed to AutoMigrate.
func (s *Store) Gorm() *gorm.DB { return s.gdb }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Path() string { return s.path }

func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
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
