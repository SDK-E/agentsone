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

	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayconfig"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
	"github.com/SDK-E/agentsone/internal/host/hostenv"
)

// memKeyStore is an in-memory key store; tests never touch the Keychain.
type memKeyStore map[routing.ProviderID]string

func (m memKeyStore) Get(_ context.Context, p routing.ProviderID) (string, error) {
	if k, ok := m[p]; ok {
		return k, nil
	}
	return "", gatewayapp.ErrKeyNotFound
}

func (m memKeyStore) Has(_ context.Context, p routing.ProviderID) (bool, error) {
	_, ok := m[p]
	return ok, nil
}

func (m memKeyStore) Set(_ context.Context, p routing.ProviderID, key string) error {
	if strings.ContainsAny(key, " \n") {
		return errors.New("bad key")
	}
	m[p] = key
	return nil
}

func runCmd(t *testing.T, dir string, keys memKeyStore, stdin string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := &hostenv.Env{Out: &out, Err: &errOut, Log: slog.New(slog.DiscardHandler), DataDir: dir}
	cmds := commands(env, deps{keys: keys, stdin: strings.NewReader(stdin), terminal: func() bool { return false }})
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
	keys := memKeyStore{"openai": "test-key"}

	out, err := runCmd(t, dir, keys, "", "providers", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"anthropic", "openai", "openrouter", "gemini", "mistral", "groq", "custom", "not needed", "set", "missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list is missing %q:\n%s", want, out)
		}
	}

	if out, err := runCmd(t, dir, keys, "", "providers", "enable", "groq"); err != nil || !strings.Contains(out, "groq enabled") {
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
	if out, err := runCmd(t, dir, keys, "", "providers", "disable", "groq"); err != nil || !strings.Contains(out, "groq disabled") {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	if _, err := runCmd(t, dir, keys, "", "providers", "enable", "nope"); err == nil {
		t.Fatal("enable of an unknown provider succeeded")
	}
}

func TestEnableWarnsWhenKeyMissing(t *testing.T) {
	out, err := runCmd(t, t.TempDir(), memKeyStore{}, "", "providers", "enable", "mistral")
	if err != nil || !strings.Contains(out, "agentsone gateway key set mistral") {
		t.Fatalf("enable: %v\n%s", err, out)
	}
}

func TestKeySetReadsStdin(t *testing.T) {
	keys := memKeyStore{}
	out, err := runCmd(t, t.TempDir(), keys, "test-key-from-stdin\n", "key", "set", "openai")
	if err != nil {
		t.Fatalf("key set: %v", err)
	}
	if keys["openai"] != "test-key-from-stdin" {
		t.Fatalf("stored = %q", keys["openai"])
	}
	if strings.Contains(out, "test-key-from-stdin") {
		t.Fatal("the key was echoed")
	}
}

func TestKeySetRefusals(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCmd(t, dir, memKeyStore{}, "", "key", "set", "openai"); err == nil {
		t.Fatal("empty stdin accepted")
	}
	if _, err := runCmd(t, dir, memKeyStore{}, "k\n", "key", "set", "nope"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := runCmd(t, dir, memKeyStore{}, "two words\n", "key", "set", "openai"); err == nil {
		t.Fatal("store error hidden")
	}
}

func TestServeRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:4000", "192.168.127.1:4000", "nonsense"} {
		if _, err := runCmd(t, t.TempDir(), memKeyStore{}, "", "serve", "--listen", addr); err == nil {
			t.Fatalf("serve --listen %s succeeded", addr)
		}
	}
}

func TestServeStopsWithItsContext(t *testing.T) {
	var out bytes.Buffer
	env := &hostenv.Env{Out: &out, Err: &out, Log: slog.New(slog.DiscardHandler), DataDir: t.TempDir()}
	c := commands(env, deps{keys: memKeyStore{}, stdin: strings.NewReader(""), terminal: func() bool { return false }})[0]
	c.SetArgs([]string{"serve", "--listen", "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop at once: the gateway opens, listens, flushes and closes
	if err := c.ExecuteContext(ctx); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.DataDir, BudgetFileName)); err != nil {
		t.Fatalf("budget.db not created: %v", err)
	}
}

func TestHelpCommands(t *testing.T) {
	for _, args := range [][]string{{}, {"providers"}, {"key"}} {
		out, err := runCmd(t, t.TempDir(), memKeyStore{}, "", args...)
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
	if _, err := runCmd(t, dir, memKeyStore{}, "", "providers", "list"); err == nil {
		t.Fatal("list accepted a bad config")
	}
}
