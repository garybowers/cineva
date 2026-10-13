package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/store"
)

// Request-side handling of storage that has stopped answering (see
// library.StorageMonitor). A request fails with a reason a person can act
// on — which library, which folder, what to check — rather than hanging.

// mediaTimeout bounds one read of a media file on a request path: long
// enough for a NAS disk spinning up from sleep, short enough that nobody is
// left looking at a button that did nothing.
const mediaTimeout = 15 * time.Second

// storageReady fails at once when the item's library is known to be
// unreachable, before anything waits on it.
func (s *server) storageReady(ctx context.Context, item store.Item) error {
	if s.storage == nil {
		return nil
	}
	st := s.storage.Status(item.LibraryID)
	if st.Available {
		return nil
	}
	return storageHTTPError(s.libraryName(ctx, item.LibraryID), st)
}

// storageFailed turns a media read that ran out of time into a clear error,
// and has the library checked now: one slow file may just be a disk waking
// up, so the library is only marked unreachable if its folders also fail to
// answer.
func (s *server) storageFailed(ctx context.Context, item store.Item, err error) error {
	if !errors.Is(err, library.ErrNotResponding) {
		return err
	}
	name := s.libraryName(ctx, item.LibraryID)
	if s.storage != nil {
		if lib, lerr := s.store.Library(ctx, item.LibraryID); lerr == nil {
			go s.storage.Check(lib)
		}
	}
	return storageHTTPError(name, library.StorageStatus{Path: item.Path, Error: err.Error()})
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
