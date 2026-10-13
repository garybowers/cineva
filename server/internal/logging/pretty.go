package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// componentWidth aligns messages after the component column; longer names
// simply push their line along.
const componentWidth = 10

// ANSI styles. Terminal colours are the terminal's own palette, so these
// follow whatever theme the terminal has.
const (
	reset  = "\x1b[0m"
	dim    = "\x1b[2m"
	bold   = "\x1b[1m"
	red    = "\x1b[31m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	cyan   = "\x1b[36m"
)

// Pretty writes one aligned line per record:
//
//	15:00:09 INFO  transcode  VAAPI detected  codec=h264_vaapi
type Pretty struct {
	mu     *sync.Mutex
	out    io.Writer
	level  slog.Leveler
	colour bool
	comp   string      // the "component" attribute, shown as a column
	attrs  []slog.Attr // other attributes added with With
	group  string      // dotted prefix from WithGroup
}

func NewPretty(out io.Writer, level slog.Leveler, colour bool) *Pretty {
	return &Pretty{mu: &sync.Mutex{}, out: out, level: level, colour: colour}
}

func (h *Pretty) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *Pretty) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range attrs {
		if a.Key == "component" && h.group == "" {
			c.comp = a.Value.String()
			continue
		}
		c.attrs = append(c.attrs, h.qualify(a))
	}
	return &c
}

func (h *Pretty) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.group = h.group + name + "."
	return &c
}

func (h *Pretty) qualify(a slog.Attr) slog.Attr {
	if h.group != "" {
		a.Key = h.group + a.Key
	}
	return a
}

func (h *Pretty) paint(style, s string) string {
	if !h.colour || style == "" {
		return s
	}
	return style + s + reset
}

func (h *Pretty) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	b.WriteString(h.paint(dim, t.Format("15:04:05")))
	b.WriteByte(' ')
	b.WriteString(h.levelTag(r.Level))
	b.WriteByte(' ')

	comp := h.comp
	var attrs []slog.Attr
	attrs = append(attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "component" && h.group == "" {
			comp = a.Value.String()
			return true
		}
		attrs = append(attrs, h.qualify(a))
		return true
	})
	if comp == "" {
		comp = "cineva"
	}
	b.WriteString(h.paint(cyan, fmt.Sprintf("%-*s", componentWidth, comp)))
	b.WriteByte(' ')
	b.WriteString(r.Message)

	for _, a := range attrs {
		h.writeAttr(&b, "", a)
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

// levelTag names the level in full, padded so messages line up.
func (h *Pretty) levelTag(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return h.paint(bold+red, "ERROR")
	case l >= slog.LevelWarn:
		return h.paint(yellow, "WARN ")
	case l >= slog.LevelInfo:
		return h.paint(blue, "INFO ")
	default:
		return h.paint(dim, "DEBUG")
	}
}

func (h *Pretty) writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := prefix + a.Key
	if a.Value.Kind() == slog.KindGroup {
		p := key + "."
		if a.Key == "" {
			p = prefix
		}
		for _, g := range a.Value.Group() {
			h.writeAttr(b, p, g)
		}
		return
	}
	val := formatValue(a.Value)
	style := ""
	if key == "err" || key == "error" || strings.HasSuffix(key, ".err") {
		style = red
	}
	b.WriteString("  ")
	b.WriteString(h.paint(dim, key+"="))
	b.WriteString(h.paint(style, val))
}

func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindDuration:
		return v.Duration().Round(time.Millisecond).String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339)
	case slog.KindString:
		return quoteIfNeeded(v.String())
	}
	if err, ok := v.Any().(error); ok {
		return quoteIfNeeded(err.Error())
	}
	return quoteIfNeeded(v.String())
}

// quoteIfNeeded keeps simple values bare and quotes anything with spaces,
// quotes or control characters, so a line still splits cleanly on spaces.
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			return strconv.Quote(s)
		}
	}
	return s
}
