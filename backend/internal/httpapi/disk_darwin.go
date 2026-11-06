package httpapi

import "syscall"

// freeBytes is the room a non-root process has on the filesystem: the
// blocks available times the block size, the unit macOS counts them in.
// Linux and macOS only: the BSDs name and type these fields otherwise, and
// Windows has no statfs.
func freeBytes(st *syscall.Statfs_t) uint64 {
	return st.Bavail * uint64(st.Bsize)
}
