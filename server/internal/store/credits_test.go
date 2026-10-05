package store

import (
	"context"
	"fmt"
	"testing"
)

func TestPersonPageFilmography(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Movies", MediaMovies, []string{"/m"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title string, year int) Item {
		it, err := st.UpsertItem(ctx, Item{
			LibraryID: lib.ID, Kind: KindMovie, Title: title, Year: year,
			Path: fmt.Sprintf("/m/%s.mkv", title),
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	goodfellas := add("Goodfellas", 1990)
	casino := add("Casino", 1995)
	irishman := add("The Irishman", 2019)

	// Directed two, and both wrote and directed a third — which should read
	// as one row with the jobs joined, not two rows.
	if err := st.ReplaceCredits(ctx, goodfellas.ID, nil, []Person{{TmdbID: 1032, Name: "Martin Scorsese", Role: "Director"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceCredits(ctx, casino.ID, nil, []Person{
		{TmdbID: 1032, Name: "Martin Scorsese", Role: "Director"},
		{TmdbID: 1032, Name: "Martin Scorsese", Role: "Writer"},
	}); err != nil {
		t.Fatal(err)
	}
	// Also acted in one he directed: two credits on one film, deliberately.
	if err := st.ReplaceCredits(ctx, irishman.ID,
		[]Person{{TmdbID: 1032, Name: "Martin Scorsese", Role: "Himself"}},
		[]Person{{TmdbID: 1032, Name: "Martin Scorsese", Role: "Director"}}); err != nil {
		t.Fatal(err)
	}

	// People are created by ReplaceCredits, so read the id back off a film.
	_, crew, err := st.CreditsForItem(ctx, goodfellas.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(crew) == 0 {
		t.Fatal("no crew recorded")
	}
	pid := crew[0].ID

	prof, err := st.PersonPage(ctx, pid)
	if err != nil {
		t.Fatalf("person page: %v", err)
	}
	if prof.Name != "Martin Scorsese" {
		t.Errorf("name = %q", prof.Name)
	}
	// Three films, four credits: The Irishman contributes both a cast and a
	// crew row because acting in one you directed is two distinct facts.
	if len(prof.Credits) != 4 {
		for _, c := range prof.Credits {
			t.Logf("  %s (%d) %s %q", c.Title, c.Year, c.CreditKind, c.Role)
		}
		t.Fatalf("got %d credits, want 4", len(prof.Credits))
	}
	if prof.CastCount != 1 || prof.CrewCount != 3 {
		t.Errorf("counts = cast %d crew %d, want 1 and 3", prof.CastCount, prof.CrewCount)
	}
	if prof.FirstYear != 1990 || prof.LastYear != 2019 {
		t.Errorf("years = %d–%d, want 1990–2019", prof.FirstYear, prof.LastYear)
	}
	// Newest first.
	if prof.Credits[0].Year != 2019 {
		t.Errorf("first credit is %d, want the newest (2019)", prof.Credits[0].Year)
	}
	// Two jobs on one film collapse into one row.
	for _, c := range prof.Credits {
		if c.Title == "Casino" {
			if c.Role != "Director · Writer" && c.Role != "Writer · Director" {
				t.Errorf("Casino role = %q, want both jobs joined", c.Role)
			}
		}
	}
	// "Director" is the most frequent job, so it leads the summary.
	if len(prof.Jobs) == 0 || prof.Jobs[0] != "Director" {
		t.Errorf("jobs = %v, want Director first", prof.Jobs)
	}
}
