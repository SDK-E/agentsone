// SPDX-License-Identifier: MIT

//go:build linux

package process

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// reportFD is the helper's report pipe: the daemon passes it as the first
// extra file. It is close-on-exec, so the daemon reads EOF with no data
// once the runner starts, or the reason the helper failed.
const reportFD = 3

// The helper runs in any binary that imports this package (the daemon,
// and the tests): /proc/self/exe is that binary. It runs before main.
func init() {
	if filepath.Base(os.Args[0]) != LaunchArgv0 {
		return
	}
	syscall.CloseOnExec(reportFD)
	os.Exit(launchMain(os.Args[1:], osLaunch{}, os.NewFile(reportFD, "launch-report")))
}

// osLaunch is launchSys on Linux.
type osLaunch struct{}

func (osLaunch) JoinCgroup(dir string) error {
	// Writing a pid moves the whole process, all its threads, at once.
	return os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644) // #nosec G306 -- cgroupfs control file
}

var rlimits = map[rlimitKind]int{
	rlimitCore: unix.RLIMIT_CORE, rlimitNoFile: unix.RLIMIT_NOFILE,
	rlimitNProc: unix.RLIMIT_NPROC, rlimitAS: unix.RLIMIT_AS,
}

func (osLaunch) LowerLimit(k rlimitKind, v uint64) error {
	res := rlimits[k]
	var cur unix.Rlimit
	if err := unix.Getrlimit(res, &cur); err != nil {
		return err
	}
	v = lowered(v, cur.Max)
	return unix.Setrlimit(res, &unix.Rlimit{Cur: v, Max: v})
}

func (osLaunch) SetIDs(uid, gid int) error {
	// The groups go first: after setuid the process may not change them.
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return err
	}
	if err := syscall.Setgid(gid); err != nil {
		return err
	}
	return syscall.Setuid(uid)
}

func (osLaunch) Exec(path string, argv []string) error {
	return unix.Exec(path, argv, os.Environ()) // #nosec G204 -- path and args come from the runner registry and passed Validate
}
