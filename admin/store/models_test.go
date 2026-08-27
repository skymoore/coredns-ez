package store

import (
	"path/filepath"
	"testing"
)

func TestAutoMigrateSchema(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Gorm() == nil {
		t.Fatal("missing gorm handle")
	}
	want := []string{
		"meta", "users", "api_tokens", "oidc_config", "oidc_state",
		"cluster_members", "join_tokens", "zones", "audit", "acls",
		"zone_views", "tsig_keys", "filter_feeds", "filter_rules",
		"records", "ixfr_journals", "query_buckets",
	}
	for _, table := range want {
		if !s.Gorm().Migrator().HasTable(table) {
			t.Errorf("missing table %s", table)
		}
	}
}
