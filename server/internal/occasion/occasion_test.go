package occasion

import (
	"testing"
	"time"
)

func TestEaster(t *testing.T) {
	for year, want := range map[int]string{
		2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05",
		2027: "2027-03-28", 2038: "2038-04-25", 2285: "2285-03-22",
	} {
		if got := Easter(year).Format(time.DateOnly); got != want {
			t.Errorf("Easter(%d) = %s, want %s", year, got, want)
		}
	}
}

func TestActive(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("no time zone data")
	}
	tests := []struct {
		at   time.Time
		want string // "" for none
		last string
	}{
		{time.Date(2026, 9, 30, 23, 59, 0, 0, london), "", ""},
		{time.Date(2026, 10, 1, 0, 0, 0, 0, london), "halloween", "2026-11-01"},
		{time.Date(2026, 11, 1, 22, 0, 0, 0, london), "halloween", "2026-11-01"},
		{time.Date(2026, 11, 2, 9, 0, 0, 0, london), "", ""},
		{time.Date(2026, 12, 1, 0, 30, 0, 0, london), "christmas", "2027-01-01"},
		{time.Date(2027, 1, 1, 18, 0, 0, 0, london), "christmas", "2027-01-01"},
		{time.Date(2027, 1, 2, 9, 0, 0, 0, london), "", ""},
		// Easter 2026 is 5 April: 26 March to 6 April.
		{time.Date(2026, 3, 25, 12, 0, 0, 0, london), "", ""},
		{time.Date(2026, 3, 26, 12, 0, 0, 0, london), "easter", "2026-04-06"},
		{time.Date(2026, 4, 6, 12, 0, 0, 0, london), "easter", "2026-04-06"},
		{time.Date(2026, 4, 7, 12, 0, 0, 0, london), "", ""},
	}
	for _, tt := range tests {
		o, last, ok := Active(tt.at)
		got, gotLast := "", ""
		if ok {
			got, gotLast = o.ID, last.Format(time.DateOnly)
		}
		if got != tt.want || gotLast != tt.last {
			t.Errorf("Active(%s) = %q until %q, want %q until %q", tt.at.Format(time.RFC3339), got, gotLast, tt.want, tt.last)
		}
	}
}
