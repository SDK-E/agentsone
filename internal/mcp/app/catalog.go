// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"sort"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// ErrDuplicateTool means two operations map to the same tool name.
var ErrDuplicateTool = errors.New("duplicate tool name")

// Catalog is every tool and command derived from the API description. It is
// built once, from the embedded contract, and never changes. Nothing in it
// is listed by hand.
type Catalog struct {
	tools        []domain.Tool
	byName       map[string]domain.Tool
	commands     []domain.Command
	toolExcluded map[string]domain.Exclusion
	cmdExcluded  map[string]domain.Exclusion
}

// NewCatalog reads the operations from spec and derives the tools and commands.
func NewCatalog(spec []byte) (*Catalog, error) {
	ops, err := domain.ParseOperations(spec)
	if err != nil {
		return nil, err
	}
	c := &Catalog{
		byName:       map[string]domain.Tool{},
		toolExcluded: map[string]domain.Exclusion{},
		cmdExcluded:  map[string]domain.Exclusion{},
	}
	for _, op := range ops {
		if why := domain.ExcludeFromCommands(op); why != domain.NotExcluded {
			c.cmdExcluded[op.ID] = why
		}
		if why := domain.ExcludeFromTools(op); why != domain.NotExcluded {
			c.toolExcluded[op.ID] = why
			continue
		}
		tool, err := domain.NewTool(op)
		if err != nil {
			return nil, err
		}
		if prev, dup := c.byName[tool.Name]; dup {
			return nil, fmt.Errorf("%w: %s and %s are both %q", ErrDuplicateTool, prev.Operation.ID, op.ID, tool.Name)
		}
		c.byName[tool.Name] = tool
		c.tools = append(c.tools, tool)
	}
	sort.Slice(c.tools, func(i, j int) bool { return c.tools[i].Name < c.tools[j].Name })

	if c.commands, err = domain.NewCommands(ops); err != nil {
		return nil, err
	}
	return c, nil
}

// Tools lists every tool, sorted by name.
func (c *Catalog) Tools() []domain.Tool { return c.tools }

// Tool finds a tool by name.
func (c *Catalog) Tool(name string) (domain.Tool, bool) {
	t, ok := c.byName[name]
	return t, ok
}

// Commands lists every command, sorted by group then name.
func (c *Catalog) Commands() []domain.Command { return c.commands }

// ToolExclusions maps each operationId that is not a tool to the reason.
func (c *Catalog) ToolExclusions() map[string]domain.Exclusion { return c.toolExcluded }

// CommandExclusions maps each operationId that is not a command to the reason.
func (c *Catalog) CommandExclusions() map[string]domain.Exclusion { return c.cmdExcluded }
