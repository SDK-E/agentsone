// SPDX-License-Identifier: MIT

package disk

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Mount is one mounted file system.
type Mount struct {
	Path   string
	FSType string
}

// Usage is how full a file system is, in bytes, as df counts it.
type Usage struct {
	TotalBytes uint64
	UsedBytes  uint64
	// AvailBytes is what an ordinary user can still write. It can be less
	// than total minus used, because file systems reserve blocks.
	AvailBytes uint64
}

// Percent is the share of the usable space that is used, rounded up, the
// way df prints it. A file system with no usable space reads 0.
func (u Usage) Percent() int {
	usable := u.UsedBytes + u.AvailBytes
	if usable == 0 {
		return 0
	}
	return int((u.UsedBytes*100 + usable - 1) / usable) // #nosec G115 -- used is at most usable, so the result is at most 100
}

// MountLister lists the mounted file systems.
type MountLister interface {
	List() ([]Mount, error)
}

// Statter reads how full the file system at a path is.
type Statter interface {
	Usage(path string) (Usage, error)
}

// MountsFile lists mounts from a file in /proc/self/mounts format.
type MountsFile string

// List reads and parses the file.
func (m MountsFile) List() ([]Mount, error) {
	b, err := os.ReadFile(string(m))
	if err != nil {
		return nil, fmt.Errorf("read mount table: %w", err)
	}
	return ParseMounts(bytes.NewReader(b)), nil
}

// ParseMounts reads a /proc/self/mounts table. Lines it cannot read are
// skipped: a source must keep watching what it can.
func ParseMounts(r io.Reader) []Mount {
	var out []Mount
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		out = append(out, Mount{Path: unescape(fields[1]), FSType: fields[2]})
	}
	return out
}

// unescape undoes the kernel's octal escapes (\040 for a space) in a path.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// pseudo are file system types that hold no disk space worth watching.
var pseudo = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true, "ramfs": true,
	"cgroup": true, "cgroup2": true, "mqueue": true, "securityfs": true, "debugfs": true,
	"tracefs": true, "pstore": true, "bpf": true, "autofs": true, "configfs": true,
	"fusectl": true, "hugetlbfs": true, "binfmt_misc": true, "nsfs": true, "selinuxfs": true,
}
