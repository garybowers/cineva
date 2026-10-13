package media

import (
	"syscall"
	"time"
)

// syscallRusage wraps getrusage so the CPU measurement in the hardware
// decode test reads clearly.
type syscallRusage struct{ r syscall.Rusage }

func getrusage(u *syscallRusage) error {
	return syscall.Getrusage(syscall.RUSAGE_SELF, &u.r)
}

// total is user plus system CPU time: the GPU path moves work off the CPU,
// so this is the number that should fall.
func (u syscallRusage) total() time.Duration {
	return time.Duration(u.r.Utime.Nano()) + time.Duration(u.r.Stime.Nano())
}
