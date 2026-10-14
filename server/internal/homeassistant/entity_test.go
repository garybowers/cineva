package homeassistant

import (
	"errors"
	"reflect"
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

func ptr[T any](v T) *T { return &v }

func TestNormalise(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   rawState
		want Entity
	}{
		{
			"dimmable light, on",
			rawState{"light.lounge", "on", map[string]any{"friendly_name": "Lounge", "brightness": 128.0, "supported_color_modes": []any{"brightness"}}},
			Entity{EntityID: "light.lounge", Domain: "light", Name: "Lounge", AreaID: "a", State: "on", Available: true,
				On: ptr(true), Dimmable: ptr(true), BrightnessPct: ptr(50)},
		},
		{
			"on/off light takes no brightness",
			rawState{"light.hall", "off", map[string]any{"supported_color_modes": []any{"onoff"}, "brightness": 255.0}},
			Entity{EntityID: "light.hall", Domain: "light", Name: "light.hall", AreaID: "a", State: "off", Available: true,
				On: ptr(false), Dimmable: ptr(false)},
		},
		{
			"unavailable switch is not on",
			rawState{"switch.fan", "unavailable", map[string]any{}},
			Entity{EntityID: "switch.fan", Domain: "switch", Name: "switch.fan", AreaID: "a", State: "unavailable", On: ptr(false)},
		},
		{
			"amplifier",
			rawState{"media_player.avr", "on", map[string]any{"volume_level": 0.4, "is_volume_muted": false,
				"source": "HDMI 1", "source_list": []any{"HDMI 1", "Tuner"}, "media_title": "Radio 3"}},
			Entity{EntityID: "media_player.avr", Domain: "media_player", Name: "media_player.avr", AreaID: "a", State: "on",
				Available: true, On: ptr(true), Volume: ptr(0.4), Muted: ptr(false), Source: "HDMI 1",
				Sources: []string{"HDMI 1", "Tuner"}, MediaTitle: "Radio 3"},
		},
		{
			"cover position",
			rawState{"cover.blind", "open", map[string]any{"current_position": 70.0}},
			Entity{EntityID: "cover.blind", Domain: "cover", Name: "cover.blind", AreaID: "a", State: "open", Available: true, Position: ptr(70)},
		},
		{
			"camera that streams",
			rawState{"camera.door", "idle", map[string]any{"supported_features": 3.0}},
			Entity{EntityID: "camera.door", Domain: "camera", Name: "camera.door", AreaID: "a", State: "idle", Available: true, Stream: ptr(true)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalise(tc.in, "a"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestServiceData(t *testing.T) {
	inputs := []string{"HDMI 1", "Tuner"}
	for _, tc := range []struct {
		name    string
		entity  string
		action  Action
		want    map[string]any
		invalid bool
	}{
		{"light on", "light.lounge", Action{Service: "turn_on"}, map[string]any{}, false},
		{"light dimmed", "light.lounge", Action{Service: "turn_on", BrightnessPct: ptr(20)}, map[string]any{"brightness_pct": 20}, false},
		{"brightness out of range", "light.lounge", Action{Service: "turn_on", BrightnessPct: ptr(120)}, nil, true},
		{"brightness on a switch", "switch.fan", Action{Service: "turn_on", BrightnessPct: ptr(20)}, nil, true},
		{"brightness with turn_off", "light.lounge", Action{Service: "turn_off", BrightnessPct: ptr(20)}, nil, true},
		{"service not allowed", "light.lounge", Action{Service: "flash"}, nil, true},
		{"unsupported domain", "lock.front", Action{Service: "unlock"}, nil, true},
		{"camera takes nothing", "camera.door", Action{Service: "turn_on"}, nil, true},
		{"volume", "media_player.avr", Action{Service: "volume_set", Volume: ptr(0.3)}, map[string]any{"volume_level": 0.3}, false},
		{"volume needs a level", "media_player.avr", Action{Service: "volume_set"}, nil, true},
		{"volume too loud", "media_player.avr", Action{Service: "volume_set", Volume: ptr(1.5)}, nil, true},
		{"mute", "media_player.avr", Action{Service: "volume_mute", Muted: ptr(true)}, map[string]any{"is_volume_muted": true}, false},
		{"input", "media_player.avr", Action{Service: "select_source", Source: "Tuner"}, map[string]any{"source": "Tuner"}, false},
		{"unknown input", "media_player.avr", Action{Service: "select_source", Source: "Aux"}, nil, true},
		{"input on the wrong service", "media_player.avr", Action{Service: "turn_on", Source: "Tuner"}, nil, true},
		{"scene", "scene.movie", Action{Service: "turn_on"}, map[string]any{}, false},
		{"blinds", "cover.blind", Action{Service: "close_cover"}, map[string]any{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serviceData(tc.entity, tc.action, inputs)
			if tc.invalid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateSteps(t *testing.T) {
	none := func(string) (Entity, bool) { return Entity{}, false }
	act := func(id, svc string) store.HAModeStep {
		return store.HAModeStep{Type: "action", EntityID: id, Service: svc}
	}
	wait := func(n int) store.HAModeStep { return store.HAModeStep{Type: "wait", Seconds: n} }
	play := store.HAModeStep{Type: "play", DeviceID: "tv"}
	many := make([]store.HAModeStep, MaxModeSteps+1)
	for i := range many {
		many[i] = act("scene.movie", "turn_on")
	}
	for _, tc := range []struct {
		name  string
		steps []store.HAModeStep
		ok    bool
	}{
		{"movie mode", []store.HAModeStep{act("scene.movie", "turn_on"), wait(5), play}, true},
		{"no steps", nil, false},
		{"too many", many, false},
		{"wait too long", []store.HAModeStep{wait(31)}, false},
		{"waits add up", []store.HAModeStep{wait(30), wait(30), wait(1)}, false},
		{"two plays", []store.HAModeStep{play, play}, false},
		{"play needs a device", []store.HAModeStep{{Type: "play"}}, false},
		{"camera action", []store.HAModeStep{act("camera.door", "turn_on")}, false},
		{"bad service", []store.HAModeStep{act("light.lounge", "flash")}, false},
		{"unknown kind", []store.HAModeStep{{Type: "reboot"}}, false},
		{"action with a device", []store.HAModeStep{{Type: "action", EntityID: "scene.movie", Service: "turn_on", DeviceID: "tv"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSteps(tc.steps, none)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestAllowed(t *testing.T) {
	open := store.LibraryAccess{}
	kids := store.LibraryAccess{Restricted: true, GroupIDs: []string{"kids"}}
	gary := store.LibraryAccess{Restricted: true, UserIDs: []string{"gary"}}
	admins := store.LibraryAccess{Restricted: true}
	for _, tc := range []struct {
		name string
		acc  store.LibraryAccess
		p    Principal
		want bool
	}{
		{"open", open, Principal{ID: "x"}, true},
		{"in the group", kids, Principal{ID: "x", GroupIDs: []string{"kids"}}, true},
		{"not in the group", kids, Principal{ID: "x", GroupIDs: []string{"adults"}}, false},
		{"listed by name", gary, Principal{ID: "gary"}, true},
		{"admins only", admins, Principal{ID: "x"}, false},
		{"an admin", admins, Principal{ID: "x", Admin: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allowed(tc.acc, tc.p); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
