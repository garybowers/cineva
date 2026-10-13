package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// Libraries are open to everyone until restricted; a restricted one is seen
// by the users and groups it names, and always by admins.
func TestLibraryAccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	admin, err := s.CreateUser(ctx, "gary", "", "h", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := s.CreateUser(ctx, "kid", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	gran, err := s.CreateUser(ctx, "gran", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	films, _ := s.CreateLibrary(ctx, "Films", MediaMovies, []string{"/f"})
	if _, err := s.CreateLibrary(ctx, "Kids TV", MediaTV, []string{"/k"}); err != nil {
		t.Fatal(err)
	}
	private, _ := s.CreateLibrary(ctx, "Private", MediaMovies, []string{"/p"})

	grownups, err := s.CreateGroup(ctx, "Grown-ups", []string{gran.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGroup(ctx, "grown-ups", nil); !errors.Is(err, ErrGroupNameUsed) {
		t.Fatalf("duplicate group name (case aside) accepted: %v", err)
	}
	// Films: grown-ups only. Private: nobody but admins. Kids TV: open.
	if err := s.SetLibraryAccess(ctx, films.ID, LibraryAccess{Restricted: true, GroupIDs: []string{grownups.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLibraryAccess(ctx, private.ID, LibraryAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}

	sees := func(u User) []string {
		t.Helper()
		got, err := s.UserByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, id := range got.LibraryIDs {
			lib, _ := s.Library(ctx, id)
			names = append(names, lib.Name)
		}
		slices.Sort(names)
		return names
	}
	tests := []struct {
		name string
		user User
		want []string
	}{
		{"admin sees everything", admin, []string{"Films", "Kids TV", "Private"}},
		{"group member sees the group's library", gran, []string{"Films", "Kids TV"}},
		{"others see only open libraries", kid, []string{"Kids TV"}},
	}
	for _, tc := range tests {
		if got := sees(tc.user); !slices.Equal(got, tc.want) {
			t.Errorf("%s: sees %v, want %v", tc.name, got, tc.want)
		}
	}

	// Naming a user directly works too, and an unknown id is refused.
	if err := s.SetLibraryAccess(ctx, private.ID, LibraryAccess{Restricted: true, UserIDs: []string{kid.ID}}); err != nil {
		t.Fatal(err)
	}
	if got := sees(kid); !slices.Equal(got, []string{"Kids TV", "Private"}) {
		t.Errorf("named user sees %v, want [Kids TV Private]", got)
	}
	if err := s.SetLibraryAccess(ctx, private.ID, LibraryAccess{Restricted: true, UserIDs: []string{"nobody"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user accepted: %v", err)
	}

	// Leaving a group, or the group going, takes its libraries away.
	if _, err := s.UpdateGroup(ctx, grownups.ID, "Grown-ups", nil); err != nil {
		t.Fatal(err)
	}
	if got := sees(gran); !slices.Equal(got, []string{"Kids TV"}) {
		t.Errorf("after leaving the group, gran sees %v", got)
	}
	if err := s.DeleteGroup(ctx, grownups.ID); err != nil {
		t.Fatal(err)
	}
	acc, err := s.LibraryAccess(ctx, films.ID)
	if err != nil || !acc.Restricted || len(acc.GroupIDs) != 0 {
		t.Errorf("films access after group deleted = %+v, %v; want restricted, no groups", acc, err)
	}

	// Reopening keeps the lists, so restricting again restores them.
	if err := s.SetLibraryAccess(ctx, private.ID, LibraryAccess{Restricted: false, UserIDs: []string{kid.ID}}); err != nil {
		t.Fatal(err)
	}
	if got := sees(gran); !slices.Contains(got, "Private") {
		t.Errorf("reopened library not visible to everyone: %v", got)
	}
	if acc, _ := s.LibraryAccess(ctx, private.ID); len(acc.UserIDs) != 1 {
		t.Errorf("reopening dropped the list: %+v", acc)
	}

	// Deleting a user removes them from access lists.
	if err := s.DeleteUser(ctx, kid.ID); err != nil {
		t.Fatal(err)
	}
	if acc, _ := s.LibraryAccess(ctx, private.ID); len(acc.UserIDs) != 0 {
		t.Errorf("deleted user still listed: %+v", acc)
	}
}
