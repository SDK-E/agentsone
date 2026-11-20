// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/machineapi"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/machine/adapters/http"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

type apt struct{ installed []string }

func (a *apt) Installed(context.Context) ([]domain.Package, error) {
	var out []domain.Package
	for _, n := range a.installed {
		out = append(out, domain.Package{Name: n, Version: "1.0", Architecture: "arm64", Summary: n})
	}
	return out, nil
}
func (a *apt) Manual(context.Context) ([]string, error)      { return a.installed, nil }
func (a *apt) Install(context.Context, string, string) error { return nil }
func (a *apt) Remove(context.Context, string) error          { return nil }

type files struct {
	mu   sync.Mutex
	data map[string]string
}

func (f *files) List(_ context.Context, a domain.Area) ([]domain.FileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.FileEntry
	for p, d := range f.data {
		if path.Dir(p) == a.Path {
			out = append(out, entry(p, d))
		}
	}
	return out, nil
}

func entry(p, d string) domain.FileEntry {
	return domain.FileEntry{Path: p, Name: path.Base(p), Type: domain.FileRegular, Size: int64(len(d)), Mode: 0o644, Owner: "root", Group: "root", Modified: coretest.Epoch}
}

func (f *files) Read(_ context.Context, a domain.Area, limit int64) ([]byte, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[a.Path]
	if !ok {
		return nil, 0, fmt.Errorf("%s: %w", a.Path, core.ErrNotFound)
	}
	return []byte(d)[:min(int64(len(d)), limit)], int64(len(d)), nil
}

func (f *files) Write(_ context.Context, a domain.Area, w app.FileWrite) (domain.FileEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[a.Path] = string(w.Data)
	return entry(a.Path, string(w.Data)), nil
}

func (f *files) Delete(_ context.Context, a domain.Area, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, a.Path)
	return nil
}

type procs struct{}

func (procs) List(context.Context) ([]domain.Process, error) {
	return []domain.Process{{PID: 7, PPID: 1, UID: 200001, User: "writer", Command: "sleep 9", Started: coretest.Epoch, Agent: "writer"}}, nil
}
func (p procs) Get(_ context.Context, pid int) (domain.Process, error) {
	if pid != 7 {
		return domain.Process{}, core.ErrNotFound
	}
	all, _ := p.List(context.Background())
	return all[0], nil
}
func (procs) Signal(context.Context, int, domain.Signal) error { return nil }

type users struct{}

func (users) List(context.Context) ([]domain.MachineUser, error) {
	return []domain.MachineUser{{Name: "root", Home: "/root", Shell: "/bin/bash", Kind: domain.UserSystem}}, nil
}

type metrics struct{}

func (metrics) Read(context.Context) (domain.Metrics, error) {
	return domain.Metrics{Hostname: "machine", Cores: 2, CPUPercent: 12.5, Load: [3]float64{0.1, 0.2, 0.3},
		Disks: []domain.Disk{{Mount: "/", Total: 100, Used: 40}}, Uptime: 90 * time.Second}, nil
}

type recipes struct {
	mu sync.Mutex
	r  *domain.Recipe
}

func (s *recipes) Load(context.Context) (domain.Recipe, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.r == nil {
		return domain.Recipe{}, false, nil
	}
	return *s.r, true, nil
}
func (s *recipes) Save(_ context.Context, r domain.Recipe) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.r = &r
	return nil
}
func (s *recipes) PutBlob(context.Context, []byte) (string, error) { return "h", nil }
func (s *recipes) Blob(context.Context, string) ([]byte, error)    { return nil, core.ErrNotFound }

type baseline struct{}

func (baseline) Baseline(context.Context) ([]string, bool, error) { return nil, false, nil }

type power struct{}

func (power) Exit(int)                        {}
func (power) AfterFunc(time.Duration, func()) {}

var (
	asHuman  = core.Caller{Kind: core.CallerHuman, Name: "alice", UID: -1}
	asCustom = core.Caller{Kind: core.CallerAgent, Name: "writer", UID: 200001}
	asHost   = core.Caller{Kind: core.CallerHost, Name: "host", UID: -1}
)

func serve(t *testing.T) (http.Handler, *app.Machine) {
	t.Helper()
	return serveWith(t, domain.AgentLimits{})
}

// serveWith serves the machine group with limits as the start probe's answer.
func serveWith(t *testing.T, limits domain.AgentLimits) (http.Handler, *app.Machine) {
	t.Helper()
	m := app.New(app.Deps{
		Packages: &apt{installed: []string{"bash", "jq"}}, Files: &files{data: map[string]string{"/srv/a.txt": "hello", "/srv/bin": "\xff\xfe"}},
		Processes: procs{}, Users: users{}, Metrics: metrics{}, Recipes: &recipes{}, Baseline: baseline{},
		Power: power{}, Timer: power{}, Audit: coretest.NewAudit(), Clock: coretest.NewClock(coretest.Epoch), IDs: coretest.NewIDs(),
	})
	mux := http.NewServeMux()
	h := httpadapter.New(m, func(name string) string { return "/home/agents/" + name }).WithAgentLimits(limits)
	machineapi.Group(h).Register(mux, core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "validation", err.Error()))
		},
		ResponseError: core.WriteError,
	})
	return mux, m
}

func call(t *testing.T, h http.Handler, c *core.Caller, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c != nil {
		req = req.WithContext(core.WithCaller(req.Context(), *c))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func want(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAnonymousCallerIsUnauthorized(t *testing.T) {
	h, _ := serve(t)
	want(t, call(t, h, nil, http.MethodGet, "/v1/machine/metrics", ""), http.StatusUnauthorized)
}

func TestMetrics(t *testing.T) {
	h, _ := serve(t)
	rec := call(t, h, &asHuman, http.MethodGet, "/v1/machine/metrics", "")
	want(t, rec, http.StatusOK)
	m := decode[map[string]any](t, rec)
	if m["hostname"] != "machine" || m["uptime_seconds"] != 90.0 {
		t.Fatalf("metrics %v", m)
	}
	// A daemon that was not told how limits hold leaves the field out.
	if _, ok := m["agent_limits"]; ok {
		t.Fatalf("agent_limits %v, want it left out", m["agent_limits"])
	}
}

func TestMetricsSayHowAgentLimitsHold(t *testing.T) {
	type hold struct {
		Mode   string  `json:"mode"`
		Reason *string `json:"reason"`
	}
	metricsOf := func(t *testing.T, l domain.AgentLimits) hold {
		t.Helper()
		h, _ := serveWith(t, l)
		rec := call(t, h, &asHuman, http.MethodGet, "/v1/machine/metrics", "")
		want(t, rec, http.StatusOK)
		return decode[struct {
			AgentLimits hold `json:"agent_limits"`
		}](t, rec).AgentLimits
	}
	t.Run("hard has no reason", func(t *testing.T) {
		// Even a reason given by mistake stays out.
		got := metricsOf(t, domain.AgentLimits{Mode: domain.LimitsHard, Reason: "cgroup v2, writable"})
		if got.Mode != "hard" || got.Reason != nil {
			t.Fatalf("agent_limits %+v, want hard without a reason", got)
		}
	})
	t.Run("soft says why", func(t *testing.T) {
		got := metricsOf(t, domain.SoftLimits("The cgroup tree is read-only."))
		if got.Mode != "soft" || got.Reason == nil || *got.Reason != "The cgroup tree is read-only." {
			t.Fatalf("agent_limits %+v, want soft with the reason", got)
		}
	})
	t.Run("soft reason is 1 to 500 characters", func(t *testing.T) {
		for _, reason := range []string{"", strings.Repeat("word ", 300)} {
			got := metricsOf(t, domain.AgentLimits{Mode: domain.LimitsSoft, Reason: reason})
			if got.Reason == nil {
				t.Fatal("soft limits without a reason")
			}
			if n := len([]rune(*got.Reason)); n < 1 || n > 500 {
				t.Fatalf("reason of %d characters, want 1 to 500", n)
			}
		}
	})
}

func TestProcessesAndSignal(t *testing.T) {
	h, _ := serve(t)
	want(t, call(t, h, &asCustom, http.MethodGet, "/v1/machine/processes?agent=writer&limit=10", ""), http.StatusOK)
	want(t, call(t, h, &asCustom, http.MethodPost, "/v1/machine/processes/7/signal", `{"signal":"TERM"}`), http.StatusNoContent)
	want(t, call(t, h, &asCustom, http.MethodPost, "/v1/machine/processes/8/signal", `{"signal":"TERM"}`), http.StatusNotFound)
	want(t, call(t, h, &asCustom, http.MethodGet, "/v1/machine/processes?cursor=zz", ""), http.StatusBadRequest)
}

func TestServices(t *testing.T) {
	h, _ := serve(t)
	rec := call(t, h, &asHuman, http.MethodGet, "/v1/machine/services?state=inactive", "")
	want(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), domain.RecipeUnit) {
		t.Fatalf("recipe unit missing: %s", rec.Body)
	}
	want(t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/services/"+domain.RecipeUnit+"/restart", ""), http.StatusOK)
	want(t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/services/"+domain.RecipeUnit+"/start", ""), http.StatusOK)
	want(t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/services/"+app.DaemonUnit+"/stop", ""), http.StatusForbidden)
}

func TestPackages(t *testing.T) {
	h, _ := serve(t)
	rec := call(t, h, &asHuman, http.MethodGet, "/v1/machine/packages?q=jq", "")
	want(t, rec, http.StatusOK)
	if items := decode[map[string][]any](t, rec)["items"]; len(items) != 1 {
		t.Fatalf("items %v", items)
	}
	want(t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/packages", `{"name":"ripgrep","version":"14.1"}`), http.StatusAccepted)
	want(t, call(t, h, &asCustom, http.MethodPost, "/v1/machine/packages", `{"name":"ripgrep"}`), http.StatusForbidden)
	want(t, call(t, h, &asHost, http.MethodDelete, "/v1/machine/packages/jq", ""), http.StatusAccepted)
}

func TestUsers(t *testing.T) {
	h, _ := serve(t)
	want(t, call(t, h, &asHuman, http.MethodGet, "/v1/machine/users", ""), http.StatusOK)
}

func TestFiles(t *testing.T) {
	h, _ := serve(t)
	rec := call(t, h, &asHuman, http.MethodGet, "/v1/machine/files?path=/srv", "")
	want(t, rec, http.StatusOK)
	rec = call(t, h, &asHuman, http.MethodGet, "/v1/machine/files/content?path=/srv/a.txt", "")
	want(t, rec, http.StatusOK)
	if c := decode[map[string]any](t, rec); c["encoding"] != "utf-8" || c["content"] != "hello" {
		t.Fatalf("text %v", c)
	}
	rec = call(t, h, &asHuman, http.MethodGet, "/v1/machine/files/content?path=/srv/bin", "")
	if c := decode[map[string]any](t, rec); c["encoding"] != "base64" || c["content"] != "//4=" {
		t.Fatalf("binary %v", c)
	}
	want(t, call(t, h, &asCustom, http.MethodGet, "/v1/machine/files/content?path=/srv/a.txt", ""), http.StatusForbidden)
	want(t, call(t, h, &asHuman, http.MethodPut, "/v1/machine/files/content", `{"path":"/srv/b.txt","encoding":"utf-8","content":"hi","mode":"0600"}`), http.StatusOK)
	want(t, call(t, h, &asHuman, http.MethodPut, "/v1/machine/files/content", `{"path":"/srv/c","encoding":"base64","content":"//4="}`), http.StatusOK)
	want(t, call(t, h, &asHuman, http.MethodPut, "/v1/machine/files/content", `{"path":"/srv/c","encoding":"base64","content":"!!"}`), http.StatusBadRequest)
	want(t, call(t, h, &asHuman, http.MethodPut, "/v1/machine/files/content", `{"path":"/srv/c","encoding":"utf-8","content":"x","mode":"9999"}`), http.StatusBadRequest)
	big := strings.Repeat("a", domain.MaxFileBytes+1)
	want(t, call(t, h, &asHuman, http.MethodPut, "/v1/machine/files/content", `{"path":"/srv/big","encoding":"utf-8","content":"`+big+`"}`), http.StatusRequestEntityTooLarge)
	want(t, call(t, h, &asHuman, http.MethodDelete, "/v1/machine/files/content?path=/srv/b.txt&recursive=false", ""), http.StatusNoContent)
}

func TestReboot(t *testing.T) {
	h, _ := serve(t)
	rec := call(t, h, &asCustom, http.MethodPost, "/v1/machine/power/reboot", `{"reason":"new kernel settings","delay_seconds":0}`)
	want(t, rec, http.StatusAccepted)
	if a := decode[map[string]any](t, rec); a["requested_by"] != "agent:writer" || a["action"] != "reboot" {
		t.Fatalf("action %v", a)
	}
	want(t, call(t, h, &asHuman, http.MethodPost, "/v1/machine/power/reboot", `{"reason":""}`), http.StatusBadRequest)
}
