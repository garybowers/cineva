//go:build !unix

package library

import "errors"

func mkfifo(string) error { return errors.New("not supported on this platform") }
