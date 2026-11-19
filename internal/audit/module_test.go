// SPDX-License-Identifier: MIT

package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SDK-E/agentsone/internal/audit"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleLifecycle(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := audit.New(deps)
	if m.Name() != "audit" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "audit")

	ports := core.NewPorts()
	m.Provide(ports)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	entry := core.AuditEntry{Actor: "system:agentsoned", Action: "machine.boot", Target: "machine"}
	if err := ports.Audit.Append(ctx, entry); err != nil {
		t.Fatalf("append through the provided port: %v", err)
	}

	rec := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rec.Code, rec.Body)
	}
}
