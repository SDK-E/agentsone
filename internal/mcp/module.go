// SPDX-License-Identifier: MIT

package mcp

import (
	"fmt"
	"net/http"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	httpadapter "github.com/SDK-E/agentsone/internal/mcp/adapters/http"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

// Version is the server version MCP clients see. Set it with
// -ldflags "-X github.com/SDK-E/agentsone/internal/mcp.Version=...".
var Version = "dev"

// Module wires the machine MCP into the daemon. It serves the mcp tag at
// /v1/mcp. Its tools are the other API operations, called in process through
// the composed handler: hand that handler over with SetHandler once the API
// layer has built it.
type Module struct {
	core.Base
	dispatcher *httpadapter.HandlerDispatcher
	server     *httpadapter.Server
}

var _ core.Module = (*Module)(nil)

// New builds the mcp module. The tools come from the embedded contract; a
// contract that cannot be mapped is a build defect that the parity test
// catches, so it stops the daemon at start instead of limping on.
func New(core.Deps) *Module {
	cat, err := app.NewCatalog(api.Spec)
	if err != nil {
		panic(fmt.Sprintf("mcp: embedded contract cannot be mapped to tools: %v", err))
	}
	d := httpadapter.NewHandlerDispatcher(nil)
	return &Module{
		dispatcher: d,
		server:     httpadapter.NewServer(cat, app.NewCallTool(cat, d), Version),
	}
}

// Name is the module and migration namespace.
func (*Module) Name() string { return "mcp" }

// SetHandler gives the module the composed API handler that tool calls run
// through. Until it is set, tool calls fail with a plain "not ready" error.
func (m *Module) SetHandler(h http.Handler) { m.dispatcher.SetHandler(h) }

// API serves the mcp tag.
func (m *Module) API() core.API {
	return core.API{Groups: []core.APIGroup{m.server.Group()}}
}
