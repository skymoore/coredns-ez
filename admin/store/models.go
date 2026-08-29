package store

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// SchemaVersion is stored in meta. Bump it whenever schemaModels or a
// GORM tag on those structs changes. Open skips AutoMigrate when this
// matches so a 300k-row filter_rules table is not rebuilt on every start
// (that rebuild holds :53/:443 closed until it finishes).
const SchemaVersion = "1"

// schemaModels is every SQLite table. AutoMigrate is the schema source of
// truth; do not add CREATE TABLE strings alongside these models.
func schemaModels() []any {
	return []any{
		&MetaRow{},
		&User{},
		&Token{},
		&OIDCConfig{},
		&OIDCState{},
		&Member{},
		&JoinToken{},
		&ZoneRow{},
		&AuditRow{},
		&ACL{},
		&ZoneView{},
		&TSIGKey{},
		&FilterFeed{},
		&FilterRule{},
		&Record{},
		&IXFRJournal{},
		&DNSSECKey{},
		&QueryBucket{},
	}
}

func (User) TableName() string       { return "users" }
func (Token) TableName() string      { return "api_tokens" }
func (OIDCConfig) TableName() string { return "oidc_config" }
func (Member) TableName() string     { return "cluster_members" }
func (JoinToken) TableName() string  { return "join_tokens" }
func (ZoneRow) TableName() string    { return "zones" }
func (AuditRow) TableName() string   { return "audit" }
func (ACL) TableName() string        { return "acls" }
func (ZoneView) TableName() string   { return "zone_views" }
func (TSIGKey) TableName() string    { return "tsig_keys" }
func (FilterFeed) TableName() string { return "filter_feeds" }
func (FilterRule) TableName() string { return "filter_rules" }

// MetaRow is one key/value in meta.
type MetaRow struct {
	Key   string `gorm:"column:k;primaryKey" json:"k"`
	Value string `gorm:"column:v;not null" json:"v"`
}

func (MetaRow) TableName() string { return "meta" }

// OIDCState is a pending OIDC login nonce.
type OIDCState struct {
	State     string `gorm:"primaryKey" json:"state"`
	Nonce     string `gorm:"not null" json:"nonce"`
	ExpiresAt int64  `gorm:"column:expires_at;not null" json:"expires_at"`
}

func (OIDCState) TableName() string { return "oidc_state" }

// Record is one DNS RR. View "" is the public zone; otherwise it is an ACL name.
type Record struct {
	Origin string `json:"origin" gorm:"primaryKey;index:idx_records_zone,priority:1"`
	View   string `json:"view" gorm:"primaryKey;default:'';index:idx_records_zone,priority:2"`
	Name   string `json:"name" gorm:"primaryKey"`
	Type   string `json:"type" gorm:"column:rrtype;primaryKey"`
	TTL    uint32 `json:"ttl" gorm:"not null"`
	Rdata  string `json:"rdata" gorm:"primaryKey"`
}

func (Record) TableName() string { return "records" }

// IXFRJournal is the RFC 1995 increment log for one origin, stored as the
// same text format the ixfr plugin used on disk.
type IXFRJournal struct {
	Origin string `gorm:"primaryKey" json:"origin"`
	Body   string `gorm:"type:text;not null" json:"body"`
}

func (IXFRJournal) TableName() string { return "ixfr_journals" }

// DNSSECKey is an on-the-fly signing CSK (ECDSAP256SHA256). Private is BIND
// keygen format; public is a DNSKEY RR.
type DNSSECKey struct {
	ID        string `json:"id" gorm:"primaryKey"`
	Origin    string `json:"origin" gorm:"uniqueIndex:uidx_dnssec_origin_tag;not null"`
	KeyTag    int    `json:"key_tag" gorm:"column:key_tag;uniqueIndex:uidx_dnssec_origin_tag;not null"`
	Algorithm int    `json:"algorithm" gorm:"not null"`
	Flags     int    `json:"flags" gorm:"not null"`
	Public    string `json:"public" gorm:"type:text;not null"`
	Private   string `json:"private" gorm:"type:text;not null"`
	CreatedAt int64  `json:"created_at" gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (DNSSECKey) TableName() string { return "dnssec_keys" }

// QueryBucket is a 10-second rollup of DNS queries on this node. Not clustered.
type QueryBucket struct {
	Ts       int64  `json:"ts" gorm:"primaryKey;autoIncrement:false"`
	Queries  int    `json:"queries" gorm:"not null"`
	Blocked  int    `json:"blocked" gorm:"not null"`
	Nxdomain int    `json:"nxdomain" gorm:"not null"`
	Servfail int    `json:"servfail" gorm:"not null"`
	Types    string `json:"types" gorm:"type:text;not null"`
	Rcodes   string `json:"rcodes" gorm:"type:text;not null"`
	Names    string `json:"names" gorm:"type:text;not null"`
	Blocks   string `json:"blocks" gorm:"type:text;not null"`
}

func (QueryBucket) TableName() string { return "query_buckets" }

// RecordFromRR flattens an RR into a row. TTL is stored; identity is name+type+rdata.
func RecordFromRR(origin, view string, rr dns.RR) Record {
	h := rr.Header()
	rdata := strings.TrimSpace(strings.TrimPrefix(rr.String(), h.String()))
	return Record{
		Origin: strings.ToLower(dns.CanonicalName(origin)),
		View:   strings.ToLower(strings.TrimSpace(view)),
		Name:   strings.ToLower(dns.CanonicalName(h.Name)),
		Type:   dns.TypeToString[h.Rrtype],
		TTL:    h.Ttl,
		Rdata:  rdata,
	}
}

// RR parses the row back into a miekg RR.
func (r Record) RR() (dns.RR, error) {
	if r.Name == "" || r.Type == "" {
		return nil, fmt.Errorf("incomplete record")
	}
	line := fmt.Sprintf("%s %d IN %s %s", r.Name, r.TTL, r.Type, r.Rdata)
	if r.TTL == 0 {
		line = fmt.Sprintf("%s IN %s %s", r.Name, r.Type, r.Rdata)
	}
	return dns.NewRR(line)
}

// RecordsFromRRs converts a zone slice. Duplicate identities keep the last TTL.
func RecordsFromRRs(origin, view string, rrs []dns.RR) []Record {
	out := make([]Record, 0, len(rrs))
	seen := map[string]int{}
	for _, rr := range rrs {
		if rr == nil {
			continue
		}
		rec := RecordFromRR(origin, view, rr)
		key := rec.Origin + "\x00" + rec.View + "\x00" + rec.Name + "\x00" + rec.Type + "\x00" + rec.Rdata
		if i, ok := seen[key]; ok {
			out[i] = rec
			continue
		}
		seen[key] = len(out)
		out = append(out, rec)
	}
	return out
}

// RRsFromRecords parses rows; malformed rows are skipped.
func RRsFromRecords(recs []Record) ([]dns.RR, error) {
	out := make([]dns.RR, 0, len(recs))
	for _, rec := range recs {
		rr, err := rec.RR()
		if err != nil {
			return nil, err
		}
		out = append(out, rr)
	}
	return out, nil
}
