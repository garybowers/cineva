package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateTrickplay(t *testing.T) {
	if _, err := os.Stat(hdrSample); err != nil {
		t.Skip("sample not present")
	}
	info, err := Probe(hdrSample)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	dir := t.TempDir()

	// A short slice of the film, so the test is quick: pretend it is four
	// minutes long and only that much gets thumbnails.
	start := time.Now()
	var calls int
	var read int64
	progress := func(n int64) error {
		calls++
		if n < read {
			t.Errorf("bytes read went backwards: %d after %d", n, read)
		}
		read = n
		return nil
	}
	if err := GenerateTrickplay(context.Background(), hdrSample, dir, "item1", 240, progress); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if calls < 20 || read <= 0 {
		t.Errorf("progress called %d times, %d bytes read; want once per thumbnail, counting the reads", calls, read)
	}
	t.Logf("240s of a %dx%d source in %v", info.Width, info.Height, time.Since(start).Round(time.Millisecond))

	sheet, vtt := TrickplayPaths(dir, "item1")
	si, err := os.Stat(sheet)
	if err != nil {
		t.Fatalf("no sheet: %v", err)
	}
	if si.Size() < 1000 {
		t.Errorf("sheet is %d bytes — suspiciously small", si.Size())
	}
	body, err := os.ReadFile(vtt)
	if err != nil {
		t.Fatalf("no vtt: %v", err)
	}
	text := string(body)
	if !strings.HasPrefix(text, "WEBVTT") {
		t.Errorf("vtt does not start with the WEBVTT header:\n%s", text[:min(80, len(text))])
	}
	// 240s at 10s intervals is 25 marks; every cue must address a region of
	// the sheet, or the player has nothing to crop to.
	cues := strings.Count(text, "#xywh=")
	if cues < 20 {
		t.Errorf("got %d cues, want ~25", cues)
	}
	if !strings.Contains(text, "00:00:00.000 --> 00:00:10.000") {
		t.Errorf("first cue is not at the start:\n%s", text[:min(200, len(text))])
	}
	// Cue regions must tile: the second thumbnail sits one width across.
	if !strings.Contains(text, "#xywh=160,0,160,") {
		t.Errorf("second cue does not point at the next column:\n%s", text[:min(300, len(text))])
	}
	t.Logf("sheet %d bytes, %d cues", si.Size(), cues)

	// The VTT is written last, so its presence implies a complete sheet.
	if fi, err := os.Stat(filepath.Join(dir, "item1.jpg")); err != nil || fi.Size() == 0 {
		t.Errorf("sheet missing or empty despite a vtt existing")
	}

	// An error from progress abandons the file, and nothing is written.
	stop := errors.New("giving way")
	err = GenerateTrickplay(context.Background(), hdrSample, dir, "item2", 240, func(int64) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("abandoned: %v, want the progress error", err)
	}
	if _, vtt := TrickplayPaths(dir, "item2"); fileExists(vtt) {
		t.Errorf("cues written for an abandoned file")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestGenerateTrickplayRejectsUnknownDuration(t *testing.T) {
	if err := GenerateTrickplay(context.Background(), "/nonexistent", t.TempDir(), "x", 0, nil); err == nil {
		t.Error("expected an error for an unknown duration")
	}
}
