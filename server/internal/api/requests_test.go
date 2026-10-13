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

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/requests"
	"github.com/garybowers/cineva/server/internal/store"
)

type stubDiscover struct{}

func (stubDiscover) SearchTitles(context.Context, string) ([]metadata.DiscoverResult, error) {
	return []metadata.DiscoverResult{{MediaType: "movie", TmdbID: 603, Title: "The Matrix"}}, nil
}
func (stubDiscover) MovieDetail(_ context.Context, id int) (metadata.TitleDetail, error) {
	return metadata.TitleDetail{MediaType: "movie", VideoResult: metadata.VideoResult{TmdbID: id, Title: "The Matrix"}}, nil
}
func (stubDiscover) TVDetail(context.Context, int) (metadata.TitleDetail, error) {
	return metadata.TitleDetail{}, metadata.ErrNotFound
}
func (stubDiscover) TVSeason(context.Context, int, int) (metadata.SeasonResult, error) {
	return metadata.SeasonResult{}, nil
}

// call runs a handler as the given user, with path parameters given as
// name, value pairs, and returns the status and body.
func call(t *testing.T, h echo.HandlerFunc, u store.User, method, body string, params ...string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	var names, values []string
	for i := 0; i+1 < len(params); i += 2 {
		names, values = append(names, params[i]), append(values, params[i+1])
	}
	c.SetParamNames(names...)
	c.SetParamValues(values...)
	c.Set(ctxUser, u)
	if err := h(c); err != nil {
		var he *echo.HTTPError
		if !errors.As(err, &he) {
			t.Fatalf("unexpected error: %v", err)
		}
		return he.Code, ""
	}
	return rec.Code, rec.Body.String()
}

func newRequestsServer(t *testing.T) (*server, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	mgr := library.NewManager(st, nil, t.TempDir())
	// Any rescan a webhook asks for waits for "playback" to end, so the
	// test never walks a folder.
	mgr.Playing = func() bool { return true }
	return &server{store: st, manager: mgr, events: bus, requests: requests.New(st, stubDiscover{}, bus)}, st
}

func TestRequestHandlers(t *testing.T) {
	ctx := context.Background()
	s, st := newRequestsServer(t)
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"HD"}]`))
		case r.URL.Path == "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/movies"}]`))
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			_, _ = w.Write([]byte(`{"title":"The Matrix","tmdbId":603}`))
		case r.URL.Path == "/api/v3/movie":
			_, _ = w.Write([]byte(`{"id":12,"tmdbId":603}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer radarr.Close()
	if _, err := st.CreateArrConnection(ctx, store.ArrConnection{Kind: "radarr", Name: "Radarr", URL: radarr.URL, APIKey: "k", CallbackURL: "http://c"}); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.CreateUser(ctx, "admin", "", "h", store.RoleAdmin)
	kid, _ := st.CreateUser(ctx, "kid", "", "h", store.RoleUser)
	none, _ := st.CreateUser(ctx, "none", "", "h", store.RoleUser)
	if err := st.SetRequestLevel(ctx, none.ID, store.RequestNone); err != nil {
		t.Fatal(err)
	}
	none.RequestLevel = store.RequestNone

	// Level none sees no request screens at all.
	for name, h := range map[string]echo.HandlerFunc{"list": s.listRequests, "search": s.discoverSearch, "create": s.createRequest} {
		body := ""
		if name == "create" {
			body = `{"mediaType":"movie","tmdbId":603}`
		}
		if code, _ := call(t, h, none, http.MethodGet, body); code != http.StatusForbidden {
			t.Errorf("%s for level none = %d, want 403", name, code)
		}
	}
	code, body := call(t, s.getRequestStatus, kid, http.MethodGet, "")
	if code != http.StatusOK || !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"tv":false`) {
		t.Fatalf("status = %d %s", code, body)
	}

	code, body = call(t, s.createRequest, kid, http.MethodPost, `{"mediaType":"movie","tmdbId":603}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	var r store.MediaRequest
	if err := json.Unmarshal([]byte(body), &r); err != nil || r.Status != store.RequestRequested {
		t.Fatalf("created = %s (%v)", body, err)
	}
	if code, _ := call(t, s.createRequest, kid, http.MethodPost, `{"mediaType":"movie","tmdbId":603}`); code != http.StatusOK {
		t.Fatalf("asking again = %d, want 200 with the same request", code)
	}
	if code, _ := call(t, s.createRequest, kid, http.MethodPost, `{"mediaType":"tv","tmdbId":1}`); code != http.StatusBadRequest {
		t.Fatalf("a series with no Sonarr = %d, want 400", code)
	}
	if code, _ := call(t, s.discoverTitle, kid, http.MethodGet, "", "type", "music", "tmdbId", "1"); code != http.StatusNotFound {
		t.Fatalf("unknown type = %d", code)
	}

	code, body = call(t, s.approveRequest, admin, http.MethodPost, `{"note":"Enjoy"}`, "id", r.ID)
	if code != http.StatusOK || !strings.Contains(body, `"status":"approved"`) || !strings.Contains(body, `"arrId":12`) {
		t.Fatalf("approve = %d %s", code, body)
	}
	if code, _ := call(t, s.approveRequest, admin, http.MethodPost, `{}`, "id", r.ID); code != http.StatusConflict {
		t.Fatalf("approving twice = %d, want 409", code)
	}
	if code, _ := call(t, s.deleteRequest, kid, http.MethodDelete, "", "id", r.ID); code != http.StatusConflict {
		t.Fatalf("withdrawing a sent request = %d, want 409", code)
	}
	if code, _ := call(t, s.declineRequest, admin, http.MethodPost, `{}`, "id", "nope"); code != http.StatusNotFound {
		t.Fatalf("declining nothing = %d", code)
	}
	log, err := st.AuditLog(ctx, 10, 0, "request.")
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("audit entries = %+v, want create and approve", log)
	}
}

func TestWebhookPathMapsAndGrabs(t *testing.T) {
	ctx := context.Background()
	s, st := newRequestsServer(t)
	tvDir, kidsDir := filepath.Join(t.TempDir(), "TV"), filepath.Join(t.TempDir(), "Kids")
	if _, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{tvDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateLibrary(ctx, "Kids", store.MediaTV, []string{kidsDir}); err != nil {
		t.Fatal(err)
	}
	secret, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := st.CreateArrConnection(ctx, store.ArrConnection{Kind: "sonarr", Name: "Sonarr", URL: "http://sonarr", APIKey: "k", CallbackURL: "http://c", SecretHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetArrOptions(ctx, conn.ID, 0, "", []store.PathMap{{From: "/tv", To: tvDir}}); err != nil {
		t.Fatal(err)
	}
	user, _ := st.CreateUser(ctx, "u", "", "h", store.RoleUser)
	req, err := st.CreateRequest(ctx, store.MediaRequest{UserID: user.ID, MediaType: "tv", TmdbID: 1396, TvdbID: 81189, Title: "Show",
		Status: store.RequestApproved, ConnectionID: conn.ID, ArrID: 5})
	if err != nil {
		t.Fatal(err)
	}

	deliver := func(body string) (int, string) {
		t.Helper()
		hr := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		hr.SetBasicAuth(webhookUser, secret)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(hr, rec)
		c.SetParamNames("id")
		c.SetParamValues(conn.ID)
		if err := s.integrationWebhook(c); err != nil {
			t.Fatal(err)
		}
		return rec.Code, rec.Body.String()
	}

	code, body := deliver(`{"eventType":"Download","series":{"id":5,"title":"Show","path":"/tv/Show"},"episodes":[{"seasonNumber":2,"episodeNumber":4}],"episodeFile":{"path":"/tv/Show/S02/e4.mkv"}}`)
	if code != http.StatusAccepted || !strings.Contains(body, `"rescanning":["TV"]`) {
		t.Fatalf("mapped download = %d %s; want only the mapped library", code, body)
	}
	got, err := st.ArrConnection(ctx, conn.ID)
	if err != nil || got.LastEvent != "Download" || got.LastEventTitle != "Show S02E04" {
		t.Fatalf("last event = %+v, %v", got, err)
	}

	if code, _ := deliver(`{"eventType":"Grab","series":{"id":5,"title":"Show","tvdbId":81189},"episodes":[{"seasonNumber":2,"episodeNumber":5}]}`); code != http.StatusOK {
		t.Fatalf("grab = %d", code)
	}
	if r, _ := st.Request(ctx, req.ID); r.Status != store.RequestDownloading {
		t.Fatalf("request after grab = %s, want downloading", r.Status)
	}
}

func TestIntegrationOptions(t *testing.T) {
	ctx := context.Background()
	s, st := newRequestsServer(t)
	conn, err := st.CreateArrConnection(ctx, store.ArrConnection{Kind: "sonarr", Name: "Sonarr", URL: "http://sonarr", APIKey: "k", CallbackURL: "http://c"})
	if err != nil {
		t.Fatal(err)
	}
	admin := store.User{ID: "a", Role: store.RoleAdmin}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"saved, empty rows dropped", `{"qualityProfileId":4,"rootFolder":"/tv","pathMaps":[{"from":"/tv","to":"/mnt/media/TV"},{"from":"","to":""}]}`, http.StatusOK},
		{"half a mapping", `{"pathMaps":[{"from":"/tv","to":""}]}`, http.StatusBadRequest},
		{"negative profile", `{"qualityProfileId":-1}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := call(t, s.setIntegrationOptions, admin, http.MethodPut, tc.body, "id", conn.ID); code != tc.want {
				t.Fatalf("status = %d %s, want %d", code, body, tc.want)
			}
		})
	}
	got, _ := st.ArrConnection(ctx, conn.ID)
	if got.QualityProfileID != 4 || got.RootFolder != "/tv" || len(got.PathMaps) != 1 {
		t.Fatalf("options = %+v", got)
	}
	// An instance that cannot be reached still answers, saying so.
	code, body := call(t, s.integrationStatus, admin, http.MethodGet, "", "id", conn.ID)
	if code != http.StatusOK || !strings.Contains(body, `"reachable":false`) {
		t.Fatalf("status of an unreachable instance = %d %s", code, body)
	}
}
