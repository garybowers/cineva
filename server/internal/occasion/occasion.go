// Package occasion is the calendar of holidays Cineva marks: when each
// one's window opens and closes in a given year. Holiday themes switch on
// inside a window; seasonal rows (docs/personalisation.md) will use the
// same calendar.
package occasion

import "time"

// Occasion is a holiday with a window around it.
type Occasion struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// window gives the first and last day of the occasion in a year, as
	// dates (the time of day is ignored). A window may run into the next
	// year, as Christmas does.
	window func(year int) (first, last time.Time)
}

// All is every occasion, in calendar order.
var All = []Occasion{
	{ID: "easter", Name: "Easter", window: func(y int) (time.Time, time.Time) {
		// Ten days before Easter Sunday to Easter Monday.
		sun := Easter(y)
		return sun.AddDate(0, 0, -10), sun.AddDate(0, 0, 1)
	}},
	{ID: "halloween", Name: "Halloween", window: func(y int) (time.Time, time.Time) {
		return date(y, time.October, 1), date(y, time.November, 1)
	}},
	{ID: "christmas", Name: "Christmas", window: func(y int) (time.Time, time.Time) {
		return date(y, time.December, 1), date(y+1, time.January, 1)
	}},
}

// Active is the occasion whose window holds the given moment, read in its
// own time zone, and the last day of its window; ok is false outside every
// window.
func Active(now time.Time) (o Occasion, last time.Time, ok bool) {
	day := date(now.Year(), now.Month(), now.Day())
	// The year before too: Christmas's window opened last December.
	for _, y := range []int{now.Year() - 1, now.Year()} {
		for _, oc := range All {
			first, end := oc.window(y)
			if !day.Before(first) && !day.After(end) {
				return oc, end, true
			}
		}
	}
	return Occasion{}, time.Time{}, false
}

// Easter is Western (Gregorian) Easter Sunday in a year: the anonymous
// Gregorian algorithm (Meeus/Jones/Butcher).
func Easter(year int) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return date(year, time.Month(month), day)
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
