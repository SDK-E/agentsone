// SPDX-License-Identifier: MIT

//go:build linux && integration

package boot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/socket"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// binEnv is the folder with the real runner binaries: agentsone-runner-echo
// and agentsone-runner-openai (built from runners/), and node, the Node.js
// binary of the machine image (image/machine/pins.txt). The go-integration
// CI job puts them next to this test binary; without the variable the
// test looks there.
const binEnv = "AGENTSONE_SYSTEST_BIN"

// scribe has the limits of the custom agent the demo machine could not run:
// its Go runner died before main when the soft memory limit was RLIMIT_AS.
var scribe = domain.Limits{CPUQuotaPercent: 25, MemoryMaxBytes: 256 << 20, TasksMax: 16}

// modelAnswer is what the fake model gateway answers every call with.
const modelAnswer = "Noted, alice."

// nodeRunner is a minimal runner on Node.js, as a runner that is not Go
// (the Claude engine runs on Node): hello, then one chat turn answered with
// the chat text. It holds 32 MiB outside the V8 heap and about as much in
// it, so both grow under the data limit before it answers, and logs what
// it uses.
const nodeRunner = `'use strict';
const net = require('node:net');
const readline = require('node:readline');
const outside = Array.from({length: 32}, () => Buffer.alloc(1 << 20, 1));
const inside = Array.from({length: 250000}, (_, i) => ({i, s: 'x' + i}));
const m = process.memoryUsage();
process.stderr.write('node runner ' + process.version + ': rss=' + m.rss + ' heapUsed=' + m.heapUsed +
  ' external=' + m.external + ' held=' + outside.length + 'MiB+' + inside.length + ' objects' +
  ' GOMEMLIMIT=' + process.env.GOMEMLIMIT + '\n');
const sock = net.connect(process.env.AGENTSONE_RUNNER_SOCKET);
const send = (msg) => sock.write(JSON.stringify(Object.assign({jsonrpc: '2.0'}, msg)) + '\n');
sock.on('connect', () => send({id: 1, method: 'hello', params: {protocolVersions: ['1.0'],
  runner: {name: 'node-script', version: '1.0.0'},
  capabilities: {streaming: true, cancel: true, compaction: true, resume: true}}}));
readline.createInterface({input: sock}).on('line', (line) => {
  const msg = JSON.parse(line);
  if (msg.id === 1 && msg.result) {
    send({method: 'status', params: {state: 'idle'}});
  } else if (msg.method === 'wake') {
    const {turnId, payload} = msg.params;
    send({id: msg.id, result: {}});
    send({method: 'status', params: {state: 'working', turnId}});
    send({method: 'output', params: {turnId, chatId: payload.chatId, chunk: 0, text: 'node: ' + payload.text, final: true}});
    send({method: 'status', params: {state: 'idle', turnId}});
  }
});
`

// A custom agent's real runners start through the daemon's launcher, as
// their own users with scribe's limits from the start probe, say hello over
// their sockets and answer a chat: with hard limits on the machine recipe
// and with soft ones on a read-only cgroup tree. Soft memory is each
// process's data limit; the address space stays unlimited, which Go and
// Node need to start.
func TestBoot_CustomAgentsRealRunnersAnswerAChat(t *testing.T) {
	if !probed {
		t.Fatalf("%s is not 1: run inside a container of its own (CI job go-integration)", inContainer)
	}
	if os.Geteuid() != 0 {
		t.Fatal("the boot system test needs root in its container (see the go-integration CI job)")
	}
	mode := domain.LimitsHard
	if os.Getenv(treeEnv) == "read-only" {
		mode = domain.LimitsSoft
	}
	if boot.Mode != mode {
		t.Fatalf("probe = %s, want %s limits for this container", boot.Line(), mode)
	}
	bin := os.Getenv(binEnv)
	if bin == "" {
		bin = filepath.Dir(os.Args[0])
	}
	model := fakeModel(t)
	tests := []struct {
		name, user string
		uid        int
		exec       func(t *testing.T) domain.Exec
		want       string
	}{
		{"echo runner, Go", "aoboot-echo", 200204, binary(bin, "agentsone-runner-echo"), "echo: hello scribe"},
		{"openai runner, Go, as scribe ran", "aoboot-scribe", 200205, binary(bin, "agentsone-runner-openai"), modelAnswer},
		{"node runner, Node.js of the machine image", "aoboot-node", 200206, nodeScript(bin), "node: hello scribe"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acc := agentUser(t, tc.user, tc.uid)
			sup, out, ended := supervisor(t, acc, tc.exec(t), model)
			chat := `{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice",` +
				`"text":"hello scribe","sentAt":"2026-11-19T19:00:01Z"}`
			if err := sup.Wake(t.Context(), acc.User, core.WakeChat, json.RawMessage(chat)); err != nil {
				t.Fatalf("wake %s: %v", acc.User, err)
			}
			answer := out.final(t, ended)
			limits := runnerLimits(t, tc.uid)
			t.Logf("proof boot runner %s (%s): as uid %d answered %q; mode=%s; %s",
				tc.name, boot.Line(), tc.uid, answer, sup.LimitMode(acc.User), limits)
			if answer != tc.want {
				t.Errorf("runner answered %q, want %q", answer, tc.want)
			}
			if m := sup.LimitMode(acc.User); m != mode {
				t.Errorf("limit mode = %s, want %s", m, mode)
			}
			data := "Max data size unlimited unlimited"
			if mode == domain.LimitsSoft {
				data = fmt.Sprintf("Max data size %d %d", scribe.MemoryMaxBytes, scribe.MemoryMaxBytes)
			}
			if as := "Max address space unlimited unlimited"; !strings.Contains(limits, as) || !strings.Contains(limits, data) {
				t.Errorf("runner limits %q, want %q and %q", limits, as, data)
			}
		})
	}
}

// binary runs the runner executable name from folder bin.
func binary(bin, name string) func(t *testing.T) domain.Exec {
	return func(t *testing.T) domain.Exec {
		t.Helper()
		path := filepath.Join(bin, name)
		if _, err := os.Stat(path); err != nil { // #nosec G703 -- the test's own runner folder
			t.Fatalf("the real runner: %v (set %s or build it next to the test binary)", err, binEnv)
		}
		return domain.Exec{Path: path}
	}
}

// nodeScript runs nodeRunner on the node binary from folder bin.
func nodeScript(bin string) func(t *testing.T) domain.Exec {
	return func(t *testing.T) domain.Exec {
		t.Helper()
		node := binary(bin, "node")(t)
		dir, err := os.MkdirTemp("", "aoboot")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("remove %s: %v", dir, err)
			}
		})
		if err := os.Chmod(dir, 0o755); err != nil { // #nosec G302 -- agent users read the script
			t.Fatal(err)
		}
		script := filepath.Join(dir, "runner.js")
		if err := os.WriteFile(script, []byte(nodeRunner), 0o644); err != nil { // #nosec G306 -- agent users read the script
			t.Fatal(err)
		}
		node.Args = []string{script}
		return node
	}
}

// fakeModel is the model gateway: it streams modelAnswer to every chat
// completion, as an OpenAI-compatible server does.
func fakeModel(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"model\":\"autoagents/one\",\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", modelAnswer)
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// supervisor is the daemon's runner supervisor over the daemon's own
// launcher (its cgroup root from the start probe) and real runner sockets,
// with acc installed as a custom agent that runs exec with scribe's limits.
// Its runners reach the model at model; the tool server is unreachable, as
// a turn without tools must still answer. ended yields why the runner
// ended by itself.
func supervisor(t *testing.T, acc domain.Account, exec domain.Exec, model string) (*app.Supervisor, *outputs, <-chan error) {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	fakes.Agents.Add(core.Agent{Name: acc.User, Kind: core.AgentAlwaysOn, UID: acc.UID})
	ended := make(chan error, 1)
	procs := &process.Launcher{CgroupRoot: boot.Root, Exited: func(_ string, err error) {
		select {
		case ended <- err:
		default:
		}
	}}
	srv := &socket.Server{Dir: filepath.Dir(domain.SocketPath(acc.User))}
	launcher := &socket.Launcher{Inner: procs, Server: srv, Ctx: t.Context()}
	cfg := app.DefaultConfig()
	cfg.GatewayURL, cfg.MCPURL = model, "http://127.0.0.1:1"+core.MCPPath
	out := &outputs{}
	sup := app.New(app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Launcher: launcher,
		Identity: out, Output: out, Summaries: out, Usage: out, Config: cfg, Wall: holdingWall{}})
	srv.Supervisor = sup
	if _, ok := sup.CheckWall(t.Context()); !ok {
		t.Fatal("the test's network wall does not hold")
	}
	exec.Env = append(exec.Env, domain.SocketEnv+"="+domain.SocketPath(acc.User))
	if err := launcher.Install(t.Context(), domain.UnitSpec{Agent: acc.User, Account: acc, Limits: scribe, Exec: exec}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := launcher.Stop(context.Background(), acc.User); err != nil {
			t.Errorf("stop %s: %v", acc.User, err)
		}
		if err := srv.Shutdown(); err != nil {
			t.Errorf("close the runner sockets: %v", err)
		}
	})
	return sup, out, ended
}

// runnerLimits reads the memory limits of the process that runs as uid:
// the runner. Its environment is not readable here (no CAP_SYS_PTRACE in
// the container); the launcher's unit tests prove GOMEMLIMIT.
func runnerLimits(t *testing.T, uid int) string {
	t.Helper()
	dirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		status, err := os.ReadFile(filepath.Join(dir, "status")) // #nosec G304 -- procfs
		if err != nil || !strings.Contains(string(status), fmt.Sprintf("\nUid:\t%d\t", uid)) {
			continue
		}
		l, err := os.ReadFile(filepath.Join(dir, "limits")) // #nosec G304 -- procfs
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, line := range strings.Split(string(l), "\n") {
			if strings.HasPrefix(line, "Max address space") || strings.HasPrefix(line, "Max data size") {
				lines = append(lines, strings.Join(strings.Fields(line)[:5], " "))
			}
		}
		return strings.Join(lines, "; ")
	}
	t.Fatalf("no process runs as uid %d", uid)
	return ""
}

// holdingWall is a machine on the inner network only.
type holdingWall struct{}

func (holdingWall) Check(context.Context) domain.NetWall {
	return domain.NetWall{Holds: true, Interfaces: []string{"eth0"}}
}

// outputs is the society side the supervisor talks to: it keeps what the
// runner answered and gives the agent an identity.
type outputs struct {
	mu     sync.Mutex
	chunks []app.OutputChunk
}

func (o *outputs) Output(_ context.Context, _ string, c app.OutputChunk) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.chunks = append(o.chunks, c)
	return nil
}

func (o *outputs) RecordUsage(context.Context, string, app.Usage) error { return nil }

func (o *outputs) Load(context.Context, string) (app.Summary, error) { return app.Summary{}, nil }

func (o *outputs) Save(context.Context, string, app.Summary) error { return nil }

func (o *outputs) Identity(_ context.Context, a string) (app.AgentIdentity, error) {
	return app.AgentIdentity{Agent: core.Agent{Name: a, Kind: core.AgentAlwaysOn}, Team: a, Charter: "Write things down.",
		OneLine: "Goal: notes. Now: waiting.", Goals: []string{"notes"}}, nil
}

// final waits for the turn's final output and returns its text. A runner
// that ends first, or no answer within waitFor, fails the test.
func (o *outputs) final(t *testing.T, ended <-chan error) string {
	t.Helper()
	deadline := time.After(waitFor)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		o.mu.Lock()
		var text strings.Builder
		for _, c := range o.chunks {
			text.WriteString(c.Text)
			if c.Final {
				o.mu.Unlock()
				return text.String()
			}
		}
		o.mu.Unlock()
		select {
		case err := <-ended:
			t.Fatalf("the runner ended before it answered: %v (see its stderr above)", err)
		case <-deadline:
			t.Fatalf("the runner did not answer within %s", waitFor)
		case <-tick.C:
		}
	}
}
