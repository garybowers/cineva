package prefs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

func vals(kv ...string) map[string]Setting {
	m := map[string]Setting{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = Setting{Value: kv[i+1]}
	}
	return m
}

func locked(k, v string) map[string]Setting { return map[string]Setting{k: {Value: v, Locked: true}} }

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        string
		layers     []Layer
		want       string
		source     Source
		group      string
		isLocked   bool
		defaultVal string
	}{
		{"built-in when nothing is set", "sub-mode", nil, "off", SourceBuiltin, "", false, "off"},
		{"the user's own value", "sub-mode", []Layer{
			{Source: SourceUser, Values: vals("sub-mode", "always")},
		}, "always", SourceUser, "", false, "off"},
		{"the server's default under the user", "audio-lang", []Layer{
			{Source: SourceUser},
			{Source: SourceServer, Values: vals("audio-lang", "en")},
		}, "en", SourceServer, "", false, "en"},
		{"the user over the server's default", "audio-lang", []Layer{
			{Source: SourceUser, Values: vals("audio-lang", "")},
			{Source: SourceServer, Values: vals("audio-lang", "en")},
		}, "", SourceUser, "", false, "en"},
		{"the first group in the admin's order", "autoplay", []Layer{
			{Source: SourceUser},
			{Source: SourceGroup, GroupName: "Kids", Values: vals("autoplay", "off")},
			{Source: SourceGroup, GroupName: "Grown-ups", Values: vals("autoplay", "on")},
			{Source: SourceServer, Values: vals("autoplay", "on")},
		}, "off", SourceGroup, "Kids", false, "off"},
		{"a group over the server", "sub-mode", []Layer{
			{Source: SourceGroup, GroupName: "Kids", Values: vals("sub-mode", "forced")},
			{Source: SourceServer, Values: vals("sub-mode", "always")},
		}, "forced", SourceGroup, "Kids", false, "forced"},
		{"a server lock beats the user", "sub-mode", []Layer{
			{Source: SourceUser, Values: vals("sub-mode", "off")},
			{Source: SourceServer, Values: locked("sub-mode", "always")},
		}, "always", SourceServer, "", true, "always"},
		{"a group lock beats the device and the user", "quality", []Layer{
			{Source: SourceDevice, Values: vals("quality", "original")},
			{Source: SourceUser, Values: vals("quality", "1080p")},
			{Source: SourceGroup, GroupName: "Kids", Values: locked("quality", "720p")},
		}, "720p", SourceGroup, "Kids", true, "720p"},
		{"a lock beats an unlocked group above it", "sub-mode", []Layer{
			{Source: SourceGroup, GroupName: "Kids", Values: vals("sub-mode", "forced")},
			{Source: SourceServer, Values: locked("sub-mode", "always")},
		}, "always", SourceServer, "", true, "always"},
		{"a group lock beats a server lock", "sub-mode", []Layer{
			{Source: SourceGroup, GroupName: "Kids", Values: locked("sub-mode", "forced")},
			{Source: SourceServer, Values: locked("sub-mode", "always")},
		}, "forced", SourceGroup, "Kids", true, "forced"},
		{"a lock on a key that cannot be locked is a default", "theme", []Layer{
			{Source: SourceUser, Values: vals("theme", "ember")},
			{Source: SourceServer, Values: locked("theme", "daylight")},
		}, "ember", SourceUser, "", false, "daylight"},
		{"the device over the user for a device key", "quality", []Layer{
			{Source: SourceDevice, Values: vals("quality", "480p")},
			{Source: SourceUser, Values: vals("quality", "1080p")},
		}, "480p", SourceDevice, "", false, "auto"},
		{"the user still applies to a device key with no device value", "quality", []Layer{
			{Source: SourceDevice},
			{Source: SourceUser, Values: vals("quality", "1080p")},
		}, "1080p", SourceUser, "", false, "auto"},
		{"a device value of a person key is ignored", "sub-mode", []Layer{
			{Source: SourceDevice, Values: vals("sub-mode", "always")},
			{Source: SourceUser},
		}, "off", SourceBuiltin, "", false, "off"},
		{"an invalid value falls through", "sub-mode", []Layer{
			{Source: SourceUser, Values: vals("sub-mode", "sometimes")},
			{Source: SourceServer, Values: vals("sub-mode", "forced")},
		}, "forced", SourceServer, "", false, "forced"},
		{"an invalid locked value is no lock", "autoplay", []Layer{
			{Source: SourceUser, Values: vals("autoplay", "off")},
			{Source: SourceServer, Values: locked("autoplay", "yes")},
		}, "off", SourceUser, "", false, "on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.layers)[tc.key]
			if got.Value != tc.want || got.Source != tc.source || got.GroupName != tc.group || got.Locked != tc.isLocked {
				t.Errorf("%s = %+v, want %q from %s %s (locked %v)", tc.key, got, tc.want, tc.source, tc.group, tc.isLocked)
			}
			if got.Default == nil || got.Default.Value != tc.defaultVal {
				t.Errorf("default = %+v, want %q", got.Default, tc.defaultVal)
			}
		})
	}
}

// Keys the registry does not know pass through from the user's own layer,
// so a client can keep a preference of its own.
func TestResolveUnknownKeys(t *testing.T) {
	got := Resolve([]Layer{
		{Source: SourceDevice, Values: vals("device-only", "x")},
		{Source: SourceUser, Values: vals("sidebar-width", "320")},
		{Source: SourceServer, Values: vals("server-only", "y")},
	})
	if r, ok := got["sidebar-width"]; !ok || r.Value != "320" || r.Source != SourceUser || r.Default != nil {
		t.Errorf("sidebar-width = %+v (present %v)", r, ok)
	}
	for _, k := range []string{"device-only", "server-only"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s resolved; only the user's own unknown keys should", k)
		}
	}
}

func TestValidate(t *testing.T) {
	lo, hi := 1, 10
	intPref := &Pref{Key: "n", Type: TypeInt, Min: &lo, Max: &hi}
	listPref := &Pref{Key: "l", Type: TypeList}
	for _, tc := range []struct {
		name string
		p    *Pref
		v    string
		ok   bool
	}{
		{"bool on", Lookup("autoplay"), "on", true},
		{"bool true is not a value", Lookup("autoplay"), "true", false},
		{"enum choice", Lookup("skip-credits"), "next", true},
		{"enum unknown", Lookup("skip-credits"), "later", false},
		{"enum empty choice", Lookup(AudioLang), "", true},
		{"string", Lookup("theme"), "ember", true},
		{"string empty with a default", Lookup("theme"), "", false},
		{"int in range", intPref, "5", true},
		{"int out of range", intPref, "11", false},
		{"int not a number", intPref, "five", false},
		{"list", listPref, `["Horror","Thriller"]`, true},
		{"list not JSON", listPref, "Horror,Thriller", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.Validate(tc.v); (err == nil) != tc.ok {
				t.Errorf("Validate(%q) = %v, want ok %v", tc.v, err, tc.ok)
			}
		})
	}
}

// Every built-in default must be a valid value, or a viewer with nothing set
// would resolve to something playback cannot use.
func TestRegistryDefaultsAreValid(t *testing.T) {
	seen := map[string]bool{}
	sections := map[string]bool{}
	for _, s := range Sections {
		sections[s.ID] = true
	}
	for _, p := range Registry() {
		if seen[p.Key] {
			t.Errorf("%s registered twice", p.Key)
		}
		seen[p.Key] = true
		if err := p.Validate(p.Default); err != nil {
			t.Errorf("%s: default %q: %v", p.Key, p.Default, err)
		}
		if !sections[p.Section] {
			t.Errorf("%s: unknown section %q", p.Key, p.Section)
		}
		if p.Label == "" {
			t.Errorf("%s: no label", p.Key)
		}
		if p.HideWhen != nil && Lookup(p.HideWhen.Key) == nil {
			t.Errorf("%s: hidden by unknown key %s", p.Key, p.HideWhen.Key)
		}
	}
}

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewService(st), st
}

func str(s string) *string { return &s }

// The whole stack, through the store: a child in Kids and Grown-ups, with
// Kids ordered first, on their own device.
func TestServiceResolveAndSet(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	kid, err := st.CreateUser(ctx, "kid", "Kid", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	grown, err := st.CreateGroup(ctx, "Grown-ups", []string{kid.ID})
	if err != nil {
		t.Fatal(err)
	}
	kids, err := st.CreateGroup(ctx, "Kids", []string{kid.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupOrder(ctx, []string{kids.ID, grown.ID}); err != nil {
		t.Fatal(err)
	}
	tablet, err := st.CreateSession(ctx, "tablet", kid.ID, "2099-01-01 00:00:00", "Safari", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(svc.SetDefaults(ctx, store.PrefOwnerServer, "", map[string]*store.PrefSetting{
		AudioLang: {Value: "en"}, "autoplay": {Value: "on"},
	}))
	must(svc.SetDefaults(ctx, store.PrefOwnerGroup, grown.ID, map[string]*store.PrefSetting{"autoplay": {Value: "on"}}))
	must(svc.SetDefaults(ctx, store.PrefOwnerGroup, kids.ID, map[string]*store.PrefSetting{
		"autoplay": {Value: "off"}, SubMode: {Value: "always", Locked: true},
	}))

	// Setting a locked key is refused and names the lock; clearing it is not.
	err = svc.SetUser(ctx, kid.ID, map[string]*string{SubMode: str("off")})
	var lockErr *LockedError
	if !errors.As(err, &lockErr) || lockErr.Key != SubMode || lockErr.GroupName != "Kids" {
		t.Fatalf("setting a locked key: err = %v", err)
	}
	must(svc.SetUser(ctx, kid.ID, map[string]*string{SubMode: nil}))

	// Invalid values and device keys in the wrong place are refused.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"bad choice", svc.SetUser(ctx, kid.ID, map[string]*string{"skip-intro": str("sometimes")})},
		{"person key on a device", svc.SetDevice(ctx, kid.ID, tablet.ID, map[string]*string{AudioLang: str("fr")})},
		{"unknown key on a device", svc.SetDevice(ctx, kid.ID, tablet.ID, map[string]*string{"whatever": str("x")})},
		{"default for an unknown key", svc.SetDefaults(ctx, store.PrefOwnerServer, "", map[string]*store.PrefSetting{"whatever": {Value: "x"}})},
		{"locking the theme", svc.SetDefaults(ctx, store.PrefOwnerServer, "", map[string]*store.PrefSetting{"theme": {Value: "ember", Locked: true}})},
	} {
		if !errors.Is(tc.err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, tc.err)
		}
	}

	must(svc.SetUser(ctx, kid.ID, map[string]*string{"quality": str("1080p"), "sidebar": str("narrow")}))
	must(svc.SetDevice(ctx, kid.ID, tablet.ID, map[string]*string{"quality": str("480p")}))

	for _, tc := range []struct {
		name, device, key, want string
		source                  Source
	}{
		{"server default", tablet.ID, AudioLang, "en", SourceServer},
		{"first group wins", tablet.ID, "autoplay", "off", SourceGroup},
		{"locked by the group", tablet.ID, SubMode, "always", SourceGroup},
		{"the device's own", tablet.ID, "quality", "480p", SourceDevice},
		{"the user's, with no device", "", "quality", "1080p", SourceUser},
		{"a client's own key", "", "sidebar", "narrow", SourceUser},
		{"built-in", "", "skip-credits", "card", SourceBuiltin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := svc.Resolve(ctx, kid.ID, tc.device)
			if err != nil {
				t.Fatal(err)
			}
			if got := r[tc.key]; got.Value != tc.want || got.Source != tc.source {
				t.Errorf("%s = %+v, want %q from %s", tc.key, got, tc.want, tc.source)
			}
		})
	}

	// The admin's table has a column per group in priority order, even one
	// with nothing set.
	if _, err := st.CreateGroup(ctx, "Book club", nil); err != nil {
		t.Fatal(err)
	}
	groups, err := st.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.AllDefaults(ctx, groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Groups) != 3 || d.Groups[0].Name != "Kids" || d.Groups[2].Name != "Book club" || len(d.Groups[2].Defaults) != 0 {
		t.Errorf("groups = %+v", d.Groups)
	}
	if d.Server[AudioLang].Value != "en" || !d.Groups[0].Defaults[SubMode].Locked {
		t.Errorf("defaults = %+v", d)
	}
}
