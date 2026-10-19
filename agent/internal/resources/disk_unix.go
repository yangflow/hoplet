//go:build linux || darwin

package resources

import "syscall"

// statDisk is the space the agent's user can use on the volume of path, and the volume's size.
func statDisk(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize)
	return uint64(st.Bavail) * bs, uint64(st.Blocks) * bs, nil
}
