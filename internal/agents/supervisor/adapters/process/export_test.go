// SPDX-License-Identifier: MIT

package process

// SetExitedHook makes f run in a runner's wait goroutine right after Stop
// has been told that the runner exited, so a test can hold it there.
func SetExitedHook(l *Launcher, f func(name string)) { l.exitedHook = f }
