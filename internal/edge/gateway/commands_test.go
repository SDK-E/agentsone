// SPDX-License-Identifier: MIT

package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	"github.com/SDK-E/agentsone/internal/edge/edgeenv"
)

// fakeAdmin stands in for the admin API client.
type fakeAdmin struct {
	providers map[string]*adminapi.Provider
	order     []string
	down      bool
	socket    string
}

var errDown = errors.New("adminapi: GET /v1/gateway/providers: is the edge running? connection refused")

func newFake() *fakeAdmin {
	f := &fakeAdmin{providers: map[string]*adminapi.Provider{}}
	for _, p := range []adminapi.Provider{
		{ID: "openai", Enabled: true, KeyRequired: true, Key: adminapi.KeyState{Set: true, Fingerprint: "ab12cd34"},
			Models: []adminapi.Model{{ID: "gpt-5-mini", Tier: "normal"}}},
		{ID: "mistral", KeyRequired: true, Models: []adminapi.Model{{ID: "mistral-small-latest", Tier: "quick"}}},
		{ID: "custom", Models: []adminapi.Model{{ID: "llama3.2", Tier: "normal"}}},
	} {
		f.providers[p.ID] = &p
		f.order = append(f.order, p.ID)
	}
	return f
}

func (f *fakeAdmin) get(id string) (*adminapi.Provider, error) {
	if f.down {
		return nil, errDown
	}
	p, ok := f.providers[id]
	if !ok {
		return nil, adminapi.NewProblem(http.StatusNotFound, adminapi.SlugNotFound, "The gateway knows no provider "+id+".")
	}
	return p, nil
}

func (f *fakeAdmin) Providers(context.Context) ([]adminapi.Provider, error) {
	if f.down {
		return nil, errDown
	}
	var out []adminapi.Provider
	for _, id := range f.order {
		out = append(out, *f.providers[id])
	}
	return out, nil
}

func (f *fakeAdmin) AddProvider(_ context.Context, id string) (adminapi.Provider, error) {
	p, err := f.get(id)
	if err != nil {
		return adminapi.Provider{}, err
	}
	p.Enabled = true
	return *p, nil
}

func (f *fakeAdmin) RemoveProvider(_ context.Context, id string) error {
	p, err := f.get(id)
	if err == nil {
		p.Enabled = false
	}
	return err
}

func (f *fakeAdmin) SetKey(_ context.Context, id, key string) (adminapi.KeyState, error) {
	p, err := f.get(id)
	if err != nil {
		return adminapi.KeyState{}, err
	}
	if strings.Contains(key, " ") {
		return adminapi.KeyState{}, adminapi.NewProblem(http.StatusBadRequest, adminapi.SlugValidation, "bad key")
	}
	p.Key = adminapi.KeyState{Set: true, Fingerprint: adminapi.Fingerprint(key)}
	return p.Key, nil
}

func (f *fakeAdmin) ClearKey(_ context.Context, id string) error {
	p, err := f.get(id)
	if err == nil {
		p.Key = adminapi.KeyState{}
	}
	return err
}

func runCmd(t *testing.T, a *fakeAdmin, stdin string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := &edgeenv.Env{Out: &out, Err: &errOut, Log: slog.New(slog.DiscardHandler), DataDir: t.TempDir()}
	d := deps{
		admin:    func(sock string) adminClient { a.socket = sock; return a },
		stdin:    strings.NewReader(stdin),
		terminal: func() bool { return false },
	}
	cmds := commands(env, d)
	if len(cmds) != 1 || cmds[0].Name() != "gateway" {
		t.Fatalf("commands = %v", cmds)
	}
	c := cmds[0]
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&errOut)
	err := c.ExecuteContext(t.Context())
	return out.String(), err
}

func TestProvidersList_ShowsKeyStateWithItsFingerprint(t *testing.T) {
	t.Parallel()
	a := newFake()

	out, err := runCmd(t, a, "", "providers", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	for _, want := range []string{"openai", "yes", "set (ab12cd34)", "mistral", "missing", "custom", "not needed", "gpt-5-mini (normal)"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if a.socket != "/run/agentsone-edge-admin/admin.sock" {
		t.Errorf("socket = %q, want the edge admin socket", a.socket)
	}
}

func TestProvidersAddAndRemove(t *testing.T) {
	t.Parallel()
	a := newFake()

	out, err := runCmd(t, a, "", "providers", "add", "mistral")
	if err != nil || !strings.Contains(out, "mistral added") || !strings.Contains(out, "agentsone gateway key set mistral") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if !a.providers["mistral"].Enabled {
		t.Error("mistral not enabled")
	}
	if out, err := runCmd(t, a, "", "providers", "remove", "openai"); err != nil || !strings.Contains(out, "openai removed") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if a.providers["openai"].Enabled || !a.providers["openai"].Key.Set {
		t.Error("remove should disable the provider and keep its key")
	}
	if _, err := runCmd(t, a, "", "providers", "add", "nope"); !adminapi.IsProblem(err, adminapi.SlugNotFound) {
		t.Errorf("add nope error = %v", err)
	}
}

func TestKeySetReadsStdinAndNeverEchoesTheKey(t *testing.T) {
	t.Parallel()
	a := newFake()

	out, err := runCmd(t, a, "sk-from-stdin\n", "--admin-socket", "/run/x.sock", "key", "set", "mistral")
	if err != nil {
		t.Fatalf("key set: %v", err)
	}

	if a.providers["mistral"].Key.Fingerprint != adminapi.Fingerprint("sk-from-stdin") || a.socket != "/run/x.sock" {
		t.Fatalf("key = %+v via %q", a.providers["mistral"].Key, a.socket)
	}
	if strings.Contains(out, "sk-from-stdin") || !strings.Contains(out, adminapi.Fingerprint("sk-from-stdin")) {
		t.Errorf("output = %q, want the fingerprint and not the key", out)
	}
}

func TestKeyClear(t *testing.T) {
	t.Parallel()
	a := newFake()

	if out, err := runCmd(t, a, "", "key", "clear", "openai"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("key clear: %v\n%s", err, out)
	}
	if a.providers["openai"].Key.Set {
		t.Error("key still set")
	}
}

func TestKeySetRefusals(t *testing.T) {
	t.Parallel()
	if _, err := runCmd(t, newFake(), "", "key", "set", "openai"); err == nil {
		t.Error("empty stdin accepted")
	}
	if _, err := runCmd(t, newFake(), "two words\n", "key", "set", "openai"); !adminapi.IsProblem(err, adminapi.SlugValidation) {
		t.Errorf("bad key error = %v", err)
	}
	down := newFake()
	down.down = true
	if _, err := runCmd(t, down, "k\n", "key", "set", "openai"); !errors.Is(err, errDown) {
		t.Errorf("edge down error = %v", err)
	}
}

func TestCommandsSayWhenTheEdgeIsDown(t *testing.T) {
	t.Parallel()
	a := newFake()
	a.down = true
	if _, err := runCmd(t, a, "", "providers", "list"); err == nil || !strings.Contains(err.Error(), "is the edge running") {
		t.Fatalf("list error = %v", err)
	}
}

func TestHelpCommands(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{}, {"providers"}, {"key"}} {
		out, err := runCmd(t, newFake(), "", args...)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Fatalf("gateway %v: %v\n%s", args, err, out)
		}
	}
}

func TestThereIsNoServeCommand(t *testing.T) {
	t.Parallel()
	cmd := Commands(&edgeenv.Env{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})[0]
	if c, _, err := cmd.Find([]string{"serve"}); err == nil && c.Name() == "serve" {
		t.Fatal("gateway serve still exists; the edge command serves the gateway")
	}
}
