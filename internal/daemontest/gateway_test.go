// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration tests: a model turn of the real openai runner goes through the
// real model gateway to a fake model provider, and back. The runner reaches
// the gateway at the address of its welcome, with the token the daemon
// signed, so a wrong gateway address, path or key fails here.

// startWithModel starts the daemon behind the gateway and the fake model,
// signs alice in and hires scribe, an openai agent.
func startWithModel(t *testing.T) (*daemontest.FakeModel, *daemontest.Client) {
	t.Helper()
	model := daemontest.NewFakeModel(t)
	d := daemontest.Start(t, daemontest.WithModelProvider(model.URL(), model.Key()))
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireRunner("scribe", "openai")
	return model, alice
}

func TestModelTurn_AChatGoesThroughTheGatewayAndTheAnswerIsStored(t *testing.T) {
	t.Parallel()
	model, alice := startWithModel(t)
	model.Script(daemontest.ModelReply{Text: "Two pages by Friday."})

	reply := alice.Chat("scribe", "When is the report done?")

	if reply.Text != "Two pages by Friday." || reply.Author != "agent:scribe" {
		t.Fatalf("reply = %q by %q, want the model's answer by agent:scribe", reply.Text, reply.Author)
	}
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("the model got %d calls, want 1: %+v", len(calls), calls)
	}
	c := calls[0]
	// The gateway picked the provider's model for the runner's weight.
	if c.Path != "/v1/chat/completions" || c.Model != daemontest.ModelNormal || !c.Scripted {
		t.Fatalf("call = %s for %q (scripted %v), want /v1/chat/completions for %q, scripted", c.Path, c.Model, c.Scripted, daemontest.ModelNormal)
	}
	if !strings.Contains(c.Prompt, "When is the report done?") {
		t.Fatalf("the model was asked %q, want the chat text in it", c.Prompt)
	}
}

func TestModelTurn_TheProviderSeesTheGatewaysKeyAndNeverTheAgentsToken(t *testing.T) {
	t.Parallel()
	model, alice := startWithModel(t)

	alice.Chat("scribe", "Hello")

	calls := model.Calls()
	if len(calls) == 0 {
		t.Fatal("the model got no call")
	}
	for i, c := range calls {
		if !c.ProviderKey {
			t.Errorf("call %d did not carry the provider key the gateway keeps", i)
		}
		if c.AgentToken {
			t.Errorf("call %d carried an agent's gateway token to the provider", i)
		}
		if c.Headers.Get("Authorization") != "" {
			t.Errorf("call %d: the fake kept a secret header", i)
		}
	}
}

// A restarted daemon listens on a new port and still gives its runners the
// same gateway and tool server.
func TestModelTurn_ARestartedDaemonStillGoesThroughTheGateway(t *testing.T) {
	t.Parallel()
	model := daemontest.NewFakeModel(t)
	d := daemontest.Start(t, daemontest.WithModelProvider(model.URL(), model.Key()))
	d.SignInHuman("alice")
	d.AsAgent("founder").HireRunner("scribe", "openai")

	d = d.Restart()
	model.Script(daemontest.ModelReply{Text: "Back after the restart."})

	reply := d.LogIn("alice").Chat("scribe", "Are you there?")

	if reply.Text != "Back after the restart." {
		t.Fatalf("reply = %q, want the model's answer", reply.Text)
	}
	calls := model.Calls()
	if len(calls) != 1 || !calls[0].ProviderKey || calls[0].Model != daemontest.ModelNormal {
		t.Fatalf("the model got %+v, want one call for %q with the provider key", calls, daemontest.ModelNormal)
	}
}

func TestModelTurn_AToolCallReachesTheMachineAndItsResultGoesBack(t *testing.T) {
	t.Parallel()
	model, alice := startWithModel(t)
	model.Script(
		daemontest.ModelReply{ToolCalls: []daemontest.ModelToolCall{{ID: "call-1", Name: "list_agents", Arguments: `{}`}}},
		daemontest.ModelReply{Text: "The founder leads."},
	)

	reply := alice.Chat("scribe", "Who leads?")

	if reply.Text != "The founder leads." {
		t.Fatalf("reply = %q, want the model's answer after the tool call", reply.Text)
	}
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("the model got %d calls, want 2: %+v", len(calls), calls)
	}
	if !slices.Contains(calls[0].Tools, "list_agents") {
		t.Fatalf("the runner offered %v, want list_agents from the tool server", calls[0].Tools)
	}
	if len(calls[1].ToolResults) != 1 || !strings.Contains(calls[1].ToolResults[0], "founder") {
		t.Fatalf("tool results = %q, want the society's agents from the machine", calls[1].ToolResults)
	}
}
