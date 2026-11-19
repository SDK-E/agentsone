// SPDX-License-Identifier: MIT

package memory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
	"github.com/SDK-E/agentsone/internal/memory"
)

func TestModule_OwnsTheMemoryTag(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := memory.New(deps)
	if m.Name() != "memory" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "memory")
}

func TestModule_WritesAndSearchesOnTheRealStore(t *testing.T) {
	deps, fakes := coretest.NewDeps(t)
	fakes.Agents.Add(core.Agent{Name: "founder", IsRoot: true})
	m := memory.New(deps)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	asFounder := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := core.Caller{Kind: core.CallerAgent, Name: "founder", UID: -1, IsRoot: true}
		moduletest.Mount(m).ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), c)))
	})

	write := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/memory",
		strings.NewReader(`{"scope":"common","title":"Disk report","body":"Journal vacuumed."}`))
	req.Header.Set("Content-Type", "application/json")
	asFounder.ServeHTTP(write, req)
	if write.Code != http.StatusCreated {
		t.Fatalf("write status %d: %s", write.Code, write.Body)
	}

	search := httptest.NewRecorder()
	asFounder.ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/v1/memory/search?q=journal", nil))
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "Disk report") {
		t.Fatalf("search status %d: %s", search.Code, search.Body)
	}
}

func TestModule_NobodySignedInIsUnauthorized(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := memory.New(deps)
	rec := httptest.NewRecorder()

	moduletest.Mount(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/memory", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}
