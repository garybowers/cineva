package store

import (
	"context"
	"testing"
	"time"
)

// Adopted sidecar fingerprints and forgotten mtimes are what an incremental
// scan reads back through FileStamps, so they are checked there; neither may
// touch updated_at, or every polling client would refetch the rows.
func TestScanStampWrites(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "A", Path: "/m/a.mkv", FileMtime: 100})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertItem(ctx, Item{LibraryID: lib.ID, Kind: KindMovie, Title: "B", Path: "/m/b.mkv", FileMtime: 200, SidecarStamp: 9})
	if err != nil {
		t.Fatal(err)
	}
	// updated_at has one-second resolution; make sure a write would show.
	if _, err := st.db.ExecContext(ctx, `UPDATE items SET updated_at = '2000-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	if err := st.SetSidecarStamps(ctx, map[string]int64{a.ID: 42}); err != nil {
		t.Fatalf("set sidecar stamps: %v", err)
	}
	if err := st.ForgetFileStamps(ctx, []string{b.ID}); err != nil {
		t.Fatalf("forget file stamps: %v", err)
	}
	stamps, err := st.FileStamps(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path         string
		mtime, stamp int64
	}{
		{"/m/a.mkv", 100, 42},
		{"/m/b.mkv", 0, 9},
	}
	for _, tc := range tests {
		got := stamps[tc.path]
		if got.FileMtime != tc.mtime || got.SidecarStamp != tc.stamp {
			t.Errorf("%s: mtime %d stamp %d, want %d %d", tc.path, got.FileMtime, got.SidecarStamp, tc.mtime, tc.stamp)
		}
	}
	var touched int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE updated_at != '2000-01-01 00:00:00'`).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched != 0 {
		t.Errorf("%d rows had updated_at changed by a stamp write", touched)
	}
}

// Only films and series without a provider id are listed for a retry, with
// the attempts recorded against them; the least recently tried come first.
func TestUnmatchedItems(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(it Item) Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	lost := upsert(Item{Kind: KindSeries, Title: "Lost Show", Path: "/tv/Lost Show", Year: 2001})
	film := upsert(Item{Kind: KindMovie, Title: "Odd Film", Path: "/tv/odd.mkv"})
	upsert(Item{Kind: KindSeries, Title: "Known", Path: "/tv/Known", TmdbID: 5})
	upsert(Item{Kind: KindSeason, ParentID: lost.ID, Title: "Season 01", Path: "/tv/Lost Show/Season 01"})

	at := time.Unix(1_700_000_000, 0)
	for range 2 {
		if err := st.RecordMatchFailure(ctx, lost.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.UnmatchedItems(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d unmatched, want 2: %+v", len(got), got)
	}
	// Never tried sorts first.
	if got[0].ID != film.ID || got[0].Attempts != 0 || !got[0].TriedAt.IsZero() {
		t.Errorf("first = %+v, want the untried film", got[0])
	}
	if got[1].ID != lost.ID || got[1].Attempts != 2 || !got[1].TriedAt.Equal(at) ||
		got[1].Kind != KindSeries || got[1].Year != 2001 || got[1].Path != "/tv/Lost Show" {
		t.Errorf("second = %+v, want the series tried twice", got[1])
	}
}
