package store

import (
	"context"
	"testing"
)

func TestUserPrefsRoundTripAndIsolation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	a, err := st.CreateUser(ctx, "ann", "Ann", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateUser(ctx, "bob", "Bob", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}

	if got, err := st.Prefs(ctx, a.ID); err != nil || len(got) != 0 {
		t.Fatalf("fresh user has prefs %v (err %v), want none", got, err)
	}

	if err := st.SetPrefs(ctx, a.ID, map[string]string{"theme": "ember", "quality": "1080p"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Prefs(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "ember" || got["quality"] != "1080p" {
		t.Errorf("prefs = %v", got)
	}

	// A merge touches only the keys sent.
	if err := st.SetPrefs(ctx, a.ID, map[string]string{"quality": "720p"}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Prefs(ctx, a.ID)
	if got["theme"] != "ember" {
		t.Errorf("merge dropped an untouched key: %v", got)
	}
	if got["quality"] != "720p" {
		t.Errorf("merge did not update: %v", got)
	}

	// An empty value clears a key, so a client needs no second endpoint.
	if err := st.SetPrefs(ctx, a.ID, map[string]string{"theme": ""}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Prefs(ctx, a.ID)
	if _, still := got["theme"]; still {
		t.Errorf("empty value did not clear the key: %v", got)
	}

	// One account's preferences are its own.
	if got, _ := st.Prefs(ctx, b.ID); len(got) != 0 {
		t.Errorf("second user sees %v — preferences leaked", got)
	}
}
