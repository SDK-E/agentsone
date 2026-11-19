// SPDX-License-Identifier: MIT

package keychain_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/keychain"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

// fakeSecurity plays the macOS `security` tool over an in-memory store. It
// never touches the real Keychain.
type fakeSecurity struct {
	items map[string]string // account -> password
	runs  []string
	stdin []string
	fail  error
}

func (f *fakeSecurity) Run(_ context.Context, stdin, name string, args ...string) (string, error) {
	f.runs = append(f.runs, name+" "+strings.Join(args, " "))
	f.stdin = append(f.stdin, stdin)
	if f.fail != nil {
		return "", f.fail
	}
	if name != "/usr/bin/security" {
		return "", errors.New("unexpected program " + name)
	}
	arg := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch args[0] {
	case "find-generic-password":
		if arg("-s") != "agentsone" {
			return "", keychain.ErrExit44
		}
		pw, ok := f.items[arg("-a")]
		if !ok {
			return "", keychain.ErrExit44
		}
		if args[len(args)-1] == "-w" {
			return pw + "\n", nil
		}
		return "keychain: ...\n", nil
	case "-i":
		// add-generic-password -U -s 'agentsone' -a 'openai' -w 'key'
		fields := strings.Fields(stdin)
		unq := func(s string) string { return strings.Trim(s, "'") }
		if len(fields) != 8 || fields[0] != "add-generic-password" || unq(fields[3]) != "agentsone" {
			return "", errors.New("bad interactive command: " + stdin)
		}
		f.items[unq(fields[5])] = unq(fields[7])
		return "", nil
	case "delete-generic-password":
		if _, ok := f.items[arg("-a")]; !ok {
			return "", keychain.ErrExit44
		}
		delete(f.items, arg("-a"))
		return "", nil
	}
	return "", errors.New("unexpected args")
}

func newKC() (*keychain.Keychain, *fakeSecurity) {
	f := &fakeSecurity{items: map[string]string{}}
	return keychain.New(f), f
}

func TestSetThenGetAndHas(t *testing.T) {
	kc, f := newKC()
	ctx := context.Background()
	if err := kc.Set(ctx, "openai", "test-key-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The key travels on stdin, never in the argument list.
	for _, r := range f.runs {
		if strings.Contains(r, "test-key-123") {
			t.Fatalf("key appeared in arguments: %q", r)
		}
	}
	got, err := kc.Get(ctx, "openai")
	if err != nil || got != "test-key-123" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	has, err := kc.Has(ctx, "openai")
	if err != nil || !has {
		t.Fatalf("Has = %v, %v", has, err)
	}
	if last := f.runs[len(f.runs)-1]; strings.HasSuffix(last, "-w") {
		t.Fatalf("Has read the secret: %q", last)
	}
}

func TestMissingKey(t *testing.T) {
	kc, _ := newKC()
	ctx := context.Background()
	if _, err := kc.Get(ctx, "groq"); !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Get error = %v, want ErrKeyNotFound", err)
	}
	has, err := kc.Has(ctx, "groq")
	if err != nil || has {
		t.Fatalf("Has = %v, %v", has, err)
	}
	if err := kc.Delete(ctx, "groq"); !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Delete error = %v", err)
	}
}

func TestDelete(t *testing.T) {
	kc, _ := newKC()
	ctx := context.Background()
	_ = kc.Set(ctx, "groq", "test-key")
	if err := kc.Delete(ctx, "groq"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := kc.Get(ctx, "groq"); !errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
}

func TestSetRefusesUnsafeKeysAndProviders(t *testing.T) {
	kc, f := newKC()
	ctx := context.Background()
	for _, bad := range []string{"", "has space", "quote'd", `back\slash`, "new\nline", strings.Repeat("k", 5000)} {
		if err := kc.Set(ctx, "openai", bad); !errors.Is(err, keychain.ErrBadKey) {
			t.Fatalf("Set(%q) error = %v, want ErrBadKey", bad, err)
		}
	}
	for _, bad := range []routing.ProviderID{"", "Open AI", "x'y", "../x"} {
		if err := kc.Set(ctx, bad, "test-key"); !errors.Is(err, keychain.ErrBadProvider) {
			t.Fatalf("Set provider %q error = %v, want ErrBadProvider", bad, err)
		}
	}
	if len(f.runs) != 0 {
		t.Fatalf("security ran for refused input: %v", f.runs)
	}
}

func TestToolFailureIsReported(t *testing.T) {
	kc, f := newKC()
	f.fail = errors.New("security: locked")
	ctx := context.Background()
	if _, err := kc.Get(ctx, "openai"); err == nil || errors.Is(err, gatewayapp.ErrKeyNotFound) {
		t.Fatalf("Get error = %v", err)
	}
	if _, err := kc.Has(ctx, "openai"); err == nil {
		t.Fatal("Has hid a tool failure")
	}
	if err := kc.Set(ctx, "openai", "test-key"); err == nil {
		t.Fatal("Set hid a tool failure")
	}
}

type lossySecurity struct{ fakeSecurity }

// Run accepts the add command but stores nothing, as `security -i` may do
// when it fails without an exit status.
func (l *lossySecurity) Run(ctx context.Context, stdin, name string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "-i" {
		return "", nil
	}
	return l.fakeSecurity.Run(ctx, stdin, name, args...)
}

func TestSetChecksTheKeyWasStored(t *testing.T) {
	l := &lossySecurity{fakeSecurity{items: map[string]string{}}}
	kc := keychain.New(l)
	if err := kc.Set(context.Background(), "openai", "test-key"); !errors.Is(err, keychain.ErrNotStored) {
		t.Fatalf("Set error = %v, want ErrNotStored", err)
	}
}
