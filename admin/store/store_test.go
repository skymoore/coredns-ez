package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestUsersAndSnapshot(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.CreateUser(User{Username: "admin", PasswordHash: "x", Role: RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	u, err := s.GetUserByName("admin")
	if err != nil || u.Role != RoleAdmin {
		t.Fatalf("%+v %v", u, err)
	}
	_, err = s.InsertJoinToken("hash", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeJoinToken("hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeJoinToken("hash"); err == nil {
		t.Fatal("join token reused")
	}
	if err := s.UpsertZone(ZoneRow{Origin: "example.com.", Kind: "primary", Source: "admin", PersistPath: "/z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMember(Member{ID: "p1", Name: "ns1", APIURL: "http://ns1:8443", DNSAddr: "192.0.2.1:53", Role: MemberPrimary}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMember(Member{Name: "ns2", APIURL: "http://ns2:8443", DNSAddr: "192.0.2.2:53", Role: MemberSecondary}); err != nil {
		t.Fatal(err)
	}
	g, err := s.BumpGeneration()
	if err != nil || g < 1 {
		t.Fatalf("gen %d %v", g, err)
	}
	snap, err := s.Snapshot()
	if err != nil || len(snap.Users) != 1 || len(snap.Zones) != 1 || len(snap.Members) != 2 {
		t.Fatalf("%+v %v", snap, err)
	}
	if snap.Members[0].Role != MemberPrimary || snap.Members[1].Role != MemberSecondary {
		t.Fatalf("member roles: %+v", snap.Members)
	}
	s2, err := Open(filepath.Join(t.TempDir(), "replica.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if snap.JWTHMAC == "" {
		t.Fatal("snapshot missing jwt_hmac")
	}
	if err := s2.ApplySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.GetUserByName("admin"); err != nil {
		t.Fatal("replica missing user")
	}
	gotHMAC, err := s2.Meta(MetaJWTHMAC)
	if err != nil || gotHMAC != snap.JWTHMAC {
		t.Fatalf("replica jwt_hmac %q %v", gotHMAC, err)
	}
	got, err := s2.ListMembers()
	if err != nil || len(got) != 2 {
		t.Fatalf("replica members %+v %v", got, err)
	}
	if got[0].Role != MemberPrimary || got[0].Name != "ns1" || got[1].Name != "ns2" {
		t.Fatalf("replica roster %+v", got)
	}
	if _, err := s.CreateTSIGKey(TSIGKey{Name: "updater.example.com.", Algorithm: TSIGAlgSHA256, Secret: "c2VjcmV0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertACL(ACL{Name: "internal", Networks: []string{"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	snap2, err := s.Snapshot()
	if err != nil || len(snap2.ACLs) != 1 || len(snap2.TSIGKeys) != 1 {
		t.Fatalf("acl/tsig snapshot acls=%+v keys=%+v %v", snap2.ACLs, snap2.TSIGKeys, err)
	}
	if err := s2.ApplySnapshot(snap2); err != nil {
		t.Fatal(err)
	}
	keys, err := s2.ListTSIGKeys()
	if err != nil || len(keys) != 1 || keys[0].Name != "updater.example.com." || keys[0].Secret != "c2VjcmV0" {
		t.Fatalf("replica tsig %+v %v", keys, err)
	}
	acls, err := s2.ListACLs()
	if err != nil || len(acls) != 1 || acls[0].Name != "internal" {
		t.Fatalf("replica acls %+v %v", acls, err)
	}
	if _, err := s.InsertFilterRule(FilterRule{Action: FilterBlock, Pattern: "ads.example.com.", Source: FilterSourceManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFilterFeed(FilterFeed{Name: "hosts", Action: FilterBlock, URL: "https://example.com/hosts.txt", Sync: FilterSyncPeriodic}); err != nil {
		t.Fatal(err)
	}
	snap3, err := s.Snapshot()
	if err != nil || len(snap3.FilterRules) != 1 || len(snap3.FilterFeeds) != 1 {
		t.Fatalf("filter snapshot %+v %v", snap3, err)
	}
	if err := s2.ApplySnapshot(snap3); err != nil {
		t.Fatal(err)
	}
	fr, err := s2.ListFilterRules("", "")
	if err != nil || len(fr) != 1 || fr[0].Pattern != "ads.example.com." {
		t.Fatalf("replica filter rules %+v %v", fr, err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var wire Snapshot
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Users[0].PasswordHash != "x" {
		t.Fatalf("cluster JSON dropped password_hash: %s", raw)
	}
}

func TestRecordsRoundTripAndSnapshot(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	origin := "example.com."
	soa, err := rr(`example.com. 300 IN SOA ns.example.com. host.example.com. 1 3600 600 86400 60`)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := rr(`example.com. 300 IN NS ns.example.com.`)
	if err != nil {
		t.Fatal(err)
	}
	a, err := rr(`www.example.com. 60 IN A 192.0.2.10`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRecords(origin, "", []dns.RR{soa, ns, a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListRecords(origin, "")
	if err != nil || len(got) != 3 {
		t.Fatalf("public records %+v %v", got, err)
	}
	viewA, err := rr(`www.example.com. 60 IN A 10.1.2.3`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRecords(origin, "internal", []dns.RR{soa, ns, viewA}); err != nil {
		t.Fatal(err)
	}
	if !s.HasRecords(origin, "internal") {
		t.Fatal("expected view rows")
	}
	snap, err := s.Snapshot()
	if err != nil || len(snap.Records) != 6 {
		t.Fatalf("snapshot records %d %v", len(snap.Records), err)
	}
	s2, err := Open(filepath.Join(t.TempDir(), "replica.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.ApplySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	pub, err := s2.ListRecords(origin, "")
	if err != nil || len(pub) != 3 {
		t.Fatalf("replica public %+v %v", pub, err)
	}
	if err := s.RenameRecordView("internal", "office"); err != nil {
		t.Fatal(err)
	}
	if s.HasRecords(origin, "internal") || !s.HasRecords(origin, "office") {
		t.Fatal("rename view")
	}
	if err := s.SaveIXFR(origin, []byte("; ixfr\n")); err != nil {
		t.Fatal(err)
	}
	body, err := s.LoadIXFR(origin)
	if err != nil || string(body) != "; ixfr\n" {
		t.Fatalf("ixfr %q %v", body, err)
	}
}

func TestEnsureRecursionSeedsFromACLsOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.InsertACL(ACL{Name: "internal", Networks: []string{"10.0.0.0/8", "192.168.0.0/16"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRecursion(); err != nil {
		t.Fatal(err)
	}
	got := s.Recursion()
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if err := s.SetRecursion([]string{"172.16.0.0/12"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRecursion(); err != nil {
		t.Fatal(err)
	}
	got = s.Recursion()
	if len(got) != 1 || got[0] != "172.16.0.0/12" {
		t.Fatalf("ensure must not overwrite %+v", got)
	}
}



func rr(line string) (dns.RR, error) {
	return dns.NewRR(line)
}

func TestUpdateACL(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.InsertACL(ACL{Name: "internal", Networks: []string{"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertZoneView(ZoneView{Origin: "example.com.", ACL: "internal", Path: "/tmp/db.example.com.internal"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.UpdateACL("internal", "office", []string{"192.168.8.0/24"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "office" || len(got.Networks) != 1 || got.Networks[0] != "192.168.8.0/24" {
		t.Fatalf("%+v", got)
	}
	if _, err := s.GetACLByName("internal"); err == nil {
		t.Fatal("old name still present")
	}
	views, err := s.ListZoneViews()
	if err != nil || len(views) != 1 || views[0].ACL != "office" {
		t.Fatalf("zone_views after rename: %+v %v", views, err)
	}
	if _, err := s.InsertACL(ACL{Name: "internal", Networks: []string{"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateACL("internal", "office", nil, nil); err == nil {
		t.Fatal("rename onto existing ACL should fail")
	}
}
