package metadata

import "testing"

func TestNormaliseTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Matrix", "matrix"},
		{"Matrix, The", "matrix"},
		{"WALL·E", "wall e"},
		{"Wall-E", "wall e"},
		{"Amélie", "amelie"},
		{"Spider-Man: No Way Home", "spider man no way home"},
		{"Spider Man  No Way Home", "spider man no way home"},
		{"A Clockwork Orange", "clockwork orange"},
		{"Æon Flux", "aeon flux"},
		{"  Léon   ", "leon"},
	}
	for _, tc := range tests {
		if got := normaliseTitle(tc.in); got != tc.want {
			t.Errorf("normaliseTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBestMatch(t *testing.T) {
	tests := []struct {
		name    string
		results []VideoResult
		title   string
		year    int
		wantID  int
	}{
		{
			// The case this exists for: TMDB puts the popular namesake
			// first, but the filename names a different year.
			name: "exact year beats TMDB's relevance order",
			results: []VideoResult{
				{TmdbID: 1, Title: "The Thing", Year: 2011},
				{TmdbID: 2, Title: "The Thing", Year: 1982},
			},
			title: "The Thing", year: 1982, wantID: 2,
		},
		{
			name: "exact title beats a near one at the same year",
			results: []VideoResult{
				{TmdbID: 1, Title: "Dune: Part Two", Year: 2024},
				{TmdbID: 2, Title: "Dune", Year: 2024},
			},
			title: "Dune", year: 2024, wantID: 2,
		},
		{
			name: "a year either side still matches",
			results: []VideoResult{
				{TmdbID: 1, Title: "Wrong Film", Year: 2020},
				{TmdbID: 2, Title: "Late Release", Year: 2021},
			},
			title: "Late Release", year: 2020, wantID: 2,
		},
		{
			name: "no year falls back to the title",
			results: []VideoResult{
				{TmdbID: 1, Title: "Something Else", Year: 1999},
				{TmdbID: 2, Title: "Heat", Year: 1995},
			},
			title: "Heat", wantID: 2,
		},
		{
			name: "equal scores keep TMDB's order",
			results: []VideoResult{
				{TmdbID: 1, Title: "Ambiguous", Year: 2000},
				{TmdbID: 2, Title: "Ambiguous", Year: 2000},
			},
			title: "Ambiguous", year: 2000, wantID: 1,
		},
		{
			name: "punctuation and articles do not block an exact match",
			results: []VideoResult{
				{TmdbID: 1, Title: "Spider-Man", Year: 2002},
				{TmdbID: 2, Title: "Spider-Man: No Way Home", Year: 2021},
			},
			title: "Spider Man No Way Home", year: 2021, wantID: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bestMatch(tc.results, tc.title, tc.year); got.TmdbID != tc.wantID {
				t.Errorf("bestMatch picked %d (%q %d), want %d", got.TmdbID, got.Title, got.Year, tc.wantID)
			}
		})
	}
}

func TestCertificationsAndRegionChoice(t *testing.T) {
	mk := func(pairs ...[2]string) *tmdbResult {
		r := &tmdbResult{}
		r.ReleaseDates = &struct {
			Results []struct {
				Country      string `json:"iso_3166_1"`
				ReleaseDates []struct {
					Certification string `json:"certification"`
					Type          int    `json:"type"`
				} `json:"release_dates"`
			} `json:"results"`
		}{}
		for _, p := range pairs {
			var entry struct {
				Country      string `json:"iso_3166_1"`
				ReleaseDates []struct {
					Certification string `json:"certification"`
					Type          int    `json:"type"`
				} `json:"release_dates"`
			}
			entry.Country = p[0]
			entry.ReleaseDates = append(entry.ReleaseDates, struct {
				Certification string `json:"certification"`
				Type          int    `json:"type"`
			}{Certification: p[1]})
			r.ReleaseDates.Results = append(r.ReleaseDates.Results, entry)
		}
		return r
	}
	tests := []struct {
		name   string
		in     *tmdbResult
		region string
		want   string
	}{
		{"picks the asked-for region", mk([2]string{"US", "R"}, [2]string{"GB", "15"}), "GB", "15"},
		{"a different region gets its own board", mk([2]string{"US", "R"}, [2]string{"DE", "16"}), "DE", "16"},
		{"lower case region still matches", mk([2]string{"GB", "12A"}), "gb", "12A"},
		{"falls back to US when unclassified locally", mk([2]string{"US", "PG-13"}, [2]string{"FR", "12"}), "GB", "PG-13"},
		{"no region means US", mk([2]string{"US", "R"}, [2]string{"GB", "15"}), "", "R"},
		{"nothing to fall back to", mk([2]string{"FR", "12"}), "GB", ""},
		{"blank certifications are skipped", mk([2]string{"GB", ""}, [2]string{"US", "R"}), "GB", "R"},
		{"absent release data", &tmdbResult{}, "GB", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := VideoResult{Certifications: tc.in.certifications()}
			if got := v.CertificationFor(tc.region); got != tc.want {
				t.Errorf("CertificationFor(%q) = %q, want %q", tc.region, got, tc.want)
			}
		})
	}
}
