package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/segments"
	"github.com/garybowers/cineva/server/internal/store"
)

// An administrator sets an episode's intro by hand, says it has no credits,
// and hands the intro back to detection; bounds outside the episode are
// refused, and so is re-detection with detection turned off.
func TestItemSegmentEditing(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(it store.Item) store.Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := save(store.Item{Kind: store.KindSeries, Title: "Show", Path: "/tv/Show"})
	season := save(store.Item{Kind: store.KindSeason, ParentID: series.ID, SeasonNum: 1, Path: "/tv/Show/S1"})
	ep := save(store.Item{Kind: store.KindEpisode, ParentID: season.ID, EpisodeNum: 1,
		Path: "/tv/Show/S1/e1.mkv", FileMtime: 1, RuntimeSecs: 1800})
	admin, err := st.CreateUser(ctx, "admin", "", "h", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}

	s := &server{store: st, events: events.NewBus()}
	call := func(method string, h echo.HandlerFunc, id, kind, body string) (int, itemSegmentsResponse) {
		t.Helper()
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("id", "kind")
		c.SetParamValues(id, kind)
		c.Set(ctxUser, admin)
		if err := h(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, itemSegmentsResponse{}
			}
			t.Fatalf("%s: %v", method, err)
		}
		var out itemSegmentsResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, out
	}
	stateOf := func(r itemSegmentsResponse, kind string) store.SegmentState {
		for _, sg := range r.Segments {
			if sg.Kind == kind {
				return sg
			}
		}
		t.Fatalf("no %s in %+v", kind, r)
		return store.SegmentState{}
	}

	tests := []struct {
		name string
		id   string
		kind string
		body string
		want int
	}{
		{"set intro", ep.ID, "intro", `{"startSecs":62,"endSecs":101}`, http.StatusOK},
		{"no credits", ep.ID, "credits", `{"none":true}`, http.StatusOK},
		{"end before start", ep.ID, "intro", `{"startSecs":90,"endSecs":60}`, http.StatusBadRequest},
		{"negative start", ep.ID, "intro", `{"startSecs":-5,"endSecs":60}`, http.StatusBadRequest},
		{"past the end", ep.ID, "credits", `{"startSecs":1700,"endSecs":1900}`, http.StatusBadRequest},
		{"unknown kind", ep.ID, "recap", `{"startSecs":0,"endSecs":60}`, http.StatusNotFound},
		{"not an episode", season.ID, "intro", `{"startSecs":0,"endSecs":60}`, http.StatusBadRequest},
		{"no such item", "nope", "intro", `{"startSecs":0,"endSecs":60}`, http.StatusNotFound},
	}
	for _, tc := range tests {
		if got, _ := call(http.MethodPut, s.putItemSegment, tc.id, tc.kind, tc.body); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}

	_, got := call(http.MethodGet, s.itemSegments, ep.ID, "", "")
	if in := stateOf(got, "intro"); !in.Found || !in.Locked || in.StartSecs != 62 || in.EndSecs != 101 {
		t.Errorf("intro = %+v, want 62-101 locked", in)
	}
	if cr := stateOf(got, "credits"); cr.Found || !cr.Locked {
		t.Errorf("credits = %+v, want none, locked", cr)
	}
	if got.Detection || got.RuntimeSecs != 1800 {
		t.Errorf("response = %+v, want detection off and the running time", got)
	}

	if code, got := call(http.MethodDelete, s.clearItemSegment, ep.ID, "intro", ""); code != http.StatusOK || stateOf(got, "intro").Locked {
		t.Errorf("clear = %d, %+v; want the intro unlocked", code, got)
	}

	if code, _ := call(http.MethodPost, s.redetectSegments, season.ID, "", ""); code != http.StatusConflict {
		t.Errorf("redetect with detection off = %d, want 409", code)
	}
	s.segments = segments.NewDetector(st)
	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{"season", season.ID, http.StatusAccepted},
		{"episode", ep.ID, http.StatusAccepted},
		{"series", series.ID, http.StatusBadRequest},
	} {
		if code, _ := call(http.MethodPost, s.redetectSegments, tc.id, "", ""); code != tc.want {
			t.Errorf("redetect %s = %d, want %d", tc.name, code, tc.want)
		}
	}
}
