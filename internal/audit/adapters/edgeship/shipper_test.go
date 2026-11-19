// SPDX-License-Identifier: MIT

package edgeship_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/adapters/edgeship"
	"github.com/SDK-E/agentsone/internal/audit/adapters/memory"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	edgehttp "github.com/SDK-E/agentsone/internal/edge/audit/adapters/http"
	edgesqlite "github.com/SDK-E/agentsone/internal/edge/audit/adapters/sqlite"
	edgeapp "github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

type edgeClock struct{}

func (edgeClock) Now() time.Time { return time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC) }

// testKey stands for the gateway key the edge shares read-only with the machine.
var testKey = bytes.Repeat([]byte{0x5a}, 32)

// tokenFor returns a token source that signs claims c with testKey, as the daemon's minter does.
func tokenFor(c gatewaytoken.Claims) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		claims := c
		claims.IssuedAt = edgeClock{}.Now().Unix()
		claims.ExpiresAt = edgeClock{}.Now().Add(5 * time.Minute).Unix()
		return gatewaytoken.Sign(testKey, claims)
	}
}

// auditToken is the daemon's token for the audit sink.
var auditToken = tokenFor(gatewaytoken.Claims{
	Agent: "system:audit", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceAudit,
})

// edge is a real edge audit sink on a loopback listener; down makes it answer 503.
type edge struct {
	sink *edgeapp.Sink
	srv  *httptest.Server
	mu   sync.Mutex
	down bool
}

func newEdge(t *testing.T) *edge {
	t.Helper()
	st, err := edgesqlite.Open(t.Context(), t.TempDir()+"/audit.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &edge{sink: edgeapp.New(st, edgeClock{}, nil)}
	inner := edgehttp.Inner(e.sink, testKey, edgeClock{}, nil)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		down := e.down
		e.mu.Unlock()
		if down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *edge) setDown(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.down = v
}

func (e *edge) head(t *testing.T) int64 {
	t.Helper()
	h, err := e.sink.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return h.Seq
}

func newLog(t *testing.T, n int) *app.Log {
	t.Helper()
	l := app.NewLog(memory.New(), coretest.NewClock(time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)))
	for range n {
		appendOne(t, l)
	}
	return l
}

func appendOne(t *testing.T, l *app.Log) {
	t.Helper()
	if err := l.Append(t.Context(), core.AuditEntry{Actor: "agent:founder", Action: "agent.hire", Target: "agent:scout"}); err != nil {
		t.Fatal(err)
	}
}

func shipper(e *edge, l *app.Log, wait func(context.Context, time.Duration) bool) *edgeship.Shipper {
	return edgeship.New(edgeship.Options{
		Sink: e.srv.URL, Source: l, Encode: app.EncodeRecord, Token: auditToken, Client: e.srv.Client(), Batch: 2, Wait: wait,
	})
}

// The sink takes records only from the daemon, so every call carries its audit token. A token
// for anything else is refused and ships nothing; the refusal is not a chain alert.
func TestShipOnce_ShipsOnlyWithTheAuditToken(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		token func(context.Context) (string, error)
		want  int64
	}{
		"audit token": {auditToken, 1},
		"model-call token": {tokenFor(gatewaytoken.Claims{
			Agent: "system:audit", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceGateway,
		}), 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEdge(t)
			s := edgeship.New(edgeship.Options{
				Sink: e.srv.URL, Source: newLog(t, 1), Encode: app.EncodeRecord, Token: tc.token, Client: e.srv.Client(),
			})
			_, err := s.ShipOnce(t.Context())
			if got := e.head(t); got != tc.want {
				t.Fatalf("edge head = %d, want %d (ShipOnce: %v)", got, tc.want, err)
			}
			if tc.want == 0 && (err == nil || errors.Is(err, edgeship.ErrRefused)) {
				t.Errorf("ShipOnce = %v, want a failure that is not a chain refusal", err)
			}
		})
	}
}

// Without the gateway key the daemon cannot mint; nothing goes to the edge and the error says why.
func TestShipOnce_SendsNothingWhenNoTokenCanBeMinted(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	noKey := func(context.Context) (string, error) {
		return "", fmt.Errorf("model gateway not reachable: %w", core.ErrUnavailable)
	}
	s := edgeship.New(edgeship.Options{Sink: srv.URL, Source: newLog(t, 1), Encode: app.EncodeRecord, Token: noKey, Client: srv.Client()})
	if _, err := s.ShipOnce(t.Context()); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("ShipOnce = %v, want the mint error", err)
	}
	unwired := edgeship.New(edgeship.Options{Sink: srv.URL, Source: newLog(t, 1), Encode: app.EncodeRecord, Client: srv.Client()})
	if _, err := unwired.ShipOnce(t.Context()); err == nil || !strings.Contains(err.Error(), "audit token") {
		t.Fatalf("ShipOnce without a token source = %v, want an error naming the audit token", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("edge called %d times without a token", n)
	}
}

func TestShipOnce_SendsTheNextBatchAfterTheEdgeHead(t *testing.T) {
	t.Parallel()
	e, l := newEdge(t), newLog(t, 3)
	s := shipper(e, l, nil)
	n, err := s.ShipOnce(t.Context())
	if err != nil || n != 2 || e.head(t) != 2 {
		t.Fatalf("first ShipOnce = %d, %v; edge head %d; want 2 shipped", n, err, e.head(t))
	}
	n, err = s.ShipOnce(t.Context())
	if err != nil || n != 1 || e.head(t) != 3 {
		t.Fatalf("second ShipOnce = %d, %v; edge head %d", n, err, e.head(t))
	}
	if n, err := s.ShipOnce(t.Context()); err != nil || n != 0 {
		t.Fatalf("idle ShipOnce = %d, %v; want nothing to ship", n, err)
	}
	if err := e.sink.Verify(t.Context(), edgeapp.Inside); err != nil {
		t.Fatalf("edge copy does not verify: %v", err)
	}
}

func TestShipOnce_KeepsRecordsWhileTheEdgeIsDownAndShipsThemLater(t *testing.T) {
	t.Parallel()
	e, l := newEdge(t), newLog(t, 2)
	s := shipper(e, l, nil)
	e.setDown(true)
	if _, err := s.ShipOnce(t.Context()); err == nil {
		t.Fatal("ShipOnce to a down edge succeeded")
	}
	// The daemon keeps writing while the edge is down: nothing waits, nothing is lost.
	appendOne(t, l)
	appendOne(t, l)
	e.setDown(false)
	for range 2 {
		if _, err := s.ShipOnce(t.Context()); err != nil {
			t.Fatalf("ShipOnce after the edge is back: %v", err)
		}
	}
	if got := e.head(t); got != 4 {
		t.Fatalf("edge head = %d, want all 4 records", got)
	}
}

func TestShipOnce_ReportsARefusalByTheEdge(t *testing.T) {
	t.Parallel()
	e := newEdge(t)
	if _, err := shipper(e, newLog(t, 1), nil).ShipOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A daemon whose log was wiped and rewritten: the edge sees a fork and refuses it.
	fresh := edgeship.New(edgeship.Options{
		Sink: e.srv.URL, Source: rewound{newLog(t, 3)}, Encode: app.EncodeRecord, Token: auditToken, Client: e.srv.Client(),
	})
	if _, err := fresh.ShipOnce(t.Context()); !errors.Is(err, edgeship.ErrRefused) {
		t.Fatalf("ShipOnce = %v, want ErrRefused", err)
	}
}

// rewound lists from the start whatever the edge head says, like a daemon whose log was replaced.
type rewound struct{ l *app.Log }

func (r rewound) List(ctx context.Context, _ int64, limit int) ([]domain.Record, error) {
	recs, err := r.l.List(ctx, 0, limit)
	for i := range recs {
		recs[i].Target.ID = "rewritten" // same places, different content
	}
	return recs, err
}

func TestShipOnce_FailsOnAnEdgeThatAnswersNonsense(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	t.Cleanup(srv.Close)
	s := edgeship.New(edgeship.Options{Sink: srv.URL, Source: newLog(t, 1), Encode: app.EncodeRecord, Token: auditToken, Client: srv.Client()})
	if _, err := s.ShipOnce(t.Context()); err == nil {
		t.Fatal("nonsense head accepted")
	}
}

func TestShipOnce_FailsOnAnUnreachableEdge(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	s := edgeship.New(edgeship.Options{Sink: url, Source: newLog(t, 1), Encode: app.EncodeRecord, Token: auditToken})
	if _, err := s.ShipOnce(t.Context()); err == nil {
		t.Fatal("ShipOnce to a closed port succeeded")
	}
}

func TestRun_BacksOffWhileDownAndCatchesUp(t *testing.T) {
	t.Parallel()
	e, l := newEdge(t), newLog(t, 3)
	e.setDown(true)
	ctx, cancel := context.WithCancel(t.Context())
	var waits []time.Duration
	wait := func(_ context.Context, d time.Duration) bool {
		waits = append(waits, d)
		switch len(waits) {
		case 3:
			e.setDown(false)
		case 6:
			cancel()
			return false
		}
		return true
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		shipper(e, l, wait).Run(ctx)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	if waits[0] != edgeship.MinBackoff || waits[1] != 2*edgeship.MinBackoff || waits[2] != 4*edgeship.MinBackoff {
		t.Fatalf("waits = %v, want doubling backoff while down", waits)
	}
	if e.head(t) != 3 {
		t.Fatalf("edge head = %d after the edge came back, want 3", e.head(t))
	}
	if waits[len(waits)-1] != edgeship.IdlePoll {
		t.Fatalf("last wait = %v, want the idle poll once caught up", waits[len(waits)-1])
	}
}

// logSink keeps log records.
type logSink struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (l *logSink) Enabled(context.Context, slog.Level) bool { return true }
func (l *logSink) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, r)
	return nil
}
func (l *logSink) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logSink) WithGroup(string) slog.Handler      { return l }

func (l *logSink) at(level slog.Level) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.recs {
		if r.Level == level {
			out = append(out, r.Message)
		}
	}
	return out
}

// An edge that is not up yet is normal at boot: the records wait in audit.db. It is said once,
// not as a warning on every retry.
func TestRun_ReportsAnOutageOnceWithoutWarnings(t *testing.T) {
	t.Parallel()
	e, l := newEdge(t), newLog(t, 1)
	e.setDown(true)
	logs := &logSink{}
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	wait := func(context.Context, time.Duration) bool {
		calls++
		if calls == 3 {
			e.setDown(false)
		}
		if calls == 5 {
			cancel()
			return false
		}
		return true
	}
	edgeship.New(edgeship.Options{
		Sink: e.srv.URL, Source: l, Encode: app.EncodeRecord, Token: auditToken, Client: e.srv.Client(), Wait: wait, Log: slog.New(logs),
	}).Run(ctx)
	if w := logs.at(slog.LevelWarn); len(w) != 0 {
		t.Fatalf("warnings = %q, want none for an edge that is not up yet", w)
	}
	if got := logs.at(slog.LevelInfo); len(got) != 2 {
		t.Fatalf("info = %q, want one line for the outage and one for the catch-up", got)
	}
}

func TestRun_StopsQuietlyInTheMiddleOfARequest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	logs := &logSink{}
	edgeship.New(edgeship.Options{
		Sink: srv.URL, Source: newLog(t, 1), Encode: app.EncodeRecord, Token: auditToken, Client: srv.Client(), Log: slog.New(logs),
	}).Run(ctx)
	logs.mu.Lock()
	defer logs.mu.Unlock()
	if len(logs.recs) != 0 {
		t.Fatalf("logged %d records on a clean stop, want none", len(logs.recs))
	}
}

func TestRun_StopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	e := newEdge(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		edgeship.New(edgeship.Options{Sink: e.srv.URL, Source: newLog(t, 0), Encode: app.EncodeRecord, Token: auditToken}).Run(ctx)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run ignored a cancelled context")
	}
}
