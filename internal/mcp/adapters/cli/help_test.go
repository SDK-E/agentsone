// SPDX-License-Identifier: MIT

package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

// helpOf runs agentctl against the real contract and returns what --help prints.
func helpOf(t *testing.T, args ...string) string {
	t.Helper()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), cat, "test", append(args, "--help"), cli.IO{In: strings.NewReader(""), Out: &out, Err: &errOut}, cli.WithTagDescriptions(api.Spec))
	if code != cli.ExitOK {
		t.Fatalf("agentctl %v --help: exit %d, stderr %q", args, code, errOut.String())
	}
	return out.String()
}

func realRoot(t *testing.T) *cobra.Command {
	t.Helper()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return cli.NewRoot(cat, "test", cli.IO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, cli.WithTagDescriptions(api.Spec))
}

// taskLines are the example lines of a "Common tasks" block in help: each
// starts with "  agentctl ".
func taskLines(help string) []string {
	var out []string
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(line, "  agentctl ") && !strings.Contains(line, "[") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestHelp_RootSaysWhatEachGroupIsFor(t *testing.T) {
	t.Parallel()
	got := helpOf(t)

	// The line comes from the contract's tag description: chat is named there.
	if !strings.Contains(got, "Hire, edit, retire, wake and sleep agents; their one-line, output and chat") {
		t.Errorf("the agents group is not described in plain words:\n%s", got)
	}
	if strings.Contains(got, "Commands for agents") {
		t.Errorf("the agents group has only the placeholder description:\n%s", got)
	}
}

func TestHelp_RootShowsHowToChatWithAnAgent(t *testing.T) {
	t.Parallel()
	got := helpOf(t)

	if !strings.Contains(got, "Common tasks") {
		t.Fatalf("no common tasks in the root help:\n%s", got)
	}
	var chat string
	for _, line := range taskLines(got) {
		if strings.Contains(line, "chat with an agent") {
			chat = line
		}
	}
	if !strings.Contains(chat, "agents send-chat-message <agent_name> --text") {
		t.Errorf("no line shows how to chat with an agent; tasks = %q", taskLines(got))
	}
}

func TestHelp_AgentsGroupShowsHowToChatWithAnAgent(t *testing.T) {
	t.Parallel()
	got := helpOf(t, "agents")

	for _, want := range []string{"send-chat-message", "chat with an agent", "stream-chat", "wake"} {
		if !strings.Contains(got, want) {
			t.Errorf("agents --help does not mention %q:\n%s", want, got)
		}
	}
}

// Every example in help runs a command that exists, with the arguments it takes.
func TestHelp_EveryExampleNamesARealCommand(t *testing.T) {
	t.Parallel()
	root := realRoot(t)
	for _, help := range [][]string{nil, {"agents"}} {
		lines := taskLines(helpOf(t, help...))
		if len(lines) == 0 {
			t.Errorf("agentctl %v --help has no examples", help)
		}
		for _, line := range lines {
			line, _, _ = strings.Cut(line, " # ")
			var path []string
			for _, word := range strings.Fields(strings.TrimPrefix(line, "agentctl ")) {
				if strings.HasPrefix(word, "<") || strings.HasPrefix(word, "-") {
					break
				}
				path = append(path, word)
			}
			cmd, rest, err := root.Find(path)
			if err != nil || len(rest) != 0 || cmd.HasSubCommands() {
				t.Errorf("example %q does not name one command (found %q, left %q, err %v)", line, cmd.CommandPath(), rest, err)
				continue
			}
			for _, word := range strings.Fields(line) {
				if strings.HasPrefix(word, "<") && !strings.Contains(cmd.Use, word) {
					t.Errorf("example %q takes %s but %q takes %q", line, word, cmd.CommandPath(), cmd.Use)
				}
				if flag, ok := strings.CutPrefix(word, "--"); ok && cmd.Flags().Lookup(flag) == nil {
					t.Errorf("example %q uses --%s, which %q does not have", line, flag, cmd.CommandPath())
				}
			}
		}
	}
}

func TestHelp_ChatHasAShortName(t *testing.T) {
	t.Parallel()
	root := realRoot(t)

	for alias, op := range map[string]string{"chat": "POST /v1/agents/{agent_name}/chat", "chat-history": "GET /v1/agents/{agent_name}/chat"} {
		cmd, _, err := root.Find([]string{"agents", alias})
		if err != nil || cmd.Name() == "agents" {
			t.Errorf("agentctl agents %s: not found (%v)", alias, err)
			continue
		}
		if !strings.Contains(cmd.Long, op) {
			t.Errorf("agentctl agents %s runs %q, want %s", alias, cmd.Long, op)
		}
	}
}

// A short name must not hide another command: names and aliases are unique in a group.
func TestHelp_NamesAndAliasesAreUniqueInAGroup(t *testing.T) {
	t.Parallel()
	for _, group := range realRoot(t).Commands() {
		seen := map[string]string{}
		for _, c := range group.Commands() {
			for _, name := range append([]string{c.Name()}, c.Aliases...) {
				if prev, dup := seen[name]; dup {
					t.Errorf("%s: %q names both %q and %q", group.Name(), name, prev, c.Name())
				}
				seen[name] = c.Name()
			}
		}
	}
}
