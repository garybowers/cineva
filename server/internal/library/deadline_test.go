package library

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWithinIdle(t *testing.T) {
	tests := []struct {
		name    string
		work    func(progress func()) (int, error)
		wantErr error
	}{
		{
			// Longer in total than the deadline, but never quiet for it.
			name: "slow but moving",
			work: func(progress func()) (int, error) {
				for range 10 {
					time.Sleep(15 * time.Millisecond)
					progress()
				}
				return 7, nil
			},
		},
		{
			name: "stops moving",
			work: func(progress func()) (int, error) {
				progress()
				time.Sleep(300 * time.Millisecond)
				return 7, nil
			},
			wantErr: ErrNotResponding,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			v, err := WithinIdle(50*time.Millisecond, tc.work)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && v != 7 {
				t.Errorf("value = %d, want 7", v)
			}
			if tc.wantErr != nil && time.Since(start) > 200*time.Millisecond {
				t.Errorf("gave up after %v; the deadline is 50ms of silence", time.Since(start))
			}
		})
	}
}

// An abandoned read leaves its file marked stuck until it returns, so a
// second read is refused rather than stranded next to the first.
func TestWithinFileMarksStuck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "film.mkv")
	release := make(chan struct{})
	_, err := WithinFile(path, 20*time.Millisecond, func() (int, error) {
		<-release
		return 0, nil
	})
	if !errors.Is(err, ErrNotResponding) {
		t.Fatalf("err = %v, want ErrNotResponding", err)
	}
	if !Stuck(path) {
		t.Fatal("an abandoned read should leave its file marked stuck")
	}
	if Stuck(path + ".other") {
		t.Error("only the file read should be marked")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for Stuck(path) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if Stuck(path) {
		t.Error("the file stayed marked after the read returned")
	}

	// A call that answers in time marks nothing.
	if _, err := WithinFile(path, time.Second, func() (int, error) { return 1, nil }); err != nil || Stuck(path) {
		t.Errorf("a prompt call: err = %v, stuck = %v", err, Stuck(path))
	}
}

// MediaFile reads, seeks and serves like a file, and gives up on a read
// that does not answer.
func TestMediaFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "film.mkv")
	want := bytes.Repeat([]byte("0123456789"), 10_000)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	f, fi, err := OpenMedia(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if fi.Size() != int64(len(want)) {
		t.Errorf("size = %d, want %d", fi.Size(), len(want))
	}
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read %d bytes, err %v; want the whole file", len(got), err)
	}
	// Seeking from the end uses the size from the open.
	if pos, err := f.Seek(-10, io.SeekEnd); err != nil || pos != int64(len(want)-10) {
		t.Errorf("Seek(-10, end) = %d, %v", pos, err)
	}
	tail, err := io.ReadAll(f)
	if err != nil || string(tail) != "0123456789" {
		t.Errorf("tail = %q, %v", tail, err)
	}

	if _, _, err := OpenMedia(filepath.Join(t.TempDir(), "missing.mkv"), time.Second); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("opening a missing file: err = %v, want not exist", err)
	}
}

// A read that blocks — here on a FIFO no one writes to, which blocks in the
// kernel just as a read from a hung share does — is abandoned on the
// deadline, fails every read after it, and marks the file stuck.
func TestMediaFileReadDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stalled")
	if err := mkfifo(path); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	// Opening a FIFO for reading blocks until a writer appears, which is
	// the open deadline's case; a writer that never writes is the read's.
	w := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			w <- f
		}
	}()
	f, _, err := OpenMedia(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	writer := <-w
	defer writer.Close()
	defer f.Close()

	start := time.Now()
	f.timeout = 50 * time.Millisecond
	_, err = f.Read(make([]byte, 64))
	if !errors.Is(err, ErrNotResponding) {
		t.Fatalf("err = %v, want ErrNotResponding", err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("gave up after %v, the deadline is 50ms", took)
	}
	if _, err := f.Read(make([]byte, 64)); !errors.Is(err, ErrNotResponding) || !errors.Is(f.Err(), ErrNotResponding) {
		t.Errorf("a read after the deadline: err = %v, Err() = %v; both should be ErrNotResponding", err, f.Err())
	}
	if !Stuck(path) {
		t.Error("the file should be marked stuck while the abandoned read is blocked")
	}
	// The writer finally sends something: the abandoned read returns and
	// the mark clears.
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for Stuck(path) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if Stuck(path) {
		t.Error("the mark stayed after the read returned")
	}
}

// Reads go to the share in large chunks; the caller still sees its own
// position, and ranges after a seek are right.
func TestMediaFileReadAhead(t *testing.T) {
	data := make([]byte, 3*readAhead+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _, err := OpenMedia(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	small := make([]byte, 10)
	if n, err := f.Read(small); err != nil || n != 10 || !bytes.Equal(small, data[:10]) {
		t.Fatalf("first read = %d, %v", n, err)
	}
	if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 10 {
		t.Fatalf("position after a 10-byte read = %d, %v; want 10", pos, err)
	}
	if _, err := f.Seek(readAhead+5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(got, data[readAhead+5:]) {
		t.Fatalf("read after seek: %d bytes, %v; want the rest of the file", len(got), err)
	}
}
