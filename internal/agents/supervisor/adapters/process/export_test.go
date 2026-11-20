// SPDX-License-Identifier: MIT

package process

// SetExitedHook makes f run in a runner's wait goroutine right after Stop
// has been told that the runner exited, so a test can hold it there.
func SetExitedHook(l *Launcher, f func(name string)) { l.exitedHook = f }

// SetDoneHook makes f run as the last thing a runner's wait goroutine
// does, so a test knows the launcher has nothing left to do for that
// runner. Set it before the first Start.
func SetDoneHook(l *Launcher, f func(name string)) { l.doneHook = f }
