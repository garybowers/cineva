package metadata

import "testing"

func TestAPIKeyPrecedence(t *testing.T) {
	saved := defaultAPIKey
	t.Cleanup(func() { defaultAPIKey = saved })

	defaultAPIKey = ""
	if k, ok := APIKey(""); ok || k != "" {
		t.Errorf("no key anywhere: got (%q, %v), want (\"\", false)", k, ok)
	}
	if k, ok := APIKey("  override  "); !ok || k != "override" {
		t.Errorf("override with no built-in: got (%q, %v)", k, ok)
	}

	defaultAPIKey = "built-in"
	if k, ok := APIKey(""); !ok || k != "built-in" {
		t.Errorf("built-in should be used when nothing overrides: got (%q, %v)", k, ok)
	}
	if k, ok := APIKey("override"); !ok || k != "override" {
		t.Errorf("an explicit key must win over the built-in: got (%q, %v)", k, ok)
	}
	// Whitespace-only is not an override; it is how an unset config key or
	// an empty env var arrives.
	if k, ok := APIKey("   "); !ok || k != "built-in" {
		t.Errorf("blank override should fall back to the built-in: got (%q, %v)", k, ok)
	}
}

func TestIsReadToken(t *testing.T) {
	tests := []struct {
		name string
		cred string
		want bool
	}{
		// v3 API Key: 32 hex characters, sent as ?api_key=
		{"api key", "4219e299c89411838049ab0dab19ebd5", false},
		// v4 Read Access Token: a JWT, sent as a Bearer header.
		{"read token", "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ4In0.sig", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReadToken(tt.cred); got != tt.want {
				t.Errorf("isReadToken(%.12s…) = %v, want %v", tt.cred, got, tt.want)
			}
		})
	}
}
