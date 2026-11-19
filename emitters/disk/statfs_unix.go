// SPDX-License-Identifier: MIT

//go:build unix

package disk

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// StatFS is the real Statter, backed by statfs(2).
type StatFS struct{}

var _ Statter = StatFS{}

// Usage asks the kernel about the file system at path.
func (StatFS) Usage(path string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Usage{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	size := uint64(st.Bsize) // #nosec G115 -- a block size is never negative
	return Usage{
		TotalBytes: st.Blocks * size,
		UsedBytes:  (st.Blocks - st.Bfree) * size,
		AvailBytes: st.Bavail * size,
	}, nil
}
