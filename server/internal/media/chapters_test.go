package media

import (
	"os"
	"testing"
)

func TestReadChaptersFromSample(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("sample not present")
	}
	info, err := Probe(hdrSample)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("%d chapters", len(info.Chapters))
	for i, c := range info.Chapters {
		if i >= 5 {
			t.Logf("  … and %d more", len(info.Chapters)-5)
			break
		}
		t.Logf("  %2d  %8.1fs → %8.1fs  %q", i+1, c.StartSecs, c.EndSecs, c.Title)
	}
	// Whatever the file holds, the marks must be ordered and non-overlapping
	// or seeking to one lands in the wrong place.
	for i, c := range info.Chapters {
		if c.EndSecs <= c.StartSecs {
			t.Errorf("chapter %d is not a forward range: %v", i, c)
		}
		if i > 0 && c.StartSecs < info.Chapters[i-1].StartSecs {
			t.Errorf("chapter %d starts before the one before it", i)
		}
	}
}
