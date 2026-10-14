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
		{"series", series.ID, http.StatusAccepted},
		{"no such item", "nope", http.StatusNotFound},
	} {
		if code, _ := call(http.MethodPost, s.redetectSegments, tc.id, "", ""); code != tc.want {
			t.Errorf("redetect %s = %d, want %d", tc.name, code, tc.want)
		}
	}
}

// Trusted editors may correct and re-detect what they can see, and nothing
// else; anyone may report wrong times; admins list and resolve reports.
func TestSegmentEditorsAndReports(t *testing.T) {
	s, st := newRequestsServer(t)
	ctx := context.Background()
	s.segments = segments.NewDetector(st)
	tv, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	films, err := st.CreateLibrary(ctx, "Films", store.MediaMovies, []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(lib store.Library, it store.Item) store.Item {
		t.Helper()
		it.LibraryID = lib.ID
		saved, err := st.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	series := save(tv, store.Item{Kind: store.KindSeries, Title: "Show", Path: "/tv/Show"})
	season := save(tv, store.Item{Kind: store.KindSeason, ParentID: series.ID, SeasonNum: 1, Path: "/tv/Show/S1"})
	ep := save(tv, store.Item{Kind: store.KindEpisode, ParentID: season.ID, EpisodeNum: 1, Path: "/tv/Show/S1/e1.mkv", FileMtime: 1, RuntimeSecs: 1800})
	film := save(films, store.Item{Kind: store.KindMovie, Title: "Film", Path: "/films/f.mkv", FileMtime: 1, RuntimeSecs: 6000})

	admin := store.User{ID: "admin", Username: "admin", Role: store.RoleAdmin}
	viewer := store.User{ID: "viewer", Username: "viewer", Role: store.RoleUser, LibraryIDs: []string{tv.ID, films.ID}}
	editor := viewer
	editor.ID, editor.Username, editor.SegmentEditor = "editor", "editor", true
	// An editor who cannot see the film library.
	tvEditor := editor
	tvEditor.ID, tvEditor.Username, tvEditor.LibraryIDs = "tv-editor", "tv-editor", []string{tv.ID}
	for _, u := range []store.User{admin, viewer, editor, tvEditor} {
		if _, err := st.CreateUser(ctx, u.Username, "", "h", u.Role); err != nil {
			t.Fatal(err)
		}
	}
	// The ids above are the test's own; reports need the stored ones.
	for _, u := range []*store.User{&admin, &viewer, &editor, &tvEditor} {
		stored, err := st.UserByUsername(ctx, u.Username)
		if err != nil {
			t.Fatal(err)
		}
		u.ID = stored.ID
	}

	guarded := requireSegmentEditor(s.redetectSegments)
	for _, tc := range []struct {
		name string
		h    echo.HandlerFunc
		u    store.User
		body string
		id   string
		kind string
		want int
	}{
		{"viewer may not re-detect", guarded, viewer, "", series.ID, "", http.StatusForbidden},
		{"editor re-detects a series", guarded, editor, "", series.ID, "", http.StatusAccepted},
		{"editor re-detects a film", guarded, editor, `{"kinds":["credits"],"includeLocked":true}`, film.ID, "", http.StatusAccepted},
		{"unknown kind", guarded, editor, `{"kinds":["recap"]}`, film.ID, "", http.StatusBadRequest},
		{"not a library they see", guarded, tvEditor, "", film.ID, "", http.StatusNotFound},
		{"editor sets a film's credits", s.putItemSegment, editor, `{"startSecs":5700,"endSecs":6000}`, film.ID, "credits", http.StatusOK},
		{"a film has no intro", s.putItemSegment, editor, `{"startSecs":0,"endSecs":60}`, film.ID, "intro", http.StatusBadRequest},
		{"hidden film", s.putItemSegment, tvEditor, `{"none":true}`, film.ID, "credits", http.StatusNotFound},
		{"viewer reports an intro", s.createSegmentReport, viewer, `{"kind":"intro","positionSecs":95,"note":"too early"}`, ep.ID, "", http.StatusCreated},
		{"viewer reports film credits", s.createSegmentReport, viewer, `{"kind":"credits","positionSecs":5600}`, film.ID, "", http.StatusCreated},
		{"a film's intro cannot be reported", s.createSegmentReport, viewer, `{"kind":"intro"}`, film.ID, "", http.StatusBadRequest},
		{"report on a hidden film", s.createSegmentReport, tvEditor, `{"kind":"credits"}`, film.ID, "", http.StatusNotFound},
		{"report on a season", s.createSegmentReport, viewer, `{"kind":"intro"}`, season.ID, "", http.StatusBadRequest},
		{"a note too long", s.createSegmentReport, viewer, `{"kind":"intro","note":"` + strings.Repeat("x", 501) + `"}`, ep.ID, "", http.StatusBadRequest},
	} {
		if code, body := call(t, tc.h, tc.u, http.MethodPost, tc.body, "id", tc.id, "kind", tc.kind); code != tc.want {
			t.Errorf("%s: status %d (%s), want %d", tc.name, code, body, tc.want)
		}
	}

	// The film's credits were replaced by hand after the re-detection, and
	// the editor's changes are in the audit log under their name.
	if segs, _ := st.Segments(ctx, film.ID); len(segs) != 1 || segs[0].StartSecs != 5700 {
		t.Errorf("film segments = %+v, want the hand-set 5700", segs)
	}
	st2 := s.segments.Status()
	if len(st2.Targets) != 2 || st2.Targets[0].ItemID != season.ID || st2.Targets[1].ItemID != film.ID {
		t.Errorf("targets = %+v, want the season then the film", st2.Targets)
	}

	// A selection, and a library (admins only, by the route).
	if code, body := call(t, s.redetectSelection, editor, http.MethodPost, `{"items":["`+ep.ID+`","`+film.ID+`"]}`); code != http.StatusAccepted {
		t.Errorf("selection = %d %s", code, body)
	}
	if code, _ := call(t, s.redetectSelection, editor, http.MethodPost, `{"items":[]}`); code != http.StatusBadRequest {
		t.Errorf("empty selection = %d, want 400", code)
	}
	if code, body := call(t, s.redetectLibrary, admin, http.MethodPost, "", "id", tv.ID); code != http.StatusAccepted {
		t.Errorf("library = %d %s", code, body)
	}

	code, body := call(t, s.listSegmentReports, admin, http.MethodGet, "")
	var reports []store.SegmentReport
	if code != http.StatusOK || json.Unmarshal([]byte(body), &reports) != nil || len(reports) != 2 {
		t.Fatalf("reports = %d %s, want two", code, body)
	}
	if code, _ := call(t, s.resolveSegmentReport, admin, http.MethodPost, "", "id", reports[0].ID); code != http.StatusNoContent {
		t.Errorf("resolve = %d, want 204", code)
	}
	if code, _ := call(t, s.resolveSegmentReport, admin, http.MethodPost, "", "id", "nope"); code != http.StatusNotFound {
		t.Errorf("resolve missing = %d, want 404", code)
	}
	if open, _ := st.SegmentReports(ctx, true, 10); len(open) != 1 {
		t.Errorf("%d open after resolving one, want 1", len(open))
	}
	log, err := st.AuditLog(ctx, 50, 0, "segments.")
	if err != nil {
		t.Fatal(err)
	}
	var who []string
	for _, e := range log {
		if e.Action == "segments.set" {
			who = append(who, e.UserID)
		}
	}
	if len(who) != 1 || who[0] != editor.ID {
		t.Errorf("segments.set audited for %v, want the editor", who)
	}
}

// An admin makes a user a trusted editor, and the log says so.
func TestUserSegmentEditorToggle(t *testing.T) {
	s, st := newRequestsServer(t)
	ctx := context.Background()
	admin, err := st.CreateUser(ctx, "admin", "", "h", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, "ed", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	code, body := call(t, s.updateUser, admin, http.MethodPatch, `{"segmentEditor":true}`, "id", u.ID)
	var got store.User
	if code != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil || !got.SegmentEditor {
		t.Fatalf("update = %d %s, want an editor", code, body)
	}
	log, _ := st.AuditLog(ctx, 10, 0, "user.")
	if len(log) == 0 || !strings.Contains(log[0].Detail, "intro & credits editor") {
		t.Fatalf("audit = %+v, want the change recorded", log)
	}
}
