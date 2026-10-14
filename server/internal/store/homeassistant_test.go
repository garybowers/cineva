package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestHomeAssistantStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	kid, err := s.CreateUser(ctx, "kid", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	kids, err := s.CreateGroup(ctx, "Kids", []string{kid.ID})
	if err != nil {
		t.Fatal(err)
	}

	// The connection: none, then one, replaced in place.
	if _, err := s.HAConnection(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no connection: err = %v", err)
	}
	if _, err := s.SaveHAConnection(ctx, HAConnection{Name: "Home", URL: "http://ha:8123", Token: "t1"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.SaveHAConnection(ctx, HAConnection{Name: "House", URL: "http://ha:8123", Token: "t2", PlaybackEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "House" || c.Token != "t2" || !c.PlaybackEvents {
		t.Fatalf("connection = %+v", c)
	}

	// Exposure and access, and a user's deletion taking their grants.
	if err := s.ExposeHAEntity(ctx, "light.lounge", LibraryAccess{}); err != nil {
		t.Fatal(err)
	}
	if err := s.ExposeHAEntity(ctx, "camera.door", LibraryAccess{Restricted: true, UserIDs: []string{kid.ID}, GroupIDs: []string{kids.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ExposeHAEntity(ctx, "camera.yard", LibraryAccess{Restricted: true, UserIDs: []string{"nobody"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: err = %v", err)
	}
	exp, err := s.HAExposures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]LibraryAccess{
		"light.lounge": {UserIDs: []string{}, GroupIDs: []string{}},
		"camera.door":  {Restricted: true, UserIDs: []string{kid.ID}, GroupIDs: []string{kids.ID}},
	}
	if !reflect.DeepEqual(exp, want) {
		t.Fatalf("exposure = %+v", exp)
	}
	if groups, err := s.UserGroupIDs(ctx, kid.ID); err != nil || !reflect.DeepEqual(groups, []string{kids.ID}) {
		t.Fatalf("groups = %v, %v", groups, err)
	}
	if err := s.HideHAEntity(ctx, "camera.door"); err != nil {
		t.Fatal(err)
	}
	if exp, _ := s.HAExposures(ctx); len(exp) != 1 {
		t.Fatalf("after hiding: %+v", exp)
	}

	// Modes: steps round-trip, names are unique ignoring case.
	pct := 20
	m, err := s.CreateHAMode(ctx, HAMode{Name: "Movie mode", Steps: []HAModeStep{
		{Type: "action", EntityID: "light.lounge", Service: "turn_on", BrightnessPct: &pct},
		{Type: "play", DeviceID: "tv"},
	}, Access: LibraryAccess{Restricted: true, GroupIDs: []string{kids.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if *m.Steps[0].BrightnessPct != 20 || m.Steps[1].DeviceID != "tv" || !m.Access.Restricted || m.Access.GroupIDs[0] != kids.ID {
		t.Fatalf("mode = %+v", m)
	}
	if _, err := s.CreateHAMode(ctx, HAMode{Name: "movie MODE"}); !errors.Is(err, ErrHAModeNameUsed) {
		t.Fatalf("duplicate name: err = %v", err)
	}
	m.Name, m.Access = "Film night", LibraryAccess{}
	if m, err = s.UpdateHAMode(ctx, m); err != nil || m.Name != "Film night" || m.Access.Restricted {
		t.Fatalf("update = %+v, %v", m, err)
	}
	if modes, err := s.HAModes(ctx); err != nil || len(modes) != 1 {
		t.Fatalf("modes = %+v, %v", modes, err)
	}
	if err := s.DeleteHAMode(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHAMode(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}

	// A device's room, set by its public id and read back on the session.
	sess, err := s.CreateSession(ctx, "hash", kid.ID, "2999-01-01 00:00:00", "Firefox · Linux", "10.0.0.2", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceArea(ctx, sess.ID, "living_room"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionByID(ctx, sess.ID); err != nil || got.AreaID != "living_room" {
		t.Fatalf("session = %+v, %v", got, err)
	}
	if err := s.SetDeviceArea(ctx, "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: err = %v", err)
	}

	// Disconnecting keeps exposure.
	if err := s.DeleteHAConnection(ctx); err != nil {
		t.Fatal(err)
	}
	if exp, _ := s.HAExposures(ctx); len(exp) != 1 {
		t.Fatal("disconnecting dropped the exposure")
	}
}

// Who is watching a series: anyone who watched or part-watched one of its
// episodes recently, and nobody else.
func TestSeriesWatchers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	lib, err := s.CreateLibrary(ctx, "TV", MediaTV, []string{"/tv"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(it Item) Item {
		t.Helper()
		saved, err := s.UpsertItem(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	show := save(Item{LibraryID: lib.ID, Kind: KindSeries, Title: "Slow Horses", Path: "/tv/Slow Horses"})
	season := save(Item{LibraryID: lib.ID, Kind: KindSeason, ParentID: show.ID, Title: "Season 1", SeasonNum: 1, Path: "/tv/Slow Horses/Season 1"})
	ep := save(Item{LibraryID: lib.ID, Kind: KindEpisode, ParentID: season.ID, Title: "Failure's Contagious", SeasonNum: 1, EpisodeNum: 1,
		Path: "/tv/Slow Horses/Season 1/e1.mkv", RuntimeSecs: 3000})
	watcher, _ := s.CreateUser(ctx, "dad", "", "h", RoleUser)
	if _, err := s.CreateUser(ctx, "kid", "", "h", RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProgress(ctx, watcher.ID, ep.ID, 600, ep.RuntimeSecs); err != nil {
		t.Fatal(err)
	}
	got, err := s.SeriesWatchers(ctx, show.ID, 180)
	if err != nil || len(got) != 1 || got[0] != watcher.ID {
		t.Fatalf("watchers = %v, %v; want only dad", got, err)
	}
	if got, _ := s.SeriesWatchers(ctx, season.ID, 180); len(got) != 0 {
		t.Fatalf("a season id matched watchers: %v", got)
	}
}
