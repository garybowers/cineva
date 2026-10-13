package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ArtistInfo is what can be found about an artist beyond their files: a
// picture and a short biography, either of which may be missing.
type ArtistInfo struct {
	MBID string
	// Image is the picture's bytes, sized for a page header.
	Image []byte
	// Bio is the opening of the artist's Wikipedia article, and BioURL
	// the article, which its licence (CC BY-SA) asks to be credited.
	Bio    string
	BioURL string
}

// ArtistProvider looks artists up. Separate from MusicProvider so a
// provider for albums alone stays complete without it.
type ArtistProvider interface {
	ArtistInfo(ctx context.Context, name string) (ArtistInfo, error)
}

var _ ArtistProvider = (*MusicBrainz)(nil)

// Where the artist lookup goes; overridden in tests.
var (
	mbBase        = "https://musicbrainz.org"
	wikidataBase  = "https://www.wikidata.org"
	wikipediaBase = "https://en.wikipedia.org"
	commonsBase   = "https://commons.wikimedia.org"
)

// ArtistInfo finds an artist on MusicBrainz by name and follows its link
// to Wikidata, which names the artist's picture on Wikimedia Commons and
// their English Wikipedia article. Five requests at most, two of them to
// MusicBrainz under its rate limit. ErrNotFound when MusicBrainz has no
// confident match; a match with no Wikidata link is returned with only
// its id, and a picture or biography that cannot be fetched is left out.
func (m *MusicBrainz) ArtistInfo(ctx context.Context, name string) (ArtistInfo, error) {
	var search struct {
		Artists []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Score int    `json:"score"`
		} `json:"artists"`
	}
	q := fmt.Sprintf(`artist:%q`, name)
	if err := m.get(ctx, mbBase+"/ws/2/artist/?fmt=json&limit=3&query="+url.QueryEscape(q), &search); err != nil {
		return ArtistInfo{}, err
	}
	var mbid string
	for _, a := range search.Artists {
		// Only a confident match of the same name: a picture of the wrong
		// band is worse than none.
		if a.Score >= 90 && strings.EqualFold(a.Name, name) {
			mbid = a.ID
			break
		}
	}
	if mbid == "" {
		return ArtistInfo{}, ErrNotFound
	}
	out := ArtistInfo{MBID: mbid}

	var artist struct {
		Relations []struct {
			Type string `json:"type"`
			URL  struct {
				Resource string `json:"resource"`
			} `json:"url"`
		} `json:"relations"`
	}
	if err := m.get(ctx, mbBase+"/ws/2/artist/"+mbid+"?fmt=json&inc=url-rels", &artist); err != nil {
		return out, err
	}
	var qid string
	for _, r := range artist.Relations {
		if r.Type == "wikidata" {
			qid = r.URL.Resource[strings.LastIndex(r.URL.Resource, "/")+1:]
			break
		}
	}
	if !strings.HasPrefix(qid, "Q") {
		return out, nil
	}

	var entity struct {
		Entities map[string]struct {
			Claims map[string][]struct {
				Mainsnak struct {
					Datavalue struct {
						Value json.RawMessage `json:"value"`
					} `json:"datavalue"`
				} `json:"mainsnak"`
			} `json:"claims"`
			Sitelinks map[string]struct {
				Title string `json:"title"`
			} `json:"sitelinks"`
		} `json:"entities"`
	}
	if err := m.getWikimedia(ctx, wikidataBase+"/wiki/Special:EntityData/"+qid+".json", &entity); err != nil {
		return out, err
	}
	ent, ok := entity.Entities[qid]
	if !ok {
		return out, nil
	}
	// P18 is Wikidata's "image" property.
	if imgs := ent.Claims["P18"]; len(imgs) > 0 {
		var file string
		if json.Unmarshal(imgs[0].Mainsnak.Datavalue.Value, &file) == nil && file != "" {
			img, err := m.fetchWikimedia(ctx, commonsBase+"/wiki/Special:FilePath/"+url.PathEscape(file)+"?width=800")
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			// A picture that will not download leaves the biography still
			// worth having.
			if err == nil {
				out.Image = img
			}
		}
	}
	if link, ok := ent.Sitelinks["enwiki"]; ok && link.Title != "" {
		page := url.PathEscape(strings.ReplaceAll(link.Title, " ", "_"))
		var summary struct {
			Extract string `json:"extract"`
		}
		err := m.getWikimedia(ctx, wikipediaBase+"/api/rest_v1/page/summary/"+page, &summary)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if err == nil && summary.Extract != "" {
			out.Bio = summary.Extract
			out.BioURL = "https://en.wikipedia.org/wiki/" + page
		}
	}
	return out, nil
}

// fetchWikimedia GETs from Wikidata, Wikipedia or Commons, which ask for a
// descriptive User-Agent as MusicBrainz does but have no one-a-second rule.
func (m *MusicBrainz) fetchWikimedia(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("wikimedia: %w", err)
	}
	req.Header.Set("User-Agent", mbUserAgent)
	res, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikimedia: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikimedia %s: status %d", req.URL.Host, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("wikimedia: %w", err)
	}
	return body, nil
}

func (m *MusicBrainz) getWikimedia(ctx context.Context, rawURL string, out any) error {
	body, err := m.fetchWikimedia(ctx, rawURL)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("wikimedia: %w", err)
	}
	return nil
}
