package arr

import (
	"path/filepath"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// MediaType is the library type an app's events belong to.
func MediaType(kind string) store.MediaType {
	switch kind {
	case "radarr":
		return store.MediaMovies
	case "lidarr":
		return store.MediaMusic
	}
	return store.MediaTV
}

// Libraries picks the libraries an event should rescan: those of the app's
// media type with a folder containing one of the event's paths. When none
// does — the app sees the files through different mount points, as it does
// in a container — it falls back to every library of that type, since a
// rescan of the wrong library costs a folder walk and a missed one costs
// the episode.
func Libraries(libs []store.Library, kind string, paths []string) []store.Library {
	want := MediaType(kind)
	var typed, matched []store.Library
	for _, lib := range libs {
		if lib.MediaType != want || lib.Source != "local" {
			continue
		}
		typed = append(typed, lib)
		if containsAny(lib.Paths, paths) {
			matched = append(matched, lib)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return typed
}

func containsAny(roots, paths []string) bool {
	for _, r := range roots {
		r = filepath.Clean(r)
		for _, p := range paths {
			p = filepath.Clean(p)
			if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}
