// SPDX-License-Identifier: MIT

package gatewayapp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

const usd = ledger.MicrosPerUSD

// fakeClock is moved by hand.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// lockedLedger adapts the pure ledger to the Budget port.
type lockedLedger struct {
	mu sync.Mutex
	l  *ledger.Ledger
}

func newBudget(total ledger.Micros) *lockedLedger {
	l := ledger.NewLedger(ledger.Snapshot{})
	_ = l.SetTotal(total)
	return &lockedLedger{l: l}
}

func (b *lockedLedger) Reserve(c ledger.Caller, m ledger.Micros) (ledger.ReservationID, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.l.Reserve(c, m)
}

func (b *lockedLedger) Settle(id ledger.ReservationID, m ledger.Micros) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.l.Settle(id, m)
}

func (b *lockedLedger) RemainingFor(c ledger.Caller) ledger.Micros {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.l.RemainingFor(c)
}

func (b *lockedLedger) state() ledger.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.l.State()
}

type fakeKeys map[routing.ProviderID]string

func (k fakeKeys) Get(_ context.Context, p routing.ProviderID) (string, error) {
	v, ok := k[p]
	if !ok {
		return "", gatewayapp.ErrKeyNotFound
	}
	return v, nil
}

// scripted is one canned upstream answer.
type scripted struct {
	status int
	body   string
	usage  *routing.Usage
	err    error
}

type fakeUpstream struct {
	mu      sync.Mutex
	answers map[routing.ProviderID][]scripted
	calls   []gatewayapp.UpstreamCall
}

func (u *fakeUpstream) Call(_ context.Context, c gatewayapp.UpstreamCall) (*gatewayapp.UpstreamResponse, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, c)
	q := u.answers[c.Provider]
	if len(q) == 0 {
		return nil, errors.New("no scripted answer")
	}
	a := q[0]
	u.answers[c.Provider] = q[1:]
	if a.err != nil {
		return nil, a.err
	}
	usage := a.usage
	return &gatewayapp.UpstreamResponse{
		Status: a.status,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   io.NopCloser(strings.NewReader(a.body)),
		Usage: func() (routing.Usage, bool) {
			if usage == nil {
				return routing.Usage{}, false
			}
			return *usage, true
		},
	}, nil
}

func (u *fakeUpstream) providers() []routing.ProviderID {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []routing.ProviderID
	for _, c := range u.calls {
		out = append(out, c.Provider)
	}
	return out
}

func price(s string) ledger.Micros {
	m, err := ledger.ParseUSD(s)
	if err != nil {
		panic(err)
	}
	return m
}

func testCatalog() routing.Catalog {
	return routing.Catalog{
		Providers: []routing.Provider{
			{ID: "anthropic", APIs: []routing.API{routing.APIAnthropic, routing.APIOpenAI}, Enabled: true},
			{ID: "openai", APIs: []routing.API{routing.APIOpenAI}, Enabled: true},
		},
		Models: []routing.Model{
			{Provider: "anthropic", ID: "claude-sonnet-5-5", Tier: routing.Normal, Family: "claude", InputPerMTok: price("2"), OutputPerMTok: price("10")},
			{Provider: "openai", ID: "gpt-mid", Tier: routing.Normal, Family: "gpt", InputPerMTok: price("1"), OutputPerMTok: price("8")},
		},
	}
}

type staticCatalog struct{ c routing.Catalog }

func (s staticCatalog) Catalog() routing.Catalog { return s.c }

type rig struct {
	router   *gatewayapp.Router
	budget   *lockedLedger
	upstream *fakeUpstream
	clock    *fakeClock
	breaker  *routing.Breaker
}

func newRig(t *testing.T, total ledger.Micros) *rig {
	t.Helper()
	r := &rig{
		budget:   newBudget(total),
		upstream: &fakeUpstream{answers: map[routing.ProviderID][]scripted{}},
		clock:    &fakeClock{now: time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)},
		breaker:  routing.NewBreaker(2, time.Minute),
	}
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog:  staticCatalog{testCatalog()},
		Keys:     fakeKeys{"anthropic": "test-key-a", "openai": "test-key-o"},
		Upstream: r.upstream,
		Budget:   r.budget,
		Breaker:  r.breaker,
		Clock:    r.clock,
		Limits:   gatewayapp.Limits{DefaultMaxOutputTokens: 1000, MaxOutputTokensCap: 2000, MaxAttempts: 3},
	})
	return r
}

var worker = ledger.Caller{Agent: "worker"}

func call(engine routing.API) gatewayapp.CallRequest {
	return gatewayapp.CallRequest{
		Caller: worker, Engine: engine, Weight: routing.Normal,
		Body: []byte(`{"model":"autoagents/one","messages":[]}`),
	}
}

func drain(t *testing.T, res *gatewayapp.CallResult) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	return string(b)
}

func TestCallGoesToCheapestModelAndSettlesActualCost(t *testing.T) {
	r := newRig(t, 10*usd)
	u := routing.Usage{Input: 1_000_000, Output: 100_000}
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{"ok":true}`, usage: &u}}

	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Model.ID != "gpt-mid" || res.Status != 200 {
		t.Fatalf("result = %+v", res)
	}
	// While the body is open the estimate is held.
	if r.budget.state().Reserved == 0 {
		t.Fatal("nothing reserved during the call")
	}
	if got := drain(t, res); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
	st := r.budget.state()
	// 1M input at $1 + 100k output at $8 = $1.80.
	if st.Spent != price("1.80") || st.Reserved != 0 {
		t.Fatalf("state = %+v", st)
	}
	c := r.upstream.calls[0]
	if c.Key != "test-key-o" || c.Model != "gpt-mid" || c.API != routing.APIOpenAI || c.MaxOutputTokens != 1000 {
		t.Fatalf("upstream call = %+v", c)
	}
}

func TestClosingTwiceSettlesOnce(t *testing.T) {
	r := newRig(t, 10*usd)
	u := routing.Usage{Output: 1000}
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`, usage: &u}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if err := res.Body.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := r.budget.state().Spent; got != price("0.008") {
		t.Fatalf("spent = %v, want $0.008", got)
	}
}

func TestUnknownUsageIsChargedAtTheEstimate(t *testing.T) {
	r := newRig(t, 10*usd)
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	body := int64(len(call(routing.APIOpenAI).Body))
	want := routing.Estimate(testCatalog().Models[1], body, 1000)
	if got := r.budget.state().Spent; got != want {
		t.Fatalf("spent = %v, want estimate %v", got, want)
	}
}

func TestAnthropicEngineOnlyUsesClaude(t *testing.T) {
	r := newRig(t, 10*usd)
	r.upstream.answers["anthropic"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	req := call(routing.APIAnthropic)
	req.MaxOutputTokens = 500
	res, err := r.router.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if res.Model.ID != "claude-sonnet-5-5" {
		t.Fatalf("model = %s", res.Model.ID)
	}
	c := r.upstream.calls[0]
	if c.API != routing.APIAnthropic || c.MaxOutputTokens != 500 {
		t.Fatalf("upstream call = %+v", c)
	}
}

func TestMaxOutputIsCapped(t *testing.T) {
	r := newRig(t, 10*usd)
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	req := call(routing.APIOpenAI)
	req.MaxOutputTokens = 999_999
	res, err := r.router.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if got := r.upstream.calls[0].MaxOutputTokens; got != 2000 {
		t.Fatalf("max output = %d, want cap 2000", got)
	}
}

func TestFailoverOnServerErrorAndBreakerTrips(t *testing.T) {
	r := newRig(t, 10*usd)
	ok := routing.Usage{}
	r.upstream.answers["openai"] = []scripted{
		{status: 503, body: `down`},
		{err: errors.New("timeout")},
	}
	r.upstream.answers["anthropic"] = []scripted{
		{status: 200, body: `{}`, usage: &ok},
		{status: 200, body: `{}`, usage: &ok},
		{status: 200, body: `{}`, usage: &ok},
	}
	for range 2 {
		res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if res.Model.Provider != "anthropic" {
			t.Fatalf("served by %s, want anthropic after openai failed", res.Model.Provider)
		}
		_ = drain(t, res)
	}
	if r.breaker.Healthy("openai", r.clock.now) {
		t.Fatal("openai breaker did not open after two failures")
	}
	// Breaker open: openai is not even tried.
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	got := r.upstream.providers()
	want := []routing.ProviderID{"openai", "anthropic", "openai", "anthropic", "anthropic"}
	if strings.Join(toStrings(got), ",") != strings.Join(toStrings(want), ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	// Failed attempts cost nothing.
	if st := r.budget.state(); st.Spent != 0 || st.Reserved != 0 {
		t.Fatalf("state = %+v", st)
	}

	// After the cooldown openai gets a probe, which succeeds and closes the breaker.
	r.clock.now = r.clock.now.Add(time.Minute)
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`, usage: &ok}}
	res, err = r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if res.Model.Provider != "openai" || r.breaker.State("openai", r.clock.now) != routing.BreakerClosed {
		t.Fatalf("probe served by %s, breaker %v", res.Model.Provider, r.breaker.State("openai", r.clock.now))
	}
}

func toStrings(ps []routing.ProviderID) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func TestClientErrorIsPassedThroughWithoutFailover(t *testing.T) {
	r := newRig(t, 10*usd)
	r.upstream.answers["openai"] = []scripted{{status: 400, body: `{"error":"bad"}`}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Status != 400 || drain(t, res) != `{"error":"bad"}` {
		t.Fatalf("result = %+v", res)
	}
	if len(r.upstream.calls) != 1 || !r.breaker.Healthy("openai", r.clock.now) {
		t.Fatal("a client error caused failover or tripped the breaker")
	}
}

func TestAllUpstreamsFailing(t *testing.T) {
	r := newRig(t, 10*usd)
	r.upstream.answers["openai"] = []scripted{{status: 500, body: "x"}}
	r.upstream.answers["anthropic"] = []scripted{{status: 429, body: "slow down"}}
	_, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	var up *gatewayapp.UpstreamError
	if !errors.As(err, &up) || up.Status != 429 {
		t.Fatalf("error = %v, want UpstreamError with last status 429", err)
	}
	if st := r.budget.state(); st.Reserved != 0 || st.Spent != 0 {
		t.Fatalf("state = %+v", st)
	}
}

type brokenKeys struct{}

func (brokenKeys) Get(context.Context, routing.ProviderID) (string, error) {
	return "", errors.New("keychain locked")
}

func TestKeyStoreFailureSkipsProvider(t *testing.T) {
	r := newRig(t, 10*usd)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: brokenKeys{}, Upstream: r.upstream,
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	_, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	var up *gatewayapp.UpstreamError
	if !errors.As(err, &up) || up.Status != 0 || !strings.Contains(up.Error(), "keychain locked") {
		t.Fatalf("error = %v, want UpstreamError wrapping the key error", err)
	}
	if len(r.upstream.calls) != 0 {
		t.Fatal("provider called without its key")
	}
}

func TestAttemptsAreCapped(t *testing.T) {
	r := newRig(t, 10*usd)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: fakeKeys{}, Upstream: r.upstream,
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock, Limits: gatewayapp.Limits{MaxAttempts: 1},
	})
	r.upstream.answers["openai"] = []scripted{{status: 502, body: "bad gateway"}}
	_, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	var up *gatewayapp.UpstreamError
	if !errors.As(err, &up) || up.Status != 502 || !strings.Contains(up.Error(), "502") || errors.Unwrap(up) == nil {
		t.Fatalf("error = %v", err)
	}
	if len(r.upstream.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(r.upstream.calls))
	}
}

func TestHalfOpenProviderGetsOnlyOneProbe(t *testing.T) {
	r := newRig(t, 10*usd)
	r.breaker.Failure("openai", r.clock.now)
	r.breaker.Failure("openai", r.clock.now)
	r.clock.now = r.clock.now.Add(time.Minute)
	// The probe is taken by another call that has not finished.
	if !r.breaker.Allow("openai", r.clock.now) {
		t.Fatal("probe not allowed")
	}
	r.upstream.answers["anthropic"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if res.Model.Provider != "anthropic" {
		t.Fatalf("served by %s, want anthropic", res.Model.Provider)
	}
	if st := r.budget.state(); st.Reserved != 0 {
		t.Fatalf("reservation left open: %+v", st)
	}
}

type flakyKeys struct {
	fakeKeys
	fail bool
}

func (f *flakyKeys) Get(ctx context.Context, p routing.ProviderID) (string, error) {
	if f.fail {
		return "", errors.New("keychain locked")
	}
	return f.fakeKeys.Get(ctx, p)
}

func TestKeyErrorDoesNotUseUpTheProbe(t *testing.T) {
	r := newRig(t, 10*usd)
	keys := &flakyKeys{fakeKeys: fakeKeys{"openai": "k", "anthropic": "k"}, fail: true}
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: keys, Upstream: r.upstream,
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	r.breaker.Failure("openai", r.clock.now)
	r.breaker.Failure("openai", r.clock.now)
	r.clock.now = r.clock.now.Add(time.Minute) // half-open: one probe allowed

	if _, err := r.router.Call(context.Background(), call(routing.APIOpenAI)); err == nil {
		t.Fatal("Call succeeded with a locked keychain")
	}
	// The keychain is back: the probe must still be available.
	keys.fail = false
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if res.Model.Provider != "openai" {
		t.Fatalf("served by %s; the probe was used up by a key error", res.Model.Provider)
	}
}

type cancelUpstream struct{}

func (cancelUpstream) Call(ctx context.Context, _ gatewayapp.UpstreamCall) (*gatewayapp.UpstreamResponse, error) {
	return nil, ctx.Err()
}

func TestCallerGoneDoesNotTripBreaker(t *testing.T) {
	r := newRig(t, 10*usd)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: fakeKeys{}, Upstream: cancelUpstream{},
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 3 {
		if _, err := r.router.Call(ctx, call(routing.APIOpenAI)); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	}
	if !r.breaker.Healthy("openai", r.clock.now) {
		t.Fatal("a caller that went away tripped the provider's breaker")
	}
	if st := r.budget.state(); st.Reserved != 0 || st.Spent != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestCountTokensGoesToClaudeWithoutCharging(t *testing.T) {
	r := newRig(t, 0) // no budget at all: counting is free
	r.upstream.answers["anthropic"] = []scripted{{status: 200, body: `{"input_tokens":42}`}}
	res, err := r.router.CountTokens(context.Background(), gatewayapp.CountRequest{
		Weight: routing.Normal, Body: []byte(`{"model":"autoagents/one","messages":[]}`),
	})
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if got := drain(t, res); got != `{"input_tokens":42}` || res.Model.ID != "claude-sonnet-5-5" {
		t.Fatalf("result %+v body %q", res.Model, got)
	}
	c := r.upstream.calls[0]
	if c.Op != gatewayapp.OpCountTokens || c.API != routing.APIAnthropic || c.Key != "test-key-a" {
		t.Fatalf("upstream call = %+v", c)
	}
	if st := r.budget.state(); st.Spent != 0 || st.Reserved != 0 {
		t.Fatalf("counting changed the budget: %+v", st)
	}
}

func TestCountTokensFailsOverAndLeavesHealthAlone(t *testing.T) {
	cat := testCatalog()
	cat.Models = append(cat.Models, routing.Model{
		Provider: "anthropic2", ID: "claude-dear", Tier: routing.Normal, Family: "claude",
		InputPerMTok: price("9"), OutputPerMTok: price("9"),
	})
	cat.Providers = append(cat.Providers, routing.Provider{ID: "anthropic2", APIs: []routing.API{routing.APIAnthropic}, Enabled: true})
	r := newRig(t, 0)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{cat}, Keys: fakeKeys{}, Upstream: r.upstream,
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	r.upstream.answers["anthropic"] = []scripted{{status: 503, body: "down"}}
	r.upstream.answers["anthropic2"] = []scripted{{status: 200, body: `{"input_tokens":7}`}}
	res, err := r.router.CountTokens(context.Background(), gatewayapp.CountRequest{Weight: routing.Normal, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	_ = drain(t, res)
	if res.Model.Provider != "anthropic2" {
		t.Fatalf("served by %s", res.Model.Provider)
	}
	if r.breaker.State("anthropic", r.clock.now) != routing.BreakerClosed {
		t.Fatal("a count call changed provider health")
	}
}

func TestCountTokensErrors(t *testing.T) {
	r := newRig(t, 0)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{routing.Catalog{}}, Keys: fakeKeys{}, Upstream: r.upstream,
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	if _, err := r.router.CountTokens(context.Background(), gatewayapp.CountRequest{Weight: routing.Normal}); !errors.Is(err, gatewayapp.ErrNoRoute) {
		t.Fatalf("error = %v, want ErrNoRoute", err)
	}
	r2 := newRig(t, 0)
	r2.upstream.answers["anthropic"] = []scripted{{err: errors.New("timeout")}}
	_, err := r2.router.CountTokens(context.Background(), gatewayapp.CountRequest{Weight: routing.Normal, Body: []byte(`{}`)})
	var up *gatewayapp.UpstreamError
	if !errors.As(err, &up) {
		t.Fatalf("error = %v, want UpstreamError", err)
	}
	r3 := newRig(t, 0)
	r3.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: brokenKeys{}, Upstream: r3.upstream,
		Budget: r3.budget, Breaker: r3.breaker, Clock: r3.clock,
	})
	if _, err := r3.router.CountTokens(context.Background(), gatewayapp.CountRequest{Weight: routing.Normal, Body: []byte(`{}`)}); !errors.As(err, &up) {
		t.Fatalf("error = %v, want UpstreamError", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r4 := newRig(t, 0)
	r4.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: fakeKeys{}, Upstream: cancelUpstream{},
		Budget: r4.budget, Breaker: r4.breaker, Clock: r4.clock,
	})
	if _, err := r4.router.CountTokens(ctx, gatewayapp.CountRequest{Weight: routing.Normal, Body: []byte(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestNoRoute(t *testing.T) {
	r := newRig(t, 10*usd)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{routing.Catalog{}}, Keys: fakeKeys{}, Upstream: r.upstream,
		Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	if _, err := r.router.Call(context.Background(), call(routing.APIOpenAI)); !errors.Is(err, gatewayapp.ErrNoRoute) {
		t.Fatalf("error = %v, want ErrNoRoute", err)
	}
}

func TestBudgetRefusal(t *testing.T) {
	r := newRig(t, 0)
	if _, err := r.router.Call(context.Background(), call(routing.APIOpenAI)); !errors.Is(err, ledger.ErrBudgetExhausted) {
		t.Fatalf("error = %v, want ErrBudgetExhausted", err)
	}
	if len(r.upstream.calls) != 0 {
		t.Fatal("upstream called with no budget")
	}
}

func TestAllocationDecidesWhichModelsFit(t *testing.T) {
	// Estimates for the test body (14 input tokens) and 1000 output tokens:
	// gpt-mid $0.008014, claude-sonnet-5-5 $0.010028. The worker's soft
	// allocation of $0.009 fits only the first.
	r := newRig(t, 10*usd)
	r.budget.l.SetAllocations(map[string]ledger.Micros{"worker": price("0.009")})
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)

	if _, err := r.router.Call(context.Background(), call(routing.APIAnthropic)); !errors.Is(err, ledger.ErrAllocationExhausted) {
		t.Fatalf("error = %v, want ErrAllocationExhausted", err)
	}
}

func TestBudgetRefusalOnFirstChoiceTriesNext(t *testing.T) {
	// Make the dearer model the first choice by putting it alone in the asked
	// tier; the cheaper fallback model still fits the allocation.
	cat := testCatalog()
	cat.Models[0].Tier = routing.Quick // claude-sonnet-5-5, estimate $0.010028
	r := newRig(t, 10*usd)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{cat}, Keys: fakeKeys{"anthropic": "k", "openai": "k"},
		Upstream: r.upstream, Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
		Limits: gatewayapp.Limits{DefaultMaxOutputTokens: 1000},
	})
	r.budget.l.SetAllocations(map[string]ledger.Micros{"worker": price("0.009")})
	r.upstream.answers["openai"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	req := call(routing.APIOpenAI)
	req.Weight = routing.Quick
	res, err := r.router.Call(context.Background(), req)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if res.Model.ID != "gpt-mid" {
		t.Fatalf("model = %s, want gpt-mid", res.Model.ID)
	}
}

func TestMissingKeySkipsProvider(t *testing.T) {
	r := newRig(t, 10*usd)
	r.router = gatewayapp.NewRouter(gatewayapp.Deps{
		Catalog: staticCatalog{testCatalog()}, Keys: fakeKeys{"anthropic": "test-key-a"},
		Upstream: r.upstream, Budget: r.budget, Breaker: r.breaker, Clock: r.clock,
	})
	r.upstream.answers["openai"] = []scripted{{err: gatewayapp.ErrKeyRequired}}
	r.upstream.answers["anthropic"] = []scripted{{status: 200, body: `{}`, usage: &routing.Usage{}}}
	res, err := r.router.Call(context.Background(), call(routing.APIOpenAI))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = drain(t, res)
	if res.Model.Provider != "anthropic" || !r.breaker.Healthy("openai", r.clock.now) {
		t.Fatalf("served by %s; a missing key must not trip the breaker", res.Model.Provider)
	}
	if r.upstream.calls[0].Key != "" {
		t.Fatalf("key = %q, want empty", r.upstream.calls[0].Key)
	}
}

func TestBudgetView(t *testing.T) {
	r := newRig(t, 0)
	v := r.router.Budget(worker)
	if !v.Exhausted || v.Remaining != 0 {
		t.Fatalf("view = %+v", v)
	}
	r2 := newRig(t, 5*usd)
	if v := r2.router.Budget(worker); v.Exhausted || v.Remaining != 5*usd {
		t.Fatalf("view = %+v", v)
	}
}
