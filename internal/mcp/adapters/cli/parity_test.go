// SPDX-License-Identifier: MIT

package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// contractOp is one operation as the contract states it. The list is read
// from the contract document itself, not from the catalog under test, so a
// catalog that loses an operation cannot hide it.
type contractOp struct {
	ID, Method, Path, Tag string
	Stream, WebSocket     bool
}

// contractOps reads every operation from an OpenAPI document.
func contractOps(t *testing.T, spec []byte) []contractOp {
	t.Helper()
	var doc struct {
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatalf("read the contract: %v", err)
	}
	base := ""
	if len(doc.Servers) > 0 {
		u, err := url.Parse(doc.Servers[0].URL)
		if err != nil {
			t.Fatalf("contract server %q: %v", doc.Servers[0].URL, err)
		}
		base = strings.TrimRight(u.Path, "/")
	}
	var ops []contractOp
	for path, item := range doc.Paths {
		for _, method := range []string{"get", "put", "post", "delete", "patch"} {
			raw, ok := item[method]
			if !ok {
				continue
			}
			var o struct {
				ID        string          `json:"operationId"`
				Tags      []string        `json:"tags"`
				Stream    json.RawMessage `json:"x-stream"`
				WebSocket json.RawMessage `json:"x-websocket"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				t.Fatalf("read %s %s: %v", method, path, err)
			}
			if o.ID == "" || len(o.Tags) == 0 {
				t.Fatalf("%s %s has no operationId or no tag", method, path)
			}
			ops = append(ops, contractOp{
				ID: o.ID, Method: strings.ToUpper(method), Path: base + path, Tag: o.Tags[0],
				Stream: o.Stream != nil, WebSocket: o.WebSocket != nil,
			})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
	return ops
}

// The documented exclusions, as rules about the contract (see
// domain.ExcludeFromCommands and domain.ExcludeFromTools): the mcp transport,
// the host door, a WebSocket upgrade, and for tools a stream.
func (o contractOp) wantsCommand() bool { return o.Tag != "mcp" && o.Tag != "host" && !o.WebSocket }
func (o contractOp) wantsTool() bool    { return o.wantsCommand() && !o.Stream }

// commandsByAPI maps "METHOD path" to the commands of the tree that say they
// run that operation. Every command help ends with "API: METHOD path".
func commandsByAPI(root *cobra.Command) map[string][]string {
	out := map[string][]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, line := range strings.Split(c.Long, "\n") {
			if call, ok := strings.CutPrefix(line, "API: "); ok {
				out[call] = append(out[call], c.CommandPath())
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

// parityGaps lists every contract operation that has no agentctl command or
// no MCP tool in root and cat, each as a sentence.
func parityGaps(ops []contractOp, root *cobra.Command, cat *app.Catalog) []string {
	byAPI := commandsByAPI(root)
	var gaps []string
	for _, op := range ops {
		if op.wantsCommand() && len(byAPI[op.Method+" "+op.Path]) != 1 {
			gaps = append(gaps, fmt.Sprintf("%s has %d agentctl commands, want 1", op.ID, len(byAPI[op.Method+" "+op.Path])))
		}
		if op.wantsTool() {
			if tool, ok := cat.Tool(domain.ToolName(op.ID)); !ok || tool.Operation.ID != op.ID {
				gaps = append(gaps, op.ID+" has no MCP tool "+domain.ToolName(op.ID))
			}
		}
	}
	return gaps
}

func rootFor(t *testing.T, spec []byte) (*cobra.Command, *app.Catalog) {
	t.Helper()
	cat, err := app.NewCatalog(spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return cli.NewRoot(cat, "test", cli.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}), cat
}

// TestParity_EveryContractOperationHasACommandAndATool fails when the
// contract grows (or the mapping shrinks) and an operation has no agentctl
// command or no MCP tool.
func TestParity_EveryContractOperationHasACommandAndATool(t *testing.T) {
	t.Parallel()
	ops := contractOps(t, api.Spec)
	root, cat := rootFor(t, api.Spec)

	if len(ops) != len(api.Routes) {
		t.Errorf("the contract has %d operations, the route table %d", len(ops), len(api.Routes))
	}
	if len(ops) < 100 {
		t.Errorf("only %d operations read from the contract, want every one", len(ops))
	}
	if gaps := parityGaps(ops, root, cat); len(gaps) > 0 {
		t.Errorf("operations without a command or tool:\n%s", strings.Join(gaps, "\n"))
	}
}

// The test above is only worth having if it can fail. Take the chat operation
// out of the commands, then add an operation the commands never heard of: both
// must show.
func TestParity_ReportsAnOperationThatHasNoCommandOrTool(t *testing.T) {
	t.Parallel()
	ops := contractOps(t, api.Spec)

	var doc map[string]any
	if err := json.Unmarshal(api.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	chat, _ := paths["/agents/{agent_name}/chat"].(map[string]any)
	if _, ok := chat["post"]; !ok {
		t.Fatal("the contract has no POST /agents/{agent_name}/chat to take out")
	}
	delete(chat, "post")
	cut, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	root, cat := rootFor(t, cut)

	gaps := strings.Join(parityGaps(ops, root, cat), "\n")
	for _, want := range []string{"sendChatMessage has 0 agentctl commands", "sendChatMessage has no MCP tool send_chat_message"} {
		if !strings.Contains(gaps, want) {
			t.Errorf("gaps = %q, want it to say %q", gaps, want)
		}
	}

	extra := append(slices.Clone(ops), contractOp{ID: "danceAgent", Method: "POST", Path: "/v1/agents/{agent_name}/dance", Tag: "agents"})
	full, fullCat := rootFor(t, api.Spec)
	if gaps := parityGaps(extra, full, fullCat); len(gaps) != 2 || !strings.HasPrefix(gaps[0], "danceAgent") {
		t.Errorf("a new operation is not reported as a gap: %q", gaps)
	}
}

// Every command runs one operation, and the tree holds nothing else but
// groups and the built-ins.
func TestParity_NoCommandWithoutAnOperation(t *testing.T) {
	t.Parallel()
	ops := contractOps(t, api.Spec)
	root, _ := rootFor(t, api.Spec)

	known := map[string]bool{}
	for _, op := range ops {
		known[op.Method+" "+op.Path] = true
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) == 0 {
			switch c.CommandPath() {
			case "agentctl version", "agentctl mcp", "agentctl help", "agentctl completion":
				return
			}
			call := ""
			for _, line := range strings.Split(c.Long, "\n") {
				if v, ok := strings.CutPrefix(line, "API: "); ok {
					call = v
				}
			}
			if !known[call] {
				t.Errorf("command %q runs %q, which is no contract operation", c.CommandPath(), call)
			}
			if c.Short == "" {
				t.Errorf("command %q has no summary", c.CommandPath())
			}
			return
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

func TestParity_NoGroupShadowsABuiltInCommand(t *testing.T) {
	t.Parallel()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cat.Commands() {
		switch c.Group {
		case "version", "mcp", "help", "completion":
			t.Errorf("group %q (%s) shadows a built-in command", c.Group, c.Operation.ID)
		}
	}
}
