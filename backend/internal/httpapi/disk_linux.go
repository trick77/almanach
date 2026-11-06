//go:build linux

package httpapi

import "syscall"

// freeBytes is the room a non-root process has on the filesystem holding
// dir: the blocks available times the fragment size, the unit Linux counts
// blocks in (f_frsize; f_bsize differs on some network mounts).
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Frsize), nil //nolint:gosec // a fragment size, never negative
}
