// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration tests: the real claude runner on a real daemon, on a machine
// where the Claude engine is not installed. That is every machine until a
// human turns the engine on, so the runner must start, answer each turn
// with the licence message, call no model and stay up.

func TestClaudeRunner_WithoutTheEngineATurnSaysSoAndCallsNoModel(t *testing.T) {
	t.Parallel()
	model := daemontest.NewFakeModel(t)
	d := daemontest.Start(t, daemontest.WithModelProvider(model.URL(), model.Key()))
	alice := d.SignInHuman("alice")
	hired := d.AsAgent("founder").HireRunner("coder", "claude")
	if hired.Runner != "claude" {
		t.Fatalf("hired runner = %q, want claude: the image ships the claude runner", hired.Runner)
	}

	alice.Do(http.MethodPost, "/agents/"+url.PathEscape("coder")+"/chat", api.ChatMessageCreate{Text: "Fix the build"}, nil)

	d.WaitLog("the turn error of the claude runner", 1, licenceError...)
	if calls := model.Calls(); len(calls) != 0 {
		t.Fatalf("the model got %d calls without an engine, want none: %+v", len(calls), calls)
	}
	if errs := d.RunnerErrors(); len(errs) != 0 {
		t.Fatalf("runners stopped with errors: %v", errs)
	}

	// A second turn meets the same wall: the runner is still up and asks
	// for the engine again rather than failing for good.
	alice.Do(http.MethodPost, "/agents/"+url.PathEscape("coder")+"/chat", api.ChatMessageCreate{Text: "Are you there?"}, nil)
	d.WaitLog("the turn error on the second chat", 2, licenceError...)
	if calls := model.Calls(); len(calls) != 0 {
		t.Fatalf("the model got %d calls without an engine, want none: %+v", len(calls), calls)
	}
}

// licenceError are the parts of the daemon's log line for a turn the claude
// runner ends because its engine is not installed: the runner's
// model-unavailable code and its licence message.
var licenceError = []string{"runner error", "code=1020", "The Claude engine is not installed yet"}
