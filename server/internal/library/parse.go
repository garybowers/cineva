// Package library implements the scanner framework and per-domain scanners.
package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
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
	// "10 Cloverfield Lane 2016 Remux-2160p" — bare year token
	// Word-bounded so consecutive years both match: a leading separator
	// would be eaten by the first match and hide the second.
	bareYearRe = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	// "Season 04" directory
	seasonDirRe = regexp.MustCompile(`(?i)^season\s*(\d{1,3})$`)
	// Trailing quality/release tags worth stripping from titles
	qualityRe = regexp.MustCompile(`(?i)[\s.-]*\b(HDTV|WEB-?DL|WEBRip|BluRay|BRRip|DVDRip|Remux|x26[45]|h26[45]|HEVC|\d{3,4}p)\b.*$`)
	// Any marker that gives a release name away, anywhere in the string.
	releaseMarkerRe = regexp.MustCompile(`(?i)\b(\d{3,4}p|x26[45]|h26[45]|HEVC|AVC|BluRay|Blu-ray|BRRip|BDRip|WEB-?DL|WEBRip|DVDRip|Remux|HDTV|DTS(-HD)?|TrueHD|Atmos|AAC|AC3|DDP?[0-9]|PROPER|REPACK|EXTENDED|UNRATED|IMAX|10bit|HDR|SDR|iTA|ENG)\b`)
)

// normaliseTitle reduces a title to its letters and digits so that
// "Bram Stoker's Dracula" and "Bram.Stokers.Dracula" compare equal.
func normaliseTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// LooksLikeRelease reports whether a string reads as a scene release name
// rather than a title — either it carries a release marker, or it is a run of
// dot-separated words with no spaces ("Almost.Famous.2000.BluRay").
func LooksLikeRelease(s string) bool {
	if releaseMarkerRe.MatchString(s) {
		return true
	}
	return !strings.Contains(s, " ") && strings.Count(s, ".") >= 2
}

// Trailing noise a title tag may carry after the title proper.
var tagTailRe = regexp.MustCompile(`(?i)[\s._-]*(\[[^\]]*\]|\((?:19|20)\d{2}\)|\b(unrated|extended|remastered|proper|repack|imax|theatrical|uncut|redux|director'?s cut|special edition)\b)\s*$`)

// trimTagTail strips trailing years and edition words from a title tag,
// leaving the title proper.
func trimTagTail(s string) string {
	for i := 0; i < 4; i++ {
		trimmed := strings.TrimSpace(tagTailRe.ReplaceAllString(s, ""))
		if trimmed == s || trimmed == "" {
			break
		}
		s = trimmed
	}
	return strings.Trim(s, " -._")
}

// PreferTagTitle reports whether an embedded title tag should replace the
// title parsed from the path.
//
// Tags earn their keep by restoring punctuation the filename flattens
// ("Bram Stokers Dracula" -> "Bram Stoker's Dracula"), so a tag is taken when
// it is the same title spelled better. What it must never do is import a
// scene release's own name: encoders routinely stamp the release string, or
// just the group ("EVO"), into the title tag, and those files sit beside
// perfectly clean paths. When the path yielded nothing, any tag that does not
// read as a release name is still better than no title at all.
func PreferTagTitle(tag, parsed string) (string, bool) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", false
	}
	if parsed == "" {
		if LooksLikeRelease(tag) {
			return "", false
		}
		return tag, true
	}
	if normaliseTitle(tag) == normaliseTitle(parsed) {
		return tag, true
	}
	// The tag may be the same title with a year or edition tacked on; keep
	// its punctuation but not the tail.
	if core := trimTagTail(tag); core != tag && normaliseTitle(core) == normaliseTitle(parsed) {
		return core, true
	}
	return "", false
}

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

// Directory names and filename suffixes that mark bonus material rather than
// the feature itself. These are the conventions Plex, Jellyfin and Kodi all
// use, so a library laid out for any of them is understood here.
var (
	extrasDirRe  = regexp.MustCompile(`(?i)^(extras?|featurettes?|behind the scenes|deleted scenes|interviews|scenes|shorts|trailers?|other|specials|bonus|sample)$`)
	extrasFileRe = regexp.MustCompile(`(?i)[ ._-](trailer|sample|featurette|behindthescenes|behind the scenes|deleted|interview|short|scene|other)\d*$`)
)

// IsExtra reports whether a video file is bonus material — a trailer, a
// featurette, a deleted scene — rather than a feature in its own right.
//
// Without this every Featurettes/ entry becomes its own "movie" in the grid,
// and a 90-second trailer sits beside the film it belongs to. The feature
// itself is never in one of these folders, so skipping the whole subtree is
// safe; a sidecar "Movie-trailer.mkv" is caught by the filename rule.
func IsExtra(path string) bool {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if extrasFileRe.MatchString(base) {
		return true
	}
	// Any ancestor directory named like an extras folder, up to the library
	// root — which the caller has already bounded by walking from it.
	for dir := filepath.Dir(path); ; {
		if extrasDirRe.MatchString(filepath.Base(dir)) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

type MovieInfo struct {
	Title string
	Year  int
	// TmdbID and ImdbID come from an explicit id hint in the filename or
	// its parent directory. They are the Jellyfin/Radarr convention and a
	// cheap escape hatch for titles that would otherwise mismatch — a
	// scene-release name with no usable title, or a common title whose
	// namesake is more popular. Zero/empty when the name carries no hint.
	TmdbID int
	ImdbID string
}

// Id hints as Jellyfin and Radarr write them:
//
//	Movie (2020) [tmdbid=12345].mkv
//	Movie (2020) {tmdb-12345}.mkv
//	Movie (2020) [imdbid=tt0133093].mkv
//
// Both bracket styles and both separators are accepted because the tools in
// this ecosystem disagree about which they emit.
var (
	tmdbHintRe = regexp.MustCompile(`(?i)[\[{]\s*tmdb(?:id)?[-=\s]\s*(\d+)\s*[\]}]`)
	imdbHintRe = regexp.MustCompile(`(?i)[\[{]\s*imdb(?:id)?[-=\s]\s*(tt\d+)\s*[\]}]`)
)

// idHints reads an explicit provider id out of a filename or its parent
// directory. The directory is checked too because the convention is often
// applied to the folder of a one-film-per-folder library.
func idHints(path string) (tmdbID int, imdbID string) {
	for _, s := range []string{filepath.Base(path), filepath.Base(filepath.Dir(path))} {
		if tmdbID == 0 {
			if m := tmdbHintRe.FindStringSubmatch(s); m != nil {
				tmdbID, _ = strconv.Atoi(m[1])
			}
		}
		if imdbID == "" {
			if m := imdbHintRe.FindStringSubmatch(s); m != nil {
				imdbID = m[1]
			}
		}
	}
	return tmdbID, imdbID
}

func cleanTitle(t string) string {
	t = qualityRe.ReplaceAllString(t, "")
	t = strings.Trim(t, " -._")
	// "American.Siege" — dots are the separators when there are no spaces.
	if !strings.Contains(t, " ") && (strings.Contains(t, ".") || strings.Contains(t, "_")) {
		t = strings.NewReplacer(".", " ", "_", " ").Replace(t)
		t = strings.Join(strings.Fields(t), " ")
	}
	return t
}

// ParseMovie extracts title/year from the filename ("Title (Year)" or
// "Title YYYY Quality..."), falling back to the parent directory, then the
// bare filename with release tags stripped.
func ParseMovie(path string) MovieInfo {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	candidates := []string{base, filepath.Base(filepath.Dir(path))}
	tmdbID, imdbID := idHints(path)
	// Applied to whichever title/year the parsing below settles on, so a
	// hint overrides the match without discarding the parsed name — which
	// is still what the operator sees in the admin list.
	withHints := func(mi MovieInfo) MovieInfo {
		mi.TmdbID, mi.ImdbID = tmdbID, imdbID
		return mi
	}
	for _, candidate := range candidates {
		if m := movieYearRe.FindStringSubmatch(candidate); m != nil {
			year, _ := strconv.Atoi(m[2])
			return withHints(MovieInfo{Title: cleanTitle(m[1]), Year: year})
		}
	}
	for _, candidate := range candidates {
		// Last match wins: an earlier year may belong to the title itself.
		all := bareYearRe.FindAllStringSubmatchIndex(candidate, -1)
		for i := len(all) - 1; i >= 0; i-- {
			loc := all[i]
			title := cleanTitle(candidate[:loc[0]])
			if title == "" {
				continue // the year is the whole title so far; try an earlier one
			}
			year, _ := strconv.Atoi(candidate[loc[2]:loc[3]])
			return withHints(MovieInfo{Title: title, Year: year})
		}
	}
	return withHints(MovieInfo{Title: cleanTitle(base)})
}
