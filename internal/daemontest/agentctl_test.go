// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/daemontest"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

// Integration tests: agentctl against the real daemon. A person chats with an
// agent from the command line, and reads the live list as text.

// agentctl runs agentctl as the founder would outside the society: with a tool
// token, on the daemon's TCP listener. It returns what the command printed.
func agentctl(t *testing.T, d *daemontest.Daemon, token string, args ...string) string {
	t.Helper()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), daemontest.Timeout)
	defer cancel()
	var out, errOut bytes.Buffer
	full := append([]string{"--url", d.TCPAddr(), "--token", token}, args...)
	code := cli.Run(ctx, cat, "test", full, cli.IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, cli.WithTagDescriptions(api.Spec))
	if code != cli.ExitOK {
		t.Fatalf("agentctl %s: exit %d, stderr %q", strings.Join(args, " "), code, errOut.String())
	}
	return out.String()
}

func founderToken(t *testing.T, d *daemontest.Daemon) string {
	t.Helper()
	founder, err := d.Ports.Agents.GetAgent(asDaemon(), "founder")
	if err != nil {
		t.Fatalf("the founder: %v", err)
	}
	scopes, err := app.RunnerScopes()
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	tok, err := d.Ports.Tokens.Issue(asDaemon(), core.Caller{Kind: core.CallerAgent, Name: founder.Name, UID: founder.UID, IsRoot: founder.IsRoot}, scopes, time.Hour)
	if err != nil {
		t.Fatalf("issue the founder's token: %v", err)
	}
	return tok.Value
}

// The chat command sends the message to the agent's chat; the agent answers
// as it does to a message from the interface, and the answer reads back
// through the other chat commands.
func TestAgentctl_ChatWithAnAgentFromTheCommandLine(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("bo")
	token := founderToken(t, d)

	replies := alice.Stream("/agents/bo/chat/stream")
	defer replies.Close()
	sent := agentctl(t, d, token, "agents", "chat", "bo", "--text", "hello from the command line", "--json")

	var msg api.ChatMessage
	if err := json.Unmarshal([]byte(sent), &msg); err != nil {
		t.Fatalf("agentctl agents chat --json printed %q, want the chat message: %v", sent, err)
	}
	if msg.Text != "hello from the command line" || msg.Agent != "bo" {
		t.Fatalf("the chat message = %+v, want the text sent to bo", msg)
	}
	replies.Wait("bo's answer to the command line", func(raw json.RawMessage) bool {
		var f api.ChatMessageFrame
		return json.Unmarshal(raw, &f) == nil && f.Kind == api.ChatMessageFrameKindMessage &&
			f.Message.Author == "agent:bo" && f.Message.Text == "echo: hello from the command line"
	})

	// The same chat reads back in plain words through the short name for the history.
	history := agentctl(t, d, token, "agents", "chat-history", "bo")
	for _, want := range []string{"hello from the command line", "echo: hello from the command line"} {
		if !strings.Contains(history, want) {
			t.Errorf("agentctl agents chat-history bo does not show %q:\n%s", want, history)
		}
	}
}

// The live list prints each agent's one-line as its text, and --json still
// gives the whole thing.
func TestAgentctl_TheLiveListShowsTheOneLineAsText(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("bo")
	alice.Chat("bo", "keep the lights on")
	token := founderToken(t, d)

	table := agentctl(t, d, token, "router", "list-live-list")
	for _, raw := range []string{`{"text"`, "updated_at"} {
		if strings.Contains(table, raw) {
			t.Errorf("the live list prints the one-line as JSON (%s):\n%s", raw, table)
		}
	}
	// The table cuts the text to the line, so only its start is certain.
	if !strings.Contains(table, "Goal: echo what I am told. Now: echoing") {
		t.Errorf("the live list does not show bo's one-line text:\n%s", table)
	}

	var list api.LiveList
	if err := json.Unmarshal([]byte(agentctl(t, d, token, "router", "list-live-list", "--json")), &list); err != nil {
		t.Fatalf("--json output is not the live list: %v", err)
	}
	var found bool
	for _, e := range list.Items {
		found = found || (e.Agent == "bo" && e.OneLine != nil && strings.Contains(e.OneLine.Text, "keep the lights on"))
	}
	if !found {
		t.Errorf("--json lost bo's one-line: %+v", list.Items)
	}
}
