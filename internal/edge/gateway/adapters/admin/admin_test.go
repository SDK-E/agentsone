// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/admin"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

type memKeys map[routing.ProviderID]string

func (m memKeys) Has(_ context.Context, p routing.ProviderID) (bool, error) {
	_, ok := m[p]
	return ok, nil
}

func (m memKeys) Set(_ context.Context, p routing.ProviderID, k string) error {
	if strings.Contains(k, " ") {
		return errors.New("bad key")
	}
	m[p] = k
	return nil
}

func (m memKeys) Delete(_ context.Context, p routing.ProviderID) error {
	if _, ok := m[p]; !ok {
		return errors.New("not found")
	}
	delete(m, p)
	return nil
}

// serve runs the admin handler on a real unix socket. The folder is short
// because socket paths are limited to about 100 bytes.
func serve(t *testing.T, keys memKeys) *admin.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao-admin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	ln, err := admin.Listen(sock, os.Getgid())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	info, err := os.Stat(sock)
	if err != nil || info.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode = %v, %v", info.Mode(), err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Gid) != os.Getgid() {
		t.Fatalf("socket group = %v", info.Sys())
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: admin.New(admin.Options{
		Keys:      keys,
		Providers: func() []string { return []string{"anthropic", "openai"} },
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return admin.NewClient(sock)
}

func TestSetListDeleteKeyOverTheSocket(t *testing.T) {
	keys := memKeys{}
	c := serve(t, keys)
	ctx := context.Background()
	if err := c.SetKey(ctx, "openai", "test-key-1"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if keys["openai"] != "test-key-1" {
		t.Fatalf("stored = %q", keys["openai"])
	}
	list, err := c.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if !list["openai"] || list["anthropic"] {
		t.Fatalf("list = %v", list)
	}
	if err := c.DeleteKey(ctx, "openai"); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}
	if _, ok := keys["openai"]; ok {
		t.Fatal("key not deleted")
	}
}

func TestRefusals(t *testing.T) {
	c := serve(t, memKeys{})
	ctx := context.Background()
	if err := c.SetKey(ctx, "nope", "k"); !errors.Is(err, admin.ErrUnknownProvider) {
		t.Fatalf("unknown provider error = %v", err)
	}
	if err := c.SetKey(ctx, "openai", "two words"); err == nil || errors.Is(err, admin.ErrUnknownProvider) {
		t.Fatalf("bad key error = %v", err)
	}
	if err := c.DeleteKey(ctx, "openai"); err == nil {
		t.Fatal("deleting a missing key succeeded")
	}
	if err := c.DeleteKey(ctx, "nope"); !errors.Is(err, admin.ErrUnknownProvider) {
		t.Fatalf("delete unknown error = %v", err)
	}
}

func TestClientWithoutServer(t *testing.T) {
	c := admin.NewClient(filepath.Join(t.TempDir(), "none.sock"))
	if err := c.SetKey(context.Background(), "openai", "k"); err == nil {
		t.Fatal("SetKey without a server succeeded")
	}
	if _, err := c.Keys(context.Background()); err == nil {
		t.Fatal("Keys without a server succeeded")
	}
}

func TestListenReplacesAStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "ao-admin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "admin.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := admin.Listen(sock, admin.KeepGroup)
	if err != nil {
		t.Fatalf("Listen over a stale file: %v", err)
	}
	_ = ln.Close()
	if _, err := admin.Listen(filepath.Join(dir, "no", "such", "admin.sock"), admin.KeepGroup); err == nil {
		t.Fatal("Listen in a missing folder succeeded")
	}
}
