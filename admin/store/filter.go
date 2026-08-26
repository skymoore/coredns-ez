package store

import (
	"database/sql"
	"fmt"
	"strings"
)

const (
	FilterAllow         = "allow"
	FilterBlock         = "block"
	FilterSourceManual  = "manual"
	FilterSyncPeriodic  = "periodic"
	FilterSyncOff       = "off"
	FilterDefaultIntSec = 86400
	FilterMinIntSec     = 300
	FilterMaxIntSec     = 7 * 24 * 3600
)

// FilterRule is one domain pattern in the compiled allow or block set.
type FilterRule struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Pattern   string `json:"pattern"`
	KidsOnly  bool   `json:"kids_only"`
	Source    string `json:"source"`
	CreatedAt int64  `json:"created_at"`
}

// FilterFeed is a remote list URL that contributes rules.
type FilterFeed struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Action          string `json:"action"`
	URL             string `json:"url"`
	Sync            string `json:"sync"`
	IntervalSeconds int    `json:"interval_seconds"`
	LastSyncAt      *int64 `json:"last_sync_at,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	LastCount       int    `json:"last_count"`
	ETag            string `json:"etag,omitempty"`
	CreatedAt       int64  `json:"created_at"`
}

func NormalizeFilterAction(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case FilterAllow:
		return FilterAllow, nil
	case FilterBlock, "deny":
		return FilterBlock, nil
	default:
		return "", fmt.Errorf("action must be allow or block")
	}
}

func NormalizeFilterSync(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", FilterSyncPeriodic, "on", "yes":
		return FilterSyncPeriodic, nil
	case FilterSyncOff, "once", "no":
		return FilterSyncOff, nil
	default:
		return "", fmt.Errorf("sync must be periodic or off")
	}
}

func ClampFilterInterval(sec int) int {
	if sec <= 0 {
		return FilterDefaultIntSec
	}
	if sec < FilterMinIntSec {
		return FilterMinIntSec
	}
	if sec > FilterMaxIntSec {
		return FilterMaxIntSec
	}
	return sec
}

func (r FilterRule) Display() string {
	if r.KidsOnly {
		return "*." + strings.TrimSuffix(r.Pattern, ".")
	}
	return strings.TrimSuffix(r.Pattern, ".")
}

func (s *Store) ListFilterRules(action, source string) ([]FilterRule, error) {
	q := `SELECT id, action, pattern, kids_only, source, created_at FROM filter_rules WHERE 1=1`
	var args []any
	if action != "" {
		q += ` AND action = ?`
		args = append(args, action)
	}
	if source != "" {
		q += ` AND source = ?`
		args = append(args, source)
	}
	q += ` ORDER BY action, pattern`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FilterRule{}
	for rows.Next() {
		r, err := scanFilterRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CountFilterRules() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT action, COUNT(*) FROM filter_rules GROUP BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{FilterAllow: 0, FilterBlock: 0}
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			return nil, err
		}
		out[action] = n
	}
	return out, rows.Err()
}

func (s *Store) GetFilterRule(id string) (FilterRule, error) {
	return scanFilterRule(s.db.QueryRow(
		`SELECT id, action, pattern, kids_only, source, created_at FROM filter_rules WHERE id = ?`, id))
}

func (s *Store) InsertFilterRule(r FilterRule) (FilterRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return insertFilterRuleLocked(s.db, r)
}

func insertFilterRuleLocked(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, r FilterRule) (FilterRule, error) {
	if r.ID == "" {
		id, err := newID()
		if err != nil {
			return FilterRule{}, err
		}
		r.ID = id
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = nowUnix()
	}
	if r.Source == "" {
		r.Source = FilterSourceManual
	}
	kids := 0
	if r.KidsOnly {
		kids = 1
	}
	_, err := exec.Exec(`INSERT INTO filter_rules(id, action, pattern, kids_only, source, created_at) VALUES(?,?,?,?,?,?)`,
		r.ID, r.Action, r.Pattern, kids, r.Source, r.CreatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return FilterRule{}, fmt.Errorf("pattern already listed")
		}
		return FilterRule{}, err
	}
	return r, nil
}

func (s *Store) DeleteFilterRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM filter_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListFilterFeeds() ([]FilterFeed, error) {
	rows, err := s.db.Query(`SELECT id, name, action, url, sync, interval_seconds, last_sync_at, last_error, last_count, etag, created_at FROM filter_feeds ORDER BY action, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FilterFeed{}
	for rows.Next() {
		f, err := scanFilterFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFilterFeed(id string) (FilterFeed, error) {
	return scanFilterFeed(s.db.QueryRow(
		`SELECT id, name, action, url, sync, interval_seconds, last_sync_at, last_error, last_count, etag, created_at FROM filter_feeds WHERE id = ?`, id))
}

func (s *Store) InsertFilterFeed(f FilterFeed) (FilterFeed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == "" {
		id, err := newID()
		if err != nil {
			return FilterFeed{}, err
		}
		f.ID = id
	}
	if f.CreatedAt == 0 {
		f.CreatedAt = nowUnix()
	}
	f.IntervalSeconds = ClampFilterInterval(f.IntervalSeconds)
	if f.Sync == "" {
		f.Sync = FilterSyncPeriodic
	}
	_, err := s.db.Exec(`INSERT INTO filter_feeds(id, name, action, url, sync, interval_seconds, last_sync_at, last_error, last_count, etag, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.Name, f.Action, f.URL, f.Sync, f.IntervalSeconds, f.LastSyncAt, f.LastError, f.LastCount, f.ETag, f.CreatedAt)
	return f, err
}

func (s *Store) UpdateFilterFeed(f FilterFeed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.IntervalSeconds = ClampFilterInterval(f.IntervalSeconds)
	_, err := s.db.Exec(`UPDATE filter_feeds SET name=?, sync=?, interval_seconds=?, last_sync_at=?, last_error=?, last_count=?, etag=? WHERE id=?`,
		f.Name, f.Sync, f.IntervalSeconds, f.LastSyncAt, f.LastError, f.LastCount, f.ETag, f.ID)
	return err
}

func (s *Store) DeleteFilterFeed(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM filter_rules WHERE source = ?`, id); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM filter_feeds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ReplaceFeedRules(feedID string, rules []FilterRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM filter_rules WHERE source = ?`, feedID); err != nil {
		return err
	}
	for _, r := range rules {
		r.Source = feedID
		if _, err := insertFilterRuleLocked(tx, r); err != nil {
			if strings.Contains(err.Error(), "already listed") {
				continue
			}
			return err
		}
	}
	return tx.Commit()
}

func applyFiltersTx(tx *sql.Tx, feeds []FilterFeed, rules []FilterRule) error {
	if feeds == nil && rules == nil {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM filter_rules`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM filter_feeds`); err != nil {
		return err
	}
	for _, f := range feeds {
		if f.ID == "" {
			id, err := newID()
			if err != nil {
				return err
			}
			f.ID = id
		}
		if f.CreatedAt == 0 {
			f.CreatedAt = nowUnix()
		}
		if f.IntervalSeconds == 0 {
			f.IntervalSeconds = FilterDefaultIntSec
		}
		if f.Sync == "" {
			f.Sync = FilterSyncOff
		}
		if _, err := tx.Exec(`INSERT INTO filter_feeds(id, name, action, url, sync, interval_seconds, last_sync_at, last_error, last_count, etag, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			f.ID, f.Name, f.Action, f.URL, f.Sync, f.IntervalSeconds, f.LastSyncAt, f.LastError, f.LastCount, f.ETag, f.CreatedAt); err != nil {
			return err
		}
	}
	for _, r := range rules {
		if _, err := insertFilterRuleLocked(tx, r); err != nil {
			if strings.Contains(err.Error(), "already listed") {
				continue
			}
			return err
		}
	}
	return nil
}

func scanFilterRule(scanner interface{ Scan(dest ...any) error }) (FilterRule, error) {
	var r FilterRule
	var kids int
	err := scanner.Scan(&r.ID, &r.Action, &r.Pattern, &kids, &r.Source, &r.CreatedAt)
	r.KidsOnly = kids != 0
	return r, err
}

func scanFilterFeed(scanner interface{ Scan(dest ...any) error }) (FilterFeed, error) {
	var f FilterFeed
	var last sql.NullInt64
	err := scanner.Scan(&f.ID, &f.Name, &f.Action, &f.URL, &f.Sync, &f.IntervalSeconds, &last, &f.LastError, &f.LastCount, &f.ETag, &f.CreatedAt)
	if last.Valid {
		v := last.Int64
		f.LastSyncAt = &v
	}
	return f, err
}
