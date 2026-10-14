package store

import (
	"context"
	"errors"
	"testing"
)

// A device is the screen: it outlives its sessions, holds one at a time,
// and goes only when forgotten or long unused.
func TestDevices(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	dad, err := s.CreateUser(ctx, "dad", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := s.CreateUser(ctx, "kid", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	tv, err := s.CreateDevice(ctx, "secret-hash", "Chrome · Android", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeviceBySecret(ctx, "secret-hash"); err != nil || got.ID != tv.ID {
		t.Fatalf("by secret = %+v, %v", got, err)
	}
	if _, err := s.DeviceBySecret(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty secret matched a device: %v", err)
	}

	// Dad signs in on the TV and names it; the session takes its id.
	sess, err := s.SignInOnDevice(ctx, tv.ID, "dad-token", dad.ID, "2099-01-01 00:00:00", "Chrome · Android", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != tv.ID {
		t.Fatalf("session id %q, want the device's %q", sess.ID, tv.ID)
	}
	if err := s.SetSessionName(ctx, "dad-token", "Living-room TV"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceArea(ctx, tv.ID, "living_room"); err != nil {
		t.Fatal(err)
	}

	// The kid chooses themselves on it: Dad's session there ends, and the
	// screen keeps its name and room.
	sess, err = s.SignInOnDevice(ctx, tv.ID, "kid-token", kid.ID, "2099-01-01 00:00:00", "Chrome · Android", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Name != "Living-room TV" || sess.AreaID != "living_room" || sess.UserID != kid.ID {
		t.Fatalf("kid's session = %+v", sess)
	}
	if _, _, err := s.SessionByHash(ctx, "dad-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dad is still signed in on the TV: %v", err)
	}
	if d, _ := s.Device(ctx, tv.ID); d.UserID != kid.ID {
		t.Fatalf("device says %q is signed in, want the kid", d.UserID)
	}

	// Signing out keeps the device.
	if err := s.DeleteSession(ctx, "kid-token"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Device(ctx, tv.ID)
	if err != nil || d.Name != "Living-room TV" || d.AreaID != "living_room" || d.UserID != "" {
		t.Fatalf("after signing out = %+v, %v", d, err)
	}

	// Recently used, so pruning leaves it; forgetting removes it.
	if n, err := s.PruneDevices(ctx); err != nil || n != 0 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if _, err := s.SignInOnDevice(ctx, tv.ID, "dad-again", dad.ID, "2099-01-01 00:00:00", "Chrome", "::1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDevice(ctx, tv.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionByHash(ctx, "dad-again"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forgetting the device left its session: %v", err)
	}

	// A device long unused with nobody on it is pruned.
	old, err := s.CreateDevice(ctx, "", "Old laptop", "::1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE devices SET last_seen_at = datetime('now', '-200 days') WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneDevices(ctx); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v; want the old laptop", n, err)
	}
}
