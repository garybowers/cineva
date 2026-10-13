package arr

import (
	"path/filepath"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// MapPaths translates paths as the instance sees them into Cineva's, using
// the longest matching prefix of each. Paths no mapping covers are kept as
// they are: an instance on the same host sees what Cineva sees.
func MapPaths(maps []store.PathMap, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		clean := filepath.Clean(p)
		best := -1
		for i, m := range maps {
			from := filepath.Clean(m.From)
			if m.From == "" || m.To == "" || !under(clean, from) {
				continue
			}
			if best < 0 || len(from) > len(filepath.Clean(maps[best].From)) {
				best = i
			}
		}
		if best < 0 {
			out = append(out, p)
			continue
		}
		from := filepath.Clean(maps[best].From)
		out = append(out, filepath.Join(filepath.Clean(maps[best].To), strings.TrimPrefix(clean, from)))
	}
	return out
}

// under reports whether p is root or inside it.
func under(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator)) || root == string(filepath.Separator)
}

// SuggestPathMaps proposes mappings for an instance's root folders onto the
// folders of Cineva's libraries of its media type. A root folder a library
// already contains (or is contained by) needs none. Otherwise it maps to
// the library folder with the same name, ignoring case — Sonarr's /tv to
// /mnt/media/TV — or, when there is just one root folder and one library
// folder, to that.
func SuggestPathMaps(kind string, roots []string, libs []store.Library) []store.PathMap {
	want := MediaType(kind)
	var folders []string
	for _, lib := range libs {
		if lib.MediaType == want && lib.Source == "local" {
			for _, p := range lib.Paths {
				folders = append(folders, filepath.Clean(p))
			}
		}
	}
	out := []store.PathMap{}
	for _, r := range roots {
		root := filepath.Clean(r)
		seen := false
		for _, f := range folders {
			if under(root, f) || under(f, root) {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		var match string
		for _, f := range folders {
			if strings.EqualFold(filepath.Base(f), filepath.Base(root)) {
				match = f
				break
			}
		}
		if match == "" && len(roots) == 1 && len(folders) == 1 {
			match = folders[0]
		}
		if match != "" {
			out = append(out, store.PathMap{From: root, To: match})
		}
	}
	return out
}
