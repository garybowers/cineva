package store

import (
	"context"
	"errors"
	"testing"
)

// Reports are listed with what they are about and how many others share
// their season; one user reporting twice counts once; resolving closes one.
func TestSegmentReports(t *testing.T) {
	f := newRedetectFixture(t)
	s := f.s
	ctx := context.Background()
	user := func(name string) User {
		t.Helper()
		u, err := s.CreateUser(ctx, name, "", "hash", RoleUser)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	ann, bob, cat := user("ann"), user("bob"), user("cat")
	report := func(u User, it Item, kind string, at float64, note string) SegmentReport {
		t.Helper()
		r, err := s.CreateSegmentReport(ctx, SegmentReport{ItemID: it.ID, UserID: u.ID, Kind: kind, PositionSecs: at, Note: note})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	first := report(ann, f.s1e1, SegmentIntro, 95, "skips into the scene")
	if first.SeriesTitle != "Show" || first.SeasonID != f.s1.ID || first.SeasonNum != 1 ||
		first.EpisodeNum != 1 || first.UserName != "ann" || first.SeasonOpen != 1 || first.ItemKind != KindEpisode {
		t.Fatalf("report = %+v", first)
	}
	// Ann again on the same episode and kind: the one report, updated.
	again := report(ann, f.s1e1, SegmentIntro, 97, "")
	if again.ID != first.ID || again.PositionSecs != 97 || again.Note != "" {
		t.Fatalf("second report from the same person = %+v, want %s updated", again, first.ID)
	}
	report(bob, f.s1e2, SegmentIntro, 30, "")
	report(cat, f.s1e1, SegmentCredits, 1300, "")
	film := report(bob, f.film, SegmentCredits, 6000, "")
	if film.SeasonID != "" || film.SeasonOpen != 1 || film.ItemKind != KindMovie {
		t.Fatalf("film report = %+v", film)
	}
	if _, err := s.CreateSegmentReport(ctx, SegmentReport{ItemID: f.film.ID, UserID: bob.ID, Kind: "recap"}); err == nil {
		t.Fatal("a report of an unknown kind was accepted")
	}

	open, err := s.SegmentReports(ctx, true, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 4 {
		t.Fatalf("%d open reports, want 4", len(open))
	}
	for _, r := range open {
		want := 3 // three on season 1
		if r.ItemID == f.film.ID {
			want = 1
		}
		if r.SeasonOpen != want {
			t.Errorf("%s %s: seasonOpen = %d, want %d", r.Title, r.Kind, r.SeasonOpen, want)
		}
	}

	if err := s.ResolveSegmentReport(ctx, first.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveSegmentReport(ctx, first.ID, "admin"); err != nil {
		t.Fatalf("resolving twice = %v, want no error", err)
	}
	if err := s.ResolveSegmentReport(ctx, "nope", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolving a missing report = %v, want ErrNotFound", err)
	}
	open, _ = s.SegmentReports(ctx, true, 50)
	if len(open) != 3 {
		t.Fatalf("%d open after resolving one, want 3", len(open))
	}
	all, _ := s.SegmentReports(ctx, false, 50)
	if len(all) != 4 {
		t.Fatalf("%d reports in all, want 4", len(all))
	}
	got, err := s.SegmentReport(ctx, first.ID)
	if err != nil || got.ResolvedAt == "" || got.ResolvedBy != "admin" {
		t.Fatalf("resolved report = %+v, %v", got, err)
	}
	// Reporting again after a resolution opens a new report.
	if r := report(ann, f.s1e1, SegmentIntro, 90, ""); r.ID == first.ID {
		t.Fatal("a report after resolution reopened the old one")
	}
}

func TestSegmentEditor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "ed", "", "hash", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if u.SegmentEditor || u.CanEditSegments() {
		t.Fatalf("new user = %+v, want no editing", u)
	}
	if err := s.SetSegmentEditor(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if u, err = s.UserByID(ctx, u.ID); err != nil || !u.SegmentEditor || !u.CanEditSegments() {
		t.Fatalf("after granting = %+v, %v", u, err)
	}
	if err := s.SetSegmentEditor(ctx, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user = %v, want ErrNotFound", err)
	}
	if !(User{Role: RoleAdmin}).CanEditSegments() {
		t.Fatal("an admin cannot edit segments")
	}
}
