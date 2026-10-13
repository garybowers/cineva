package media

import (
	"strings"
	"sync"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"

	"github.com/garybowers/cineva/server/internal/logging"
)

var ffmpegLog = logging.For("ffmpeg")

// RouteFFmpegLogs sends FFmpeg's own messages through Cineva's logger
// instead of straight to stderr. FFmpeg is chatty about things that are
// not problems for us — deprecated options, odd timestamps it copes with —
// so its warnings are debug-level here and its errors warnings: when one
// matters, Cineva logs the consequence itself (a failed transcode, an
// unreadable file) with the context FFmpeg lacks.
func RouteFFmpegLogs() {
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogWarning)
	var mu sync.Mutex
	var line strings.Builder
	ffmpeg.AVLogSetCallback(func(ctx *ffmpeg.LogCtx, level int, msg string) {
		mu.Lock()
		defer mu.Unlock()
		// FFmpeg hands over fragments; a message ends at a newline.
		for {
			before, after, found := strings.Cut(msg, "\n")
			line.WriteString(before)
			if !found {
				return
			}
			msg = after
			text := strings.TrimSpace(line.String())
			line.Reset()
			if text == "" {
				continue
			}
			scope := "ffmpeg"
			if ctx != nil {
				if class := ctx.Class(); class != nil {
					scope = class.ItemName()(ctx.RawPtr()).String()
				}
			}
			if level >= 0 {
				level &= 0xff
			}
			if level <= ffmpeg.AVLogError {
				ffmpegLog.Warn(text, "scope", scope)
			} else {
				ffmpegLog.Debug(text, "scope", scope)
			}
		}
	})
}
