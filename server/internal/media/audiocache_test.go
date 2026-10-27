package media

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowEncoder writes its text to dst a chunk at a time, pausing between
// chunks, so a reader can be seen to get bytes before it finishes.
func slowEncoder(text string, chunk int, pause time.Duration, started chan<- struct{}, encodes *atomic.Int32) Encoder {
	return func(ctx context.Context, dst string) error {
		encodes.Add(1)
		f, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer f.Close()
		if started != nil {
			close(started)
		}
		for i := 0; i < len(text); i += chunk {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := f.WriteString(text[i:min(i+chunk, len(text))]); err != nil {
				return err
			}
			time.Sleep(pause)
		}
		return nil
	}
}

// A reader sees bytes while the encoder is still writing, and gets the
// whole file by the end; a second reader of the same key joins the one
// encode rather than starting another; a later reader gets the finished
// file with seeking.
func TestAudioCacheStreamsWhileEncoding(t *testing.T) {
	c := NewAudioCache(t.TempDir(), 0)
	text := strings.Repeat("0123456789", 50)
	var encodes atomic.Int32
	enc := slowEncoder(text, 100, 30*time.Millisecond, nil, &encodes)
	ctx := context.Background()

	first, err := c.Open(ctx, "k", "mp3", enc)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if first.Complete() {
		t.Fatal("complete before the encoder finished")
	}
	buf := make([]byte, 100)
	n, err := first.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("first read: %d %v", n, err)
	}
	if _, err := first.Seek(0, io.SeekStart); err == nil {
		t.Error("seek allowed on a growing file")
	}

	// A second reader, mid-encode, reads the same job.
	second, err := c.Open(ctx, "k", "mp3", enc)
	if err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(second)
	second.Close()
	if err != nil || string(all) != text {
		t.Fatalf("second reader: %v, %d bytes", err, len(all))
	}
	rest, err := io.ReadAll(first)
	if err != nil || string(buf[:n])+string(rest) != text {
		t.Fatalf("first reader: %v, %d+%d bytes", err, n, len(rest))
	}
	if encodes.Load() != 1 {
		t.Errorf("%d encodes, want 1", encodes.Load())
	}

	// Done: the next open is the finished file, seekable and sized.
	third, err := c.Open(ctx, "k", "mp3", enc)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if !third.Complete() || third.Size != int64(len(text)) || third.Path != filepath.Join(c.Dir, "k.mp3") {
		t.Errorf("finished stream: %+v", third)
	}
	if _, err := third.Seek(10, io.SeekStart); err != nil {
		t.Error(err)
	}
	if encodes.Load() != 1 {
		t.Errorf("%d encodes after a cached open, want 1", encodes.Load())
	}
}

// Many concurrent openers of one key share one encode.
func TestAudioCacheSingleflight(t *testing.T) {
	c := NewAudioCache(t.TempDir(), 0)
	var encodes atomic.Int32
	enc := slowEncoder(strings.Repeat("x", 1000), 200, 20*time.Millisecond, nil, &encodes)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := c.Open(context.Background(), "same", "opus", enc)
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			if all, err := io.ReadAll(s); err != nil || len(all) != 1000 {
				t.Errorf("reader: %v %d", err, len(all))
			}
		}()
	}
	wg.Wait()
	if encodes.Load() != 1 {
		t.Errorf("%d encodes, want 1", encodes.Load())
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "same.opus")); err != nil {
		t.Error(err)
	}
	if left, _ := filepath.Glob(filepath.Join(c.Dir, "*.part.*")); len(left) != 0 {
		t.Errorf("part files left: %v", left)
	}
}

// A failed encode leaves nothing behind and reports the error to every
// reader; the next open tries again.
func TestAudioCacheFailedEncode(t *testing.T) {
	c := NewAudioCache(t.TempDir(), 0)
	calls := 0
	enc := func(_ context.Context, dst string) error {
		calls++
		if calls == 1 {
			_ = os.WriteFile(dst, []byte("half"), 0o644)
			return io.ErrUnexpectedEOF
		}
		return os.WriteFile(dst, []byte("whole"), 0o644)
	}
	_, err := c.Wait(context.Background(), "k", "mp3", enc)
	if err == nil {
		t.Fatal("expected the encoder's error")
	}
	if left, _ := filepath.Glob(filepath.Join(c.Dir, "*")); len(left) != 0 {
		t.Errorf("files left after a failure: %v", left)
	}
	path, err := c.Wait(context.Background(), "k", "mp3", enc)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "whole" {
		t.Errorf("second attempt: %q", data)
	}
}

// The sweep drops stale part files and evicts least recently used files
// until the cache is under its bound, never a file being written.
func TestAudioCacheSweep(t *testing.T) {
	dir := t.TempDir()
	c := NewAudioCache(dir, 250)
	write := func(name string, size int, age time.Duration) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("old.mp3", 100, 3*time.Hour)
	write("mid.mp3", 100, 2*time.Hour)
	write("new.mp3", 100, time.Hour)
	write("dead.part.mp3", 50, 4*time.Hour)
	// A job in progress keeps its part file.
	live := &audioJob{part: filepath.Join(dir, "live.part.mp3"), final: filepath.Join(dir, "live.mp3"), done: make(chan struct{})}
	c.jobs["live"] = live
	write("live.part.mp3", 10, time.Minute)

	n, freed := c.Sweep()
	if n != 2 || freed != 150 {
		t.Errorf("swept %d files, %d bytes; want 2 and 150", n, freed)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	names := make([]string, len(left))
	for i, p := range left {
		names[i] = filepath.Base(p)
	}
	want := map[string]bool{"mid.mp3": true, "new.mp3": true, "live.part.mp3": true}
	if len(names) != 3 {
		t.Fatalf("left %v", names)
	}
	for _, nm := range names {
		if !want[nm] {
			t.Errorf("unexpected survivor %s", nm)
		}
	}

	// Opening a cached file counts as a use: it is no longer the oldest.
	s, err := c.Open(context.Background(), "mid", "mp3", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	write("newer.mp3", 100, 0)
	c.MaxBytes = 200
	c.Sweep()
	if _, err := os.Stat(filepath.Join(dir, "mid.mp3")); err != nil {
		t.Error("the recently used file was evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "new.mp3")); err == nil {
		t.Error("the least recently used file survived")
	}
}
