// SPDX-License-Identifier: MIT

package domain

import (
	"path/filepath"
	"sort"
	"strings"
)

// RunnerSocketEnv is the environment variable that carries the socket path
// to a runner process (runner PROTOCOL.md).
const RunnerSocketEnv = "AGENTSONE_RUNNER_SOCKET"

// Exec is how to start one runner process.
type Exec struct {
	Path string
	Args []string
	Env  []string // KEY=value, sorted
	Dir  string
}

// RunnerExec builds the command of a plugin runner. dir is the installed
// plugin directory; every ${PLUGIN_ROOT} in the command, arguments and
// environment becomes dir. The socket goes into the environment.
func RunnerExec(dir string, r Runner, socket string) Exec {
	expand := func(s string) string { return strings.ReplaceAll(s, PluginRoot, dir) }
	out := Exec{Path: expand(r.Command), Dir: dir}
	for _, a := range r.Args {
		out.Args = append(out.Args, expand(a))
	}
	for k, v := range r.Env {
		out.Env = append(out.Env, k+"="+expand(v))
	}
	out.Env = append(out.Env, RunnerSocketEnv+"="+socket)
	sort.Strings(out.Env)
	return out
}

// BuiltinRunner is a runner that ships in the image.
type BuiltinRunner struct {
	Runner
	DisplayName string
	Model       string // model called through the host gateway; empty for none
	Version     string
}

// BuiltinRunners lists the runners under <libDir>/runners: echo, openai and
// claude. The claude runner ships in the image; its engine downloads after
// a human accepts the engine's licence.
func BuiltinRunners(libDir string) []BuiltinRunner {
	bin := func(name string) string { return filepath.Join(libDir, "runners", name) }
	return []BuiltinRunner{
		{
			Runner:      Runner{Name: "echo", Description: "Answers with what it was told. For tests and demos.", Command: bin("echo"), ProtocolVersions: []string{"1.1", "1.0"}},
			DisplayName: "Echo", Version: "1.1.0",
		},
		{
			Runner:      Runner{Name: "openai", Description: "Calls the model through the host gateway.", Command: bin("openai"), ProtocolVersions: []string{"1.1", "1.0"}},
			DisplayName: "OpenAI compatible", Model: "autoagents/one", Version: "1.1.0",
		},
		{
			Runner:      Runner{Name: "claude", Description: "Runs the Claude Code engine through the host gateway. The engine is off until a human turns it on in Settings and accepts its licence.", Command: bin("claude"), ProtocolVersions: []string{"1.1", "1.0"}},
			DisplayName: "Claude Code", Model: "autoagents/one", Version: "1.1.0",
		},
	}
}
