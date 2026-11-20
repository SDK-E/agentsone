// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// SDK-2008: each human opens their own editor inside the interface. The
// daemon starts the editor server as the human's own Linux user, on a Unix
// socket only (no port), and its proxy under /ide/<human>/ carries only that
// signed-in human there: another human reaches neither the page nor the
// controls. The harness's editor stands in for the VSCodium server; the
// image job (image/machine/test/editor.test.sh) and the e2e ide stage run
// the real one, an Open VSX extension install included.
func TestIDE_EachHumanOpensOnlyTheirOwnEditor(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	alice.Do(http.MethodPost, "/humans", api.HumanCreate{Name: "bob", DisplayName: "Bob", Password: new(daemontest.Password("bob"))}, nil)
	bob := d.LogIn("bob")

	var srv api.IdeServer
	alice.Do(http.MethodGet, "/ide/servers/alice", nil, &srv)
	if srv.State != api.IdeServerStateStopped {
		t.Fatalf("alice's editor before she starts it = %s, want stopped", srv.State)
	}
	alice.Do(http.MethodPut, "/ide/servers/alice", nil, &srv)
	srv = waitRunning(t, alice, "alice")
	if srv.Url == nil || *srv.Url != "/ide/alice/" {
		t.Fatalf("alice's editor url = %v, want /ide/alice/", srv.Url)
	}

	// It runs as alice's own Linux user, on a socket, never on a port.
	runs := d.Editors()
	if len(runs) != 1 {
		t.Fatalf("editors started = %d, want 1 (alice's): %+v", len(runs), runs)
	}
	run := runs[0]
	if run.User != "h_alice" || run.UID == 0 || run.Home != "/home/humans/alice" {
		t.Errorf("alice's editor runs as %s (uid %d, home %s), want h_alice with her own uid and home", run.User, run.UID, run.Home)
	}
	if !slices.Contains(run.Args, "--socket-path") || !slices.Contains(run.Args, "/ide/alice") {
		t.Errorf("alice's editor args = %v, want her socket and base path /ide/alice", run.Args)
	}
	for _, a := range run.Args {
		if a == "--port" || a == "--host" || strings.HasPrefix(a, "--port=") || strings.HasPrefix(a, "--host=") {
			t.Errorf("alice's editor args = %v: it listens on a port, want its socket only", run.Args)
		}
	}

	// Alice opens it through the daemon; the editor never sees her session.
	code, header, body := alice.Page("/ide/alice/")
	if code != http.StatusOK || !strings.Contains(body, "editor of h_alice") {
		t.Fatalf("alice opens /ide/alice/: status %d: %q, want her editor", code, body)
	}
	if got := header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("alice's editor X-Frame-Options = %q, want SAMEORIGIN (framed only by the interface)", got)
	}
	for _, h := range d.EditorRequests() {
		if h.Get("Cookie") != "" || h.Get("Authorization") != "" {
			t.Errorf("alice's editor saw cookie %q and authorization %q, want neither", h.Get("Cookie"), h.Get("Authorization"))
		}
	}

	// Bob reaches neither her page nor her controls, and her editor never
	// hears of him.
	seen := len(d.EditorRequests())
	if code, _, body := bob.Page("/ide/alice/"); code != http.StatusForbidden {
		t.Errorf("bob opens /ide/alice/: status %d: %q, want 403", code, body)
	}
	if code, _, body := bob.Page("/ide/alice/static/file.js"); code != http.StatusForbidden {
		t.Errorf("bob opens a file of alice's editor: status %d: %q, want 403", code, body)
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		if code, raw := bob.Try(m, "/ide/servers/alice", nil); code != http.StatusForbidden {
			t.Errorf("bob %s alice's editor: status %d: %s, want 403", m, code, raw)
		}
	}
	if n := len(d.EditorRequests()); n != seen {
		t.Errorf("alice's editor got %d requests while bob tried, want none", n-seen)
	}
	// Without a session nobody gets in either.
	if code, _, body := d.Anonymous().Page("/ide/alice/"); code != http.StatusUnauthorized {
		t.Errorf("no session opens /ide/alice/: status %d: %q, want 401", code, body)
	}
	alice.Do(http.MethodGet, "/ide/servers/alice", nil, &srv)
	if srv.State != api.IdeServerStateRunning {
		t.Errorf("alice's editor after bob tried = %s, want still running", srv.State)
	}

	// Bob's own editor is his, as his own user, apart from hers.
	bob.Do(http.MethodPut, "/ide/servers/bob", nil, nil)
	waitRunning(t, bob, "bob")
	if code, _, body := bob.Page("/ide/bob/"); code != http.StatusOK || !strings.Contains(body, "editor of h_bob") {
		t.Errorf("bob opens /ide/bob/: status %d: %q, want his editor", code, body)
	}
	runs = d.Editors()
	if len(runs) != 2 || runs[1].User != "h_bob" || runs[1].UID == runs[0].UID || runs[1].Socket == runs[0].Socket {
		t.Errorf("editors = %+v, want bob's as h_bob with his own uid and socket", runs)
	}

	// Alice stops hers; the proxy then says so instead of reaching it.
	alice.Do(http.MethodDelete, "/ide/servers/alice", nil, nil)
	alice.Do(http.MethodGet, "/ide/servers/alice", nil, &srv)
	if srv.State != api.IdeServerStateStopped || srv.Url != nil {
		t.Errorf("alice's editor after stop = %s (url %v), want stopped without url", srv.State, srv.Url)
	}
	if code, _, _ := alice.Page("/ide/alice/"); code != http.StatusServiceUnavailable {
		t.Errorf("alice opens her stopped editor: status %d, want 503", code)
	}
}

// waitRunning polls the human's editor until it runs.
func waitRunning(t *testing.T, h *daemontest.Client, human string) api.IdeServer {
	t.Helper()
	deadline := time.Now().Add(daemontest.Timeout)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var srv api.IdeServer
		h.Do(http.MethodGet, "/ide/servers/"+human, nil, &srv)
		switch {
		case srv.State == api.IdeServerStateRunning:
			return srv
		case srv.State == api.IdeServerStateFailed:
			t.Fatalf("%s's editor failed: %v", human, deref(srv.Error))
		case time.Now().After(deadline):
			t.Fatalf("%s's editor is %s after %s, want running", human, srv.State, daemontest.Timeout)
		}
		<-tick.C
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
