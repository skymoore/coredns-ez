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
	g, err := s.BumpGeneration()
	if err != nil || g < 1 {
		t.Fatalf("gen %d %v", g, err)
	}
	snap, err := s.Snapshot()
	if err != nil || len(snap.Users) != 1 || len(snap.Zones) != 1 {
		t.Fatalf("%+v %v", snap, err)
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
