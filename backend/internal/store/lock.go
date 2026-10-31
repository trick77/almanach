package store

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// lockPoll is how often a waiting migrator tries the lock again.
const lockPoll = 50 * time.Millisecond

// lockFile takes an exclusive flock on the file at path, creating it, and
// returns its release. It waits for another holder, in this process or
// another on the same host, until ctx ends. The database is on one host's
// disk by rule, so a host lock is the lock every migrator meets.
func lockFile(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // beside the configured database file
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) //nolint:gosec // a file descriptor fits an int
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}
