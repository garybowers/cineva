package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// plex reads a Plex Media Server with its owner's token. The token's own
// account gets its full state (positions too) from the libraries; every
// other account gets what the server's watch history holds, which is
// watched titles and when, but no positions.
type plex struct {
	base, token string
	http        *http.Client
	ownerID     string // the token's account, found by Check
}

func (p *plex) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	u := p.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Token", p.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Client-Identifier", "cineva-import")
	req.Header.Set("X-Plex-Product", "Cineva")
	res, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Plex at %s: %w", p.base, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("plex refused the token")
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("plex answered %s for %s", res.Status, path)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 256<<20)).Decode(out)
}

type plexMeta struct {
	RatingKey            string `json:"ratingKey"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	Year                 int    `json:"year"`
	ParentTitle          string `json:"parentTitle"`
	GrandparentTitle     string `json:"grandparentTitle"`
	GrandparentRatingKey string `json:"grandparentRatingKey"`
	ParentIndex          int    `json:"parentIndex"`
	Index                int    `json:"index"`
	ViewCount            int    `json:"viewCount"`
	ViewOffset           int64  `json:"viewOffset"` // milliseconds
	LastViewedAt         int64  `json:"lastViewedAt"`
	ViewedAt             int64  `json:"viewedAt"`  // in history
	AccountID            int    `json:"accountID"` // in history
	Guid                 []struct {
		ID string `json:"id"`
	} `json:"Guid"`
	Media []struct {
		Part []struct {
			File string `json:"file"`
		} `json:"Part"`
	} `json:"Media"`
}

type plexContainer struct {
	MediaContainer struct {
		FriendlyName string     `json:"friendlyName"`
		Version      string     `json:"version"`
		TotalSize    int        `json:"totalSize"`
		Size         int        `json:"size"`
		Metadata     []plexMeta `json:"Metadata"`
		Directory    []struct {
			Key   string `json:"key"`
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"Directory"`
		Account []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"Account"`
	} `json:"MediaContainer"`
}

// paged reads every page of a listing.
func (p *plex) paged(ctx context.Context, path string, q url.Values) ([]plexMeta, error) {
	var out []plexMeta
	const size = 500
	for start := 0; ; start += size {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("X-Plex-Container-Start", strconv.Itoa(start))
		qq.Set("X-Plex-Container-Size", strconv.Itoa(size))
		var c plexContainer
		if err := p.get(ctx, path, qq, &c); err != nil {
			return nil, err
		}
		out = append(out, c.MediaContainer.Metadata...)
		if len(c.MediaContainer.Metadata) < size {
			return out, nil
		}
	}
}

func (p *plex) Check(ctx context.Context) (Info, error) {
	var root plexContainer
	if err := p.get(ctx, "/", nil, &root); err != nil {
		return Info{}, err
	}
	var accounts plexContainer
	if err := p.get(ctx, "/accounts", nil, &accounts); err != nil {
		return Info{}, err
	}
	var me struct {
		MyPlex struct {
			Username string `json:"username"`
		} `json:"MyPlex"`
	}
	_ = p.get(ctx, "/myplex/account", nil, &me) // best effort: names the token's account
	info := Info{Kind: "plex", Name: root.MediaContainer.FriendlyName, Version: root.MediaContainer.Version, Users: []User{}}
	for _, a := range accounts.MediaContainer.Account {
		if a.ID == 0 || a.Name == "" {
			continue // the server's own system account
		}
		id := strconv.Itoa(a.ID)
		owner := (me.MyPlex.Username != "" && strings.EqualFold(a.Name, me.MyPlex.Username)) || (me.MyPlex.Username == "" && a.ID == 1)
		if owner {
			p.ownerID = id
		}
		info.Users = append(info.Users, User{ID: id, Name: a.Name, Admin: owner})
	}
	owner := "the token's own account"
	for _, u := range info.Users {
		if u.ID == p.ownerID {
			owner = u.Name
		}
	}
	info.Caveats = []string{"Plex gives resume positions only for " + owner +
		"; other accounts bring what they have watched and when, but not where they stopped."}
	return info, nil
}

func plexTmdb(m plexMeta) int {
	for _, g := range m.Guid {
		if strings.HasPrefix(g.ID, "tmdb://") {
			n, _ := strconv.Atoi(strings.TrimPrefix(g.ID, "tmdb://"))
			return n
		}
	}
	return 0
}

func plexPath(m plexMeta) string {
	for _, md := range m.Media {
		for _, part := range md.Part {
			if part.File != "" {
				return part.File
			}
		}
	}
	return ""
}

// showTmdb resolves series' TMDB ids by rating key, a batch at a time.
func (p *plex) showTmdb(ctx context.Context, keys map[string]bool) (map[string]int, map[string]string, error) {
	ids, titles := map[string]int{}, map[string]string{}
	list := make([]string, 0, len(keys))
	for k := range keys {
		if k != "" {
			list = append(list, k)
		}
	}
	for start := 0; start < len(list); start += 50 {
		var c plexContainer
		batch := strings.Join(list[start:min(start+50, len(list))], ",")
		if err := p.get(ctx, "/library/metadata/"+batch, url.Values{"includeGuids": {"1"}}, &c); err != nil {
			return nil, nil, err
		}
		for _, m := range c.MediaContainer.Metadata {
			ids[m.RatingKey], titles[m.RatingKey] = plexTmdb(m), m.Title
		}
	}
	return ids, titles, nil
}

func (p *plex) Watches(ctx context.Context, u User) ([]Watch, error) {
	if p.ownerID == "" {
		if _, err := p.Check(ctx); err != nil {
			return nil, err
		}
	}
	var metas []plexMeta
	if u.ID == p.ownerID {
		var secs plexContainer
		if err := p.get(ctx, "/library/sections", nil, &secs); err != nil {
			return nil, err
		}
		for _, d := range secs.MediaContainer.Directory {
			typ := ""
			switch d.Type {
			case "movie":
				typ = "1"
			case "show":
				typ = "4"
			default:
				continue
			}
			got, err := p.paged(ctx, "/library/sections/"+d.Key+"/all", url.Values{"type": {typ}, "includeGuids": {"1"}})
			if err != nil {
				return nil, err
			}
			for _, m := range got {
				if m.ViewCount > 0 || m.ViewOffset > 0 {
					metas = append(metas, m)
				}
			}
		}
	} else {
		// History names titles by rating key; their ids and files come from
		// the library, a batch at a time.
		hist, err := p.paged(ctx, "/status/sessions/history/all", url.Values{"accountID": {u.ID}, "sort": {"viewedAt:desc"}})
		if err != nil {
			return nil, err
		}
		latest := map[string]int64{}
		var keys []string
		for _, h := range hist {
			if h.Type != "movie" && h.Type != "episode" {
				continue
			}
			if _, ok := latest[h.RatingKey]; !ok {
				keys = append(keys, h.RatingKey)
			}
			latest[h.RatingKey] = max(latest[h.RatingKey], h.ViewedAt)
		}
		for start := 0; start < len(keys); start += 50 {
			var c plexContainer
			batch := strings.Join(keys[start:min(start+50, len(keys))], ",")
			if err := p.get(ctx, "/library/metadata/"+batch, url.Values{"includeGuids": {"1"}}, &c); err != nil {
				return nil, err
			}
			for _, m := range c.MediaContainer.Metadata {
				// Watched by this account, as history says; the library's
				// own counts are the token account's.
				m.ViewCount, m.ViewOffset, m.LastViewedAt = 1, 0, latest[m.RatingKey]
				metas = append(metas, m)
			}
		}
	}
	shows := map[string]bool{}
	for _, m := range metas {
		if m.Type == "episode" {
			shows[m.GrandparentRatingKey] = true
		}
	}
	showIDs, showTitles, err := p.showTmdb(ctx, shows)
	if err != nil {
		return nil, err
	}
	out := []Watch{}
	for _, m := range metas {
		w := Watch{
			Movie: m.Type == "movie", Title: m.Title, Year: m.Year, Path: plexPath(m),
			Watched: m.ViewCount > 0 && m.ViewOffset == 0, PositionSecs: int(m.ViewOffset / 1000),
		}
		if m.LastViewedAt > 0 {
			w.At = time.Unix(m.LastViewedAt, 0)
		}
		if w.Movie {
			w.TmdbID = plexTmdb(m)
		} else {
			w.SeriesTmdbID, w.SeriesTitle = showIDs[m.GrandparentRatingKey], showTitles[m.GrandparentRatingKey]
			if w.SeriesTitle == "" {
				w.SeriesTitle = m.GrandparentTitle
			}
			w.Season, w.Episode = m.ParentIndex, m.Index
		}
		out = append(out, w)
	}
	return out, nil
}

// Playlists are the token account's music playlists; Plex has no
// favourites to bring.
func (p *plex) Playlists(ctx context.Context, u User) ([]Playlist, error) {
	if p.ownerID == "" {
		if _, err := p.Check(ctx); err != nil {
			return nil, err
		}
	}
	out := []Playlist{}
	if u.ID != p.ownerID {
		return out, nil
	}
	lists, err := p.paged(ctx, "/playlists", url.Values{"playlistType": {"audio"}})
	if err != nil {
		return nil, err
	}
	for _, pl := range lists {
		items, err := p.paged(ctx, "/playlists/"+pl.RatingKey+"/items", nil)
		if err != nil {
			return nil, err
		}
		list := Playlist{Name: pl.Title}
		for _, it := range items {
			if it.Type == "track" {
				list.Tracks = append(list.Tracks, Track{Artist: it.GrandparentTitle, Album: it.ParentTitle, Title: it.Title, Path: plexPath(it)})
			}
		}
		if len(list.Tracks) > 0 {
			out = append(out, list)
		}
	}
	return out, nil
}
