package api

import (
	"net/http"
	"sort"
	"syscall"

	"github.com/labstack/echo/v4"
)

// diskVolume is one filesystem holding library paths. Libraries that share
// a filesystem are reported once, so the dashboard shows real volumes.
type diskVolume struct {
	Paths      []string `json:"paths"`
	Libraries  []string `json:"libraries"`
	TotalBytes uint64   `json:"totalBytes"`
	FreeBytes  uint64   `json:"freeBytes"` // available to unprivileged users
	UsedBytes  uint64   `json:"usedBytes"`
	Error      string   `json:"error,omitempty"` // path missing/unreadable
}

// GET /stats/disks — space on every filesystem that backs a library.
func (s *server) diskStats(c echo.Context) error {
	libs, err := s.store.Libraries(c.Request().Context())
	if err != nil {
		return err
	}
	type fsKey struct{ a, b int32 }
	byFS := map[fsKey]*diskVolume{}
	var broken []diskVolume
	for _, lib := range libs {
		for _, p := range lib.Paths {
			var st syscall.Statfs_t
			if err := syscall.Statfs(p, &st); err != nil {
				broken = append(broken, diskVolume{Paths: []string{p}, Libraries: []string{lib.Name}, Error: err.Error()})
				continue
			}
			k := fsKey{int32(st.Fsid.X__val[0]), int32(st.Fsid.X__val[1])}
			v, ok := byFS[k]
			if !ok {
				bs := uint64(st.Bsize)
				v = &diskVolume{
					TotalBytes: st.Blocks * bs,
					FreeBytes:  st.Bavail * bs,
					UsedBytes:  (st.Blocks - st.Bfree) * bs,
				}
				byFS[k] = v
			}
			v.Paths = appendUnique(v.Paths, p)
			v.Libraries = appendUnique(v.Libraries, lib.Name)
		}
	}
	out := make([]diskVolume, 0, len(byFS)+len(broken))
	for _, v := range byFS {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Paths[0] < out[j].Paths[0] })
	out = append(out, broken...)
	return c.JSON(http.StatusOK, out)
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
