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
	if err := s.CreateSession(ctx, "h1", kid.ID, "2099-01-01 00:00:00", "Firefox", "::1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "h2", kid.ID, "2000-01-01 00:00:00", "Old", "::1"); err != nil {
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
	k, err := s.CreateAPIKey(ctx, kid.ID, "phone", "kh1")
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
