package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/store"
)

// Download jobs and the library ids list, for native apps keeping music on
// the device (server/docs/offline.md, stage 1).

// downloadProfiles are what a job may ask for: the original file, or an
// Opus transcode at a bit rate. Video profiles come with a later stage.
var downloadProfiles = map[string]struct {
	format string
	kbps   int
}{
	"original": {},
	"opus-128": {format: "opus", kbps: 128},
	"opus-64":  {format: "opus", kbps: 64},
}

// appDeviceSecret is the device secret for a native app's own device id:
// an HMAC under the identity key, so the same app on the same phone is the
// same device at every sign-in and nothing else can name it (the Jellyfin
// shim does the same for its DeviceId).
func (s *server) appDeviceSecret(ctx context.Context, deviceID string) (string, error) {
	id, err := s.store.EnsureIdentity(ctx)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, id.PrivateKey.Seed())
	mac.Write([]byte("app-device:" + deviceID))
	return "app_" + hex.EncodeToString(mac.Sum(nil)), nil
}

// preparing holds the jobs an encode is running for, so a job is never
// prepared twice at once and a restart can pick up what it finds queued.
type preparing struct{ m sync.Map }

func (p *preparing) start(id string) bool { _, busy := p.m.LoadOrStore(id, true); return !busy }
func (p *preparing) done(id string)       { p.m.Delete(id) }

// downloadRequest is POST /downloads: one item or several, at a profile.
type downloadRequest struct {
	ItemID  string   `json:"itemId"`
	ItemIDs []string `json:"itemIds"`
	Profile string   `json:"profile"`
}

// POST /downloads — makes a job per track. An album or artist id expands
// to its tracks. An original is ready at once; a transcode is prepared in
// the background through the audio cache.
func (s *server) createDownloads(c echo.Context) error {
	ctx := c.Request().Context()
	var req downloadRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	profile, ok := downloadProfiles[req.Profile]
	if !ok {
		if strings.HasPrefix(req.Profile, "mp4") {
			return echo.NewHTTPError(http.StatusBadRequest, "video download profiles are not available yet")
		}
		return echo.NewHTTPError(http.StatusBadRequest, "unknown profile; use original, opus-128 or opus-64")
	}
	ids := req.ItemIDs
	if req.ItemID != "" {
		ids = append([]string{req.ItemID}, ids...)
	}
	if len(ids) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "itemId or itemIds is required")
	}
	me := currentUser(c)
	var tracks []store.Item
	seen := map[string]bool{}
	for _, id := range ids {
		item, err := s.store.Item(ctx, id)
		if err != nil || !canSeeLibrary(me, item.LibraryID) {
			return echo.NewHTTPError(http.StatusNotFound, "item not found")
		}
		var under []store.Item
		switch item.Kind {
		case store.KindTrack:
			under = []store.Item{item}
		case store.KindAlbum, store.KindArtist:
			if under, err = s.store.TracksUnder(ctx, me.ID, item); err != nil {
				return err
			}
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "only tracks, albums and artists can be downloaded in this version")
		}
		for _, t := range under {
			if !seen[t.ID] {
				seen[t.ID] = true
				tracks = append(tracks, t)
			}
		}
	}
	unfinished, err := s.store.UnfinishedDownloadJobs(ctx, me.ID)
	if err != nil {
		return err
	}
	pending := 0
	if profile.format != "" {
		pending = len(tracks)
	}
	if unfinished+pending > store.MaxUnfinishedDownloads {
		return echo.NewHTTPError(http.StatusTooManyRequests, "too many downloads still being prepared; fetch some first")
	}
	jobs, err := s.store.CreateDownloadJobs(ctx, me.ID, sessionIDOf(c), req.Profile, tracks, func(it store.Item) (string, int64) {
		if profile.format == "" {
			return store.DownloadReady, it.SizeBytes
		}
		return store.DownloadQueued, 0
	})
	if err != nil {
		return err
	}
	for i := range jobs {
		s.startPreparing(jobs[i], tracks[i])
		s.decorateJob(c, &jobs[i])
	}
	s.audit(c, "download.request", "item", ids[0], "", req.Profile+" · "+strconv.Itoa(len(jobs))+" tracks")
	return c.JSON(http.StatusCreated, jobs)
}

// startPreparing runs a transcode job's encode in the background, once.
// prepareDownload is what encodes; tests replace it.
func (s *server) startPreparing(job store.DownloadJob, item store.Item) {
	if job.Status == store.DownloadReady || job.Status == store.DownloadFailed {
		return
	}
	if !s.preparing.start(job.ID) {
		return
	}
	profile := downloadProfiles[job.Profile]
	go func() {
		defer s.preparing.done(job.ID)
		ctx := context.Background()
		if err := s.store.SetDownloadJobStatus(ctx, job.ID, store.DownloadPreparing, 0, ""); err != nil {
			downloadLog.Warn("marking a download preparing failed", "job", job.ID, "err", err)
		}
		prepare := s.prepareDownload
		if prepare == nil {
			prepare = s.prepareTranscode
		}
		bytes, err := prepare(ctx, item, profile.format, profile.kbps)
		if err != nil {
			downloadLog.Warn("preparing a download failed", "title", item.Title, "profile", job.Profile, "err", err)
			if serr := s.store.SetDownloadJobStatus(ctx, job.ID, store.DownloadFailed, 0, err.Error()); serr != nil {
				downloadLog.Warn("marking a download failed failed", "job", job.ID, "err", serr)
			}
			return
		}
		if err := s.store.SetDownloadJobStatus(ctx, job.ID, store.DownloadReady, bytes, ""); err != nil {
			downloadLog.Warn("marking a download ready failed", "job", job.ID, "err", err)
		}
	}()
}

// prepareTranscode encodes a track through the audio cache (or finds it
// there) and reports the file's size.
func (s *server) prepareTranscode(ctx context.Context, item store.Item, format string, kbps int) (int64, error) {
	path, _, err := s.transcodedTrack(ctx, item, format, kbps)
	if err != nil {
		return 0, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// decorateJob fills the URL a ready job is fetched from.
func (s *server) decorateJob(c echo.Context, j *store.DownloadJob) {
	if j.Status == store.DownloadReady {
		j.URL = "/api/v1/downloads/" + j.ID + "/file"
	}
}

// jobFor loads a job the caller owns, and restarts its preparation if a
// restart left it queued.
func (s *server) jobFor(c echo.Context) (store.DownloadJob, store.Item, error) {
	ctx := c.Request().Context()
	job, err := s.store.DownloadJob(ctx, c.Param("id"))
	if err != nil || job.UserID != userID(c) {
		return store.DownloadJob{}, store.Item{}, echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	item, err := s.store.Item(ctx, job.ItemID)
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return store.DownloadJob{}, store.Item{}, echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	if job.Status == store.DownloadQueued || job.Status == store.DownloadPreparing {
		s.startPreparing(job, item)
	}
	return job, item, nil
}

// GET /downloads — the caller's jobs.
func (s *server) listDownloads(c echo.Context) error {
	jobs, err := s.store.DownloadJobs(c.Request().Context(), userID(c))
	if err != nil {
		return err
	}
	for i := range jobs {
		s.decorateJob(c, &jobs[i])
	}
	return c.JSON(http.StatusOK, jobs)
}

// GET /downloads/:id — one job, polled until ready.
func (s *server) getDownload(c echo.Context) error {
	job, _, err := s.jobFor(c)
	if err != nil {
		return err
	}
	s.decorateJob(c, &job)
	return c.JSON(http.StatusOK, job)
}

// GET /downloads/:id/file — the file, with ranges. An original comes from
// the library; a transcode from the audio cache, waited for if it is not
// finished yet.
func (s *server) downloadFile(c echo.Context) error {
	job, item, err := s.jobFor(c)
	if err != nil {
		return err
	}
	if job.Status == store.DownloadFailed {
		return echo.NewHTTPError(http.StatusConflict, "the download failed: "+job.Error)
	}
	profile := downloadProfiles[job.Profile]
	if profile.format == "" {
		return s.serveMedia(c, item, item.Path, filepath.Base(item.Path))
	}
	return s.downloadTranscoded(c, item, profile.format, profile.kbps)
}

// DELETE /downloads/:id — forgets the job; the cache keeps the file.
func (s *server) deleteDownload(c echo.Context) error {
	if err := s.store.DeleteDownloadJob(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "download not found")
	}
	return c.NoContent(http.StatusNoContent)
}

// GET /libraries/:id/ids — every item id in the library, for a client's
// local copy to find deletions.
func (s *server) libraryIDs(c echo.Context) error {
	if !canSeeLibrary(currentUser(c), c.Param("id")) {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	ids, err := s.store.LibraryItemIDs(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, ids)
}

// sessionIDOf is the caller's session (its device), or "" for an API key.
func sessionIDOf(c echo.Context) string {
	id, _ := c.Get(ctxSessionID).(string)
	return id
}
