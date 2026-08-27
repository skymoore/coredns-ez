package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type User struct {
	ID           string `json:"id" gorm:"primaryKey"`
	Username     string `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string `json:"password_hash,omitempty" gorm:"column:password_hash;not null"`
	Role         string `json:"role" gorm:"not null"`
	Disabled     bool   `json:"disabled" gorm:"not null;default:0;type:integer"`
	CreatedAt    int64  `json:"created_at" gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt    int64  `json:"updated_at" gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

type Token struct {
	ID        string `json:"id" gorm:"primaryKey"`
	UserID    string `json:"user_id" gorm:"column:user_id;index;not null"`
	Name      string `json:"name" gorm:"not null"`
	TokenHash string `json:"token_hash,omitempty" gorm:"column:token_hash;uniqueIndex;not null"`
	Prefix    string `json:"prefix" gorm:"not null"`
	Role      string `json:"role" gorm:"not null"`
	ExpiresAt *int64 `json:"expires_at,omitempty" gorm:"column:expires_at"`
	CreatedAt int64  `json:"created_at" gorm:"column:created_at;not null;autoCreateTime:false"`
}

func ValidRole(r string) bool {
	switch r {
	case RoleAdmin, RoleOperator, RoleViewer:
		return true
	}
	return false
}

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) AdminCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ? AND disabled = 0`, RoleAdmin).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		u.ID = id
	}
	now := nowUnix()
	if u.CreatedAt == 0 {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO users(id, username, password_hash, role, disabled, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAt, u.UpdatedAt)
	return err
}

func (s *Store) GetUser(id string) (User, error) {
	return scanUser(s.db.QueryRow(`SELECT id, username, password_hash, role, disabled, created_at, updated_at FROM users WHERE id = ?`, id))
}

func (s *Store) GetUserByName(name string) (User, error) {
	return scanUser(s.db.QueryRow(`SELECT id, username, password_hash, role, disabled, created_at, updated_at FROM users WHERE username = ?`, name))
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, password_hash, role, disabled, created_at, updated_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET username=?, password_hash=?, role=?, disabled=?, updated_at=? WHERE id=?`,
		u.Username, u.PasswordHash, u.Role, boolInt(u.Disabled), nowUnix(), u.ID)
	return err
}

func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *Store) CreateToken(t Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		t.ID = id
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = nowUnix()
	}
	var exp any
	if t.ExpiresAt != nil {
		exp = *t.ExpiresAt
	}
	_, err := s.db.Exec(`INSERT INTO api_tokens(id, user_id, name, token_hash, prefix, role, expires_at, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		t.ID, t.UserID, t.Name, t.TokenHash, t.Prefix, t.Role, exp, t.CreatedAt)
	return err
}

func (s *Store) GetTokenByHash(hash string) (Token, error) {
	return scanToken(s.db.QueryRow(`SELECT id, user_id, name, token_hash, prefix, role, expires_at, created_at FROM api_tokens WHERE token_hash = ?`, hash))
}

func (s *Store) ListTokens(userID string) ([]Token, error) {
	q := `SELECT id, user_id, name, token_hash, prefix, role, expires_at, created_at FROM api_tokens`
	args := []any{}
	if userID != "" {
		q += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	q += ` ORDER BY created_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanTokenRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

func (s *Store) GetToken(id string) (Token, error) {
	return scanToken(s.db.QueryRow(`SELECT id, user_id, name, token_hash, prefix, role, expires_at, created_at FROM api_tokens WHERE id = ?`, id))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	var dis int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &dis, &u.CreatedAt, &u.UpdatedAt)
	u.Disabled = dis != 0
	return u, err
}

func scanUserRow(rows *sql.Rows) (User, error) {
	var u User
	var dis int
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &dis, &u.CreatedAt, &u.UpdatedAt)
	u.Disabled = dis != 0
	return u, err
}

func scanToken(row *sql.Row) (Token, error) {
	var t Token
	var exp sql.NullInt64
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix, &t.Role, &exp, &t.CreatedAt)
	if exp.Valid {
		t.ExpiresAt = &exp.Int64
	}
	return t, err
}

func scanTokenRow(rows *sql.Rows) (Token, error) {
	var t Token
	var exp sql.NullInt64
	err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix, &t.Role, &exp, &t.CreatedAt)
	if exp.Valid {
		t.ExpiresAt = &exp.Int64
	}
	return t, err
}

func (t Token) Expired() bool {
	return t.ExpiresAt != nil && time.Now().Unix() > *t.ExpiresAt
}

func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func RequireRole(have, need string) error {
	rank := map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}
	if rank[have] < rank[need] {
		return fmt.Errorf("forbidden")
	}
	return nil
}
