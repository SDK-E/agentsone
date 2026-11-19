// SPDX-License-Identifier: MIT

package core_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

func TestConfigPaths(t *testing.T) {
	c := core.DefaultConfig()
	if got := c.PluginsDir(); got != "/var/lib/agentsone/plugins" {
		t.Errorf("PluginsDir %s", got)
	}
	if got := c.AgentHome("bob"); got != "/home/agents/bob" {
		t.Errorf("AgentHome %s", got)
	}
	c.API.Socket = "/tmp/a1.sock"
	if got := c.SocketPath(); got != "/tmp/a1.sock" {
		t.Errorf("SocketPath override %s", got)
	}
}

func TestDurationText(t *testing.T) {
	b, err := core.Duration{Duration: 90 * time.Second}.MarshalText()
	if err != nil || string(b) != "1m30s" {
		t.Fatalf("MarshalText %q %v", b, err)
	}
	var d core.Duration
	if err := d.UnmarshalText([]byte("soon")); err == nil {
		t.Fatal("bad duration accepted")
	}
}

func TestLoadConfigRejectsBadDuration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[budget]\ncheck_interval = \"soon\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadConfig(p); err == nil {
		t.Fatal("bad duration accepted")
	}
}

func TestLoadConfigReportsUnreadableFile(t *testing.T) {
	if _, err := core.LoadConfig(t.TempDir()); err == nil {
		t.Fatal("reading a folder as config succeeded")
	}
}

func TestBaseIsANoOpModule(t *testing.T) {
	var b core.Base
	ctx := context.Background()
	b.Provide(core.NewPorts())
	if err := errors.Join(b.Migrate(ctx), b.Start(ctx), b.Stop(ctx)); err != nil {
		t.Fatal(err)
	}
	if api := b.API(); len(api.Groups) != 0 || len(api.Routes) != 0 {
		t.Fatalf("Base serves %+v", api)
	}
}

func TestProblemError(t *testing.T) {
	if got := (&core.Problem{Title: "Not Found"}).Error(); got != "Not Found" {
		t.Errorf("without detail %q", got)
	}
	if got := (&core.Problem{Title: "Not Found", Detail: "agent bob"}).Error(); got != "Not Found: agent bob" {
		t.Errorf("with detail %q", got)
	}
}

func TestWriteErrorMapsSentinel(t *testing.T) {
	rec := httptest.NewRecorder()
	core.WriteError(rec, httptest.NewRequest(http.MethodGet, "/v1/agents/bob", nil), core.ErrNotFound)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code %d", rec.Code)
	}
}
