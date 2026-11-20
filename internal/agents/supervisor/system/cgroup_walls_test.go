// SPDX-License-Identifier: MIT

//go:build linux && integration

package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// dialEnv, when set, makes this test binary a runner that calls the daemon
// API socket at that path and writes what it was told to $OUT.
const dialEnv = "AOTEST_DIAL"

// dialHelper is the runner side: it writes "$R whoami=<answer>" to $OUT,
// then idles until the test stops it. It runs as the agent's user, in the
// agent's cgroup, where the launcher put it.
func dialHelper() int {
	line := os.Getenv("R") + " whoami=" + whoami(os.Getenv(dialEnv))
	if err := os.WriteFile(os.Getenv("OUT"), []byte(line+"\n"), 0); err != nil { // #nosec G703 -- the test's own FIFO, from the environment the test set
		fmt.Fprintln(os.Stderr, "write result:", err)
		return 1
	}
	// Idle like a runner until the launcher stops it when the test ends.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	return 0
}

func whoami(sock string) string {
	c := &http.Client{Timeout: waitFor, Transport: &http.Transport{DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	resp, err := c.Get("http://agentsoned/v1/test-whoami")
	if err != nil {
		return "error:" + strings.ReplaceAll(err.Error(), " ", "_")
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("status:%d", resp.StatusCode)
	}
	return strings.TrimSpace(string(body))
}

// whoamiModule answers with the caller the API layer set.
type whoamiModule struct{ core.Base }

func (whoamiModule) Name() string { return "whoami" }

func (whoamiModule) API() core.API {
	return core.API{Routes: []core.Route{{
		Pattern: "GET /v1/test-whoami",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := core.CallerFrom(r.Context())
			if err := json.NewEncoder(w).Encode(map[string]any{"kind": c.Kind, "name": c.Name, "uid": c.UID}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}),
	}}}
}

// apiSocket serves the real daemon API on a Unix socket every user can
// reach, as /run/agentsone is, and returns its path.
func apiSocket(t *testing.T) string {
	t.Helper()
	return apiSocketWith(t, whoamiModule{}, nil)
}

// apiSocketWith is apiSocket serving mod, with fakes set up by setup when
// it is not nil.
func apiSocketWith(t *testing.T, mod core.Module, setup func(*coretest.Fakes)) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(dir, 0o755); err != nil { // #nosec G302 -- agent users must reach the socket, as in /run/agentsone
		t.Fatal(err)
	}
	deps, fakes := coretest.NewDeps(t)
	if setup != nil {
		setup(fakes)
	}
	cfg := *deps.Config
	cfg.RuntimeDir, cfg.LibDir, cfg.API.Addr = dir, t.TempDir(), "127.0.0.1:0"
	deps.Config = &cfg
	srv, err := api.NewServer([]core.Module{mod}, deps)
	if err != nil {
		t.Fatal(err)
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
	return cfg.SocketPath()
}

// requireRootOwned checks that dir and every file in it belong to root and
// that neither group nor others may write them.
func requireRootOwned(t *testing.T, dir string) {
	t.Helper()
	paths := []string{dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 || info.Mode().Perm()&0o022 != 0 {
			t.Errorf("%s: mode %v owner %d:%d, want root:root and not group or world writable", p, info.Mode().Perm(), st.Uid, st.Gid)
		}
	}
}

// A custom agent cannot leave its cgroup, join another agent's, make a new
// one or raise its own limits, and the daemon socket still names it.
func TestCgroup_CustomAgentCannotLeaveOrChangeItsWall(t *testing.T) {
	root := requireCgroups(t)
	other := agentUser(t, "aotest-cara", 200108)
	acc := agentUser(t, "aotest-bren", 200109)
	limits := domain.Limits{TasksMax: 64}
	sock := apiSocket(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(root, "aotest-evil")
	t.Cleanup(func() {
		if err := os.Remove(evil); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove %s: %v", evil, err)
		}
	})

	run(t, other, `echo up > "$OUT"; exec sleep 600`, limits, root)
	otherDir := filepath.Join(root, other.User)
	selfDir := filepath.Join(root, acc.User)
	// Each attempt runs in a subshell, so a write that worked would move or
	// change only that subshell; the answer is in the exit status.
	script := `if (echo $$ > "$OTHER/cgroup.procs") 2>/dev/null; then a=moved; else a=refused; fi; ` +
		`if (echo $$ > "$DAEMON_CG/cgroup.procs") 2>/dev/null; then b=moved; else b=refused; fi; ` +
		`if mkdir "$EVIL" 2>/dev/null; then c=made; else c=refused; fi; ` +
		`if (echo max > "$SELF_CG/pids.max") 2>/dev/null; then d=raised; else d=refused; fi; ` +
		`export R="join-other=$a join-daemon=$b mkdir=$c raise-limit=$d"; exec "$BIN"`
	out := fifo(t)
	l := &process.Launcher{CgroupRoot: root}
	spec := domain.UnitSpec{Agent: acc.User, Account: acc, Limits: limits,
		Exec: domain.Exec{Path: "/bin/sh", Args: []string{"-c", script}, Env: []string{
			"OUT=" + out, "BIN=" + self, dialEnv + "=" + sock,
			"OTHER=" + otherDir, "DAEMON_CG=" + filepath.Join(filepath.Dir(root), process.DaemonCgroup),
			"EVIL=" + evil, "SELF_CG=" + selfDir,
		}}}
	if err := l.Install(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(t.Context(), acc.User); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Stop(context.Background(), acc.User); err != nil {
			t.Errorf("stop: %v", err)
		}
	})

	got := readFifo(t, out)
	t.Logf("proof cgroup wall: runner as uid 200109 in %s reported %q; pids.max=%s", selfDir, got, value(t, selfDir, "pids.max"))
	want := "join-other=refused join-daemon=refused mkdir=refused raise-limit=refused whoami="
	if !strings.HasPrefix(got, want) {
		t.Fatalf("runner reported %q, want prefix %q", got, want)
	}
	var caller struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		UID  int    `json:"uid"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got, want)), &caller); err != nil {
		t.Fatalf("socket answer %q: %v", strings.TrimPrefix(got, want), err)
	}
	if caller.Kind != "agent" || caller.Name != acc.User || caller.UID != 200109 {
		t.Errorf("socket named the runner %+v, want agent %s uid 200109", caller, acc.User)
	}
	if v := value(t, selfDir, "pids.max"); v != "64" {
		t.Errorf("pids.max = %s, want 64 unchanged", v)
	}
	if _, err := os.Stat(evil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists: %v", evil, err)
	}
	requireRootOwned(t, root)
	requireRootOwned(t, selfDir)
	requireRootOwned(t, otherDir)
}
