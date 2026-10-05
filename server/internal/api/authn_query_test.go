package api

import "testing"

func TestQueryKeyAllowedOnlyOnMediaRoutes(t *testing.T) {
	// A key in a URL ends up in history, logs and referrers, so it is
	// accepted only where a header genuinely cannot be sent.
	allowed := []string{
		"/api/v1/items/:id/stream",
		"/api/v1/items/:id/download",
		"/api/v1/subtitles/:file",
		"/api/v1/play/:sid/index.m3u8",
		"/api/v1/play/:sid/:file",
	}
	for _, r := range allowed {
		if !queryKeyAllowed(r) {
			t.Errorf("%s should accept ?api_key= — a media player opens it directly", r)
		}
	}
	denied := []string{
		"/api/v1/libraries",
		"/api/v1/users",
		"/api/v1/settings",
		"/api/v1/items/:id",
		"/api/v1/auth/keys",
		"/api/v1/libraries/:id/items",
		"", // an unmatched route must not be a hole
	}
	for _, r := range denied {
		if queryKeyAllowed(r) {
			t.Errorf("%s must not accept a key in the query string", r)
		}
	}
}
