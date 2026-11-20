// SPDX-License-Identifier: MIT

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/api/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// whoami answers with the caller the API layer set.
func whoamiModule() fakeModule {
	return fakeModule{name: "whoami", api: core.API{Routes: []core.Route{{
		Pattern: "GET /v1/test-whoami",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := core.CallerFrom(r.Context())
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"kind": c.Kind, "name": c.Name, "uid": c.UID, "root": c.IsRoot}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}),
	}}}}
}

// socketServer is a real daemon API on a Unix socket. setup runs before the
// listener opens, so it may replace what the server reads about peers.
func socketServer(t *testing.T, setup func(*api.Server, core.Deps, *coretest.Fakes)) (string, *coretest.Fakes) {
	t.Helper()
	short, err := os.MkdirTemp("", "ap")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(short); err != nil {
			t.Error(err)
		}
	})
	deps, fakes := coretest.NewDeps(t)
	cfg := *deps.Config
	cfg.RuntimeDir, cfg.LibDir, cfg.API.Addr = short, t.TempDir(), "127.0.0.1:0"
	deps.Config = &cfg
	srv, err := api.NewServer([]core.Module{setupModule(), whoamiModule()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(srv, deps, fakes)
	}
	run, err := srv.Listen()
	if err != nil {
		t.Fatal(err)
	}
	errs := run.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := run.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		for range 2 {
			if err := <-errs; !errors.Is(err, http.ErrServerClosed) {
				t.Error(err)
			}
		}
	})
	return cfg.SocketPath(), fakes
}

// getOnSocket sends GET path on the socket from this process.
func getOnSocket(t *testing.T, sock, path string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	resp, err := c.Get("http://agentsoned" + path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("close body: %v", cerr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// curlOnSocket sends GET path on the socket from a child process: a process
// the daemon's process started, with our uid, that is not the daemon.
func curlOnSocket(t *testing.T, sock, path string) (int, string) {
	t.Helper()
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatalf("this test needs curl to call the socket from another process: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, curl, "-sS", "--unix-socket", sock, "-w", "\n%{http_code}", "http://agentsoned"+path).Output() // #nosec G204 -- fixed program, test socket path
	if err != nil {
		t.Fatalf("curl: %v", err)
	}
	// -w puts the status on the last line, after the body.
	i := strings.LastIndex(string(out), "\n")
	if i < 0 {
		t.Fatalf("curl output %q has no status line", out)
	}
	body, code := strings.TrimSpace(string(out[:i])), strings.TrimSpace(string(out[i+1:]))
	n, err := strconv.Atoi(code)
	if err != nil {
		t.Fatalf("curl output %q: %v", out, err)
	}
	return n, body
}

// fakePeer makes every connection look like p, whose /proc cgroup file is cgroup.
func fakePeer(p domain.Peer, cgroup string) func(*api.Server, core.Deps, *coretest.Fakes) {
	return func(s *api.Server, _ core.Deps, _ *coretest.Fakes) {
		api.SetPeerSource(s, func(net.Conn) (domain.Peer, error) { return p, nil },
			fstest.MapFS{strconv.Itoa(p.PID) + "/cgroup": {Data: []byte(cgroup)}})
	}
}

// refusedAudit checks the one audit record of a refused peer.
func refusedAudit(t *testing.T, f *coretest.Fakes, p domain.Peer, reason string) {
	t.Helper()
	var got []core.AuditEntry
	for _, e := range f.Audit.Entries() {
		if e.Action == "api.socket.refused" {
			got = append(got, e)
		}
	}
	if len(got) != 1 {
		t.Fatalf("refusal audit records %+v, want one", got)
	}
	e := got[0]
	if e.Actor != "system:api" || e.Target != fmt.Sprintf("uid:%d", p.UID) || e.Outcome != core.AuditDenied {
		t.Errorf("record %+v", e)
	}
	var d struct {
		PID    int    `json:"pid"`
		UID    int    `json:"uid"`
		Reason string `json:"reason"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(e.Detail, &d); err != nil {
		t.Fatal(err)
	}
	if (p.PID != 0 && d.PID != p.PID) || d.UID != p.UID || d.Reason != reason || d.Path != "/v1/test-whoami" {
		t.Errorf("detail %s, want pid %d uid %d reason %q", e.Detail, p.PID, p.UID, reason)
	}
}

func TestSocket_TheDaemonsOwnProcessIsTheDaemon(t *testing.T) {
	t.Parallel()
	sock, _ := socketServer(t, nil)
	if code, body := getOnSocket(t, sock, "/v1/test-whoami"); code != http.StatusOK || !strings.Contains(body, `"name":"daemon"`) {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestSocket_AChildProcessOfTheDaemonIsRefusedAndAudited(t *testing.T) {
	t.Parallel()
	sock, fakes := socketServer(t, nil)
	if code, body := curlOnSocket(t, sock, "/v1/test-whoami"); code != http.StatusForbidden || strings.Contains(body, "daemon\"") {
		t.Fatalf("got %d %s", code, body)
	}
	reason := domain.ReasonNobody
	if os.Getuid() == 0 {
		reason = domain.ReasonRootOutsideAgents
	}
	refusedAudit(t, fakes, domain.Peer{UID: os.Getuid()}, reason)
}

func TestSocket_AHumansLinuxUserIsThatHuman(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Fatal("run this test as a user other than root: uid 0 is never a human")
	}
	sock, _ := socketServer(t, func(_ *api.Server, _ core.Deps, f *coretest.Fakes) {
		f.Humans.Add("ada", os.Getuid())
	})
	code, body := curlOnSocket(t, sock, "/v1/test-whoami")
	if code != http.StatusOK || !strings.Contains(body, `"kind":"human"`) || !strings.Contains(body, `"name":"ada"`) ||
		!strings.Contains(body, `"root":false`) {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestSocket_ARootWebTerminalIsRefusedAndAudited(t *testing.T) {
	t.Parallel()
	// Terminals are started by the daemon, so they live in its cgroup.
	p := domain.Peer{PID: 4101, UID: 0}
	sock, fakes := socketServer(t, fakePeer(p, "0::/daemon\n"))
	if code, body := getOnSocket(t, sock, "/v1/test-whoami"); code != http.StatusForbidden {
		t.Fatalf("got %d %s", code, body)
	}
	refusedAudit(t, fakes, p, domain.ReasonRootOutsideAgents)
}

func TestSocket_AProcessInACustomAgentsTerminalIsThatAgent(t *testing.T) {
	t.Parallel()
	p := domain.Peer{PID: 4102, UID: 200001}
	sock, _ := socketServer(t, func(s *api.Server, d core.Deps, f *coretest.Fakes) {
		f.Agents.Add(core.Agent{Name: "scout", UID: 200001})
		fakePeer(p, "0::/daemon\n")(s, d, f)
	})
	code, body := getOnSocket(t, sock, "/v1/test-whoami")
	if code != http.StatusOK || !strings.Contains(body, `"kind":"agent"`) || !strings.Contains(body, `"name":"scout"`) {
		t.Fatalf("got %d %s", code, body)
	}
}

// failingAgents is an agent directory whose reads fail.
type failingAgents struct{ core.AgentDirectory }

func (failingAgents) ListAgents(context.Context) ([]core.Agent, error) {
	return nil, errors.New("database is locked")
}

func TestSocket_AnAgentDirectoryErrorIsRefusedAndAudited(t *testing.T) {
	t.Parallel()
	p := domain.Peer{PID: 4103, UID: 200001}
	sock, fakes := socketServer(t, func(s *api.Server, d core.Deps, f *coretest.Fakes) {
		f.Agents.Add(core.Agent{Name: "scout", UID: 200001})
		d.Ports.Agents = failingAgents{f.Agents}
		fakePeer(p, "0::/\n")(s, d, f)
	})
	if code, body := getOnSocket(t, sock, "/v1/test-whoami"); code != http.StatusForbidden {
		t.Fatalf("got %d %s", code, body)
	}
	refusedAudit(t, fakes, p, domain.ReasonAgentLookup)
}

// failingHumans is a human directory whose reads fail.
type failingHumans struct{ core.HumanDirectory }

func (failingHumans) HumanByUID(context.Context, int) (core.HumanAccount, error) {
	return core.HumanAccount{}, errors.New("database is locked")
}

func TestSocket_AHumanDirectoryErrorIsRefusedAndAudited(t *testing.T) {
	t.Parallel()
	p := domain.Peer{PID: 4104, UID: 1000}
	sock, fakes := socketServer(t, func(s *api.Server, d core.Deps, f *coretest.Fakes) {
		f.Humans.Add("ada", 1000)
		d.Ports.Humans = failingHumans{f.Humans}
		fakePeer(p, "0::/\n")(s, d, f)
	})
	if code, body := getOnSocket(t, sock, "/v1/test-whoami"); code != http.StatusForbidden {
		t.Fatalf("got %d %s", code, body)
	}
	refusedAudit(t, fakes, p, domain.ReasonHumanLookup)
}
