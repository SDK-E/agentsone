// SPDX-License-Identifier: MIT

//go:build linux && integration

package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/machine/adapters/system"
	machine "github.com/SDK-E/agentsone/internal/machine/domain"
)

// callerEnv, when set, makes this test binary a program run in a test
// terminal: it asks the daemon API socket at that path who it is and for
// a root-only action, prints both answers and exits.
const callerEnv = "AOTEST_CALLER"

func callerHelper() int {
	sock := os.Getenv(callerEnv)
	c := &http.Client{Timeout: waitFor, Transport: &http.Transport{DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	who, whoStatus := ask(c, "/v1/test-caller")
	if whoStatus != http.StatusOK {
		who = "-"
	}
	_, rootStatus := ask(c, "/v1/test-root-only")
	fmt.Printf("caller=%d:%s root-only=%d\n", whoStatus, who, rootStatus)
	return 0
}

// ask GETs path and returns the body on one line and the status, or an
// error word and status 0.
func ask(c *http.Client, path string) (string, int) {
	resp, err := c.Get("http://agentsoned" + path)
	if err != nil {
		return "error:" + strings.ReplaceAll(err.Error(), " ", "_"), 0
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "error:read", 0
	}
	return strings.TrimSpace(string(body)), resp.StatusCode
}

// callerModule answers with the caller the API layer set, and serves one
// action only root callers may take.
type callerModule struct{ core.Base }

func (callerModule) Name() string { return "caller" }

func (callerModule) API() core.API {
	return core.API{Routes: []core.Route{
		{Pattern: "GET /v1/test-caller", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := core.CallerFrom(r.Context())
			body := map[string]any{"kind": c.Kind, "name": c.Name, "uid": c.UID, "root": c.IsRoot, "assurance": c.Assurance}
			if err := json.NewEncoder(w).Encode(body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})},
		{Pattern: "GET /v1/test-root-only", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, _ := core.CallerFrom(r.Context()); !c.IsRoot {
				http.Error(w, "root only", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})},
	}}
}

// The test human. The fake human directory knows only this name.
const testHuman = "aotest-ada"

// callerSocket serves callerModule on the real daemon API socket, with
// testHuman in the human directory.
func callerSocket(t *testing.T) string {
	t.Helper()
	return apiSocketWith(t, callerModule{}, func(f *coretest.Fakes) { f.Humans.Add(testHuman, 1000) })
}

// shellReport is what a test terminal's script prints: the shell's own
// cgroup, uid and pids.max, then the socket's answers.
const shellReport = `cg=$(sed -n 's/^0:://p' /proc/self/cgroup); ` +
	`echo "cg=$cg uid=$(id -u) pids=$(cat "/sys/fs/cgroup$cg/pids.max" 2>/dev/null || echo none)"; ` +
	`AOTEST_CALLER=%s %s; echo end.`

var (
	reportLine = regexp.MustCompile(`cg=(\S*) uid=(\d+) pids=(\S+)`)
	callerLine = regexp.MustCompile(`caller=(\d+):(\S+) root-only=(\d+)`)
)

// report is a test terminal's report.
type report struct {
	cgroup, pids   string
	uid            int
	callerStatus   int
	caller         callerJSON
	rootOnlyStatus int
}

type callerJSON struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	UID       int    `json:"uid"`
	Root      bool   `json:"root"`
	Assurance string `json:"assurance"`
}

// terminal runs script in a real terminal as uid and gid in cell, the way
// the machine module opens one, and returns all it printed up to "end.".
func terminal(t *testing.T, script string, uid, gid int, home string, cell core.ProcessCell) string {
	t.Helper()
	spec := machine.PtySpec{Command: []string{"/bin/sh", "-c", script}, Cols: 400, Rows: 24}
	p, err := system.PtyStarter{}.Start(t.Context(), spec, uid, gid, home, cell)
	if err != nil {
		t.Fatalf("start terminal in %+v: %v", cell, err)
	}
	var out bytes.Buffer
	chunk := make([]byte, 512)
	for !strings.Contains(out.String(), "end.") {
		n, err := p.Read(chunk)
		out.Write(chunk[:n])
		if err != nil {
			t.Fatalf("terminal printed %q, then: %v", out.String(), err)
		}
	}
	if code := p.Wait(); code != 0 {
		t.Errorf("terminal exited %d, printed %q", code, out.String())
	}
	coretest.Close(t, p)
	return out.String()
}

// reportOf runs the shell report in a terminal and parses it.
func reportOf(t *testing.T, sock string, uid, gid int, home string, cell core.ProcessCell) report {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, fmt.Sprintf(shellReport, sock, self), uid, gid, home, cell)
	t.Logf("proof terminal: cell %+v as uid %d printed %q", cell, uid, out)
	m := reportLine.FindStringSubmatch(out)
	c := callerLine.FindStringSubmatch(out)
	if m == nil || c == nil {
		t.Fatalf("terminal printed %q, want a cgroup line and a caller line", out)
	}
	r := report{cgroup: m[1], pids: m[3]}
	r.uid, _ = strconv.Atoi(m[2])
	r.callerStatus, _ = strconv.Atoi(c[1])
	r.rootOnlyStatus, _ = strconv.Atoi(c[3])
	if r.callerStatus == http.StatusOK {
		if err := json.Unmarshal([]byte(c[2]), &r.caller); err != nil {
			t.Fatalf("caller answer %q: %v", c[2], err)
		}
	}
	return r
}

// removeCgroup removes a test cgroup once its processes are gone.
func removeCgroup(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove %s: %v", dir, err)
		}
	})
}

func gidOf(t *testing.T, name string) int {
	t.Helper()
	u, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Fatal(err)
	}
	return gid
}

// A terminal for a custom agent runs as the agent, inside the agent's own
// cgroup under the agent's pids.max, even while its runner is asleep, and
// the daemon socket names it as that agent.
func TestTerminalCell_AgentTerminalRunsInTheAgentsCgroupUnderItsLimits(t *testing.T) {
	root := requireCgroups(t)
	acc := agentUser(t, "aotest-tess", 200111)
	sock := callerSocket(t)
	l := &process.Launcher{CgroupRoot: root}
	spec := domain.UnitSpec{Agent: acc.User, Account: acc, Limits: domain.Limits{TasksMax: 48},
		Exec: domain.Exec{Path: "/bin/sleep", Args: []string{"600"}}}
	if err := l.Install(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, acc.User)
	removeCgroup(t, dir)

	cell, err := l.AgentCell(t.Context(), acc.User)
	if err != nil {
		t.Fatal(err)
	}
	if cell.Cgroup != dir || cell.NoFile != process.DefaultNoFile {
		t.Fatalf("cell %+v, want cgroup %s and the runner's open-file limit", cell, dir)
	}
	r := reportOf(t, sock, 200111, gidOf(t, acc.User), acc.Home, cell)

	if want := "/" + process.AgentsCgroup + "/" + acc.User; r.cgroup != want {
		t.Errorf("shell cgroup %q, want %q (never /daemon)", r.cgroup, want)
	}
	if r.uid != 200111 || r.pids != "48" {
		t.Errorf("shell uid %d pids.max %s, want uid 200111 under the agent's pids.max 48", r.uid, r.pids)
	}
	want := callerJSON{Kind: "agent", Name: acc.User, UID: 200111}
	if r.callerStatus != http.StatusOK || r.caller != want {
		t.Errorf("socket answered %d %+v, want 200 %+v", r.callerStatus, r.caller, want)
	}
	requireRootOwned(t, dir)
}

// Without writable cgroups, a terminal for a custom agent gets the same
// process limits its runner gets: processes and open files. Its memory is
// not capped, as the runner's is not.
func TestTerminalCell_AgentTerminalGetsTheRunnersLimitsWithoutCgroups(t *testing.T) {
	acc := agentUser(t, "aotest-sol", 200112)
	l := &process.Launcher{}
	spec := domain.UnitSpec{Agent: acc.User, Account: acc, Limits: domain.Limits{TasksMax: 48, MemoryMaxBytes: 1 << 30},
		Exec: domain.Exec{Path: "/bin/sleep", Args: []string{"600"}}}
	if err := l.Install(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	cell, err := l.AgentCell(t.Context(), acc.User)
	if err != nil {
		t.Fatal(err)
	}
	want := core.ProcessCell{NoFile: process.DefaultNoFile, NProc: 48}
	if cell != want {
		t.Fatalf("cell %+v, want %+v", cell, want)
	}
	out := terminal(t, `grep -E '^Max (processes|open files|data size|address space)' /proc/self/limits | tr -s ' '; echo end.`,
		200112, gidOf(t, acc.User), acc.Home, cell)
	t.Logf("proof soft limits: %q", out)
	for _, line := range []string{"Max processes 48 48", "Max open files 4096 4096", "Max data size unlimited unlimited",
		"Max address space unlimited unlimited"} {
		if !strings.Contains(out, line) {
			t.Errorf("terminal limits %q, want %q", out, line)
		}
	}
}

// A human's web terminal is root, in the root-owned cgroup /humans/<name>,
// never /daemon. On the socket it is that human with human rights only:
// never root, soft, and a root-only action is refused.
func TestTerminalCell_HumanTerminalIsRootInTheHumansCgroupAndTheSocketNamesTheHuman(t *testing.T) {
	root := requireCgroups(t)
	requireRoot(t)
	sock := callerSocket(t)
	l := &process.Launcher{CgroupRoot: root}
	humans := filepath.Join(filepath.Dir(root), process.HumansCgroup)
	dir := filepath.Join(humans, testHuman)
	removeCgroup(t, dir)

	cell, err := l.HumanCell(t.Context(), testHuman)
	if err != nil {
		t.Fatal(err)
	}
	if cell != (core.ProcessCell{Cgroup: dir}) {
		t.Fatalf("cell %+v, want cgroup %s only", cell, dir)
	}
	r := reportOf(t, sock, 0, 0, "/root", cell)

	if want := "/" + process.HumansCgroup + "/" + testHuman; r.cgroup != want || r.uid != 0 {
		t.Errorf("shell in %q as uid %d, want root in %q (never /daemon)", r.cgroup, r.uid, want)
	}
	want := callerJSON{Kind: "human", Name: testHuman, UID: 0, Root: false, Assurance: string(core.AssuranceSoft)}
	if r.callerStatus != http.StatusOK || r.caller != want {
		t.Errorf("socket answered %d %+v, want 200 %+v", r.callerStatus, r.caller, want)
	}
	if r.rootOnlyStatus != http.StatusForbidden {
		t.Errorf("root-only action answered %d, want 403: a human terminal is never root on the socket", r.rootOnlyStatus)
	}
	requireRootOwned(t, humans)
	requireRootOwned(t, dir)
}

// A root terminal in a human cgroup whose name no human has is refused.
func TestTerminalCell_UnknownHumanCgroupIsRefused(t *testing.T) {
	root := requireCgroups(t)
	requireRoot(t)
	sock := callerSocket(t)
	l := &process.Launcher{CgroupRoot: root}
	const ghost = "aotest-ghost"
	removeCgroup(t, filepath.Join(filepath.Dir(root), process.HumansCgroup, ghost))
	cell, err := l.HumanCell(t.Context(), ghost)
	if err != nil {
		t.Fatal(err)
	}
	r := reportOf(t, sock, 0, 0, "/root", cell)
	if r.callerStatus != http.StatusForbidden || r.rootOnlyStatus != http.StatusForbidden {
		t.Errorf("socket answered %d and %d, want 403 for both", r.callerStatus, r.rootOnlyStatus)
	}
}

// Without writable cgroups a human terminal has no cell: it is a root
// process outside every agent and human cgroup, and the socket refuses it.
func TestTerminalCell_RootTerminalOutsideAgentAndHumanCgroupsIsRefused(t *testing.T) {
	requireRoot(t)
	sock := callerSocket(t)
	cell, err := (&process.Launcher{}).HumanCell(t.Context(), testHuman)
	if err != nil {
		t.Fatal(err)
	}
	if cell.Placed() {
		t.Fatalf("cell %+v without a cgroup root, want none", cell)
	}
	r := reportOf(t, sock, 0, 0, "/root", cell)
	if strings.HasPrefix(r.cgroup, "/"+process.AgentsCgroup+"/") || strings.HasPrefix(r.cgroup, "/"+process.HumansCgroup+"/") {
		t.Fatalf("shell in %q, want outside every agent and human cgroup", r.cgroup)
	}
	if r.callerStatus != http.StatusForbidden || r.rootOnlyStatus != http.StatusForbidden {
		t.Errorf("socket answered %d and %d, want 403 for both", r.callerStatus, r.rootOnlyStatus)
	}
}
