// SPDX-License-Identifier: MIT

package process

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// Docker mounts cgroupfs read-only without writable-cgroups, so the first
// write fails with EROFS; the real mount is proven by the boot system test.
func TestPrepareReason_NamesAReadOnlyTree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: create daemon cgroup: %w", ErrCgroupsReadOnly, &os.PathError{Op: "mkdir", Path: "/sys/fs/cgroup/daemon", Err: syscall.EROFS}), "cgroup tree is read-only"},
		{fmt.Errorf("%w: enable controllers at the root: %w", ErrCgroupsReadOnly, syscall.EBUSY), "cgroup tree is not writable"},
	}
	for _, tt := range tests {
		if got := prepareReason(tt.err); got != tt.want {
			t.Errorf("prepareReason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
