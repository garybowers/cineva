package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestEnsureIdentityIsStable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.EnsureIdentity(ctx)
	if err != nil {
		t.Fatalf("first EnsureIdentity: %v", err)
	}
	second, err := s.EnsureIdentity(ctx)
	if err != nil {
		t.Fatalf("second EnsureIdentity: %v", err)
	}
	if !bytes.Equal(first.PublicKey, second.PublicKey) {
		t.Fatal("identity changed between calls; it must persist")
	}
}

func TestCreateLibraryValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		libName   string
		mediaType MediaType
		paths     []string
		wantErr   bool
	}{
		{"valid movies", "Movies", MediaMovies, []string{"/media/movies"}, false},
		{"valid tv", "TV", MediaTV, []string{"/media/tv"}, false},
		{"empty name", "", MediaMovies, []string{"/media/x"}, true},
		{"bad media type", "Photos", MediaType("photos"), []string{"/media/photos"}, true},
		{"no paths", "Empty", MediaMusic, nil, true},
		{"duplicate name", "Movies", MediaMovies, []string{"/media/other"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateLibrary(ctx, tt.libName, tt.mediaType, tt.paths)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateLibrary(%q) error = %v, wantErr %v", tt.libName, err, tt.wantErr)
			}
		})
	}
}

func TestLibraryRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateLibrary(ctx, "Kids Movies", MediaMovies, []string{"/media/kids", "/media/more-kids"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Source != "local" {
		t.Errorf("source = %q, want local", created.Source)
	}
	if len(created.Paths) != 2 {
		t.Errorf("paths = %v, want 2 entries", created.Paths)
	}

	libs, err := s.Libraries(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(libs) != 1 || libs[0].ID != created.ID {
		t.Fatalf("list = %+v, want the created library", libs)
	}

	if err := s.DeleteLibrary(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteLibrary(ctx, created.ID); err == nil {
		t.Fatal("second delete should report not found")
	}
	if libs, _ = s.Libraries(ctx); len(libs) != 0 {
		t.Fatalf("libraries after delete = %+v, want empty", libs)
	}
}

// Guards the resume query against itemColumns/scan-list drift: adding a
// column to the items table broke ContinueWatching silently until it was
// queried, because its row scan is written out by hand.
func TestContinueWatchingSurvivesSchema(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	item, err := s.UpsertItem(ctx, Item{
		LibraryID: lib.ID, Kind: KindMovie, Title: "Film", Path: "/m/film.mkv", RuntimeSecs: 5400,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := s.SetProgress(ctx, LocalUser, item.ID, 600, item.RuntimeSecs); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	got, err := s.ContinueWatching(ctx, LocalUser, 10)
	if err != nil {
		t.Fatalf("continue watching: %v", err)
	}
	if len(got) != 1 || got[0].ID != item.ID || got[0].PositionSecs != 600 {
		t.Fatalf("continue watching = %+v, want the single in-progress item at 600s", got)
	}
}

func TestPlaysByDayHasContinuousSpine(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	lib, err := s.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	item, err := s.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "Film", Path: "/m/f.mkv"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordPlay(ctx, LocalUser, item.ID, KindMovie); err != nil {
			t.Fatalf("record play: %v", err)
		}
	}
	days, err := s.PlaysByDay(ctx, 7)
	if err != nil {
		t.Fatalf("plays by day: %v", err)
	}
	if len(days) != 7 {
		t.Fatalf("got %d days, want 7 (empty days must be present)", len(days))
	}
	today := days[6]
	if today.Movies != 3 || today.Episodes != 0 || today.Tracks != 0 {
		t.Fatalf("today = %+v, want 3 movie plays", today)
	}
	for _, d := range days[:6] {
		if d.Movies+d.Episodes+d.Tracks != 0 {
			t.Fatalf("unexpected plays on %s: %+v", d.Date, d)
		}
	}
}

func TestAuditLogFilterAndOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, a := range []string{"scan.start", "play.start", "scan.completed"} {
		if err := s.RecordAudit(ctx, AuditEntry{UserID: "local", Action: a, TargetTitle: a}); err != nil {
			t.Fatalf("record %s: %v", a, err)
		}
	}
	all, err := s.AuditLog(ctx, 10, 0, "")
	if err != nil || len(all) != 3 || all[0].Action != "scan.completed" {
		t.Fatalf("all = %+v, %v; want 3 newest-first", all, err)
	}
	scans, err := s.AuditLog(ctx, 10, 0, "scan.")
	if err != nil || len(scans) != 2 {
		t.Fatalf("scan. filter = %+v, %v; want 2", scans, err)
	}
}
