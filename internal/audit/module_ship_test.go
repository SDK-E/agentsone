// SPDX-License-Identifier: MIT

package audit_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func withConfig(t *testing.T, toml string) core.Deps {
	t.Helper()
	deps, _ := coretest.NewDeps(t)
	cfg, err := core.ParseConfig([]byte(toml))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	deps.Config = &cfg
	return deps
}

func TestModule_ShipsRecordsToTheEdgeSinkWhenSet(t *testing.T) {
	got := make(chan string, 1)
	var once sync.Once
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"seq":0,"hash":"0"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		once.Do(func() { got <- string(body) })
		_, _ = w.Write([]byte(`{"head":{"seq":1}}`))
	}))
	t.Cleanup(edge.Close)
	m := audit.New(withConfig(t, "[audit]\nedge_sink = \""+edge.URL+"\"\n"))
	ports := core.NewPorts()
	m.Provide(ports)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ports.Audit.Append(ctx, core.AuditEntry{Actor: "system:agentsoned", Action: "machine.boot", Target: "machine"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	select {
	case body := <-got:
		if !strings.Contains(body, `"action":"machine.boot"`) {
			t.Fatalf("edge got %s", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing shipped to the edge")
	}
}

func TestModule_RefusesABadEdgeSink(t *testing.T) {
	t.Parallel()
	for _, toml := range []string{"[audit]\nedge_sink = \"gopher://x\"\n", "[audit]\nbogus = 1\n"} {
		m := audit.New(withConfig(t, toml))
		if err := m.Start(context.Background()); err == nil {
			t.Errorf("Start with %q succeeded", toml)
		}
	}
}

func TestModule_ShipsNothingByDefault(t *testing.T) {
	t.Parallel()
	deps, _ := coretest.NewDeps(t)
	m := audit.New(deps)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
