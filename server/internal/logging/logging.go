// Package logging sets up Cineva's log output: levelled, structured
// (log/slog), aligned and coloured in a terminal, one JSON object a line
// anywhere else — under systemd or Docker, or piped into a log tool.
//
// Packages log through a component logger,
//
//	var logger = logging.For("scan")
//	logger.Warn("file unreadable", "path", p, "err", err)
//
// which follows whatever Setup configured, even though package variables
// are created before Setup runs.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Levels are the names log.level accepts.
var Levels = []string{"debug", "info", "warn", "error"}

// Formats are the names log.format accepts: auto picks pretty for a
// terminal and json otherwise.
var Formats = []string{"auto", "pretty", "json"}

// ParseLevel reads a level name.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("log level %q: want one of %s", name, strings.Join(Levels, ", "))
}

// Setup makes slog's default logger — and with it the standard library's
// log package — write at level, in format, to out. It reports whether the
// output is the pretty, human-facing one.
func Setup(level, format string, out *os.File) (pretty bool, err error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "auto":
		pretty = isTerminal(out)
	case "pretty":
		pretty = true
	case "json":
	default:
		return false, fmt.Errorf("log format %q: want one of %s", format, strings.Join(Formats, ", "))
	}
	var h slog.Handler
	if pretty {
		h = NewPretty(out, lvl, Colour(out))
	} else {
		h = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lvl})
	}
	slog.SetDefault(slog.New(h))
	return pretty, nil
}

// Colour reports whether output to f should be coloured: a terminal, and
// NO_COLOR (no-color.org) not set.
func Colour(f *os.File) bool { return isTerminal(f) && os.Getenv("NO_COLOR") == "" }

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// For returns the logger for a component ("scan", "transcode", "http").
func For(component string) *slog.Logger {
	return slog.New(lazy{ops: []func(slog.Handler) slog.Handler{
		func(h slog.Handler) slog.Handler {
			return h.WithAttrs([]slog.Attr{slog.String("component", component)})
		},
	}})
}

// lazy resolves slog's default handler on every record rather than when
// the logger is made, so a package-level logger made at init time still
// writes where Setup later pointed it.
type lazy struct {
	ops []func(slog.Handler) slog.Handler
}

func (l lazy) resolve() slog.Handler {
	h := slog.Default().Handler()
	for _, op := range l.ops {
		h = op(h)
	}
	return h
}

func (l lazy) Enabled(ctx context.Context, lvl slog.Level) bool {
	return slog.Default().Handler().Enabled(ctx, lvl)
}

func (l lazy) Handle(ctx context.Context, r slog.Record) error { return l.resolve().Handle(ctx, r) }

func (l lazy) WithAttrs(attrs []slog.Attr) slog.Handler {
	return lazy{ops: append(l.ops[:len(l.ops):len(l.ops)], func(h slog.Handler) slog.Handler { return h.WithAttrs(attrs) })}
}

func (l lazy) WithGroup(name string) slog.Handler {
	return lazy{ops: append(l.ops[:len(l.ops):len(l.ops)], func(h slog.Handler) slog.Handler { return h.WithGroup(name) })}
}
