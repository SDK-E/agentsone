// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/SDK-E/agentsone/internal/core"
)

// agentNameRe is the contract's AgentName pattern.
var agentNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// templateHead is the part of an agent-template document the catalog reads.
// The rest travels unchanged in Raw.
type templateHead struct {
	Name   string         `json:"name"`
	Kind   core.AgentKind `json:"kind"`
	IsRoot bool           `json:"isRoot"`
	Runner struct {
		Name string `json:"name"`
	} `json:"runner"`
}

// ParseAgentTemplate reads one agent-template document shipped by plugin.
// Its id is "<plugin>/<name>": the template's own name, so renaming the
// file does not change it.
func ParseAgentTemplate(plugin string, raw []byte) (core.AgentTemplate, error) {
	var h templateHead
	if err := json.Unmarshal(raw, &h); err != nil {
		return core.AgentTemplate{}, fmt.Errorf("%w: agent template: %w", ErrInvalidManifest, err)
	}
	if !agentNameRe.MatchString(h.Name) {
		return core.AgentTemplate{}, fmt.Errorf("%w: agent template name %q", ErrInvalidManifest, h.Name)
	}
	switch h.Kind {
	case core.AgentAlwaysOn, core.AgentChat, core.AgentWaker:
	default:
		return core.AgentTemplate{}, fmt.Errorf("%w: agent template %s: kind %q", ErrInvalidManifest, h.Name, h.Kind)
	}
	if !componentRe.MatchString(h.Runner.Name) {
		return core.AgentTemplate{}, fmt.Errorf("%w: agent template %s: runner %q", ErrInvalidManifest, h.Name, h.Runner.Name)
	}
	return core.AgentTemplate{
		ID: plugin + "/" + h.Name, Name: h.Name, Kind: h.Kind, IsRoot: h.IsRoot, Runner: h.Runner.Name, Raw: raw,
	}, nil
}
