//go:build !linux

package httpapi

import "syscall"

// freeBytes is the room a non-root process has on the filesystem: the
// blocks available times the block size (the BSDs count in it).
func freeBytes(st *syscall.Statfs_t) uint64 {
	return st.Bavail * uint64(st.Bsize)
}
