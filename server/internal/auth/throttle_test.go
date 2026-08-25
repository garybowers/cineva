package auth

import (
	"testing"
	"time"
)

func TestThrottleBackoff(t *testing.T) {
	th := NewThrottle()
	clock := time.Unix(0, 0)
	th.now = func() time.Time { return clock }

	for i := 0; i < 4; i++ {
		if d := th.Fail("u"); d != 0 {
			t.Fatalf("locked after %d failures (delay %v), threshold is 5", i+1, d)
		}
	}
	if ok, _ := th.Allow("u"); !ok {
		t.Fatal("locked before threshold")
	}
	if d := th.Fail("u"); d != 30*time.Second {
		t.Fatalf("5th failure delay = %v, want 30s", d)
	}
	if ok, wait := th.Allow("u"); ok || wait != 30*time.Second {
		t.Fatalf("Allow after lock = %v, %v", ok, wait)
	}
	clock = clock.Add(31 * time.Second)
	if ok, _ := th.Allow("u"); !ok {
		t.Fatal("still locked after the delay passed")
	}
	if d := th.Fail("u"); d != 60*time.Second {
		t.Fatalf("6th failure delay = %v, want 60s (doubling)", d)
	}
	for i := 0; i < 10; i++ {
		th.Fail("u")
	}
	if d := th.Fail("u"); d != 15*time.Minute {
		t.Fatalf("delay not capped: %v", d)
	}
	if ok, _ := th.Allow("other"); !ok {
		t.Fatal("unrelated key locked")
	}
	th.Reset("u")
	if ok, _ := th.Allow("u"); !ok {
		t.Fatal("Reset did not clear the lock")
	}
}
