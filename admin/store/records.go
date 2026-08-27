package store

import (
	"database/sql"
	"strings"

	"github.com/miekg/dns"
	"gorm.io/gorm"
)

func viewName(view string) string {
	return strings.ToLower(strings.TrimSpace(view))
}

func (s *Store) ReplaceRecords(origin, view string, rrs []dns.RR) error {
	origin = canonicalOrigin(origin)
	view = viewName(view)
	recs := RecordsFromRRs(origin, view, rrs)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("origin = ? AND view = ?", origin, view).Delete(&Record{}).Error; err != nil {
			return err
		}
		if len(recs) == 0 {
			return nil
		}
		return tx.Create(&recs).Error
	})
}

func (s *Store) ListRecords(origin, view string) ([]dns.RR, error) {
	origin = canonicalOrigin(origin)
	view = viewName(view)
	var recs []Record
	err := s.gdb.Where("origin = ? AND view = ?", origin, view).Order("name, rrtype, rdata").Find(&recs).Error
	if err != nil {
		return nil, err
	}
	return RRsFromRecords(recs)
}

func (s *Store) ListAllRecords() ([]Record, error) {
	var recs []Record
	err := s.gdb.Order("origin, view, name, rrtype, rdata").Find(&recs).Error
	if err != nil {
		return nil, err
	}
	if recs == nil {
		recs = []Record{}
	}
	return recs, nil
}

func (s *Store) HasSOA(origin string) bool {
	rrs, err := s.ListRecords(origin, "")
	if err != nil {
		return false
	}
	for _, rr := range rrs {
		if _, ok := rr.(*dns.SOA); ok {
			return true
		}
	}
	return false
}

func (s *Store) HasRecords(origin, view string) bool {
	origin = canonicalOrigin(origin)
	view = viewName(view)
	var n int64
	s.gdb.Model(&Record{}).Where("origin = ? AND view = ?", origin, view).Count(&n)
	return n > 0
}

func (s *Store) DeleteRecords(origin, view string) error {
	origin = canonicalOrigin(origin)
	view = viewName(view)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Where("origin = ? AND view = ?", origin, view).Delete(&Record{}).Error
}

func (s *Store) DeleteOriginRecords(origin string) error {
	origin = canonicalOrigin(origin)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Where("origin = ?", origin).Delete(&Record{}).Error
}

func (s *Store) DeleteRecordsByView(view string) error {
	view = viewName(view)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Where("view = ?", view).Delete(&Record{}).Error
}

func (s *Store) RenameRecordView(oldView, newView string) error {
	oldView, newView = viewName(oldView), viewName(newView)
	if oldView == newView {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Model(&Record{}).Where("view = ?", oldView).Update("view", newView).Error
}

func (s *Store) SaveIXFR(origin string, body []byte) error {
	origin = canonicalOrigin(origin)
	s.mu.Lock()
	defer s.mu.Unlock()
	row := IXFRJournal{Origin: origin, Body: string(body)}
	return s.gdb.Save(&row).Error
}

func (s *Store) LoadIXFR(origin string) ([]byte, error) {
	origin = canonicalOrigin(origin)
	var row IXFRJournal
	err := s.gdb.First(&row, "origin = ?", origin).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(row.Body), nil
}

func (s *Store) DeleteIXFR(origin string) error {
	origin = canonicalOrigin(origin)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Where("origin = ?", origin).Delete(&IXFRJournal{}).Error
}

func canonicalOrigin(origin string) string {
	if origin == "" {
		return ""
	}
	return strings.ToLower(dns.CanonicalName(origin))
}

func applyRecordsTx(tx *sql.Tx, recs []Record) error {
	if recs == nil {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM records`); err != nil {
		return err
	}
	for _, r := range recs {
		if _, err := tx.Exec(`INSERT INTO records(origin, view, name, rrtype, ttl, rdata) VALUES(?,?,?,?,?,?)`,
			r.Origin, r.View, r.Name, r.Type, r.TTL, r.Rdata); err != nil {
			return err
		}
	}
	return nil
}
