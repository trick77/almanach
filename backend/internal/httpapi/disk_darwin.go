package httpapi

import "syscall"

// freeBytes is the room a non-root process has on the filesystem holding
// dir: the blocks available times the block size, the unit macOS counts
// them in.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
