package auth

import "testing"

func TestSealRoundTrip(t *testing.T) {
	key, err := NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(key, "cnv_secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(key, sealed)
	if err != nil || got != "cnv_secret" {
		t.Fatalf("open: %q %v", got, err)
	}
	// Each seal is different (a fresh nonce), and the wrong key fails.
	again, _ := Seal(key, "cnv_secret")
	if string(again) == string(sealed) {
		t.Error("two seals of one value were identical")
	}
	other, _ := NewSecretKey()
	if _, err := Open(other, sealed); err == nil {
		t.Error("opened with the wrong key")
	}
	if _, err := Open(key, sealed[:5]); err == nil {
		t.Error("opened a truncated value")
	}
	if _, err := Seal([]byte("short"), "x"); err == nil {
		t.Error("sealed with a short key")
	}
}
