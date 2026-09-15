//go:build linux

package hostmetrics

import (
	"errors"
	"math"
	"syscall"
)

func rootDiskUsage() (uint64, uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs("/", &stats); err != nil {
		return 0, 0, err
	}
	blockSize := uint64(stats.Bsize)
	if blockSize == 0 || stats.Blocks > math.MaxUint64/blockSize || stats.Bfree > stats.Blocks {
		return 0, 0, errors.New("root filesystem returned invalid counters")
	}
	total := stats.Blocks * blockSize
	used := (stats.Blocks - stats.Bfree) * blockSize
	if total == 0 {
		return 0, 0, errors.New("root filesystem size is zero")
	}
	return used, total, nil
}
