package media

import (
	"errors"
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// parsedGraph is a filter graph built from a spec string, with its own
// buffer source and sink.
//
// Used for the hardware-decode path, where the frames reaching the filter
// are downloaded device surfaces rather than the decoder's own output: the
// vendored av helper derives its source parameters from the codec context,
// which for a hardware decoder reports the device surface format, not the
// format the downloaded frames actually arrive in.
type parsedGraph struct {
	graph *ffmpeg.AVFilterGraph
	src   *ffmpeg.AVFilterContext
	sink  *ffmpeg.AVFilterContext
	out   *ffmpeg.AVFrame
}

// newParsedGraph builds a graph fed by frames shaped like sample.
//
// The source is described from an actual frame rather than from the codec
// context because the two disagree on a hardware decode: the context
// reports the device surface format, the frames arrive downloaded. The
// colour matrix and range are declared as well as the geometry — a filter
// link carries them, and a tone-mapping chain fed from a link that claims
// neither fails with "no path between colorspaces" rather than guessing.
func newParsedGraph(spec string, sample *ffmpeg.AVFrame, timeBase *ffmpeg.AVRational) (*parsedGraph, error) {
	graph := ffmpeg.AVFilterGraphAlloc()
	if graph == nil {
		return nil, errors.New("allocate filter graph")
	}
	g := &parsedGraph{graph: graph}
	full := fmt.Sprintf(
		"buffer=video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=1/1"+
			":colorspace=%d:range=%d,%s,buffersink",
		sample.Width(), sample.Height(), sample.Format(),
		timeBase.Num(), timeBase.Den(),
		int(sample.Colorspace()), int(sample.ColorRange()), spec)

	cs := ffmpeg.ToCStr(full)
	_, err := ffmpeg.AVFilterGraphParsePtr(graph, cs, nil, nil, nil)
	cs.Free()
	if err != nil {
		g.Close()
		return nil, fmt.Errorf("parse %q: %w", full, err)
	}
	g.src, g.sink = g.find("buffer"), g.find("buffersink")
	if g.src == nil || g.sink == nil {
		g.Close()
		return nil, errors.New("graph is missing its source or sink")
	}
	if _, err := ffmpeg.AVFilterGraphConfig(graph, nil); err != nil {
		g.Close()
		return nil, fmt.Errorf("configure %q: %w", full, err)
	}
	g.out = ffmpeg.AVFrameAlloc()
	if g.out == nil {
		g.Close()
		return nil, errors.New("allocate filter output frame")
	}
	return g, nil
}

// find returns the first filter of a type, by type rather than instance
// name: the naming of parsed filters is an implementation detail.
func (g *parsedGraph) find(typeName string) *ffmpeg.AVFilterContext {
	filters := g.graph.Filters()
	if filters == nil {
		return nil
	}
	for i := uint(0); i < g.graph.NbFilters(); i++ {
		fc := filters.Get(uintptr(i))
		if fc == nil {
			continue
		}
		if f := fc.Filter(); f != nil && f.Name().String() == typeName {
			return fc
		}
	}
	return nil
}

func (g *parsedGraph) Push(frame *ffmpeg.AVFrame) error {
	// KeepRef: the caller still owns the frame.
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(g.src, frame, ffmpeg.AVBuffersrcFlagKeepRef); err != nil {
		return fmt.Errorf("push frame to graph: %w", err)
	}
	return nil
}

func (g *parsedGraph) Pull(fn func(*ffmpeg.AVFrame) error) error {
	for {
		_, err := ffmpeg.AVBuffersinkGetFrame(g.sink, g.out)
		if err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				return nil // nothing ready, or nothing more ever
			}
			return fmt.Errorf("pull frame from graph: %w", err)
		}
		cbErr := fn(g.out)
		ffmpeg.AVFrameUnref(g.out)
		if cbErr != nil {
			return cbErr
		}
	}
}

func (g *parsedGraph) Close() {
	if g.out != nil {
		ffmpeg.AVFrameFree(&g.out)
	}
	if g.graph != nil {
		ffmpeg.AVFilterGraphFree(&g.graph)
	}
}
