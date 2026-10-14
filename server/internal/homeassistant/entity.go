package homeassistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Entity is an entity's state as clients see it: normalised rather than
// Home Assistant's raw attributes, with only the fields its domain uses.
type Entity struct {
	EntityID      string   `json:"entityId"`
	Domain        string   `json:"domain"`
	Name          string   `json:"name"`
	AreaID        string   `json:"areaId"`
	State         string   `json:"state"`
	Available     bool     `json:"available"`
	On            *bool    `json:"on,omitempty"`
	Dimmable      *bool    `json:"dimmable,omitempty"`
	BrightnessPct *int     `json:"brightnessPct,omitempty"`
	Volume        *float64 `json:"volume,omitempty"`
	Muted         *bool    `json:"muted,omitempty"`
	Source        string   `json:"source,omitempty"`
	Sources       []string `json:"sources,omitempty"`
	MediaTitle    string   `json:"mediaTitle,omitempty"`
	Position      *int     `json:"position,omitempty"`
	Stream        *bool    `json:"stream,omitempty"`
}

// services are what may be asked of each supported domain: the whole of
// what user input can make Home Assistant do. A domain missing here is not
// supported at all; camera is supported for viewing and takes no service.
var services = map[string][]string{
	"light":         {"turn_on", "turn_off", "toggle"},
	"switch":        {"turn_on", "turn_off", "toggle"},
	"fan":           {"turn_on", "turn_off", "toggle"},
	"input_boolean": {"turn_on", "turn_off", "toggle"},
	"cover":         {"open_cover", "close_cover", "stop_cover"},
	"media_player":  {"turn_on", "turn_off", "volume_set", "volume_mute", "select_source", "media_play_pause"},
	"scene":         {"turn_on"},
	"script":        {"turn_on"},
	"camera":        {},
}

// Supported reports whether Cineva works with an entity's domain.
func Supported(entityID string) bool {
	_, ok := services[domainOf(entityID)]
	return ok
}

func domainOf(entityID string) string {
	d, _, _ := strings.Cut(entityID, ".")
	return d
}

// rawState is one entry of get_states, or a state_changed event's
// new_state.
type rawState struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

// cameraStream is the stream bit of a camera's supported_features.
const cameraStream = 2

// normalise turns a raw state into what clients see. areaID is resolved
// by the caller from the registries.
func normalise(st rawState, areaID string) Entity {
	domain := domainOf(st.EntityID)
	e := Entity{
		EntityID: st.EntityID, Domain: domain, AreaID: areaID, State: st.State,
		Available: st.State != "unavailable" && st.State != "unknown",
		Name:      str(st.Attributes["friendly_name"]),
	}
	if e.Name == "" {
		e.Name = st.EntityID
	}
	on := func() { b := st.State != "off" && e.Available; e.On = &b }
	switch domain {
	case "light":
		on()
		modes, _ := st.Attributes["supported_color_modes"].([]any)
		dim := false
		for _, m := range modes {
			if s := str(m); s != "" && s != "onoff" {
				dim = true
			}
		}
		e.Dimmable = &dim
		if b, ok := num(st.Attributes["brightness"]); ok && dim {
			pct := int(math.Round(b * 100 / 255))
			e.BrightnessPct = &pct
		}
	case "switch", "fan", "input_boolean":
		on()
	case "media_player":
		on()
		if v, ok := num(st.Attributes["volume_level"]); ok {
			e.Volume = &v
		}
		if m, ok := st.Attributes["is_volume_muted"].(bool); ok {
			e.Muted = &m
		}
		e.Source = str(st.Attributes["source"])
		if list, ok := st.Attributes["source_list"].([]any); ok {
			for _, s := range list {
				if s := str(s); s != "" {
					e.Sources = append(e.Sources, s)
				}
			}
		}
		e.MediaTitle = str(st.Attributes["media_title"])
	case "cover":
		if p, ok := num(st.Attributes["current_position"]); ok {
			pos := int(p)
			e.Position = &pos
		}
	case "camera":
		f, _ := num(st.Attributes["supported_features"])
		stream := int(f)&cameraStream != 0
		e.Stream = &stream
	}
	return e
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// Action is a request to work one control, as clients send it and as a
// mode's action step holds it.
type Action struct {
	Service       string   `json:"service"`
	BrightnessPct *int     `json:"brightnessPct,omitempty"`
	Volume        *float64 `json:"volume,omitempty"`
	Muted         *bool    `json:"muted,omitempty"`
	Source        string   `json:"source,omitempty"`
}

// ErrInvalid is a control or mode the rules do not allow. Errors that are
// one (errors.Is) carry a message meant for the person who asked.
var ErrInvalid = errors.New("invalid")

type invalidError struct{ msg string }

func (e invalidError) Error() string        { return e.msg }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(format string, args ...any) error {
	return invalidError{fmt.Sprintf(format, args...)}
}

// serviceData checks an action against the allowed services and fields
// for the entity's domain, and returns Home Assistant's service_data for
// it. sources, when known, are the entity's inputs a select_source must
// be one of.
func serviceData(entityID string, a Action, sources []string) (map[string]any, error) {
	domain := domainOf(entityID)
	allowed, ok := services[domain]
	if !ok {
		return nil, invalid("%s is not a kind of entity Cineva works with", entityID)
	}
	if !slices.Contains(allowed, a.Service) {
		return nil, invalid("%s cannot be asked to %s", entityID, a.Service)
	}
	data := map[string]any{}
	only := func(field string, set bool, want bool) error {
		if set && !want {
			return invalid("%s does not take %s with %s", entityID, field, a.Service)
		}
		if !set && want {
			return invalid("%s.%s needs %s", domain, a.Service, field)
		}
		return nil
	}
	// brightnessPct is optional on light.turn_on (on at the last level),
	// and every other field is required by exactly one service.
	lightOn := domain == "light" && a.Service == "turn_on"
	if a.BrightnessPct != nil && !lightOn {
		return nil, invalid("%s does not take brightnessPct with %s", entityID, a.Service)
	}
	if a.BrightnessPct != nil {
		if *a.BrightnessPct < 0 || *a.BrightnessPct > 100 {
			return nil, invalid("brightnessPct must be between 0 and 100")
		}
		data["brightness_pct"] = *a.BrightnessPct
	}
	isMedia := domain == "media_player"
	if err := only("volume", a.Volume != nil, isMedia && a.Service == "volume_set"); err != nil {
		return nil, err
	}
	if a.Volume != nil {
		if *a.Volume < 0 || *a.Volume > 1 {
			return nil, invalid("volume must be between 0 and 1")
		}
		data["volume_level"] = *a.Volume
	}
	if err := only("muted", a.Muted != nil, isMedia && a.Service == "volume_mute"); err != nil {
		return nil, err
	}
	if a.Muted != nil {
		data["is_volume_muted"] = *a.Muted
	}
	if err := only("source", a.Source != "", isMedia && a.Service == "select_source"); err != nil {
		return nil, err
	}
	if a.Source != "" {
		if sources != nil && !slices.Contains(sources, a.Source) {
			return nil, invalid("%s has no input called %q", entityID, a.Source)
		}
		data["source"] = a.Source
	}
	return data, nil
}
