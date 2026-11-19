// SPDX-License-Identifier: MIT

//go:build preview

package web_test

// The local preview of the control page, for looking at every state in a browser. It is not part
// of the normal test run. Start it with:
//
//	go test -tags preview -run TestPreview -timeout 0 ./internal/edge/control/adapters/web/
//
// It serves, on the loopback address, with fake edges and the passphrase below:
//
//	http://127.0.0.1:7791  first start: no passphrase yet (the setup code is in the test log)
//	http://127.0.0.1:7792  empty: nothing allowed, no keys, no budget
//	http://127.0.0.1:7793  loaded: a typical stack
//	http://127.0.0.1:7794  error: the edge does not answer
//	http://127.0.0.1:7795  long text and frozen

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/control/adapters/web"
	"github.com/SDK-E/agentsone/internal/edge/control/app"
	"github.com/SDK-E/agentsone/internal/edge/control/controltest"
)

// previewPassphrase signs in to the preview. It is a test value for the fake edge only.
const previewPassphrase = "preview passphrase"

func TestPreview(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	clock := controltest.NewClock(time.Date(2026, 11, 19, 9, 30, 0, 0, time.UTC))
	states := map[string]func(*controltest.Edge){
		"127.0.0.1:7791": func(*controltest.Edge) {},
		"127.0.0.1:7792": func(*controltest.Edge) {},
		"127.0.0.1:7793": loaded,
		"127.0.0.1:7794": func(e *controltest.Edge) { loaded(e); e.Err = controltest.ErrDown },
		"127.0.0.1:7795": long,
	}
	for addr, seed := range states {
		creds := &controltest.Credentials{}
		if !strings.HasSuffix(addr, "7791") {
			_ = creds.Create(t.Context(), "fake$"+previewPassphrase)
		}
		edge := controltest.NewEdge(clock.Now, providers(addr)...)
		seed(edge)
		auth := app.NewAuth(app.AuthOptions{Credentials: creds, Hasher: controltest.Hasher{}, Clock: clock, Entropy: rand.Reader, Log: log})
		powers := app.NewPowers(app.PowersOptions{Network: edge, Models: edge, Money: edge, Freeze: edge})
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		var h http.Handler = web.New(web.Options{Auth: auth, Powers: powers, Log: log})
		if !strings.HasSuffix(addr, "7791") {
			h = signedIn(t, auth, h)
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Logf("preview on http://%s", addr)
	}
	<-t.Context().Done()
}

// signedIn lets a browser without a cookie (a headless screenshot) in as a signed-in visitor.
// Preview only: the page itself has no such door.
func signedIn(t *testing.T, auth *app.Auth, next http.Handler) http.Handler {
	v, err := auth.Visit(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := auth.Login(t.Context(), v.Session.ID, previewPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Cookies()) == 0 {
			r.AddCookie(&http.Cookie{Name: "agentsone_control", Value: sess.ID}) // #nosec G124 -- preview only
		}
		next.ServeHTTP(w, r)
	})
}

func providers(addr string) []app.Provider {
	if strings.HasSuffix(addr, "7792") || strings.HasSuffix(addr, "7791") {
		return []app.Provider{{ID: "openai", KeyRequired: true, Models: []app.Model{{ID: "gpt-5", Tier: "normal"}}}}
	}
	ps := []app.Provider{
		{ID: "anthropic", KeyRequired: true, Models: []app.Model{{ID: "claude-quick", Tier: "quick"}, {ID: "claude-deep", Tier: "deep"}}},
		{ID: "openai", KeyRequired: true, Models: []app.Model{{ID: "gpt-5", Tier: "normal"}}},
		{ID: "local-ollama", Models: []app.Model{{ID: "llama-small", Tier: "quick"}}},
	}
	if strings.HasSuffix(addr, "7795") {
		ps = append(ps, app.Provider{ID: "a-provider-with-a-really-long-identifier-from-the-gateway-settings", KeyRequired: true,
			Models: []app.Model{{ID: "a-model-with-an-equally-long-identifier-that-wraps", Tier: "deep"}}})
	}
	return ps
}

func loaded(e *controltest.Edge) {
	ctx := context.Background()
	_, _ = e.Allow(ctx, "deb.debian.org", "Debian packages")
	_, _ = e.Allow(ctx, "*.github.com", "Code and releases")
	_, _ = e.Allow(ctx, "registry.npmjs.org", "")
	_, _ = e.SetKey(ctx, "anthropic", "sk-preview-not-a-real-key")
	_ = e.Enable(ctx, "anthropic")
	_ = e.Enable(ctx, "local-ollama")
	e.SeedBudget(app.Budget{
		Currency: "USD", TotalMicros: 50_000_000, SpentMicros: 12_340_000, RemainingMicros: 37_660_000, Period: "month",
		PeriodStart: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Defence: app.Defence{Enabled: true, SharePct: 10, ReservedMicros: 5_000_000, RemainingMicros: 4_200_000},
	})
}

func long(e *controltest.Edge) {
	ctx := context.Background()
	_, _ = e.Allow(ctx, "a-very-long-subdomain-name-that-keeps-going.and-another-long-label.example-of-a-long-domain.org",
		"A long reason that explains in many words why the agents need this name: package mirrors, documentation, release notes and the status page of the provider")
	_, _ = e.Allow(ctx, "*.example.org", strings.Repeat("words ", 30))
	_, _ = e.SetKey(ctx, "openai", "sk-preview-not-a-real-key")
	_ = e.Freeze(ctx)
	e.SeedBudget(app.Budget{
		Currency: "USD", TotalMicros: 1_234_567_890_000, SpentMicros: 1_234_567_890_000, RemainingMicros: 0, Exhausted: true, Period: "week",
		PeriodEnd: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
	})
}
