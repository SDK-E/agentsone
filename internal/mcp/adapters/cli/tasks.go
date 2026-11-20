// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
)

// Option changes how NewRoot builds the command tree.
type Option func(*options)

type options struct {
	// tags maps a tag to its description in the contract.
	tags map[string]string
}

// WithTagDescriptions takes the group descriptions from the tags of the API
// description spec, so the help of every group says in plain words what it is
// for. A spec that cannot be read leaves the groups with a plain title.
func WithTagDescriptions(spec []byte) Option {
	return func(o *options) {
		var doc struct {
			Tags []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tags"`
		}
		if err := json.Unmarshal(spec, &doc); err != nil {
			return
		}
		o.tags = map[string]string{}
		for _, t := range doc.Tags {
			o.tags[t.Name] = t.Description
		}
	}
}

// aliases are short names for commands people look for by what they want to
// do. The command keeps its operation name too. A test checks that each key is
// an operation of the contract and that no alias hides another command.
var aliases = map[string][]string{
	"sendChatMessage":  {"chat"},
	"listChatMessages": {"chat-history"},
}

// task is one example shown in help: what to type and what it does. A test
// checks that the command exists and takes the arguments and flags shown.
type task struct {
	group string
	usage string
	what  string
}

var tasks = []task{
	{"agents", "agentctl agents list", "list the agents"},
	{"agents", `agentctl agents chat <agent_name> --text "Hello"`, "chat with an agent (same as send-chat-message)"},
	{"agents", "agentctl agents chat-history <agent_name>", "read the chat so far"},
	{"agents", "agentctl agents stream-chat <agent_name>", "follow an agent's chat as it happens"},
	{"agents", "agentctl agents wake <agent_name>", "wake an agent that sleeps"},
	{"router", "agentctl router list-live-list", "see what every agent is doing now"},
	{"tasks", "agentctl tasks list", "list the tasks"},
}

// exampleLines lays out the examples of group, or of every group when it is empty.
func exampleLines(group string) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, t := range tasks {
		if group == "" || t.group == group {
			fmt.Fprintf(tw, "  %s\t# %s\n", t.usage, t.what) //nolint:errcheck // a strings.Builder does not fail
		}
	}
	tw.Flush() //nolint:errcheck,gosec // a strings.Builder does not fail
	return b.String()
}

func rootHelp() string {
	return "agentctl runs one daemon API operation per command, grouped by topic.\n" +
		"By default it talks to " + DefaultURL + ".\n\n" +
		"Common tasks:\n" + exampleLines("") + "\n" +
		"Ask any group for its commands, for example: agentctl agents --help.\n" +
		"Add --json to a command to get the full JSON answer."
}

func groupHelp(tag, about string) string {
	help := about + "."
	if lines := exampleLines(tag); lines != "" {
		help += "\n\nCommon tasks:\n" + lines
	}
	return help
}
