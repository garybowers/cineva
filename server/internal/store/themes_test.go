package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCustomThemes(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	alice, err := st.CreateUser(ctx, "alice", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "bob", "", "h", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{"bg": "#101010", "text": "#ffffff"}
	mine, err := st.SaveCustomTheme(ctx, CustomTheme{OwnerID: alice.ID, Name: "  Neon ", Appearance: "dark", Tokens: tokens, Dim: 60, Effect: "snow"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mine.ID, CustomThemePrefix) || mine.Name != "Neon" || mine.Tokens["bg"] != "#101010" || mine.Dim != 60 || mine.Effect != "snow" {
		t.Fatalf("saved = %+v", mine)
	}
	shared, err := st.SaveCustomTheme(ctx, CustomTheme{OwnerID: bob.ID, Name: "Cinema", Appearance: "dark", Tokens: tokens, Dim: 70, Household: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveCustomTheme(ctx, CustomTheme{OwnerID: bob.ID, Name: "Bob's own", Appearance: "light", Tokens: tokens, Dim: 70}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		user string
		want []string
	}{
		{alice.ID, []string{"Cinema", "Neon"}},
		{bob.ID, []string{"Bob's own", "Cinema"}},
		{"", []string{"Cinema"}},
	}
	for _, tt := range tests {
		got, err := st.CustomThemes(ctx, tt.user)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, th := range got {
			names = append(names, th.Name)
		}
		if strings.Join(names, ",") != strings.Join(tt.want, ",") {
			t.Errorf("themes for %q = %v, want %v", tt.user, names, tt.want)
		}
	}

	mine.Name, mine.Household = "Neon nights", true
	if mine, err = st.SaveCustomTheme(ctx, mine); err != nil || mine.Name != "Neon nights" || !mine.Household {
		t.Fatalf("update = %+v, %v", mine, err)
	}
	if err := st.SetCustomThemeBackground(ctx, mine.ID, "/artwork/theme-x.jpg"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.CustomTheme(ctx, mine.ID); got.Background != "/artwork/theme-x.jpg" {
		t.Fatalf("background = %q", got.Background)
	}
	if err := st.DeleteCustomTheme(ctx, shared.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CustomTheme(ctx, shared.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	// A user's themes go with them.
	if err := st.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CustomTheme(ctx, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after deleting the owner: %v", err)
	}
}
