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
