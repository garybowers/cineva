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
