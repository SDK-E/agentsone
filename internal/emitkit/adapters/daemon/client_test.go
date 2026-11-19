// SPDX-License-Identifier: MIT

package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/emitkit/adapters/daemon"
	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// serve starts h on a Unix socket and returns its path.
func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "emk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

func tick(t *testing.T) domain.Draft {
	t.Helper()
	d, err := domain.NewDraft("agentsone.clock.tick", "", map[string]string{"minute": "2026-11-20T00:01:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPublish_PostsTheEventAsTheNamedSource(t *testing.T) {
	t.Parallel()
	var got map[string]any
	var method, path, ctype string
	sock := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, ctype = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))

	err := daemon.New(sock, "clock").Publish(context.Background(), tick(t))

	if err != nil {
		t.Fatalf("Publish error = %v", err)
	}
	if method != http.MethodPost || path != "/v1/events" || ctype != "application/json" {
		t.Fatalf("request = %s %s (%s), want POST /v1/events as JSON", method, path, ctype)
	}
	if got["type"] != "agentsone.clock.tick" || got["source"] != "/emitters/clock" {
		t.Fatalf("body = %v, want type and source set", got)
	}
	if _, has := got["subject"]; has {
		t.Fatalf("body = %v, an empty subject must be left out", got)
	}
	data, _ := got["data"].(map[string]any)
	if data["minute"] != "2026-11-20T00:01:00Z" {
		t.Fatalf("data = %v", got["data"])
	}
}

func TestPublish_SendsTheSubject(t *testing.T) {
	t.Parallel()
	var got map[string]any
	sock := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
	}))
	d := tick(t)
	d.Subject = "/var"

	if err := daemon.New(sock, "disk").Publish(context.Background(), d); err != nil {
		t.Fatalf("Publish error = %v", err)
	}
	if got["subject"] != "/var" {
		t.Fatalf("subject = %v, want /var", got["subject"])
	}
}

func TestPublish_ClassifiesStatuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status   int
		body     string
		rejected bool
	}{
		{http.StatusBadRequest, `{"detail":"data does not match"}`, true},
		{http.StatusForbidden, `{"detail":"source forbidden"}`, true},
		{http.StatusRequestEntityTooLarge, ``, true},
		{http.StatusTooManyRequests, `{"detail":"slow down"}`, false},
		{http.StatusInternalServerError, `oops`, false},
		{http.StatusServiceUnavailable, ``, false},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			sock := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))

			err := daemon.New(sock, "clock").Publish(context.Background(), tick(t))

			if err == nil {
				t.Fatal("Publish error = nil, want an error")
			}
			if got := errors.Is(err, app.ErrRejected); got != tc.rejected {
				t.Fatalf("Is(ErrRejected) = %v for %d, want %v (error: %v)", got, tc.status, tc.rejected, err)
			}
		})
	}
}

func TestPublish_ErrorMentionsTheDaemonsReason(t *testing.T) {
	t.Parallel()
	sock := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"data does not match the schema"}`))
	}))
	err := daemon.New(sock, "clock").Publish(context.Background(), tick(t))
	if err == nil || !strings.Contains(err.Error(), "data does not match the schema") {
		t.Fatalf("error = %v, want the daemon's detail in it", err)
	}
}

func TestPublish_UnreachableDaemonIsTransient(t *testing.T) {
	t.Parallel()
	err := daemon.New(filepath.Join(t.TempDir(), "none.sock"), "clock").Publish(context.Background(), tick(t))
	if err == nil || errors.Is(err, app.ErrRejected) {
		t.Fatalf("error = %v, want a transient error", err)
	}
}
