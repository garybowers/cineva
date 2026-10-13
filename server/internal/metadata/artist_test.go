package metadata

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The lookup follows MusicBrainz to Wikidata, then fetches the Commons
// picture and the Wikipedia summary it names; a weak or different-name
// match is no match.
func TestArtistInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("%s requested without a User-Agent", r.URL.Path)
		}
		switch r.URL.Path {
		case "/ws/2/artist/":
			q := r.URL.Query().Get("query")
			switch {
			case strings.Contains(q, "Portishead"):
				fmt.Fprint(w, `{"artists":[{"id":"mb-ph","name":"Portishead","score":100}]}`)
			case strings.Contains(q, "Nobody"):
				fmt.Fprint(w, `{"artists":[{"id":"mb-x","name":"Nobody Else","score":100},{"id":"mb-y","name":"Nobody","score":60}]}`)
			default:
				fmt.Fprint(w, `{"artists":[]}`)
			}
		case "/ws/2/artist/mb-ph":
			fmt.Fprint(w, `{"relations":[{"type":"official homepage","url":{"resource":"https://portishead.co.uk"}},
				{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q191352"}}]}`)
		case "/wiki/Special:EntityData/Q191352.json":
			fmt.Fprint(w, `{"entities":{"Q191352":{"claims":{"P18":[{"mainsnak":{"datavalue":{"value":"Portishead live.jpg"}}}]},
				"sitelinks":{"enwiki":{"title":"Portishead (band)"}}}}}`)
		case "/wiki/Special:FilePath/Portishead live.jpg":
			fmt.Fprint(w, "JPEG")
		case "/api/rest_v1/page/summary/Portishead_(band)":
			fmt.Fprint(w, `{"extract":"Portishead are an English band formed in 1991 in Bristol."}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	for _, base := range []*string{&mbBase, &wikidataBase, &wikipediaBase, &commonsBase} {
		old := *base
		*base = srv.URL
		t.Cleanup(func() { *base = old })
	}
	mb := NewMusicBrainz()
	ctx := context.Background()

	info, err := mb.ArtistInfo(ctx, "Portishead")
	if err != nil {
		t.Fatal(err)
	}
	if info.MBID != "mb-ph" || string(info.Image) != "JPEG" || !strings.HasPrefix(info.Bio, "Portishead are") ||
		info.BioURL != "https://en.wikipedia.org/wiki/Portishead_%28band%29" {
		t.Fatalf("info = %+v", info)
	}

	for _, name := range []string{"Nobody", "Unknown"} {
		if _, err := mb.ArtistInfo(ctx, name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}
