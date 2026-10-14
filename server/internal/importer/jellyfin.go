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

// jellyfin reads a Jellyfin (or Emby) server with an API key from its
// dashboard.
type jellyfin struct {
	base, token string
	http        *http.Client
}

func (j *jellyfin) get(ctx context.Context, path string, q url.Values, out any) error {
	u := j.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", `MediaBrowser Client="Cineva", Device="Cineva import", DeviceId="cineva-import", Version="1", Token="`+j.token+`"`)
	req.Header.Set("Accept", "application/json")
	res, err := j.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Jellyfin at %s: %w", j.base, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("jellyfin refused the API key")
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("jellyfin answered %s for %s", res.Status, path)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 256<<20)).Decode(out)
}

type jfItem struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	ProductionYear    int               `json:"ProductionYear"`
	ProviderIds       map[string]string `json:"ProviderIds"`
	Path              string            `json:"Path"`
	SeriesID          string            `json:"SeriesId"`
	SeriesName        string            `json:"SeriesName"`
	ParentIndexNumber int               `json:"ParentIndexNumber"`
	IndexNumber       int               `json:"IndexNumber"`
	Album             string            `json:"Album"`
	AlbumArtist       string            `json:"AlbumArtist"`
	Artists           []string          `json:"Artists"`
	UserData          struct {
		Played                bool   `json:"Played"`
		PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
		LastPlayedDate        string `json:"LastPlayedDate"`
	} `json:"UserData"`
}

type jfPage struct {
	Items            []jfItem `json:"Items"`
	TotalRecordCount int      `json:"TotalRecordCount"`
}

// items pages through /Items for a user.
func (j *jellyfin) items(ctx context.Context, userID string, q url.Values) ([]jfItem, error) {
	var out []jfItem
	const page = 500
	for start := 0; ; start += page {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("userId", userID)
		qq.Set("Recursive", "true")
		qq.Set("StartIndex", strconv.Itoa(start))
		qq.Set("Limit", strconv.Itoa(page))
		var p jfPage
		if err := j.get(ctx, "/Items", qq, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Items...)
		if len(p.Items) < page || start+page >= p.TotalRecordCount {
			return out, nil
		}
	}
}

func (j *jellyfin) Check(ctx context.Context) (Info, error) {
	var sys struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
	}
	if err := j.get(ctx, "/System/Info", nil, &sys); err != nil {
		return Info{}, err
	}
	var users []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := j.get(ctx, "/Users", nil, &users); err != nil {
		return Info{}, err
	}
	info := Info{Kind: "jellyfin", Name: sys.ServerName, Version: sys.Version, Users: []User{}, Caveats: []string{}}
	for _, u := range users {
		info.Users = append(info.Users, User{ID: u.ID, Name: u.Name, Admin: u.Policy.IsAdministrator})
	}
	return info, nil
}

// ticksPerSecond: Jellyfin counts time in 100-nanosecond ticks.
const ticksPerSecond = 10_000_000

func (j *jellyfin) Watches(ctx context.Context, u User) ([]Watch, error) {
	fields := "ProviderIds,Path"
	var all []jfItem
	// Watched, then part-watched: Jellyfin's filters combine as "and".
	for _, filter := range []string{"IsPlayed", "IsResumable"} {
		got, err := j.items(ctx, u.ID, url.Values{"IncludeItemTypes": {"Movie,Episode"}, "Filters": {filter}, "Fields": {fields}})
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	// Series' provider ids, for their episodes.
	seriesIDs := map[string]bool{}
	for _, it := range all {
		if it.Type == "Episode" && it.SeriesID != "" {
			seriesIDs[it.SeriesID] = true
		}
	}
	seriesTmdb := map[string]int{}
	if len(seriesIDs) > 0 {
		ids := make([]string, 0, len(seriesIDs))
		for id := range seriesIDs {
			ids = append(ids, id)
		}
		for start := 0; start < len(ids); start += 100 {
			batch := ids[start:min(start+100, len(ids))]
			series, err := j.items(ctx, u.ID, url.Values{"Ids": {strings.Join(batch, ",")}, "Fields": {"ProviderIds"}})
			if err != nil {
				return nil, err
			}
			for _, s := range series {
				seriesTmdb[s.ID] = tmdbOf(s.ProviderIds)
			}
		}
	}
	seen := map[string]bool{}
	out := []Watch{}
	for _, it := range all {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		w := Watch{
			Movie: it.Type == "Movie", Title: it.Name, Year: it.ProductionYear, Path: it.Path,
			Watched: it.UserData.Played, PositionSecs: int(it.UserData.PlaybackPositionTicks / ticksPerSecond),
		}
		if t, err := time.Parse(time.RFC3339Nano, it.UserData.LastPlayedDate); err == nil {
			w.At = t
		}
		if w.Movie {
			w.TmdbID = tmdbOf(it.ProviderIds)
		} else {
			w.SeriesTitle, w.SeriesTmdbID = it.SeriesName, seriesTmdb[it.SeriesID]
			w.Season, w.Episode = it.ParentIndexNumber, it.IndexNumber
		}
		out = append(out, w)
	}
	return out, nil
}

func tmdbOf(ids map[string]string) int {
	for k, v := range ids {
		if strings.EqualFold(k, "tmdb") {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

func jfTrack(it jfItem) Track {
	artist := it.AlbumArtist
	if artist == "" && len(it.Artists) > 0 {
		artist = it.Artists[0]
	}
	return Track{Artist: artist, Album: it.Album, Title: it.Name, Path: it.Path}
}

func (j *jellyfin) Playlists(ctx context.Context, u User) ([]Playlist, error) {
	out := []Playlist{}
	lists, err := j.items(ctx, u.ID, url.Values{"IncludeItemTypes": {"Playlist"}})
	if err != nil {
		return nil, err
	}
	for _, pl := range lists {
		var p jfPage
		if err := j.get(ctx, "/Playlists/"+pl.ID+"/Items", url.Values{"userId": {u.ID}, "Fields": {"Path"}}, &p); err != nil {
			return nil, err
		}
		list := Playlist{Name: pl.Name}
		for _, it := range p.Items {
			if it.Type == "Audio" {
				list.Tracks = append(list.Tracks, jfTrack(it))
			}
		}
		if len(list.Tracks) > 0 {
			out = append(out, list)
		}
	}
	favs, err := j.items(ctx, u.ID, url.Values{"IncludeItemTypes": {"Audio"}, "Filters": {"IsFavorite"}, "Fields": {"Path"}})
	if err != nil {
		return nil, err
	}
	if len(favs) > 0 {
		fav := Playlist{Name: "Favourites"}
		for _, it := range favs {
			fav.Tracks = append(fav.Tracks, jfTrack(it))
		}
		out = append(out, fav)
	}
	return out, nil
}
