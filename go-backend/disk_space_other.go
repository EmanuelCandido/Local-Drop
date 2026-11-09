//go:build !windows

package main

import "golang.org/x/sys/unix"

func availableDiskBytes(path string) (uint64, error) {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return 0, err
	}
	return stats.Bavail * uint64(stats.Bsize), nil
}
