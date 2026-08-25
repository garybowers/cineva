package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("not PHC format: %s", h)
	}
	tests := []struct {
		name string
		pw   string
		hash string
		want bool
	}{
		{"matches", "correct horse battery", h, true},
		{"wrong password", "correct horse batter", h, false},
		{"empty password", "", h, false},
		{"garbage hash", "correct horse battery", "nope", false},
		{"wrong algorithm", "correct horse battery", "$bcrypt$v=1$x$y$z", false},
	}
	for _, tc := range tests {
		if got := VerifyPassword(tc.pw, tc.hash); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	h2, _ := HashPassword("correct horse battery")
	if h2 == h {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestTokens(t *testing.T) {
	tok, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 40 || HashToken(tok) != hash {
		t.Fatalf("token %q / hash %q inconsistent", tok, hash)
	}
	tok2, _, _ := NewToken()
	if tok2 == tok {
		t.Fatal("tokens must be unique")
	}
}
