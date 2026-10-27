package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AudioCache keeps transcoded tracks on local disk (server/docs/music.md,
// part 3): a track is encoded once per format and bit rate, every request
// for it reads the same file, from the first packet while it is still
// being encoded, and the cache is bounded, least recently used first.
//
// Keyed by the caller (see Key), which includes the source file's size and
// modification time, so a replaced file is encoded again rather than served
// from the old copy.
type AudioCache struct {
	// Dir is where the files live. Not the transcode scratch, which is
	// often RAM: a track encoded once is worth keeping for the next play.
	Dir string
	// MaxBytes bounds the cache; 0 means no bound.
	MaxBytes int64
	// EncodeTimeout bounds one encode end to end; a job that runs longer
	// is given up on. Zero means ten minutes.
	EncodeTimeout time.Duration

	mu   sync.Mutex
	jobs map[string]*audioJob
}

// NewAudioCache makes a cache in dir, bounded by maxBytes.
func NewAudioCache(dir string, maxBytes int64) *AudioCache {
	return &AudioCache{Dir: dir, MaxBytes: maxBytes, jobs: map[string]*audioJob{}}
}

// Encoder writes the transcode of a source to dst. It is what Open runs
// once per key: TranscodeAudio wrapped in whatever deadlines the caller
// wants (the source may be on storage that stops answering).
type Encoder func(ctx context.Context, dst string) error

// audioJob is one encode in progress, shared by every reader of its key.
type audioJob struct {
	part, final string
	done        chan struct{}
	err         error
}

// CachedAudio is a transcode being served: either the finished file, which
// supports seeking and ranges, or the file as it grows, which does not.
type CachedAudio struct {
	// Path is the finished file's path, set once Complete reports true.
	Path string
	// Size is the finished file's size, 0 while it grows.
	Size int64
	// ModTime is the finished file's time, for conditional requests.
	ModTime time.Time

	file *os.File
	job  *audioJob
	done bool
}

// Complete reports whether the whole file is on disk.
func (s *CachedAudio) Complete() bool { return s.done }

// Read serves the file's bytes; while the encode runs, it waits for more
// rather than returning EOF, and returns the encoder's error if it fails.
func (s *CachedAudio) Read(p []byte) (int, error) {
	for {
		n, err := s.file.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if s.done {
			return 0, io.EOF
		}
		// The encoder is still writing: wait for it to finish, or for a
		// moment, then look again. ffmpeg's muxer flushes in blocks, so a
		// short wait costs nothing audible.
		select {
		case <-s.job.done:
			if s.job.err != nil {
				return 0, s.job.err
			}
			// The part file was renamed into place; the open descriptor
			// still reads the same inode, so finish it off.
			s.done = true
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Seek works on a finished file only; a growing one refuses it, which is
// what makes the response unranged.
func (s *CachedAudio) Seek(offset int64, whence int) (int64, error) {
	if !s.done {
		return 0, errors.New("audio cache: cannot seek while encoding")
	}
	return s.file.Seek(offset, whence)
}

// Close releases the file; the encode carries on for the next reader.
func (s *CachedAudio) Close() error { return s.file.Close() }

// Key names a transcode in the cache: the item, what its file was when
// cached (size and modification time, so a replaced file is re-encoded),
// the format and the bit rate.
func Key(itemID string, size, mtime int64, format string, kbps int) string {
	return fmt.Sprintf("%s-%d-%d-%s-%d", itemID, size, mtime, format, kbps)
}

// Open returns the transcode under key, encoding it with enc if it is not
// cached: the first caller starts the encode, later callers read the same
// growing file. The encode is not tied to any one caller's context: a
// listener who skips on has still left a file the next play uses.
func (c *AudioCache) Open(ctx context.Context, key, ext string, enc Encoder) (*CachedAudio, error) {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("audio cache: %w", err)
	}
	final := filepath.Join(c.Dir, key+"."+ext)
	c.mu.Lock()
	job := c.jobs[key]
	if job == nil {
		// A finished file on disk, not being written: serve it as is.
		if st, err := os.Stat(final); err == nil && st.Size() > 0 {
			c.mu.Unlock()
			f, err := os.Open(final)
			if err != nil {
				return nil, fmt.Errorf("audio cache: %w", err)
			}
			now := time.Now()
			// Touched on every use, so eviction is by last use, not by
			// when the file was made.
			_ = os.Chtimes(final, now, now)
			return &CachedAudio{Path: final, Size: st.Size(), ModTime: st.ModTime(), file: f, done: true}, nil
		}
		// The extension stays on the part file so the muxer is chosen by
		// it; the ".part" in the middle keeps it from being served.
		job = &audioJob{part: filepath.Join(c.Dir, key+".part."+ext), final: final, done: make(chan struct{})}
		if c.jobs == nil {
			c.jobs = map[string]*audioJob{}
		}
		c.jobs[key] = job
		go c.run(key, job, enc)
	}
	c.mu.Unlock()

	// The part file appears when the encoder opens it; wait for that, or
	// for the job to end (success or failure) first.
	var f *os.File
	for {
		var err error
		if f, err = os.Open(job.part); err == nil {
			break
		}
		select {
		case <-job.done:
			if job.err != nil {
				return nil, job.err
			}
			// Finished before we saw the part file: the final file is
			// there.
			return c.Open(ctx, key, ext, enc)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return &CachedAudio{job: job, file: f}, nil
}

// Wait blocks until the transcode under key is on disk in full and returns
// its path: for downloads and archives, which want the whole file.
func (c *AudioCache) Wait(ctx context.Context, key, ext string, enc Encoder) (string, error) {
	s, err := c.Open(ctx, key, ext, enc)
	if err != nil {
		return "", err
	}
	defer s.Close()
	if s.done {
		return s.Path, nil
	}
	select {
	case <-s.job.done:
		if s.job.err != nil {
			return "", s.job.err
		}
		return s.job.final, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (c *AudioCache) run(key string, job *audioJob, enc Encoder) {
	timeout := c.EncodeTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := enc(ctx, job.part)
	if err == nil {
		err = os.Rename(job.part, job.final)
	}
	if err != nil {
		if rerr := os.Remove(job.part); rerr != nil && !os.IsNotExist(rerr) {
			logger.Warn("removing a failed transcode failed", "path", job.part, "err", rerr)
		}
		job.err = err
	}
	c.mu.Lock()
	delete(c.jobs, key)
	c.mu.Unlock()
	close(job.done)
	if err == nil {
		c.Sweep()
	}
}

// Sweep removes leftover part files of encodes that died with the process
// and evicts least recently used files until the cache is under its bound.
// Files being written now are never touched. Returns what it removed.
func (c *AudioCache) Sweep() (int, int64) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return 0, 0
	}
	c.mu.Lock()
	writing := make(map[string]bool, len(c.jobs))
	for _, j := range c.jobs {
		writing[filepath.Base(j.part)] = true
	}
	c.mu.Unlock()
	var total int64
	var files []dirUsage
	var n int
	var freed int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(c.Dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if strings.Contains(e.Name(), ".part.") {
			if writing[e.Name()] {
				continue
			}
			if err := os.Remove(p); err == nil {
				n++
				freed += info.Size()
			}
			continue
		}
		total += info.Size()
		files = append(files, dirUsage{path: p, bytes: info.Size(), mod: info.ModTime()})
	}
	if c.MaxBytes <= 0 || total <= c.MaxBytes {
		return n, freed
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= c.MaxBytes {
			break
		}
		if err := os.Remove(f.path); err != nil {
			logger.Warn("could not evict a cached transcode", "path", f.path, "err", err)
			continue
		}
		total -= f.bytes
		n++
		freed += f.bytes
	}
	if n > 0 {
		logger.Info("audio cache over its limit, evicted transcodes", "limit", humanBytes(c.MaxBytes), "count", n, "freed", humanBytes(freed))
	}
	return n, freed
}
