// SPDX-License-Identifier: MIT

package cli_test

import (
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

// TestParity_EveryCommandMapsToExactlyOneOperation fails when the contract
// grows without a command: the tree is built from the embedded contract, and
// this test checks it against the operation list.
func TestParity_EveryCommandMapsToExactlyOneOperation(t *testing.T) {
	t.Parallel()
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	root := cli.NewRoot(cat, "test", cli.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard})

	leaves := map[string]*cobra.Command{}
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		if len(c.Commands()) == 0 {
			leaves[strings.Join(path, " ")] = c
			return
		}
		for _, sub := range c.Commands() {
			walk(sub, append(path[:len(path):len(path)], sub.Name()))
		}
	}
	for _, sub := range root.Commands() {
		walk(sub, []string{sub.Name()})
	}

	want := map[string]string{} // command path to operationId
	for _, c := range cat.Commands() {
		path := c.Group + " " + c.Name
		if prev, dup := want[path]; dup {
			t.Errorf("%s and %s are both %q", prev, c.Operation.ID, path)
		}
		want[path] = c.Operation.ID
	}
	var missing []string
	for path, id := range want {
		cmd, ok := leaves[path]
		if !ok {
			missing = append(missing, id+" ("+path+")")
			continue
		}
		if !strings.Contains(cmd.Long, c2api(cat, id)) {
			t.Errorf("command %q does not name its operation %s", path, id)
		}
		if cmd.Short == "" {
			t.Errorf("command %q has no summary", path)
		}
		delete(leaves, path)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("operations with no command: %v", missing)
	}
	for path := range leaves {
		if path != "version" && path != "mcp" {
			t.Errorf("command %q maps to no operation", path)
		}
	}
	if len(want) < 100 {
		t.Errorf("only %d commands, want one per API operation", len(want))
	}
}

func c2api(cat *app.Catalog, id string) string {
	for _, c := range cat.Commands() {
		if c.Operation.ID == id {
			return c.Operation.Method + " " + c.Operation.Path
		}
	}
	return id
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
