package arr

import (
	"encoding/json"
	"fmt"
)

// Event is a webhook delivery, reduced to what decides a rescan.
type Event struct {
	Type string
	// Paths are the folders and files the event names, as the app sees
	// them — which may not be how Cineva sees them, when the app runs in a
	// container with its own mount points.
	Paths []string
}

// Test is the event an app sends when a webhook is saved or its Test button
// pressed.
func (e Event) Test() bool { return e.Type == "Test" }

// ChangesFiles reports whether the event added, replaced, renamed or
// removed media files — the ones worth a rescan.
func (e Event) ChangesFiles() bool {
	switch e.Type {
	case "Download", "ImportComplete", "Rename",
		"SeriesDelete", "EpisodeFileDelete",
		"MovieDelete", "MovieFileDelete":
		return true
	}
	return false
}

type pathed struct {
	Path       string `json:"path"`
	FolderPath string `json:"folderPath"`
}

// ParseEvent reads a Sonarr or Radarr webhook body.
func ParseEvent(body []byte) (Event, error) {
	var raw struct {
		EventType    string   `json:"eventType"`
		Series       *pathed  `json:"series"`
		Movie        *pathed  `json:"movie"`
		EpisodeFile  *pathed  `json:"episodeFile"`
		EpisodeFiles []pathed `json:"episodeFiles"`
		MovieFile    *pathed  `json:"movieFile"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Event{}, fmt.Errorf("webhook body: %w", err)
	}
	ev := Event{Type: raw.EventType}
	add := func(p *pathed) {
		if p == nil {
			return
		}
		for _, s := range []string{p.Path, p.FolderPath} {
			if s != "" {
				ev.Paths = append(ev.Paths, s)
			}
		}
	}
	add(raw.EpisodeFile)
	add(raw.MovieFile)
	for i := range raw.EpisodeFiles {
		add(&raw.EpisodeFiles[i])
	}
	add(raw.Series)
	add(raw.Movie)
	return ev, nil
}
