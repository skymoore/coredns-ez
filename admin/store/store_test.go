package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
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
	if err := s2.ApplySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.GetUserByName("admin"); err != nil {
		t.Fatal("replica missing user")
	}
	got, err := s2.ListMembers()
	if err != nil || len(got) != 2 {
		t.Fatalf("replica members %+v %v", got, err)
	}
	if got[0].Role != MemberPrimary || got[0].Name != "ns1" || got[1].Name != "ns2" {
		t.Fatalf("replica roster %+v", got)
	}
	if _, err := s.InsertACL(ACL{Name: "internal", Networks: []string{"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	snap2, err := s.Snapshot()
	if err != nil || len(snap2.ACLs) != 1 {
		t.Fatalf("acl snapshot %+v %v", snap2.ACLs, err)
	}
	if err := s2.ApplySnapshot(snap2); err != nil {
		t.Fatal(err)
	}
	acls, err := s2.ListACLs()
	if err != nil || len(acls) != 1 || acls[0].Name != "internal" {
		t.Fatalf("replica acls %+v %v", acls, err)
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
