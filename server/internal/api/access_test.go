package api

import (
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

// nil means every library, so only an admin may get it: a user who can see
// nothing must get an empty scope, not an unscoped query.
func TestVisibleLibraryIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		user store.User
		want []string
	}{
		{"admin sees everything", store.User{Role: store.RoleAdmin, LibraryIDs: []string{"a"}}, nil},
		{"user sees their libraries", store.User{Role: store.RoleUser, LibraryIDs: []string{"a", "b"}}, []string{"a", "b"}},
		{"user with none sees none", store.User{Role: store.RoleUser}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := visibleLibraryIDs(tc.user)
			if (got == nil) != (tc.want == nil) || len(got) != len(tc.want) {
				t.Fatalf("visibleLibraryIDs = %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("visibleLibraryIDs = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}
