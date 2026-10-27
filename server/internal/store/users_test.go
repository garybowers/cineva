package store

import (
	"context"
	"errors"
	"testing"
)

func TestUsersSessionsGrants(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("fresh store has %d users", n)
	}
	admin, err := s.CreateUser(ctx, "gary", "", "hash1", RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if admin.DisplayName != "gary" || !admin.IsAdmin() {
		t.Fatalf("admin = %+v", admin)
	}
	if _, err := s.CreateUser(ctx, "GARY", "", "x", RoleUser); !errors.Is(err, ErrUsernameUsed) {
		t.Fatalf("case-insensitive duplicate username accepted: %v", err)
	}
	if _, err := s.CreateUser(ctx, "bad", "", "x", "root"); err == nil {
		t.Fatal("invalid role accepted")
	}
	kid, err := s.CreateUser(ctx, "kid", "The Kid", "hash2", RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Sessions: expired ones do not resolve; delete works.
	if _, err := s.CreateSession(ctx, "h1", kid.ID, "2099-01-01 00:00:00", "Firefox", "::1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, "h2", kid.ID, "2000-01-01 00:00:00", "Old", "::1", ""); err != nil {
		t.Fatal(err)
	}
	if _, u, err := s.SessionByHash(ctx, "h1"); err != nil || u.ID != kid.ID {
		t.Fatalf("live session: %v %+v", err, u)
	}
	if _, _, err := s.SessionByHash(ctx, "h2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session resolved: %v", err)
	}
	if err := s.DeleteUserSessions(ctx, kid.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionByHash(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("session survived DeleteUserSessions")
	}

	// API keys.
	k, err := s.CreateAPIKey(ctx, kid.ID, "phone", "kh1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, u, err := s.APIKeyByHash(ctx, "kh1"); err != nil || u.ID != kid.ID {
		t.Fatalf("api key lookup: %v", err)
	}
	if err := s.DeleteAPIKey(ctx, admin.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("another user could delete the key")
	}
	if err := s.DeleteAPIKey(ctx, kid.ID, k.ID); err != nil {
		t.Fatal(err)
	}

	// Placeholder state adoption.
	lib, err := s.CreateLibrary(ctx, "Kids", MediaMovies, []string{"/k"})
	if err != nil {
		t.Fatal(err)
	}
	item, _ := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "F", Path: "/k/f.mkv", RuntimeSecs: 100})
	if _, err := s.SetProgress(ctx, LocalUser, item.ID, 30, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptLocalState(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.PlayStateFor(ctx, admin.ID, item.ID); ps.PositionSecs != 30 {
		t.Fatalf("admin did not inherit local play state: %+v", ps)
	}

	// Deleting a user cascades their sessions.
	if err := s.DeleteUser(ctx, kid.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserByID(ctx, kid.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted user still found")
	}
}

// A session is a device: listed (live ones only), named, signed out alone
// or with every other, holding a queue — and always scoped to its owner.
func TestSessionDevices(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	gary, err := s.CreateUser(ctx, "gary", "", "h", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := s.CreateUser(ctx, "kid", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	laptop, err := s.CreateSession(ctx, "laptop", gary.ID, "2099-01-01 00:00:00", "Firefox · Linux", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	tv, err := s.CreateSession(ctx, "tv", gary.ID, "2099-01-01 00:00:00", "Chrome · Android", "::1", "Living-room TV")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, "old", gary.ID, "2000-01-01 00:00:00", "Old", "::1", ""); err != nil {
		t.Fatal(err)
	}
	kidPhone, err := s.CreateSession(ctx, "kidphone", kid.ID, "2099-01-01 00:00:00", "Safari · iOS", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	if laptop.ID == "" || laptop.ID == tv.ID || tv.Name != "Living-room TV" {
		t.Fatalf("sessions = %+v, %+v: want distinct ids and the name kept", laptop, tv)
	}

	list, err := s.UserSessions(ctx, gary.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("live sessions = %d (%v), want 2 — expired and other users' left out", len(list), err)
	}
	if _, err := s.UserSession(ctx, gary.ID, kidPhone.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reached another user's device: %v", err)
	}
	if err := s.DeleteUserSession(ctx, gary.ID, kidPhone.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("signed out another user's device: %v", err)
	}

	if err := s.SetSessionName(ctx, "laptop", "  Study laptop "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionQueue(ctx, "laptop", SessionQueue{ItemIDs: []string{"a", "b", "c"}, Index: 7}); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserSession(ctx, gary.ID, laptop.ID)
	if err != nil {
		t.Fatal(err)
	}
	q, err := got.ParsedQueue()
	if err != nil || got.Name != "Study laptop" || len(q.Entries) != 3 || q.Index != 2 || q.Entries[2].Ord != 2 || q.Repeat != "off" {
		t.Fatalf("session = %+v, queue %+v (%v): want the trimmed name and the index clamped to 2", got, q, err)
	}
	if err := s.SetSessionQueue(ctx, "laptop", SessionQueue{ItemIDs: make([]string, MaxQueueItems+1)}); err == nil {
		t.Fatal("accepted an oversized queue")
	}
	// A heartbeat moves the position on, but only for the entry the queue
	// is on; the whole record survives with it.
	if err := s.SetSessionQueue(ctx, "laptop", SessionQueue{Entries: []QueueEntry{{ID: "a", Ord: 1}, {ID: "b", Ord: 0}}, Index: 1,
		Shuffle: true, Repeat: "all", Source: &QueueSource{Kind: "album", ID: "al", Title: "AM"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionQueuePosition(ctx, "laptop", "a", 41.5); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionQueuePosition(ctx, "laptop", "b", 12); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserSession(ctx, gary.ID, laptop.ID)
	q, err = got.ParsedQueue()
	if err != nil || q.PositionSecs != 12 || !q.Shuffle || q.Repeat != "all" || q.Source == nil || q.Source.Title != "AM" || q.CurrentID() != "b" || q.UpdatedAt == "" {
		t.Fatalf("queue after heartbeats = %+v (%v)", q, err)
	}
	// The shape before migration 0046 reads as entries in order.
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET queue = '{"itemIds":["x","y"],"index":1}' WHERE token_hash = 'laptop'`); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserSession(ctx, gary.ID, laptop.ID)
	if q, err := got.ParsedQueue(); err != nil || len(q.Entries) != 2 || q.Entries[1].ID != "y" || q.Entries[1].Ord != 1 || q.Index != 1 || q.ItemIDs != nil {
		t.Fatalf("old shape = %+v (%v)", q, err)
	}

	if n, err := s.DeleteOtherSessions(ctx, gary.ID, "laptop"); err != nil || n != 2 {
		t.Fatalf("deleted %d others (%v), want 2 (the TV and the expired one)", n, err)
	}
	if list, _ := s.UserSessions(ctx, gary.ID); len(list) != 1 || list[0].ID != laptop.ID {
		t.Fatalf("after signing out the rest: %+v", list)
	}
	if _, err := s.UserSession(ctx, kid.ID, kidPhone.ID); err != nil {
		t.Fatalf("another user's device went too: %v", err)
	}
}

func TestAvatarFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "gary", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAvatar(ctx, u.ID, "/artwork/avatar-x.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAvatarColour(ctx, u.ID, "chart-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAvatarColour(ctx, u.ID, "#ff0000"); err == nil {
		t.Fatal("accepted a colour that is not a theme token")
	}
	if err := s.SetAvatar(ctx, "nobody", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set an avatar on a missing user: %v", err)
	}
	got, err := s.UserByID(ctx, u.ID)
	if err != nil || got.Avatar != "/artwork/avatar-x.jpg" || got.AvatarColour != "chart-2" {
		t.Fatalf("user = %+v (%v)", got, err)
	}
}

// A queue that is, or ends in, a mix keeps that with it (server/docs/mix.md).
func TestSessionQueueMix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "mixer", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(ctx, "mixhash", u.ID, "2099-01-01 00:00:00", "test", "::1", ""); err != nil {
		t.Fatal(err)
	}
	q := SessionQueue{Entries: []QueueEntry{{ID: "a"}, {ID: "b"}}, Index: 1,
		Mix: &QueueMix{SeedID: "seed", Kind: "track", Title: "Dancing Queen", After: "b", From: 1, Basis: []string{"genre"}}}
	if err := s.SetSessionQueue(ctx, "mixhash", q); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.SessionByHash(ctx, "mixhash")
	if err != nil {
		t.Fatal(err)
	}
	back, err := got.ParsedQueue()
	if err != nil {
		t.Fatal(err)
	}
	if back.Mix == nil || back.Mix.SeedID != "seed" || back.Mix.Title != "Dancing Queen" || back.Mix.From != 1 || len(back.Mix.Basis) != 1 {
		t.Fatalf("mix did not round-trip: %+v", back.Mix)
	}
}
