package playback

import (
	"testing"
	"time"
)

func TestRegistryLifecycle(t *testing.T) {
	changes := 0
	r := NewRegistry(time.Second)
	r.OnChange = func() { changes++ }

	r.Start(Playback{UserID: "u", ItemID: "a", Title: "A", Mode: "hls", SessionID: "s1"})
	r.Start(Playback{UserID: "u", ItemID: "b", Title: "B", Mode: "direct"})
	if got := len(r.List()); got != 2 {
		t.Fatalf("active = %d, want 2", got)
	}
	if !r.Touch("u", "a", 42, true) {
		t.Fatal("touch of a live playback reported stale")
	}
	if r.Touch("u", "zzz", 1, false) {
		t.Fatal("touch of an unknown playback reported live")
	}
	if p := r.List(); p[len(p)-1].PositionSecs != 42 || !p[len(p)-1].Paused {
		t.Fatalf("touch did not update position/paused: %+v", p)
	}
	// Restarting the same item replaces, not duplicates.
	r.Start(Playback{UserID: "u", ItemID: "a", Title: "A", Mode: "direct"})
	if got := len(r.List()); got != 2 {
		t.Fatalf("after restart active = %d, want 2", got)
	}
	// Idle reaping drops silent clients only.
	r.Touch("u", "b", 5, false)
	r.mu.Lock()
	r.byID[key("u", "a")].LastSeenAt = time.Now().Add(-2 * time.Second)
	r.mu.Unlock()
	r.reap(time.Now())
	if l := r.List(); len(l) != 1 || l[0].ItemID != "b" {
		t.Fatalf("after reap = %+v, want only b", l)
	}
	if sid, ok := r.Stop("u", "b"); !ok || sid != "" {
		t.Fatalf("stop b = (%q,%v)", sid, ok)
	}
	if changes == 0 {
		t.Fatal("OnChange never fired")
	}
}

func TestParseUserAgent(t *testing.T) {
	tests := []struct{ ua, browser, os string }{
		{"Mozilla/5.0 (X11; Linux x86_64; rv:153.0) Gecko/20100101 Firefox/153.0", "Firefox", "Linux"},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36", "Chrome", "Linux"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0 Safari/537.36 Edg/150.0", "Edge", "Windows"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Mobile/15E148 Safari/604.1", "Safari", "iOS"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_2) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15", "Safari", "macOS"},
		{"curl/8.21.0", "curl", ""},
		{"", "Unknown", ""},
	}
	for _, tc := range tests {
		b, o := ParseUserAgent(tc.ua)
		if b != tc.browser || o != tc.os {
			t.Errorf("%q → (%q,%q), want (%q,%q)", tc.ua, b, o, tc.browser, tc.os)
		}
	}
}

// A playback's history row has to be closed however the playback ends.
// Browsers close tabs without telling the server, so the reap path matters
// as much as the explicit stop.
func TestOnEndFiresForEveryExitPath(t *testing.T) {
	cases := []struct {
		name string
		end  func(r *Registry)
	}{
		{"explicit stop", func(r *Registry) { r.Stop("u1", "i1") }},
		{"transcode session killed", func(r *Registry) { r.StopSession("sess-1") }},
		{"idle reap", func(r *Registry) { r.reap(time.Now().UTC().Add(time.Hour)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ended []Playback
			r := NewRegistry(time.Second)
			r.OnEnd = func(p Playback) { ended = append(ended, p) }
			r.Start(Playback{
				UserID: "u1", ItemID: "i1", SessionID: "sess-1", EventID: 42,
				DurationSecs: 100,
			})
			tc.end(r)
			if len(ended) != 1 {
				t.Fatalf("OnEnd fired %d times, want 1", len(ended))
			}
			if ended[0].EventID != 42 {
				t.Errorf("EventID = %d, want 42 — the history row cannot be closed without it", ended[0].EventID)
			}
			if len(r.List()) != 0 {
				t.Errorf("playback still listed after ending")
			}
		})
	}
}

// Ending twice must not fire twice: a reap racing an explicit stop would
// otherwise close the same history row with two different durations.
func TestOnEndFiresOncePerPlayback(t *testing.T) {
	var n int
	r := NewRegistry(time.Second)
	r.OnEnd = func(Playback) { n++ }
	r.Start(Playback{UserID: "u1", ItemID: "i1", SessionID: "s1", EventID: 7})
	r.Stop("u1", "i1")
	r.Stop("u1", "i1")
	r.StopSession("s1")
	r.reap(time.Now().UTC().Add(time.Hour))
	if n != 1 {
		t.Errorf("OnEnd fired %d times, want 1", n)
	}
}

// A registry with no OnEnd must still work — `scan` and the tests use one.
func TestNilOnEndIsSafe(t *testing.T) {
	r := NewRegistry(time.Second)
	r.Start(Playback{UserID: "u", ItemID: "i"})
	r.Stop("u", "i") // must not panic
}

// An administrator's stop has to survive the client's own heartbeat. The
// progress endpoint re-registers a playback it cannot find — so that a reap
// or a restart does not lose a stream that is still running — which would
// otherwise undo the stop a few seconds later.
func TestAdminStopSurvivesHeartbeat(t *testing.T) {
	r := NewRegistry(time.Second)
	r.Start(Playback{UserID: "u1", ItemID: "i1", EventID: 1})

	if _, ok := r.StopByAdmin("u1", "i1"); !ok {
		t.Fatal("StopByAdmin did not find the playback")
	}
	if len(r.List()) != 0 {
		t.Fatal("playback still listed after an admin stop")
	}
	// The heartbeat that follows must be told, exactly once.
	if !r.Stopped("u1", "i1") {
		t.Fatal("Stopped() false right after an admin stop — the client would never be told")
	}
	if r.Stopped("u1", "i1") {
		t.Error("Stopped() true twice — the viewer could never restart the item")
	}
}

// A stop for a playback that is not currently listed is still remembered:
// the client can be mid-heartbeat when the stop arrives.
func TestAdminStopOfUnlistedPlaybackIsRemembered(t *testing.T) {
	r := NewRegistry(time.Second)
	if _, ok := r.StopByAdmin("u1", "i1"); ok {
		t.Error("StopByAdmin reported finding a playback that was never started")
	}
	if !r.Stopped("u1", "i1") {
		t.Error("stop was not remembered, so a racing heartbeat would resurrect the playback")
	}
}

// Markers must not accumulate for clients that never come back.
func TestStopMarkersArePruned(t *testing.T) {
	r := NewRegistry(time.Second)
	r.StopByAdmin("u1", "i1")
	r.reap(time.Now().UTC().Add(stopGrace + time.Minute))
	if r.Stopped("u1", "i1") {
		t.Error("a stop marker outlived its grace window")
	}
}

// Stopping one playback must not stop another.
func TestAdminStopIsScopedToOnePlayback(t *testing.T) {
	r := NewRegistry(time.Second)
	r.Start(Playback{UserID: "u1", ItemID: "i1"})
	r.Start(Playback{UserID: "u2", ItemID: "i1"}) // same item, different viewer
	r.StopByAdmin("u1", "i1")
	if r.Stopped("u2", "i1") {
		t.Error("stopping one viewer's playback also stopped another's")
	}
	if len(r.List()) != 1 {
		t.Errorf("%d playbacks left, want 1", len(r.List()))
	}
}

// A heartbeat that flips paused reports it once; one that does not, never.
func TestOnPause(t *testing.T) {
	r := NewRegistry(time.Minute)
	var got []bool
	r.OnPause = func(p Playback) { got = append(got, p.Paused) }
	r.Start(Playback{UserID: "u", ItemID: "a"})
	r.Touch("u", "a", 10, false)
	r.Touch("u", "a", 11, true)
	r.Touch("u", "a", 11, true)
	r.Touch("u", "a", 12, false)
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("pauses reported = %v, want [true false]", got)
	}
}
