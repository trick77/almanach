//go:build linux

package httpapi

import "syscall"

// freeBytes is the room a non-root process has on the filesystem: the
// blocks available times the fragment size, the unit Linux counts blocks
// in (f_frsize; f_bsize differs on some network mounts).
func freeBytes(st *syscall.Statfs_t) uint64 {
	return st.Bavail * uint64(st.Frsize) //nolint:gosec // a fragment size, never negative
}
