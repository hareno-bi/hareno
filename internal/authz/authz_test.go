package authz

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rosschiu/kiban/sdk"
)

func setup() (*Authz, *Fake, *time.Time) {
	f := &Fake{Companies: map[string][]sdk.Company{"alice": {{ID: "c1", Code: "ALPHA", IsActive: true}}}}
	now := time.Unix(0, 0)
	a := &Authz{K: f, Module: "hareno", Feature: "hareno.use", AllowTTL: 30 * time.Second, DenyTTL: 5 * time.Second,
		Now: func() time.Time { return now }}
	return a, f, &now
}

var report = Item{"report", "r1", "viewer"}

func TestCacheTTLs(t *testing.T) {
	a, f, now := setup()
	ctx := context.Background()
	ask := func() bool {
		ok, err := a.Allowed(ctx, "alice", "alice", "c1", []Item{report})
		if err != nil {
			t.Fatal(err)
		}
		return ok[report]
	}
	f.Grant("c1", "report", "r1", "viewer", "alice")
	if !ask() {
		t.Fatal("granted access must be allowed")
	}
	f.Revoke("c1", "report", "r1", "viewer", "alice")
	*now = now.Add(29 * time.Second)
	if !ask() || f.Calls != 1 {
		t.Fatalf("an allow is cached for 30 s (calls %d)", f.Calls)
	}
	*now = now.Add(2 * time.Second)
	if ask() {
		t.Fatal("revocation must apply after 30 s")
	}
	f.Grant("c1", "report", "r1", "viewer", "alice")
	*now = now.Add(4 * time.Second)
	if ask() {
		t.Fatal("a denial is cached for 5 s")
	}
	*now = now.Add(2 * time.Second)
	if !ask() {
		t.Fatal("a new grant applies after the 5 s denial")
	}
}

func TestErrorsDenyAndAreNotCached(t *testing.T) {
	a, f, _ := setup()
	ctx := context.Background()
	f.Grant("c1", "report", "r1", "viewer", "alice")
	f.Err = errors.New("kiban down")
	if _, err := a.Allowed(ctx, "alice", "alice", "c1", []Item{report}); err == nil {
		t.Fatal("a Kiban error must be returned, never an allow")
	}
	f.Err = nil
	ok, err := a.Allowed(ctx, "alice", "alice", "c1", []Item{report})
	if err != nil || !ok[report] {
		t.Fatalf("after recovery: %v %v", ok, err)
	}
}

func TestBatchesOf100(t *testing.T) {
	a, f, _ := setup()
	var items []Item
	for i := range 250 {
		items = append(items, Item{"report", fmt.Sprint(i), "viewer"})
	}
	f.Grant("c1", "report", "249", "viewer", "alice")
	ok, err := a.Allowed(context.Background(), "alice", "alice", "c1", items)
	if err != nil || f.Calls != 3 || !ok[items[249]] || ok[items[0]] {
		t.Fatalf("calls %d, err %v", f.Calls, err)
	}
}

func TestCompaniesAndTokens(t *testing.T) {
	a, _, _ := setup()
	ctx := context.Background()
	if _, err := a.User(ctx, "bad"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("bad token: %v", err)
	}
	if _, err := a.User(ctx, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("no token: %v", err)
	}
	if id, err := a.Company(ctx, "alice", "alice", "ALPHA"); id != "c1" || err != nil {
		t.Errorf("ALPHA: %q %v", id, err)
	}
	if _, err := a.Company(ctx, "alice", "alice", "BETA"); !errors.Is(err, ErrNoCompany) {
		t.Errorf("BETA: %v", err)
	}
}

func TestFreshSkipsTheCache(t *testing.T) {
	a, f, _ := setup()
	ctx := context.Background()
	f.Grant("c1", "report", "r1", "viewer", "alice")
	if ok, _ := a.Allowed(ctx, "alice", "alice", "c1", []Item{report}); !ok[report] {
		t.Fatal("granted access must be allowed")
	}
	f.Revoke("c1", "report", "r1", "viewer", "alice")
	if ok, err := a.Fresh(ctx, "alice", "alice", "c1", []Item{report}); err != nil || ok[report] {
		t.Fatalf("Fresh must see the revocation at once: %v %v", ok, err)
	}
	if ok, _ := a.Allowed(ctx, "alice", "alice", "c1", []Item{report}); ok[report] {
		t.Fatal("Fresh's answer must replace the cached allow")
	}
}
