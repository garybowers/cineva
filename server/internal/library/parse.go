// Package library implements the scanner framework and per-domain scanners.
package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true,
	".mov": true, ".webm": true, ".ts": true, ".wmv": true,
}

func IsVideoFile(path string) bool {
	return videoExts[strings.ToLower(filepath.Ext(path))]
}

var (
	// "Succession - S04E02 - Rehearsal HDTV-1080p.mkv"
	episodeRe = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*E(\d{1,3})\b`)
	// "Heat (1995).mkv" / "Heat (1995) Remastered"
	movieYearRe = regexp.MustCompile(`^(.*?)\s*\((\d{4})\)`)
	// "Season 04" directory
	seasonDirRe = regexp.MustCompile(`(?i)^season\s*(\d{1,3})$`)
	// Trailing quality tags worth stripping from episode titles
	qualityRe = regexp.MustCompile(`(?i)\s*\b(HDTV|WEB-?DL|WEBRip|BluRay|BRRip|DVDRip|\d{3,4}p)\b.*$`)
)

type EpisodeInfo struct {
	Show    string
	Season  int
	Episode int
	Title   string
}

// ParseEpisode extracts show/season/episode from a file path, using the
// SxxEyy marker in the filename and the directory layout
// (Show/Season NN/file) as fallback for the show name and season.
func ParseEpisode(path string) (EpisodeInfo, bool) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	m := episodeRe.FindStringSubmatchIndex(base)
	if m == nil {
		return EpisodeInfo{}, false
	}
	season, _ := strconv.Atoi(base[m[2]:m[3]])
	episode, _ := strconv.Atoi(base[m[4]:m[5]])

	info := EpisodeInfo{Season: season, Episode: episode}

	// Show: text before the marker, else the grandparent directory.
	show := strings.Trim(strings.TrimRight(base[:m[0]], " -."), " ")
	seasonDir := filepath.Base(filepath.Dir(path))
	showDir := filepath.Base(filepath.Dir(filepath.Dir(path)))
	if sm := seasonDirRe.FindStringSubmatch(seasonDir); sm != nil {
		if s, _ := strconv.Atoi(sm[1]); s > 0 {
			info.Season = s
		}
		if show == "" {
			show = showDir
		}
	} else if show == "" {
		show = seasonDir
	}
	info.Show = show

	// Episode title: text after the marker, minus quality tags.
	title := strings.Trim(strings.TrimLeft(base[m[1]:], " -."), " ")
	title = strings.TrimSpace(qualityRe.ReplaceAllString(title, ""))
	info.Title = title
	return info, true
}

type MovieInfo struct {
	Title string
	Year  int
}

// ParseMovie extracts title/year from "Title (Year)" in the filename, falling
// back to the parent directory, then the bare filename.
func ParseMovie(path string) MovieInfo {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, candidate := range []string{base, filepath.Base(filepath.Dir(path))} {
		if m := movieYearRe.FindStringSubmatch(candidate); m != nil {
			year, _ := strconv.Atoi(m[2])
			return MovieInfo{Title: strings.TrimSpace(m[1]), Year: year}
		}
	}
	title := strings.TrimSpace(qualityRe.ReplaceAllString(base, ""))
	return MovieInfo{Title: title}
}
