// SPDX-License-Identifier: MIT

package gatewaymint_test

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

func depsWith(t *testing.T, toml string) (core.Deps, *bytes.Buffer) {
	t.Helper()
	deps, f := coretest.NewDeps(t)
	f.Agents.Add(core.Agent{Name: "founder", Kind: core.AgentAlwaysOn, IsRoot: true})
	cfg, err := core.ParseConfig([]byte(toml))
	if err != nil {
		t.Fatal(err)
	}
	deps.Config = &cfg
	var logs bytes.Buffer
	deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	return deps, &logs
}

// The [gateway] key_file setting picks the key; the port mints with it.
func TestModule_ProvidesMinterUsingTheConfiguredKeyFile(t *testing.T) {
	t.Parallel()
	path := keyFile(t, testKey)
	deps, logs := depsWith(t, "[gateway]\nkey_file = \""+path+"\"\n")
	m := gatewaymint.New(deps)
	if m.Name() != "gateway" {
		t.Errorf("Name() = %q", m.Name())
	}
	m.Provide(deps.Ports)
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Errorf("Start logged %q with a good key", logs.String())
	}
	tok, err := deps.Ports.GatewayTokens.Mint(t.Context(), core.GatewayGrant{Subject: "founder", ChartPath: "founder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gatewaytoken.Verify(testKey, tok, deps.Clock.Now()); err != nil {
		t.Fatalf("token from the configured key refused: %v", err)
	}
}

// Without the key the daemon still starts, says why once, and every mint
// says the model gateway is not reachable.
func TestModule_StartsWithoutKeyAndSaysSo(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "absent")
	deps, logs := depsWith(t, "[gateway]\nkey_file = \""+path+"\"\n")
	m := gatewaymint.New(deps)
	m.Provide(deps.Ports)
	if err := m.Start(t.Context()); err != nil {
		t.Fatalf("Start without key error = %v, want a running daemon", err)
	}
	if !strings.Contains(logs.String(), "model gateway not reachable") {
		t.Errorf("log = %q", logs.String())
	}
	if _, err := deps.Ports.GatewayTokens.Mint(t.Context(), core.GatewayGrant{Subject: "founder", ChartPath: "founder"}); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Mint error = %v, want ErrUnavailable", err)
	}
}

func TestModule_DefaultsToTheEdgeVolumePath(t *testing.T) {
	t.Parallel()
	deps, _ := depsWith(t, "")
	m := gatewaymint.New(deps)
	m.Provide(deps.Ports)
	_, err := deps.Ports.GatewayTokens.Mint(t.Context(), core.GatewayGrant{Subject: "founder", ChartPath: "founder"})
	if !strings.Contains(err.Error(), gatewaymint.DefaultKeyFile) {
		t.Fatalf("Mint error = %v, want it to name %s", err, gatewaymint.DefaultKeyFile)
	}
}

func TestModule_FailsStartOnBadSettings(t *testing.T) {
	t.Parallel()
	for name, toml := range map[string]string{
		"unknown key": "[gateway]\nkeyfile = \"/x\"\n",
		"empty path":  "[gateway]\nkey_file = \"\"\n",
	} {
		deps, _ := depsWith(t, toml)
		if err := gatewaymint.New(deps).Start(t.Context()); err == nil || !strings.Contains(err.Error(), "gateway settings") {
			t.Errorf("%s: Start error = %v", name, err)
		}
	}
}
