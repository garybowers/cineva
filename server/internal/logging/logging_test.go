package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"debug", slog.LevelDebug, true},
		{"INFO", slog.LevelInfo, true},
		{"", slog.LevelInfo, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"loud", 0, false},
	} {
		got, err := ParseLevel(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("ParseLevel(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestPrettyLine(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewPretty(&buf, slog.LevelInfo, false)).With("component", "storage")
	l.Debug("hidden")
	l.Warn("library unreachable", "library", "TV Shows", "err", errors.New("not responding"), "after", 1500*time.Millisecond)
	line := buf.String()
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("got %q, want exactly one line (debug filtered out)", line)
	}
	for _, want := range []string{" WARN  storage    library unreachable", `library="TV Shows"`, `err="not responding"`, "after=1.5s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
}

// A component logger made before Setup still writes through what Setup set.
func TestForFollowsSetup(t *testing.T) {
	logger := For("scan")
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(NewPretty(&buf, slog.LevelDebug, false)))
	logger.Info("done", "files", 3)
	if !strings.Contains(buf.String(), " INFO  scan       done  files=3") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestCard(t *testing.T) {
	var buf bytes.Buffer
	Card(&buf, false, "cineva v1.0.0", "http://localhost:4747 · VAAPI")
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("card is %d lines, want 4:\n%s", len(lines), buf.String())
	}
	width := len([]rune(lines[0]))
	for _, l := range lines {
		if len([]rune(l)) != width {
			t.Fatalf("ragged card:\n%s", buf.String())
		}
	}
}
