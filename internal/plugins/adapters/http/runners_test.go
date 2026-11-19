// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api/runnersapi"
	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/plugins/adapters/http"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

type fakeRunners struct {
	runners []domain.RunnerEntry
	err     error
}

var _ httpadapter.RunnerService = (*fakeRunners)(nil)

func (f *fakeRunners) List(context.Context) ([]domain.RunnerEntry, error) { return f.runners, f.err }

func (f *fakeRunners) Get(_ context.Context, name string) (domain.RunnerEntry, error) {
	if f.err != nil {
		return domain.RunnerEntry{}, f.err
	}
	for _, r := range f.runners {
		if r.Name == name {
			return r, nil
		}
	}
	return domain.RunnerEntry{}, fmt.Errorf("runner %s: %w", name, domain.ErrNotFound)
}

func newRunners() *fakeRunners {
	return &fakeRunners{runners: []domain.RunnerEntry{
		{Name: "echo", DisplayName: "Echo", Version: "1.0.0", ProtocolVersion: "1.0", Builtin: true, Status: domain.RunnerReady, Description: "Repeats what it hears."},
		{Name: "openai", DisplayName: "OpenAI", Version: "1.0.0", ProtocolVersion: "1.0", Builtin: true, Model: "autoagents/one", Status: domain.RunnerReady, Description: "Chats through the gateway."},
		{Name: "tinker", DisplayName: "Tinker", Version: "0.3.0", ProtocolVersion: "1.0", PluginID: pluginID, Status: domain.RunnerUnavailable, Description: "From a plugin."},
	}}
}

func serveRunners(t *testing.T, svc httpadapter.RunnerService) http.Handler {
	t.Helper()
	return serve(ada(), func(mux *http.ServeMux) {
		runnersapi.Group(httpadapter.NewRunnersHandler(svc)).Register(mux, apiOptions())
	})
}

type runner struct {
	Name            string  `json:"name"`
	DisplayName     string  `json:"display_name"`
	Version         string  `json:"version"`
	ProtocolVersion string  `json:"protocol_version"`
	Builtin         bool    `json:"builtin"`
	PluginID        *string `json:"plugin_id"`
	Model           *string `json:"model"`
	Status          string  `json:"status"`
	Description     string  `json:"description"`
}

type runnerPage struct {
	Items      []runner `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

func TestListRunners_ReturnsBuiltinsAndPluginRunnersInPages(t *testing.T) {
	t.Parallel()
	h := serveRunners(t, newRunners())

	first := decode[runnerPage](t, send(t, h, http.MethodGet, "/v1/runners?limit=2", nil), http.StatusOK)
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first = %+v", first)
	}
	if e := first.Items[0]; e.Name != "echo" || !e.Builtin || e.PluginID != nil || e.Model != nil || e.Status != "ready" {
		t.Errorf("echo = %+v", e)
	}
	if o := first.Items[1]; o.Model == nil || *o.Model != "autoagents/one" {
		t.Errorf("openai = %+v", o)
	}
	second := decode[runnerPage](t, send(t, h, http.MethodGet, "/v1/runners?limit=2&cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("second = %+v", second)
	}
	if p := second.Items[0]; p.PluginID == nil || *p.PluginID != pluginID || p.Builtin || p.Status != "unavailable" {
		t.Errorf("plugin runner = %+v", p)
	}
}

func TestListRunners_NoLimitReturnsEverything(t *testing.T) {
	t.Parallel()
	h := serveRunners(t, newRunners())

	got := decode[runnerPage](t, send(t, h, http.MethodGet, "/v1/runners", nil), http.StatusOK)

	if len(got.Items) != 3 || got.NextCursor != nil {
		t.Fatalf("page = %+v", got)
	}
}

// The contract documents no failure status for listRunners, so the service
// error is checked on the handler directly.
func TestListRunners_ServiceErrorIsReturned(t *testing.T) {
	t.Parallel()
	r := newRunners()
	r.err = fmt.Errorf("registry: %w", domain.ErrNotFound)
	h := httpadapter.NewRunnersHandler(r)

	_, err := h.ListRunners(context.Background(), runnersapi.ListRunnersRequestObject{})

	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v, want core.ErrNotFound", err)
	}
}

func TestGetRunner(t *testing.T) {
	t.Parallel()
	h := serveRunners(t, newRunners())

	got := decode[runner](t, send(t, h, http.MethodGet, "/v1/runners/echo", nil), http.StatusOK)
	if got.Name != "echo" || got.DisplayName != "Echo" || got.ProtocolVersion != "1.0" {
		t.Errorf("runner = %+v", got)
	}
	problemStatus(t, send(t, h, http.MethodGet, "/v1/runners/missing", nil), http.StatusNotFound)
}
