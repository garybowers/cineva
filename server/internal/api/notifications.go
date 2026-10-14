package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/notify"
	"github.com/garybowers/cineva/server/internal/store"
)

// Notifications (see docs/notifications.md): household destinations the
// admin sets up, users' own, and the messages each event becomes.

var notifyLog = logging.For("notify")

// watchingDays is how recently someone must have watched an episode of a
// series to be told of new ones.
const watchingDays = 180

// startNotifications starts sending, and hooks the events that become
// messages.
func (s *server) startNotifications(ctx context.Context) {
	go s.notify.Run(ctx)
	if s.requests != nil {
		s.requests.Notify = s.notifyRequest
	}
	s.manager.OnScanFinished = s.notifyScan
	s.ha.OnConnection = func(up bool, detail string) {
		if up {
			s.notify.Household(notify.Notice{Event: "homeassistant.up", Title: "Home Assistant is back",
				Body: "Room controls, cameras and modes work again.", Path: "/admin/integrations"})
			return
		}
		s.notify.Household(notify.Notice{Event: "homeassistant.down", Title: "Home Assistant unreachable",
			Body: "Room controls, cameras and modes are off until it answers again. " + detail,
			Path: "/admin/integrations", High: true})
	}
}

func requestTitle(r store.MediaRequest) string {
	if r.Year > 0 {
		return fmt.Sprintf("%s (%d)", r.Title, r.Year)
	}
	return r.Title
}

// notifyRequest turns a request's change into a message: household ones
// for the admin, personal ones for whoever asked.
func (s *server) notifyRequest(event string, r store.MediaRequest) {
	t := requestTitle(r)
	switch event {
	case "request.waiting":
		who := r.UserName
		if who == "" {
			who = "Someone"
		}
		s.notify.Household(notify.Notice{Event: event, Title: "Request waiting: " + t,
			Body: who + " asked for it. Approve or decline it on the Requests page.", Path: "/admin/requests"})
	case "request.failed":
		s.notify.Household(notify.Notice{Event: event, Title: "Request not sent: " + t,
			Body: r.Error, Path: "/admin/requests"})
	case "request.approved":
		body := "It has been sent to be downloaded."
		if r.Note != "" {
			body += " " + r.Note
		}
		s.notify.Personal(r.UserID, notify.Notice{Event: event, Title: "Request approved: " + t, Body: body, Path: "/requests"})
	case "request.declined":
		body := "An admin declined it."
		if r.Note != "" {
			body = r.Note
		}
		s.notify.Personal(r.UserID, notify.Notice{Event: event, Title: "Request declined: " + t, Body: body, Path: "/requests"})
	case "request.available":
		path := "/requests"
		kind := store.KindMovie
		if r.MediaType == store.RequestTV {
			kind = store.KindSeries
		}
		if items, err := s.store.ItemsByTmdbIDs(context.Background(), kind, []int{r.TmdbID}); err == nil && len(items) > 0 {
			path = "/items/" + items[0].ID
		}
		s.notify.Personal(r.UserID, notify.Notice{Event: event, Title: "Ready to watch: " + t,
			Body: "What you asked for is in the library.", Path: path})
	}
}

// notifyStorage tells the household a library's storage went or came back.
func (s *server) notifyStorage(lib store.Library, st library.StorageStatus) {
	if st.Available {
		s.notify.Household(notify.Notice{Event: "storage.up", Title: lib.Name + " storage is back",
			Body: "Its titles can be played again.", Path: "/admin/libraries"})
		return
	}
	s.notify.Household(notify.Notice{Event: "storage.down", Title: lib.Name + " storage unreachable",
		Body: st.Message(lib.Name), Path: "/admin/libraries", High: true})
}

// newListMax is how many titles a "new in the library" message names.
const newListMax = 8

// notifyScan tells of a failed scan, and of the titles a scan added: to
// the household, one message per library, and to each person watching a
// series, one message per series.
func (s *server) notifyScan(lib store.Library, scan store.Scan, added []store.Item) {
	if scan.Status == "failed" {
		s.notify.Household(notify.Notice{Event: "scan.failed", Title: "Scan failed: " + lib.Name,
			Body: scan.Error, Path: "/admin/libraries", High: true})
		return
	}
	if len(added) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Episodes are grouped by series, so a season pack is one line.
	type series struct {
		item     store.Item
		episodes []store.Item
	}
	var lines []string
	var bySeries []*series
	seen := map[string]*series{}
	for _, it := range added {
		switch it.Kind {
		case store.KindEpisode:
			season, err := s.store.Item(ctx, it.ParentID)
			if err != nil {
				continue
			}
			sr, ok := seen[season.ParentID]
			if !ok {
				show, err := s.store.Item(ctx, season.ParentID)
				if err != nil {
					continue
				}
				sr = &series{item: show}
				seen[season.ParentID] = sr
				bySeries = append(bySeries, sr)
			}
			sr.episodes = append(sr.episodes, it)
		case store.KindAlbum:
			if it.Artist != "" {
				lines = append(lines, it.Title+" by "+it.Artist)
			} else {
				lines = append(lines, it.Title)
			}
		default:
			if it.Year > 0 {
				lines = append(lines, fmt.Sprintf("%s (%d)", it.Title, it.Year))
			} else {
				lines = append(lines, it.Title)
			}
		}
	}
	for _, sr := range bySeries {
		lines = append(lines, sr.item.Title+": "+episodeCodes(sr.episodes))
	}
	if len(lines) > 0 {
		body := strings.Join(lines[:min(len(lines), newListMax)], "\n")
		if len(lines) > newListMax {
			body += fmt.Sprintf("\nand %d more", len(lines)-newListMax)
		}
		path := "/"
		if len(added) == 1 {
			path = "/items/" + added[0].ID
		} else if len(bySeries) == 1 && len(lines) == 1 {
			path = "/items/" + bySeries[0].item.ID
		}
		s.notify.Household(notify.Notice{Event: "library.new", Title: "New in " + lib.Name, Body: body,
			Path: path, LibraryID: lib.ID})
	}

	// Personal: whoever is watching each series, if they may see it.
	for _, sr := range bySeries {
		watchers, err := s.store.SeriesWatchers(ctx, sr.item.ID, watchingDays)
		if err != nil {
			notifyLog.Warn("finding a series' watchers failed", "series", sr.item.Title, "err", err)
			continue
		}
		title := "New episode: " + sr.item.Title
		if len(sr.episodes) > 1 {
			title = "New episodes: " + sr.item.Title
		}
		body := episodeCodes(sr.episodes)
		if len(sr.episodes) == 1 && sr.episodes[0].Title != "" {
			body += " · " + sr.episodes[0].Title
		}
		for _, uid := range watchers {
			u, err := s.store.UserByID(ctx, uid)
			if err != nil || u.Disabled || !canSeeLibrary(u, lib.ID) {
				continue
			}
			s.notify.Personal(uid, notify.Notice{Event: "episode.new", Title: title, Body: body,
				Path: "/items/" + sr.episodes[0].ID})
		}
	}
}

// episodeCodes names episodes compactly: "S06E05", "S06E05-E07", or "3
// new episodes" when they are scattered.
func episodeCodes(eps []store.Item) string {
	if len(eps) == 0 {
		return ""
	}
	slices.SortFunc(eps, func(a, b store.Item) int {
		if a.SeasonNum != b.SeasonNum {
			return a.SeasonNum - b.SeasonNum
		}
		return a.EpisodeNum - b.EpisodeNum
	})
	first, last := eps[0], eps[len(eps)-1]
	code := fmt.Sprintf("S%02dE%02d", first.SeasonNum, first.EpisodeNum)
	if len(eps) == 1 {
		return code
	}
	if first.SeasonNum == last.SeasonNum && last.EpisodeNum-first.EpisodeNum == len(eps)-1 {
		return code + fmt.Sprintf("-E%02d", last.EpisodeNum)
	}
	return fmt.Sprintf("%d new episodes", len(eps))
}

// ---- destinations ----

// destinationOut is a destination as the API shows it: secrets masked.
type destinationOut struct {
	store.NotifyDestination
	Config map[string]string `json:"config"`
}

func outDestination(d store.NotifyDestination) destinationOut {
	return destinationOut{NotifyDestination: d, Config: notify.Mask(d.Kind, d.Config)}
}

func outDestinations(ds []store.NotifyDestination) []destinationOut {
	out := make([]destinationOut, 0, len(ds))
	for _, d := range ds {
		out = append(out, outDestination(d))
	}
	return out
}

type destinationRequest struct {
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Config     map[string]string `json:"config"`
	Events     []string          `json:"events"`
	LibraryIDs []string          `json:"libraryIds"`
	Enabled    *bool             `json:"enabled"`
}

func (s *server) ntfyServer(ctx context.Context) string {
	v, err := s.store.Setting(ctx, notify.SettingNtfyServer, "")
	if err != nil {
		return ""
	}
	return v
}

// saveDestination creates (existing nil) or updates a destination of the
// given scope and owner from a request body.
func (s *server) saveDestination(c echo.Context, scope notify.Scope, ownerID string, existing *store.NotifyDestination) error {
	ctx := c.Request().Context()
	var req destinationRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	d := store.NotifyDestination{OwnerID: ownerID, Kind: req.Kind, Enabled: true}
	if existing != nil {
		d = *existing
	}
	if req.Name != "" {
		d.Name = strings.TrimSpace(req.Name)
	}
	if d.Name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a destination needs a name")
	}
	if req.Config != nil {
		if existing != nil {
			notify.KeepSecrets(d.Kind, req.Config, existing.Config)
		}
		d.Config = req.Config
	}
	if d.Config == nil {
		d.Config = map[string]string{}
	}
	if req.Events != nil {
		d.Events = req.Events
	}
	if req.LibraryIDs != nil && scope == notify.Household {
		d.LibraryIDs = req.LibraryIDs
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	if err := notify.Validate(scope, d.Kind, d.Config, d.Events, s.ntfyServer(ctx)); err != nil {
		var inv notify.ErrInvalid
		if errors.As(err, &inv) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return err
	}
	var saved store.NotifyDestination
	var err error
	if existing == nil {
		saved, err = s.store.CreateNotifyDestination(ctx, d)
	} else {
		saved, err = s.store.UpdateNotifyDestination(ctx, d)
	}
	if err != nil {
		return err
	}
	action := "notify.update"
	code := http.StatusOK
	if existing == nil {
		action, code = "notify.create", http.StatusCreated
	}
	s.audit(c, action, "notify", saved.ID, saved.Name, string(scope)+" "+saved.Kind)
	return c.JSON(code, outDestination(saved))
}

// destinationFor loads a destination the caller may manage: a household
// one for the admin routes, the caller's own for theirs.
func (s *server) destinationFor(c echo.Context, ownerID string) (store.NotifyDestination, error) {
	d, err := s.store.NotifyDestination(c.Request().Context(), c.Param("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.OwnerID != ownerID) {
		return d, echo.NewHTTPError(http.StatusNotFound, "destination not found")
	}
	return d, err
}

func (s *server) testDestination(c echo.Context, d store.NotifyDestination) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 20*time.Second)
	defer cancel()
	if err := s.notify.Test(ctx, d); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- admin: household destinations and settings ----

type notifySettings struct {
	PublicURL  string `json:"publicUrl"`
	NtfyServer string `json:"ntfyServer"`
}

func (s *server) notifySettings(ctx context.Context) (notifySettings, error) {
	pub, err := s.store.Setting(ctx, notify.SettingPublicURL, "")
	if err != nil {
		return notifySettings{}, err
	}
	return notifySettings{PublicURL: pub, NtfyServer: s.ntfyServer(ctx)}, nil
}

// GET /notifications (admin)
func (s *server) getNotifications(c echo.Context) error {
	ctx := c.Request().Context()
	dests, err := s.store.NotifyDestinations(ctx, "")
	if err != nil {
		return err
	}
	set, err := s.notifySettings(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"destinations": outDestinations(dests), "events": notify.EventsFor(notify.Household),
		"kinds": notify.Kinds, "settings": set,
	})
}

// PUT /notifications/settings (admin)
func (s *server) putNotifySettings(c echo.Context) error {
	ctx := c.Request().Context()
	var req notifySettings
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	for key, v := range map[string]*string{notify.SettingPublicURL: &req.PublicURL, notify.SettingNtfyServer: &req.NtfyServer} {
		*v = strings.TrimRight(strings.TrimSpace(*v), "/")
		if *v != "" && !httpURL(*v) {
			return echo.NewHTTPError(http.StatusBadRequest, "addresses must be http(s) URLs")
		}
		if err := s.store.SetSetting(ctx, key, *v); err != nil {
			return err
		}
	}
	s.audit(c, "notify.settings", "settings", "", "Notifications", "public address "+req.PublicURL+", ntfy "+req.NtfyServer)
	set, err := s.notifySettings(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, set)
}

// POST /notifications/destinations (admin)
func (s *server) createHouseholdDestination(c echo.Context) error {
	return s.saveDestination(c, notify.Household, "", nil)
}

// PATCH /notifications/destinations/:id (admin)
func (s *server) updateHouseholdDestination(c echo.Context) error {
	d, err := s.destinationFor(c, "")
	if err != nil {
		return err
	}
	return s.saveDestination(c, notify.Household, "", &d)
}

// DELETE /notifications/destinations/:id (admin)
func (s *server) deleteHouseholdDestination(c echo.Context) error {
	return s.deleteDestination(c, "")
}

// POST /notifications/destinations/:id/test (admin)
func (s *server) testHouseholdDestination(c echo.Context) error {
	d, err := s.destinationFor(c, "")
	if err != nil {
		return err
	}
	return s.testDestination(c, d)
}

func (s *server) deleteDestination(c echo.Context, ownerID string) error {
	d, err := s.destinationFor(c, ownerID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteNotifyDestination(c.Request().Context(), d.ID); err != nil {
		return err
	}
	s.audit(c, "notify.delete", "notify", d.ID, d.Name, d.Kind)
	return c.NoContent(http.StatusNoContent)
}

// ---- the caller's own destinations ----

// personalKinds are the kinds a user may set up, with the ntfy server
// fixed by the admin (and ntfy left out without one).
func (s *server) personalKinds(ctx context.Context) []notify.Kind {
	server := s.ntfyServer(ctx)
	out := []notify.Kind{}
	for _, k := range notify.Kinds {
		if !k.Personal || (k.ID == "ntfy" && server == "") {
			continue
		}
		if k.ID == "ntfy" {
			fields := []notify.Field{}
			for _, f := range k.Fields {
				if f.Name != "server" {
					fields = append(fields, f)
				}
			}
			k.Fields = fields
			k.Label = "ntfy (" + server + ")"
		}
		out = append(out, k)
	}
	return out
}

// GET /auth/me/notifications
func (s *server) getMyNotifications(c echo.Context) error {
	ctx := c.Request().Context()
	dests, err := s.store.NotifyDestinations(ctx, userID(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"destinations": outDestinations(dests), "events": notify.EventsFor(notify.Personal),
		"kinds": s.personalKinds(ctx),
	})
}

// POST /auth/me/notifications
func (s *server) createMyDestination(c echo.Context) error {
	return s.saveDestination(c, notify.Personal, userID(c), nil)
}

// PATCH /auth/me/notifications/:id
func (s *server) updateMyDestination(c echo.Context) error {
	d, err := s.destinationFor(c, userID(c))
	if err != nil {
		return err
	}
	return s.saveDestination(c, notify.Personal, userID(c), &d)
}

// DELETE /auth/me/notifications/:id
func (s *server) deleteMyDestination(c echo.Context) error {
	return s.deleteDestination(c, userID(c))
}

// POST /auth/me/notifications/:id/test
func (s *server) testMyDestination(c echo.Context) error {
	d, err := s.destinationFor(c, userID(c))
	if err != nil {
		return err
	}
	return s.testDestination(c, d)
}
