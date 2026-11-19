// SPDX-License-Identifier: MIT

package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/edge/audit"
	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/tokenkey"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

// testKey stands for the gateway key, which the edge loads before any part starts.
var testKey = bytes.Repeat([]byte{0x5a}, tokenkey.Size)

func newEnv(t *testing.T, dir string) *component.Env {
	t.Helper()
	return &component.Env{
		InnerAddr: netip.MustParseAddr("127.0.0.1"), DataDir: dir,
		Log: slog.New(slog.DiscardHandler), Admin: http.NewServeMux(), TokenKey: testKey,
	}
}

// signed signs c with key for five minutes from now.
func signed(t *testing.T, key []byte, c gatewaytoken.Claims) string {
	t.Helper()
	now := time.Now()
	c.IssuedAt, c.ExpiresAt = now.Unix(), now.Add(5*time.Minute).Unix()
	tok, err := gatewaytoken.Sign(key, c)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// daemonToken is the token the daemon's shipper sends.
func daemonToken(t *testing.T) string {
	t.Helper()
	return signed(t, testKey, gatewaytoken.Claims{
		Agent: "system:audit", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceAudit,
	})
}

func start(t *testing.T, env *component.Env) *audit.Sink {
	t.Helper()
	s, err := audit.Component{Port: -1}.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sink, ok := s.(*audit.Sink)
	if !ok {
		t.Fatalf("Start returned %T", s)
	}
	return sink
}

func record(t *testing.T) []byte {
	t.Helper()
	raw := `{"seq":1,"time":"2026-11-19T21:00:00Z","actor":{"kind":"agent","name":"founder"},` +
		`"action":"agent.hire","target":{"kind":"agent","id":"scout"},"outcome":"success","details":{},"origin":"inside"}`
	sealed, err := auditchain.Seal([]byte(raw), auditchain.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"records": []json.RawMessage{sealed}})
	return body
}

func TestComponent_TakesMachineRecordsOnTheInnerAddressAndShowsThemOnAdmin(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir())
	sink := start(t, env)
	t.Cleanup(func() { _ = sink.Stop() })
	if !strings.HasPrefix(sink.Addr().String(), "127.0.0.1:") {
		t.Fatalf("Addr = %v, want the inner address", sink.Addr())
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		fmt.Sprintf("http://%s/v1/audit/records", sink.Addr()), bytes.NewReader(record(t)))
	req.Header.Set("Authorization", "Bearer "+daemonToken(t))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST = %d", res.StatusCode)
	}

	rec := httptest.NewRecorder()
	env.Admin.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/audit/inside", nil))
	if !strings.Contains(rec.Body.String(), `"id":"scout"`) {
		t.Fatalf("admin inside = %s", rec.Body)
	}
}

func TestComponent_SharesTheProofRecorder(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir())
	sink := start(t, env)
	t.Cleanup(func() { _ = sink.Stop() })
	if env.Proof == nil {
		t.Fatal("env.Proof not set")
	}
	err := env.Proof.RecordProof(context.Background(), component.Proof{
		Action: "net.deny", Target: "network:example.org", Outcome: "denied", Details: map[string]any{"port": 443},
	})
	if err != nil {
		t.Fatalf("RecordProof: %v", err)
	}
	rec := httptest.NewRecorder()
	env.Admin.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/audit/proof", nil))
	if !strings.Contains(rec.Body.String(), `"action":"net.deny"`) {
		t.Fatalf("admin proof = %s", rec.Body)
	}
}

func TestComponent_KeepsTheCopyAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := newEnv(t, dir)
	s1 := start(t, first)
	if err := first.Proof.RecordProof(t.Context(), component.Proof{Action: "freeze.on", Target: "machine:all", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	second := newEnv(t, dir)
	s2 := start(t, second)
	t.Cleanup(func() { _ = s2.Stop() })
	rec := httptest.NewRecorder()
	second.Admin.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/audit/proof", nil))
	if !strings.Contains(rec.Body.String(), "freeze.on") {
		t.Fatalf("proof after restart = %s", rec.Body)
	}
}

func TestComponent_FailsWithoutADataFolder(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir()+"/missing/deeper")
	if _, err := (audit.Component{}).Start(t.Context(), env); err == nil {
		t.Fatal("Start without a data folder succeeded")
	}
}

// Without the gateway key no token could be checked; the sink must not open at all.
func TestComponent_FailsWithoutTheGatewayKey(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir())
	env.TokenKey = nil
	if s, err := (audit.Component{Port: -1}).Start(t.Context(), env); err == nil {
		_ = s.Stop()
		t.Fatal("Start without the gateway key succeeded")
	}
	if env.Proof != nil {
		t.Error("env.Proof shared by a sink that did not start")
	}
}

func TestComponent_FailsWhenThePortIsTaken(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir())
	first := start(t, env)
	t.Cleanup(func() { _ = first.Stop() })
	port := netip.MustParseAddrPort(first.Addr().String()).Port()
	if _, err := (audit.Component{Port: int(port)}).Start(t.Context(), newEnv(t, t.TempDir())); err == nil {
		t.Fatal("Start on a taken port succeeded")
	}
}
