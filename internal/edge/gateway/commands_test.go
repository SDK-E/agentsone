// SPDX-License-Identifier: MIT

package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// fakeAdmin stands in for the admin socket client.
type fakeAdmin struct {
	keys   map[string]string
	down   bool
	socket string
}

var errDown = errors.New("edge not running")

func (f *fakeAdmin) SetKey(_ context.Context, p, k string) error {
	if f.down {
		return errDown
	}
	if strings.Contains(k, " ") {
		return errors.New("bad key")
	}
	f.keys[p] = k
	return nil
}

func (f *fakeAdmin) DeleteKey(_ context.Context, p string) error {
	if f.down {
		return errDown
	}
	if _, ok := f.keys[p]; !ok {
		return errors.New("no key")
	}
	delete(f.keys, p)
	return nil
}

func (f *fakeAdmin) Keys(context.Context) (map[string]bool, error) {
	if f.down {
		return nil, errDown
	}
	out := map[string]bool{}
	for p := range f.keys {
		out[p] = true
	}
	return out, nil
}

func newFake(keys map[string]string) *fakeAdmin {
	if keys == nil {
		keys = map[string]string{}
	}
	return &fakeAdmin{keys: keys}
}

func runCmd(t *testing.T, dir string, a *fakeAdmin, stdin string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := &hostenv.Env{Out: &out, Err: &errOut, Log: slog.New(slog.DiscardHandler), DataDir: dir}
	d := deps{
		admin:    func(sock string) keyClient { a.socket = sock; return a },
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
	err := c.Execute()
	return out.String(), err
}

func TestProvidersListEnableDisable(t *testing.T) {
	dir := t.TempDir()
	a := newFake(map[string]string{"openai": "test-key"})

	out, err := runCmd(t, dir, a, "", "providers", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"anthropic", "openai", "openrouter", "gemini", "mistral", "groq", "custom", "not needed", "set", "missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list is missing %q:\n%s", want, out)
		}
	}
	if a.socket != filepath.Join(dir, "admin.sock") {
		t.Fatalf("socket = %q", a.socket)
	}

	if out, err := runCmd(t, dir, a, "", "providers", "enable", "groq"); err != nil || !strings.Contains(out, "groq enabled") {
		t.Fatalf("enable: %v\n%s", err, out)
	}
	f, err := gatewayconfig.Load(filepath.Join(dir, gatewayconfig.FileName))
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := f.Catalog()
	if p, _ := cat.Provider("groq"); !p.Enabled {
		t.Fatal("groq not enabled in the file")
	}
	if out, err := runCmd(t, dir, a, "", "providers", "disable", "groq"); err != nil || !strings.Contains(out, "groq disabled") {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	if _, err := runCmd(t, dir, a, "", "providers", "enable", "nope"); err == nil {
		t.Fatal("enable of an unknown provider succeeded")
	}
}

func TestListWhenEdgeIsDown(t *testing.T) {
	a := newFake(nil)
	a.down = true
	out, err := runCmd(t, t.TempDir(), a, "", "providers", "list")
	if err != nil || !strings.Contains(out, "unknown (edge not running)") {
		t.Fatalf("list: %v\n%s", err, out)
	}
}

func TestEnableWarnsWhenKeyMissing(t *testing.T) {
	out, err := runCmd(t, t.TempDir(), newFake(nil), "", "providers", "enable", "mistral")
	if err != nil || !strings.Contains(out, "agentsone gateway key set mistral") {
		t.Fatalf("enable: %v\n%s", err, out)
	}
}

func TestKeySetAndDeleteGoThroughTheAdminSocket(t *testing.T) {
	a := newFake(nil)
	dir := t.TempDir()
	out, err := runCmd(t, dir, a, "test-key-from-stdin\n", "--admin-socket", "/run/x.sock", "key", "set", "openai")
	if err != nil {
		t.Fatalf("key set: %v", err)
	}
	if a.keys["openai"] != "test-key-from-stdin" || a.socket != "/run/x.sock" {
		t.Fatalf("stored = %q via %q", a.keys["openai"], a.socket)
	}
	if strings.Contains(out, "test-key-from-stdin") {
		t.Fatal("the key was echoed")
	}
	if out, err := runCmd(t, dir, a, "", "key", "delete", "openai"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("key delete: %v\n%s", err, out)
	}
	if _, err := runCmd(t, dir, a, "", "key", "delete", "openai"); err == nil {
		t.Fatal("deleting a missing key succeeded")
	}
}

func TestKeySetRefusals(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCmd(t, dir, newFake(nil), "", "key", "set", "openai"); err == nil {
		t.Fatal("empty stdin accepted")
	}
	if _, err := runCmd(t, dir, newFake(nil), "two words\n", "key", "set", "openai"); err == nil {
		t.Fatal("store error hidden")
	}
	down := newFake(nil)
	down.down = true
	if _, err := runCmd(t, dir, down, "k\n", "key", "set", "openai"); !errors.Is(err, errDown) {
		t.Fatalf("edge down error = %v", err)
	}
}

func TestServeRefusesBadListen(t *testing.T) {
	if _, err := runCmd(t, t.TempDir(), newFake(nil), "", "serve", "--listen", "nonsense"); err == nil {
		t.Fatal("serve --listen nonsense succeeded")
	}
}

func TestServeStopsWithItsContext(t *testing.T) {
	// A short folder: unix socket paths are limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "ao")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var out bytes.Buffer
	env := &hostenv.Env{Out: &out, Err: &out, Log: slog.New(slog.DiscardHandler), DataDir: dir}
	c := Commands(env)[0]
	c.SetArgs([]string{"serve", "--listen", "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop at once: the gateway opens, listens, flushes and closes
	if err := c.ExecuteContext(ctx); err != nil {
		t.Fatalf("serve: %v", err)
	}
	for _, name := range []string{BudgetFileName, "keys"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s not created: %v", name, err)
		}
	}
}

func TestHelpCommands(t *testing.T) {
	for _, args := range [][]string{{}, {"providers"}, {"key"}} {
		out, err := runCmd(t, t.TempDir(), newFake(nil), "", args...)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Fatalf("gateway %v: %v\n%s", args, err, out)
		}
	}
}

func TestBadConfigIsReported(t *testing.T) {
	dir := t.TempDir()
	f := gatewayconfig.Default()
	f.Providers[0].OpenAIBaseURL = "ftp://nope"
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), f); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, dir, newFake(nil), "", "providers", "list"); err == nil {
		t.Fatal("list accepted a bad config")
	}
	if _, err := runCmd(t, dir, newFake(nil), "k\n", "key", "set", "openai"); err == nil {
		t.Fatal("key set accepted a bad config")
	}
}
