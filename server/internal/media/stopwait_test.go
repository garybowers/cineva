package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A session superseded moments after it started had its directory deleted
// while ffmpeg was still opening init.mp4, which ffmpeg reported as "Failed
// to open segment". shutdown must wait for the transcoder to exit before
// removing anything.
func TestShutdownWaitsForTranscoder(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	s := &Session{ID: "test", Dir: dir}
	s.cancel = func() {}
	s.done = done

	// Stands in for the transcode goroutine: it is still writing when the
	// stop arrives, and only finishes when released.
	go func() {
		defer close(done)
		close(started)
		<-release
		// Writing here must still succeed: the directory cannot have been
		// removed while the transcoder was live.
		if err := os.WriteFile(filepath.Join(dir, "init.mp4"), []byte("x"), 0o644); err != nil {
			t.Errorf("transcoder could not write before shutdown finished: %v", err)
		}
	}()
	<-started

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		s.shutdown()
	}()

	// shutdown must still be waiting, not already deleting.
	select {
	case <-stopped:
		t.Fatal("shutdown returned while the transcoder was still running")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return after the transcoder exited")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("session directory survived shutdown: %v", err)
	}
}

// A transcoder that ignores cancellation must not wedge the caller forever.
func TestShutdownGivesUpOnAWedgedTranscoder(t *testing.T) {
	s := &Session{ID: "wedged", Dir: t.TempDir()}
	s.cancel = func() {}
	s.done = make(chan struct{}) // never closed
	start := time.Now()
	s.stopTranscode()
	if elapsed := time.Since(start); elapsed < transcodeStopTimeout {
		t.Errorf("returned after %v, expected to wait the full %v", elapsed, transcodeStopTimeout)
	}
}

// A restored session never started a transcoder, so there is nothing to wait
// for and shutdown must not block.
func TestShutdownOfRestoredSessionDoesNotBlock(t *testing.T) {
	s := &Session{ID: "restored", Dir: t.TempDir(), restored: true}
	done := make(chan struct{})
	go func() { defer close(done); s.stopTranscode() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopTranscode blocked on a session that never ran")
	}
}
