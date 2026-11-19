// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	httpadapter "github.com/SDK-E/agentsone/internal/audit/adapters/http"
	"github.com/SDK-E/agentsone/internal/audit/adapters/memory"
	"github.com/SDK-E/agentsone/internal/audit/app"
	"github.com/SDK-E/agentsone/internal/contract/api/auditapi"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func serve(t *testing.T, entries int) http.Handler {
	t.Helper()
	log := app.NewLog(memory.New(), coretest.NewClock(coretest.Epoch))
	for range entries {
		err := log.Append(context.Background(), core.AuditEntry{
			Actor: "agent:founder", Action: "agent.hire", Target: "agent:builder",
			Detail: json.RawMessage(`{"template":"small-seed/builder"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	auditapi.Group(httpadapter.New(log)).Register(mux, core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	})
	return mux
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

type page struct {
	Items []struct {
		Seq int `json:"seq"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func decodePage(t *testing.T, rec *httptest.ResponseRecorder) page {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var p page
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListAuditRecordsMatchesTheContract(t *testing.T) {
	p := decodePage(t, get(t, serve(t, 3), "/v1/audit"))
	if len(p.Items) != 3 || p.Items[0].Seq != 1 || p.NextCursor != nil {
		t.Fatalf("page %+v", p)
	}
}

func TestListAuditRecordsPagesAfterASequence(t *testing.T) {
	p := decodePage(t, get(t, serve(t, 3), "/v1/audit?after_seq=1&limit=1"))
	if len(p.Items) != 1 || p.Items[0].Seq != 2 {
		t.Fatalf("page %+v", p)
	}
}

func TestListAuditRecordsOnAnEmptyLog(t *testing.T) {
	if p := decodePage(t, get(t, serve(t, 0), "/v1/audit")); len(p.Items) != 0 {
		t.Fatalf("page %+v", p)
	}
}

// getUnchecked skips contract validation: the contract does not declare 501
// responses, which stubs and unbuilt operations answer.
func getUnchecked(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestUnsupportedFiltersAnswerNotImplemented(t *testing.T) {
	if rec := getUnchecked(serve(t, 1), "/v1/audit?action=agent.*"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestOperationsNotBuiltYetAnswerNotImplemented(t *testing.T) {
	if rec := getUnchecked(serve(t, 0), "/v1/audit/stream"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d", rec.Code)
	}
}
