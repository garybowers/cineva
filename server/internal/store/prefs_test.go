package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func str(s string) *string { return &s }

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

	for _, step := range []struct {
		name string
		set  map[string]*string
		want map[string]string
	}{
		{"set two", map[string]*string{"theme": str("ember"), "quality": str("1080p")},
			map[string]string{"theme": "ember", "quality": "1080p"}},
		{"a merge touches only the keys sent", map[string]*string{"quality": str("720p")},
			map[string]string{"theme": "ember", "quality": "720p"}},
		{"an empty string is a value", map[string]*string{"audio-lang": str("")},
			map[string]string{"theme": "ember", "quality": "720p", "audio-lang": ""}},
		{"nil clears a key", map[string]*string{"theme": nil, "audio-lang": nil},
			map[string]string{"quality": "720p"}},
	} {
		if err := st.SetPrefs(ctx, a.ID, step.set); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		got, err := st.Prefs(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, step.want) {
			t.Errorf("%s: prefs = %v, want %v", step.name, got, step.want)
		}
	}

	// One account's preferences are its own.
	if got, _ := st.Prefs(ctx, b.ID); len(got) != 0 {
		t.Errorf("second user sees %v — preferences leaked", got)
	}
}

// A device's values belong to the screen: they survive signing out, and go
// only when the device is forgotten.
func TestDevicePrefsFollowTheDevice(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "ann", "Ann", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	tv, err := st.CreateSession(ctx, "tv-hash", u.ID, "2099-01-01 00:00:00", "Chrome", "::1", "TV")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := st.CreateSession(ctx, "phone-hash", u.ID, "2099-01-01 00:00:00", "Safari", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDevicePrefs(ctx, tv.ID, map[string]*string{"quality": str("original")}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDevicePrefs(ctx, phone.ID, map[string]*string{"quality": str("480p")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.DevicePrefs(ctx, tv.ID); got["quality"] != "original" {
		t.Errorf("tv prefs = %v", got)
	}
	if err := st.DeleteUserSession(ctx, u.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.DevicePrefs(ctx, tv.ID); got["quality"] != "original" {
		t.Errorf("signing out lost the device's prefs: %v", got)
	}
	if err := st.DeleteDevice(ctx, tv.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.DevicePrefs(ctx, tv.ID); len(got) != 0 {
		t.Errorf("forgotten device kept %v", got)
	}
	if got, _ := st.DevicePrefs(ctx, phone.ID); got["quality"] != "480p" {
		t.Errorf("another device lost its prefs: %v", got)
	}
}

func TestPrefDefaults(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	kids, err := st.CreateGroup(ctx, "Kids", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SetPrefDefaults(ctx, PrefOwnerServer, "ignored", map[string]*PrefSetting{
		"audio-lang": {Value: "en"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrefDefaults(ctx, PrefOwnerGroup, kids.ID, map[string]*PrefSetting{
		"sub-mode": {Value: "always", Locked: true},
		"autoplay": {Value: "off"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrefDefaults(ctx, PrefOwnerGroup, kids.ID, map[string]*PrefSetting{"autoplay": nil}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrefDefaults(ctx, PrefOwnerGroup, "no-such-group", map[string]*PrefSetting{"autoplay": {Value: "on"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown group: err = %v, want ErrNotFound", err)
	}

	got, err := st.PrefDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []PrefDefault{
		{OwnerKind: PrefOwnerGroup, OwnerID: kids.ID, Key: "sub-mode", Value: "always", Locked: true},
		{OwnerKind: PrefOwnerServer, OwnerID: "", Key: "audio-lang", Value: "en"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}

	// A group's defaults go with it.
	if err := st.DeleteGroup(ctx, kids.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = st.PrefDefaults(ctx)
	if len(got) != 1 || got[0].OwnerKind != PrefOwnerServer {
		t.Errorf("after deleting the group: %+v", got)
	}
}

func TestGroupPriority(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "kid", "Kid", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	grown, err := st.CreateGroup(ctx, "Grown-ups", []string{u.ID})
	if err != nil {
		t.Fatal(err)
	}
	kids, err := st.CreateGroup(ctx, "Kids", []string{u.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateGroup(ctx, "Book club", nil)
	if err != nil {
		t.Fatal(err)
	}
	names := func(gs []UserGroup) []string {
		out := []string{}
		for _, g := range gs {
			out = append(out, g.Name)
		}
		return out
	}

	// A new group goes last, whatever its name.
	all, err := st.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(all); !reflect.DeepEqual(got, []string{"Grown-ups", "Kids", "Book club"}) {
		t.Errorf("groups = %v, want creation order", got)
	}

	for _, tc := range []struct {
		name  string
		order []string
		err   bool
		want  []string // the user's groups afterwards
	}{
		{"kids first", []string{kids.ID, other.ID, grown.ID}, false, []string{"Kids", "Grown-ups"}},
		{"a group missing", []string{kids.ID, grown.ID}, true, []string{"Kids", "Grown-ups"}},
		{"a group twice", []string{kids.ID, kids.ID, grown.ID}, true, []string{"Kids", "Grown-ups"}},
		{"an unknown group", []string{kids.ID, grown.ID, "nope"}, true, []string{"Kids", "Grown-ups"}},
		{"grown-ups first", []string{grown.ID, kids.ID, other.ID}, false, []string{"Grown-ups", "Kids"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := st.SetGroupOrder(ctx, tc.order)
			if tc.err != (err != nil) {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if tc.err && !errors.Is(err, ErrInvalidOrder) {
				t.Errorf("err = %v, want ErrInvalidOrder", err)
			}
			mine, err := st.GroupsOf(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := names(mine); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("user's groups = %v, want %v", got, tc.want)
			}
		})
	}
}
