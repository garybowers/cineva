package media

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
)

// SubtitleStream describes one embedded subtitle stream in a media file.
type SubtitleStream struct {
	Index    int
	Codec    string
	Language string
	Title    string
	Forced   bool
}

func dictValue(dict *ffmpeg.AVDictionary, key string) string {
	if dict == nil {
		return ""
	}
	k := ffmpeg.ToCStr(key)
	defer k.Free()
	entry := ffmpeg.AVDictGet(dict, k, nil, 0)
	if entry == nil {
		return ""
	}
	return entry.Value().String()
}

// ProbeSubtitles lists the embedded subtitle streams of a media file.
func ProbeSubtitles(path string) ([]SubtitleStream, error) {
	in, err := av.Open(path)
	if err != nil {
		return nil, fmt.Errorf("probe subtitles %s: %w", path, err)
	}
	defer in.Close()

	var out []SubtitleStream
	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		st := streams.Get(uintptr(i))
		par := st.Codecpar()
		if ffmpeg.AVCodecGetType(par.CodecId()) != ffmpeg.AVMediaTypeSubtitle {
			continue
		}
		out = append(out, SubtitleStream{
			Index:    st.Index(),
			Codec:    ffmpeg.AVCodecGetName(par.CodecId()).String(),
			Language: dictValue(st.Metadata(), "language"),
			Title:    dictValue(st.Metadata(), "title"),
			Forced:   st.Disposition()&ffmpeg.AVDispositionForced != 0,
		})
	}
	return out, nil
}

// cue is one subtitle entry on its way to WebVTT.
type cue struct {
	startMs, endMs int64
	text           string
}

func vttTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

func renderVTT(cues []cue) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, c := range cues {
		text := strings.TrimSpace(c.text)
		if text == "" || c.endMs <= c.startMs {
			continue
		}
		fmt.Fprintf(&b, "%s --> %s\n%s\n\n", vttTimestamp(c.startMs), vttTimestamp(c.endMs), text)
	}
	return b.String()
}

var assTagRe = regexp.MustCompile(`\{[^}]*\}`)

// assDialogueText extracts the text field from an embedded ASS packet
// ("ReadOrder,Layer,Style,Name,MarginL,MarginR,MarginV,Effect,Text").
func assDialogueText(payload string) string {
	parts := strings.SplitN(payload, ",", 9)
	text := parts[len(parts)-1]
	text = assTagRe.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, `\N`, "\n")
	text = strings.ReplaceAll(text, `\n`, "\n")
	return text
}

// ExtractVTT demuxes one embedded text-subtitle stream and converts it to
// WebVTT. Text subtitle packets carry the cue text directly (subrip: plain
// text; ass: dialogue line; mov_text: 2-byte length prefix + UTF-8).
//
// The whole file is read to find the track's packets, which on a film over
// a network share takes a while; onRead, when set, is called each time a
// read returns, so a caller can tell slow from stopped (see
// library.WithinIdle).
func ExtractVTT(path string, streamIndex int, codec string, onRead func()) (string, error) {
	if onRead == nil {
		onRead = func() {}
	}
	in, err := av.Open(path)
	if err != nil {
		return "", fmt.Errorf("extract subtitles %s: %w", path, err)
	}
	defer in.Close()
	onRead()

	var tb *ffmpeg.AVRational
	streams := in.Streams()
	for i := uint(0); i < in.NbStreams(); i++ {
		st := streams.Get(uintptr(i))
		if st.Index() == streamIndex {
			tb = st.TimeBase()
		}
	}
	if tb == nil {
		return "", fmt.Errorf("stream %d not found in %s", streamIndex, path)
	}
	msTB := ffmpeg.AVMakeQ(1, 1000)

	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)

	var cues []cue
	for {
		err := in.ReadPacket(pkt)
		onRead()
		if errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read subtitles: %w", err)
		}
		if pkt.StreamIndex() != streamIndex || pkt.Size() == 0 {
			ffmpeg.AVPacketUnref(pkt)
			continue
		}
		data := unsafe.Slice((*byte)(pkt.Data()), pkt.Size())
		var text string
		switch codec {
		case "ass", "ssa":
			text = assDialogueText(string(data))
		case "mov_text":
			if len(data) > 2 {
				text = string(data[2:])
			}
		default: // subrip and other plain-text formats
			text = string(data)
		}
		startMs := ffmpeg.AVRescaleQ(pkt.Pts(), tb, msTB)
		durMs := ffmpeg.AVRescaleQ(pkt.Duration(), tb, msTB)
		if durMs <= 0 {
			durMs = 3000
		}
		cues = append(cues, cue{startMs: startMs, endMs: startMs + durMs, text: text})
		ffmpeg.AVPacketUnref(pkt)
	}
	return renderVTT(cues), nil
}

var (
	srtTimeRe = regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2})[,.](\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2})[,.](\d{3})`)
)

func srtMs(h, m, s, ms string) int64 {
	// srtTimeRe has already matched each part as digits.
	toI := func(v string) int64 { return int64(parseNum(v)) }
	return toI(h)*3600000 + toI(m)*60000 + toI(s)*1000 + toI(ms)
}

// ConvertSidecarVTT reads a sidecar subtitle file (.srt or .vtt) and returns
// WebVTT content.
func ConvertSidecarVTT(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read sidecar: %w", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	content = strings.TrimPrefix(content, "\ufeff")

	if strings.EqualFold(filepath.Ext(path), ".vtt") || strings.HasPrefix(content, "WEBVTT") {
		return content, nil
	}

	// SRT: blocks of "index / timing / text..." separated by blank lines.
	var cues []cue
	for _, block := range strings.Split(content, "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		for i, line := range lines {
			m := srtTimeRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			cues = append(cues, cue{
				startMs: srtMs(m[1], m[2], m[3], m[4]),
				endMs:   srtMs(m[5], m[6], m[7], m[8]),
				text:    strings.Join(lines[i+1:], "\n"),
			})
			break
		}
	}
	return renderVTT(cues), nil
}

// Sidecar is a subtitle file living next to a media file.
type Sidecar struct {
	Path     string
	Codec    string // "srt" | "vtt"
	Language string
	Forced   bool
}

// SidecarSubtitles finds subtitle files next to a media file:
// "Movie.srt", "Movie.en.srt", "Movie.eng.forced.vtt", ...
func SidecarSubtitles(mediaPath string) []Sidecar {
	base := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	dir := filepath.Dir(mediaPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Sidecar
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || (ext != ".srt" && ext != ".vtt") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if stem != base && !strings.HasPrefix(stem, base+".") {
			continue
		}
		s := Sidecar{Path: filepath.Join(dir, name), Codec: strings.TrimPrefix(ext, ".")}
		// Trailing dot-separated tokens carry language and flags.
		for _, tok := range strings.Split(strings.TrimPrefix(stem, base), ".") {
			switch t := strings.ToLower(tok); {
			case t == "forced":
				s.Forced = true
			case len(t) == 2 || len(t) == 3:
				s.Language = t
			}
		}
		out = append(out, s)
	}
	return out
}
