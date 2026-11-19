// SPDX-License-Identifier: MIT

package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/adapters/edgeship"
	auditdomain "github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

// sealedChain returns machine records hiring each of names in turn, chained from the start.
func sealedChain(t *testing.T, names ...string) []json.RawMessage {
	t.Helper()
	prev := auditchain.Genesis
	var out []json.RawMessage
	for i, name := range names {
		raw := fmt.Sprintf(`{"seq":%d,"time":"2026-11-19T21:00:00Z","actor":{"kind":"agent","name":"founder"},`+
			`"action":"agent.hire","target":{"kind":"agent","id":%q},"outcome":"success","details":{},"origin":"inside"}`, i+1, name)
		sealed, err := auditchain.Seal([]byte(raw), prev)
		if err != nil {
			t.Fatal(err)
		}
		if prev, err = auditchain.Hash(sealed); err != nil {
			t.Fatal(err)
		}
		out = append(out, sealed)
	}
	return out
}

// daemonLog is the daemon's audit.db as the shipper reads it: records numbered from 1.
type daemonLog []json.RawMessage

func (l daemonLog) List(_ context.Context, after int64, limit int) ([]auditdomain.Record, error) {
	var out []auditdomain.Record
	for seq := after + 1; seq <= int64(len(l)) && len(out) < limit; seq++ {
		out = append(out, auditdomain.Record{Seq: seq})
	}
	return out, nil
}

func (l daemonLog) encode(r auditdomain.Record) ([]byte, error) { return l[r.Seq-1], nil }

// postAs posts one record to the sink at base the way any process in the machine could.
func postAs(t *testing.T, base, auth string, rec json.RawMessage) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{"records": []json.RawMessage{rec}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/v1/audit/records", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// A custom agent reaches the sink on the inner address like the daemon does. It cannot read the
// gateway key (root-only inside the machine), so it holds no audit token: only its own token for
// model calls. Were a forged record that continues the chain taken, the daemon's real record with
// the same number would be a fork, the edge would refuse it, and shipping would stop until a
// person looked. The sink refuses the forgery, so the daemon's records still go through.
func TestShipping_ACustomAgentCannotPauseItWithAForgedRecord(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir())
	sink := start(t, env)
	t.Cleanup(func() { _ = sink.Stop() })
	base := "http://" + sink.Addr().String()

	forged := sealedChain(t, "intruder")[0]
	own := signed(t, testKey, gatewaytoken.Claims{
		Agent: "dev", Kind: gatewaytoken.KindChat, Chart: "founder/dev", Audience: gatewaytoken.AudienceGateway,
	})
	for name, auth := range map[string]string{"no token": "", "its model-call token": "Bearer " + own} {
		if code := postAs(t, base, auth, forged); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("forged record with %s = %d, want 401 or 403", name, code)
		}
	}

	// The daemon mints through its one minting place, from the key file the edge shares.
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, testKey, 0o600); err != nil {
		t.Fatal(err)
	}
	minter := &gatewaymint.Minter{KeyFile: keyFile, Clock: core.SystemClock{}, Ports: core.NewPorts()}
	grant := core.GatewayGrant{Subject: "system:audit", Audience: core.AudienceAudit, TTL: 5 * time.Minute}
	log := daemonLog(sealedChain(t, "scout", "critic"))
	shipper := edgeship.New(edgeship.Options{
		Sink: base, Source: log, Encode: log.encode,
		Token: func(ctx context.Context) (string, error) { return minter.Mint(ctx, grant) },
	})
	if n, err := shipper.ShipOnce(t.Context()); err != nil || n != 2 {
		t.Fatalf("daemon ShipOnce = %d, %v; want 2 records shipped", n, err)
	}

	for path, check := range map[string]func(string) bool{
		"/v1/audit/inside": func(b string) bool {
			return strings.Contains(b, `"id":"scout"`) && strings.Contains(b, `"id":"critic"`) && !strings.Contains(b, "intruder")
		},
		"/v1/audit/alerts": func(b string) bool { return strings.Contains(b, `"alerts":[]`) },
		"/v1/audit/verify": func(b string) bool { return strings.Contains(b, `"inside":{"ok":true}`) },
	} {
		rec := httptest.NewRecorder()
		env.Admin.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if !check(rec.Body.String()) {
			t.Errorf("admin %s = %s", path, rec.Body)
		}
	}
}
