package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/store"
)

// Request-side handling of storage that has stopped answering (see
// library.StorageMonitor). A request fails with a reason a person can act
// on — which library, which folder, what to check — rather than hanging.

var storageLog = logging.For("storage")

// mediaTimeout bounds one read of a media file on a request path: long
// enough for a NAS disk spinning up from sleep, short enough that nobody is
// left looking at a button that did nothing.
const mediaTimeout = 15 * time.Second

// storageReady fails at once when the item's library is known to be
// unreachable, or when an earlier read of the item's file was abandoned and
// is still blocked — another would only join it — before anything waits.
func (s *server) storageReady(ctx context.Context, item store.Item) error {
	if s.storage != nil {
		if st := s.storage.Status(item.LibraryID); !st.Available {
			return storageHTTPError(s.libraryName(ctx, item.LibraryID), st)
		}
	}
	if library.Stuck(item.Path) {
		return storageHTTPError(s.libraryName(ctx, item.LibraryID),
			library.StorageStatus{Path: item.Path, Error: library.StuckReason})
	}
	return nil
}

// storageFailed turns a media read that ran out of time into a clear error,
// and has the library checked now: one slow file may just be a disk waking
// up, so the library is only marked unreachable if its folders also fail to
// answer.
func (s *server) storageFailed(ctx context.Context, item store.Item, err error) error {
	if !errors.Is(err, library.ErrNotResponding) {
		return err
	}
	s.recheckStorage(ctx, item)
	return storageHTTPError(s.libraryName(ctx, item.LibraryID),
		library.StorageStatus{Path: item.Path, Error: library.ErrNotResponding.Error()})
}

// recheckStorage has the item's library checked now, in the background, so
// a library whose share has gone is flagged — and every client told — within
// seconds of a read failing rather than at the next scheduled check.
func (s *server) recheckStorage(ctx context.Context, item store.Item) {
	if s.storage == nil {
		return
	}
	if lib, err := s.store.Library(ctx, item.LibraryID); err == nil {
		go s.storage.Check(lib)
	}
}

// onTranscodeStall is told when a transcode's source stops answering (see
// media.Session.checkStall). The file is marked stuck until the blocked read
// returns, so nobody starts another on it, and the library is checked now.
func (s *server) onTranscodeStall(sess *media.Session, stuck <-chan struct{}) {
	library.MarkStuck(sess.InputPath, stuck)
	ctx := context.Background()
	item, err := s.store.Item(ctx, sess.ItemID)
	if err != nil {
		storageLog.Warn("looking up a stalled session's item failed", "session", sess.ID, "err", err)
		return
	}
	s.recheckStorage(ctx, item)
}

// sessionFailed answers a request on a transcode session that cannot give it
// what it asked for. A source that stopped answering is a storage failure,
// said the same way as anywhere else — 503 with which library and file, and
// what to check — so the client shows one message for all of them.
func (s *server) sessionFailed(ctx context.Context, sess *media.Session, err error) error {
	switch {
	case errors.Is(err, media.ErrInputStalled):
		item, ierr := s.store.Item(ctx, sess.ItemID)
		if ierr != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "The storage for this title isn't responding. Check that the share is connected, then try again.")
		}
		return storageHTTPError(s.libraryName(ctx, item.LibraryID),
			library.StorageStatus{Path: item.Path, Error: library.ErrNotResponding.Error()})
	case errors.Is(err, media.ErrTranscodeFailed):
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	case errors.Is(err, context.Canceled):
		return err // the client went away
	}
	return echo.NewHTTPError(http.StatusGatewayTimeout, err.Error())
}

// serveMedia sends a media file with HTTP range support, as c.File does,
// but with a deadline on every touch of the storage: the open and stat, and
// each read. A share that stops answering mid-stream ends the response
// rather than holding the request, and the browser's connection, open for
// as long as the share is gone. attachment, when set, is the download name.
func (s *server) serveMedia(c echo.Context, item store.Item, path, attachment string) error {
	ctx := c.Request().Context()
	if err := s.storageReady(ctx, item); err != nil {
		return err
	}
	f, fi, err := library.OpenMedia(path, mediaTimeout)
	switch {
	case errors.Is(err, library.ErrNotResponding):
		return s.storageFailed(ctx, item, err)
	case errors.Is(err, os.ErrNotExist):
		return echo.NewHTTPError(http.StatusNotFound, "the file is no longer on disk")
	case err != nil:
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if fi.IsDir() {
		return echo.NewHTTPError(http.StatusNotFound, "not a file")
	}

	// The first bytes are read here, before any header is written, so a file
	// that opens but does not read answers with the storage message instead
	// of a 200 that never arrives. They also give the content type where
	// the extension does not, which spares ServeContent sniffing for itself.
	head := make([]byte, 512)
	n, err := f.Read(head)
	if errors.Is(err, library.ErrNotResponding) {
		return s.storageFailed(ctx, item, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind %s: %w", path, err)
	}
	ctype := mime.TypeByExtension(filepath.Ext(path))
	if ctype == "" {
		ctype = http.DetectContentType(head[:n])
	}
	res := c.Response()
	res.Header().Set(echo.HeaderContentType, ctype)
	if attachment != "" {
		res.Header().Set(echo.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", attachment))
	}
	http.ServeContent(res, c.Request(), fi.Name(), fi.ModTime(), f)
	if err := f.Err(); err != nil {
		// Too late for a status: the response is already under way and has
		// simply been cut short. Have the library looked at, so the
		// client's retry is refused with the reason.
		storageLog.Warn("storage stopped answering mid-stream", "path", path, "err", err)
		s.recheckStorage(context.WithoutCancel(ctx), item)
	}
	return nil
}

// itemStorageResponse is a fresh look at the storage behind an item.
type itemStorageResponse struct {
	library.StorageStatus
	Library string `json:"library"`
	// Message is the sentence to show when unavailable.
	Message string `json:"message,omitempty"`
}

// GET /items/:id/storage — checks the storage behind an item now, for a
// client whose playback has stopped receiving data and wants to say why.
// Bounded by the monitor's own deadline, so it answers within seconds even
// when the share does not.
func (s *server) itemStorage(c echo.Context) error {
	ctx := c.Request().Context()
	item, err := s.store.Item(ctx, c.Param("id"))
	if err != nil || !canSeeLibrary(currentUser(c), item.LibraryID) {
		return echo.NewHTTPError(http.StatusNotFound, "item not found")
	}
	lib, err := s.store.Library(ctx, item.LibraryID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "library not found")
	}
	st := library.StorageStatus{Available: true}
	if s.storage != nil {
		st = s.storage.Check(lib)
	}
	if st.Available && library.Stuck(item.Path) {
		st = library.StorageStatus{Path: item.Path, Error: library.StuckReason}
	}
	resp := itemStorageResponse{StorageStatus: st, Library: lib.Name}
	if !st.Available {
		resp.Message = st.Message(lib.Name)
	}
	return c.JSON(http.StatusOK, resp)
}

func storageHTTPError(libraryName string, st library.StorageStatus) *echo.HTTPError {
	return echo.NewHTTPError(http.StatusServiceUnavailable, st.Message(libraryName))
}

func (s *server) libraryName(ctx context.Context, id string) string {
	if lib, err := s.store.Library(ctx, id); err == nil {
		return lib.Name
	}
	return "this library"
}
