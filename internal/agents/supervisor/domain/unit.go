// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Socket paths and the runner's environment. Adapters take a directory so
// tests can listen below a temp dir.
const (
	// SocketDir holds one runner socket per agent. /run/agentsone is the
	// daemon's runtime directory.
	SocketDir = "/run/agentsone/runners"
	// SocketEnv names the socket for the runner (PROTOCOL.md transport).
	SocketEnv = "AGENTSONE_RUNNER_SOCKET"
)

// ErrUnsafeValue is a run value that could smuggle in something else, such
// as a line break or NUL, a relative program path, or an environment entry
// without a name.
var ErrUnsafeValue = errors.New("unsafe run value")

// SocketPath is the runner socket of agent name.
func SocketPath(name string) string { return SocketDir + "/" + name + ".sock" }

// Limits are the resource limits of one runner. Zero means not set.
type Limits struct {
	CPUQuotaPercent int   // 100 is one full CPU
	MemoryMaxBytes  int64 // memory, swap not allowed
	TasksMax        int   // processes and threads
}

// LimitMode says how an agent's limits hold, for the API's limits status.
type LimitMode string

// Limit modes.
const (
	// LimitsHard: a per-agent cgroup v2 enforces CPU, memory (no swap) and
	// tasks. Only custom agents are held.
	LimitsHard LimitMode = "hard"
	// LimitsSoft: no writable cgroup tree; only process resource limits
	// (address space, processes, open files) apply and CPU is not capped.
	LimitsSoft LimitMode = "soft"
	// LimitsNone: no limits. Root agents are trusted and run as root, so
	// they can change their own limits within the container's ceiling.
	LimitsNone LimitMode = "none"
)

// DefaultCustomLimits apply to custom agents unless the caller sets others:
// one CPU, 2 GiB of memory and 512 tasks. Root agents get no limits; they are
// trusted and run without a sandbox.
var DefaultCustomLimits = Limits{CPUQuotaPercent: 100, MemoryMaxBytes: 2 << 30, TasksMax: 512}

// Exec is the command that runs the runner, from the runner registry.
type Exec struct {
	Path string
	Args []string
	Env  []string // KEY=value
	Dir  string
}

// UnitSpec is how one agent's runner runs: as whom, what, and within which limits.
type UnitSpec struct {
	Agent   string
	Account Account
	Exec    Exec
	Limits  Limits
}

// Validate refuses a spec a launcher must not run: a bad agent name, a
// relative program path, an environment entry without a name, or any value
// holding a line break or NUL. The runner registry is a plugin surface, so
// this is checked before anything starts.
func (u UnitSpec) Validate() error {
	if err := ValidateName(u.Agent); err != nil {
		return err
	}
	if !strings.HasPrefix(u.Exec.Path, "/") {
		return fmt.Errorf("exec path %q is not absolute: %w", u.Exec.Path, ErrUnsafeValue)
	}
	values := append([]string{u.Exec.Path, u.Exec.Dir}, u.Exec.Args...)
	for _, e := range u.Exec.Env {
		if k, _, ok := strings.Cut(e, "="); !ok || k == "" {
			return fmt.Errorf("environment entry %q: %w", e, ErrUnsafeValue)
		}
		values = append(values, e)
	}
	for _, v := range values {
		if strings.ContainsAny(v, "\n\r\x00") {
			return fmt.Errorf("value %q holds a line break or NUL: %w", v, ErrUnsafeValue)
		}
	}
	return nil
}
