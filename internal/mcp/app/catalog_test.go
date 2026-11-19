// SPDX-License-Identifier: MIT

package app_test

import (
	"errors"
	"sort"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func TestNewCatalog_BuildsToolsAndCommandsFromTheSpec(t *testing.T) {
	t.Parallel()

	cat, err := app.NewCatalog([]byte(miniSpec))

	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	if _, ok := cat.Tool("hire_agent"); !ok {
		t.Error("tool hire_agent missing")
	}
	if len(cat.Tools()) != 2 {
		t.Errorf("tools = %d, want 2 (hireAgent and getAgent, the stream is left out)", len(cat.Tools()))
	}
	if why := cat.ToolExclusions()["streamAgentOutput"]; why != domain.ExcludedStream {
		t.Errorf("stream exclusion = %q", why)
	}
	if len(cat.Commands()) != 3 {
		t.Errorf("commands = %d, want 3 (the stream is a command)", len(cat.Commands()))
	}
}

func TestNewCatalog_RefusesASpecThatCannotBeMapped(t *testing.T) {
	t.Parallel()

	_, err := app.NewCatalog([]byte(`{`))

	if !errors.Is(err, domain.ErrInvalidSpec) {
		t.Fatalf("NewCatalog() error = %v, want ErrInvalidSpec", err)
	}
}

func TestNewCatalog_RefusesTwoOperationsWithTheSameToolName(t *testing.T) {
	t.Parallel()
	doc := `{"paths":{"/a":{"get":{"operationId":"listA","tags":["t"],"responses":{}}},"/b":{"get":{"operationId":"list_a","tags":["t"],"responses":{}}}}}`

	_, err := app.NewCatalog([]byte(doc))

	if !errors.Is(err, app.ErrDuplicateTool) {
		t.Fatalf("NewCatalog() error = %v, want ErrDuplicateTool", err)
	}
}

// Parity: the contract grows, the mapping must grow with it. This test fails
// the build when an operation has neither a tool nor a documented exclusion,
// or neither a command nor a documented exclusion.
func TestParity_EveryOperationHasOneToolAndOneCommandOrAnExclusion(t *testing.T) {
	t.Parallel()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog(real spec) error = %v", err)
	}

	toolOps := map[string]int{}
	for _, tool := range cat.Tools() {
		toolOps[tool.Operation.ID]++
	}
	cmdOps := map[string]int{}
	for _, c := range cat.Commands() {
		cmdOps[c.Operation.ID]++
	}

	for _, r := range api.Routes {
		wantTool := 1
		if _, excluded := cat.ToolExclusions()[r.OperationID]; excluded {
			wantTool = 0
		}
		if toolOps[r.OperationID] != wantTool {
			t.Errorf("%s has %d tools, want %d", r.OperationID, toolOps[r.OperationID], wantTool)
		}
		wantCmd := 1
		if _, excluded := cat.CommandExclusions()[r.OperationID]; excluded {
			wantCmd = 0
		}
		if cmdOps[r.OperationID] != wantCmd {
			t.Errorf("%s has %d commands, want %d", r.OperationID, cmdOps[r.OperationID], wantCmd)
		}
	}
	if got, want := len(toolOps)+len(cat.ToolExclusions()), len(api.Routes); got != want {
		t.Errorf("tools + exclusions = %d, operations = %d", got, want)
	}
	if got, want := len(cmdOps)+len(cat.CommandExclusions()), len(api.Routes); got != want {
		t.Errorf("commands + exclusions = %d, operations = %d", got, want)
	}
}

func TestParity_ExclusionsAreTheDocumentedOnes(t *testing.T) {
	t.Parallel()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}

	for _, r := range api.Routes {
		why, excluded := cat.ToolExclusions()[r.OperationID]
		switch {
		case r.Tag == "mcp" && why != domain.ExcludedProtocol:
			t.Errorf("%s: want protocol exclusion, got %q", r.OperationID, why)
		case r.Tag == "host" && why != domain.ExcludedHostDoor:
			t.Errorf("%s: want host door exclusion, got %q", r.OperationID, why)
		case r.Tag != "mcp" && r.Tag != "host" && excluded && !r.Stream && !r.WebSocket:
			t.Errorf("%s excluded as %q with no stream or websocket", r.OperationID, why)
		}
	}
}

func TestParity_NamesAreUniqueAndStable(t *testing.T) {
	t.Parallel()
	first, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	second, _ := app.NewCatalog(api.Spec)

	seen := map[string]bool{}
	for _, c := range first.Commands() {
		key := c.Group + " " + c.Name
		if seen[key] {
			t.Errorf("command %q is defined twice", key)
		}
		seen[key] = true
	}
	names := func(c *app.Catalog) []string {
		var out []string
		for _, tool := range c.Tools() {
			out = append(out, tool.Name)
		}
		return out
	}
	if !sort.StringsAreSorted(names(first)) {
		t.Error("tool list is not sorted by name")
	}
	a, b := names(first), names(second)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tool order differs at %d", i)
		}
	}
}
