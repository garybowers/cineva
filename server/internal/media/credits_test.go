package media

import "testing"

func TestCreditsChapter(t *testing.T) {
	// A two-hour film's chapters, the last one named as given.
	film := func(names ...string) []Chapter {
		out := []Chapter{{StartSecs: 0, EndSecs: 3600, Title: "Chapter 1"}}
		at := 3600.0
		lens := []float64{3000, 600}
		if len(names) == 3 {
			lens = []float64{2800, 500, 300}
		}
		for i, n := range names {
			out = append(out, Chapter{StartSecs: at, EndSecs: at + lens[i], Title: n})
			at += lens[i]
		}
		return out
	}
	tests := []struct {
		name       string
		chapters   []Chapter
		runtime    int
		start, end float64
		ok         bool
	}{
		{"end credits", film("The chase", "End Credits"), 7200, 6600, 7200, true},
		{"plain credits, upper case", film("The chase", "CREDITS"), 7200, 6600, 7200, true},
		{"numbered", film("The chase", "12. End Titles"), 7200, 6600, 7200, true},
		{"chapter word and number", film("The chase", "Chapter 24 - Closing Credits"), 7200, 6600, 7200, true},
		{"German", film("Die Jagd", "Abspann"), 7200, 6600, 7200, true},
		{"French", film("La poursuite", "Générique de fin"), 7200, 6600, 7200, true},
		{"Spanish", film("La persecución", "Créditos"), 7200, 6600, 7200, true},
		{"Italian", film("L'inseguimento", "Titoli di coda"), 7200, 6600, 7200, true},
		{"Dutch", film("De achtervolging", "Aftiteling"), 7200, 6600, 7200, true},
		{"Swedish", film("Jakten", "Eftertexter"), 7200, 6600, 7200, true},
		{"followed by a post-credits scene", film("The chase", "End Credits", "Post-credits scene"), 7200, 6400, 6900, true},
		{"unknown running time", film("The chase", "End Credits"), 0, 6600, 7200, true},
		{"unnamed chapters", film("Chapter 2", "Chapter 3"), 7200, 0, 0, false},
		{"something else entirely", film("The chase", "Epilogue"), 7200, 0, 0, false},
		{"credits and more in one name", film("The chase", "Credits and post-credits scene"), 7200, 0, 0, false},
		{"single chapter", []Chapter{{StartSecs: 0, EndSecs: 7200, Title: "Credits"}}, 7200, 0, 0, false},
		{"opening credits early on", []Chapter{
			{StartSecs: 0, EndSecs: 120, Title: "Intro"},
			{StartSecs: 120, EndSecs: 400, Title: "Credits"},
		}, 7200, 0, 0, false},
		{"too short", []Chapter{
			{StartSecs: 0, EndSecs: 7190, Title: "Film"},
			{StartSecs: 7190, EndSecs: 7200, Title: "Credits"},
		}, 7200, 0, 0, false},
		{"too long", []Chapter{
			{StartSecs: 0, EndSecs: 3600, Title: "Film"},
			{StartSecs: 3600, EndSecs: 7200, Title: "Credits"},
		}, 7200, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := CreditsChapter(tc.chapters, tc.runtime)
			if ok != tc.ok || start != tc.start || end != tc.end {
				t.Fatalf("CreditsChapter = %v-%v %v, want %v-%v %v", start, end, ok, tc.start, tc.end, tc.ok)
			}
		})
	}
}
