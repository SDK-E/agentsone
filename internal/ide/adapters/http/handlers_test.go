// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contract "github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/ideapi"
	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/ide/adapters/http"
)

var epoch = time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)

func api(t *testing.T, s *servers, c core.Caller) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	h := httpadapter.New(s, httpadapter.Build{Name: "vscodium", Version: "1.143.17232"})
	ideapi.Group(h).Register(mux, core.APIOptions{
		RequestError:  func(w http.ResponseWriter, r *http.Request, err error) { core.WriteError(w, r, err) },
		ResponseError: core.WriteError,
	})
	return door(c, mux)
}

func call(t *testing.T, h http.Handler, method, target string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v %s", method, target, err, rec.Body)
		}
	}
	return rec.Code
}

func TestStatusShowsTheOpenBuildAndEveryHumansServer(t *testing.T) {
	var st contract.IdeStatus
	if code := call(t, api(t, &servers{}, ada), http.MethodGet, "/v1/ide", &st); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if st.Build != "vscodium" || st.Version != "1.143.17232" || st.ActiveBuild != contract.IdeStatusActiveBuildOpen ||
		st.Official.State != contract.OfficialIdeStateNotRequested {
		t.Fatalf("status %+v", st)
	}
	if len(st.Servers) != 2 || st.Servers[0].Url == nil || *st.Servers[0].Url != "/ide/ada/" ||
		st.Servers[0].StartedAt == nil || st.Servers[1].Error == nil || st.Servers[1].Url != nil {
		t.Fatalf("servers %+v", st.Servers)
	}
}

func TestOfficialBuildIsNotRequestedAndCannotBeDownloadedYet(t *testing.T) {
	h := api(t, &servers{}, ada)
	var off contract.OfficialIdeDownload
	if code := call(t, h, http.MethodGet, "/v1/ide/official", &off); code != http.StatusOK || off.State != contract.OfficialIdeStateNotRequested {
		t.Fatalf("official %d %+v", code, off)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/ide/official", strings.NewReader(`{"accept_licence":true}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("download %d", rec.Code)
	}
}

func TestServerOperationsFollowTheOwnerRule(t *testing.T) {
	s := &servers{}
	var srv contract.IdeServer
	if code := call(t, api(t, s, ada), http.MethodPut, "/v1/ide/servers/ada", &srv); code != http.StatusAccepted ||
		srv.State != contract.IdeServerStateStarting || srv.Url != nil {
		t.Fatalf("start %d %+v", code, srv)
	}
	if code := call(t, api(t, s, bob), http.MethodPut, "/v1/ide/servers/ada", nil); code != http.StatusForbidden {
		t.Fatalf("bob starts ada's: %d", code)
	}
	if code := call(t, api(t, s, ada), http.MethodDelete, "/v1/ide/servers/ada", nil); code != http.StatusNoContent {
		t.Fatalf("stop %d", code)
	}
	if code := call(t, api(t, s, bob), http.MethodDelete, "/v1/ide/servers/ada", nil); code != http.StatusForbidden {
		t.Fatalf("bob stops ada's: %d", code)
	}
	if len(s.started) != 1 || len(s.stopped) != 1 {
		t.Fatalf("started %q stopped %q", s.started, s.stopped)
	}
	if code := call(t, api(t, s, bob), http.MethodGet, "/v1/ide/servers/ada", &srv); code != http.StatusOK || srv.State != contract.IdeServerStateStopped {
		t.Fatalf("get %d %+v", code, srv)
	}
	if code := call(t, api(t, s, ada), http.MethodGet, "/v1/ide/servers/eve", nil); code != http.StatusNotFound {
		t.Fatalf("unknown human %d", code)
	}
}

func TestStatusReportsAFailingDirectory(t *testing.T) {
	s := &servers{err: errors.Join(core.ErrUnavailable)}
	if code := call(t, api(t, s, ada), http.MethodGet, "/v1/ide", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", code)
	}
}
