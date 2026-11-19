// SPDX-License-Identifier: MIT

package main

import (
	"github.com/SDK-E/agentsone/internal/agents"
	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/audit"
	"github.com/SDK-E/agentsone/internal/auth"
	"github.com/SDK-E/agentsone/internal/bus"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch"
	"github.com/SDK-E/agentsone/internal/events"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/machine"
	"github.com/SDK-E/agentsone/internal/mcp"
	"github.com/SDK-E/agentsone/internal/memory"
	"github.com/SDK-E/agentsone/internal/plugins"
	"github.com/SDK-E/agentsone/internal/router"
	"github.com/SDK-E/agentsone/internal/sched"
)

// modules lists every daemon module in start order. Builders fill their own
// package; this list only changes through a contract change.
func modules(deps core.Deps) []core.Module {
	return []core.Module{
		audit.New(deps),
		gatewaymint.New(deps),
		agents.New(deps),
		auth.New(deps),
		api.New(deps),
		bus.New(deps),
		events.New(deps),
		sched.New(deps),
		router.New(deps),
		memory.New(deps),
		plugins.New(deps),
		machine.New(deps),
		mcp.New(deps),
		enginefetch.New(deps),
	}
}
